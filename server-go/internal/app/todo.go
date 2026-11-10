package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
)

/* To-dos & Planung – Verhalten wie backend/todo.js (v6.18), je Person getrennt (plan_tasks + Spalten aus Migration 017).
   Spalten: Überfällig · Heute · Morgen (nächster Arbeitstag) · Diese Woche · Später · Eingang.
   Bewusst anders:
   • Abgleich nach außen (Fälligkeit → Plane/Jira, Tempo-Plan, Plane-Startdatum) ist noch nicht portiert: die Schalter
     werden gespeichert und angezeigt, es wird aber nichts übertragen (offen).
   • Vorschläge sind die „Light“-Variante und dreifach abschaltbar (suggestAllowedFor); Termin-Vorbereitung entfällt
     (Kalender bleibt im Client).
   • Kapazität: „meetings“ immer 0 (Kalender bleibt im Client). */

const todoSelect = `SELECT p.*, pr.name AS project_name, t.jira_ticket AS task_ticket, t.title AS task_title,
	(SELECT COALESCE(SUM(e.duration_seconds),0) FROM time_entries e WHERE e.plan_task_id=p.id AND e.user_id=p.user_id) AS tracked_seconds
	FROM plan_tasks p LEFT JOIN projects pr ON pr.id=p.project_id AND pr.user_id=p.user_id LEFT JOIN tasks t ON t.id=p.task_id AND t.user_id=p.user_id`

const todoOpen = `COALESCE(p.status,'open')`

var (
	todoRuleRE   = regexp.MustCompile(`^(month-last-workday|month-first-workday|month-([1-9]|1[0-9]|2[0-8])|week-[1-5]|day)$`)
	todoSourceRE = regexp.MustCompile(`^[a-z_-]{1,30}$`)
	todoSuggest  sync.Map // Person → todoSugCached (5 Minuten, wie sugCache in todo.js)
)

type todoSugCached struct {
	at   time.Time
	data []map[string]any
}

func (a *App) routesTodo(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/todo", a.auth(a.todoBoard))
	mux.HandleFunc("GET /api/todo/range", a.auth(a.todoRange))
	mux.HandleFunc("POST /api/todo", a.auth(a.todoCreate))
	mux.HandleFunc("PUT /api/todo/{id}", a.auth(a.todoUpdate))
	mux.HandleFunc("DELETE /api/todo/{id}", a.auth(a.todoDelete))
	mux.HandleFunc("POST /api/todo/{id}/start", a.auth(a.todoStart))
	mux.HandleFunc("PUT /api/todo-settings", a.auth(a.todoSettings))
	mux.HandleFunc("GET /api/todo/suggest", a.auth(a.todoSuggestRoute))
	mux.HandleFunc("POST /api/todo/suggest/dismiss", a.auth(a.todoDismiss))
	mux.HandleFunc("GET /api/todo/templates", a.auth(a.todoTemplates))
	mux.HandleFunc("POST /api/todo/templates", a.auth(a.todoTemplateCreate))
	mux.HandleFunc("DELETE /api/todo/templates/{id}", a.auth(a.todoTemplateDelete))
}

/* ── reine Logik (getestet) ──────────────────────────────────────── */

// todoWhen: „today“ / „tomorrow“ / „week“ / „nextweek“ / Datum → (scheduled_date, scheduled_week); sonst Eingang (nil, nil)
func todoWhen(w, today string, cal workCal) (any, any) {
	if isoDateRE.MatchString(w) {
		if _, ok := parseDay(w); ok {
			return w, isoWeekOf(w)
		}
	}
	switch w {
	case "today":
		return today, isoWeekOf(today)
	case "tomorrow":
		n := cal.next(today)
		return n, isoWeekOf(n)
	case "week":
		return nil, isoWeekOf(today)
	case "nextweek":
		return nil, isoWeekOf(addDays(weekEnd(today), 1))
	}
	return nil, nil
}

// todoDueFor: Fälligkeit einer Vorlage im Monat/der Woche von ref ("" = nicht dran)
// Regeln: month-last-workday · month-first-workday · month-<1..28> · week-<1..5> (Mo..Fr) · day
func todoDueFor(rule, ref string, cal workCal) string {
	d, ok := parseDay(ref)
	if !ok {
		return ""
	}
	wdIn := func(t time.Time, step int) string {
		for i := 0; i < 40 && !cal.workday(dayStr(t)); i++ {
			t = t.AddDate(0, 0, step)
		}
		return dayStr(t)
	}
	first := time.Date(d.Year(), d.Month(), 1, 0, 0, 0, 0, time.UTC)
	switch {
	case rule == "month-last-workday":
		return wdIn(first.AddDate(0, 1, -1), -1)
	case rule == "month-first-workday":
		return wdIn(first, 1)
	case strings.HasPrefix(rule, "month-"):
		var n int
		if _, err := fmt.Sscanf(rule, "month-%d", &n); err != nil || n < 1 {
			return ""
		}
		return wdIn(first.AddDate(0, 0, min(28, n)-1), 1)
	case strings.HasPrefix(rule, "week-"):
		var n int
		if _, err := fmt.Sscanf(rule, "week-%d", &n); err != nil || n < 1 || n > 5 {
			return ""
		}
		mon := d.AddDate(0, 0, -((int(d.Weekday()) + 6) % 7))
		return dayStr(mon.AddDate(0, 0, n-1))
	case rule == "day":
		if cal.workday(ref) {
			return ref
		}
	}
	return ""
}

// todoColumn: Spalte eines offenen To-dos (wie board() in todo.js)
func todoColumn(sched, week, due, today, tomorrow, weekEndD, wk string) string {
	switch {
	case (sched != "" && sched < today) || (due != "" && due < today):
		return "overdue"
	case sched == today:
		return "today"
	case sched == tomorrow:
		return "tomorrow"
	case (sched != "" && sched <= weekEndD) || (sched == "" && week == wk):
		return "week"
	case sched != "" || week != "":
		return "later"
	}
	return "inbox"
}

/* ── Ticket-System: Plane, wenn das Präfix ein Plane-Projekt ist, sonst Jira ── */

type ticketSys struct{ planeIdents, planeTickets map[string]bool }

func (s ticketSys) of(key string) any {
	k := strings.ToUpper(key)
	m := keyRE.FindStringSubmatch(k)
	if m == nil {
		return nil
	}
	if s.planeIdents[m[1]] || s.planeTickets[k] {
		return "plane"
	}
	return "jira"
}

func (a *App) ticketSystems(ctx context.Context, uid int64, keys []string) (ticketSys, error) {
	s := ticketSys{map[string]bool{}, map[string]bool{}}
	rows, err := a.DB.Query(ctx, `SELECT UPPER(plane_identifier) FROM projects WHERE user_id=$1 AND plane_project_id IS NOT NULL AND COALESCE(plane_identifier,'')<>''`, uid)
	if err != nil {
		return s, err
	}
	for rows.Next() {
		var k string
		if rows.Scan(&k) == nil {
			s.planeIdents[k] = true
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil || len(keys) == 0 {
		return s, err
	}
	rows, err = a.DB.Query(ctx, `SELECT DISTINCT UPPER(jira_ticket) FROM tasks WHERE user_id=$1 AND plane_work_item_id IS NOT NULL AND UPPER(jira_ticket) = ANY($2)`, uid, keys)
	if err != nil {
		return s, err
	}
	defer rows.Close()
	for rows.Next() {
		var k string
		if rows.Scan(&k) == nil {
			s.planeTickets[k] = true
		}
	}
	return s, rows.Err()
}

// todoShape = shape() in todo.js: ticket, system, ticket_title; task_ticket/task_title bleiben wie in Node dabei
func (a *App) todoShape(ctx context.Context, uid int64, rows []map[string]any) ([]map[string]any, error) {
	keys := []string{}
	for _, r := range rows {
		if k := strings.ToUpper(jsOr(asStr(r["jira_ticket"]), asStr(r["task_ticket"]))); k != "" {
			keys = append(keys, k)
		}
	}
	sys, err := a.ticketSystems(ctx, uid, keys)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		k := strings.ToUpper(jsOr(asStr(r["jira_ticket"]), asStr(r["task_ticket"])))
		r["ticket"], r["system"] = nz(k), nil
		if k != "" {
			r["system"] = sys.of(k)
		}
		r["ticket_title"] = nz(jsOr(asStr(r["ticket_title"]), asStr(r["task_title"])))
	}
	return rows, nil
}

func (a *App) todoByID(ctx context.Context, uid, id int64) (map[string]any, error) {
	rs, err := a.maps(ctx, todoSelect+` WHERE p.id=$1 AND p.user_id=$2`, id, uid)
	if err != nil || len(rs) == 0 {
		return nil, err
	}
	rs, err = a.todoShape(ctx, uid, rs)
	if err != nil {
		return nil, err
	}
	return rs[0], nil
}

// todoCal: Feiertage vom Monatsanfang bis gut zwei Monate voraus (reicht für Vorlagen, „morgen“ und die Woche)
func (a *App) todoCal(ctx context.Context, uid int64, today string) (workCal, error) {
	return a.workCalendar(ctx, uid, today[:8]+"01", addDays(today, 70))
}

/* ── Wiederkehrendes und „Zeiten nachtragen“ ─────────────────────── */

// todoGenerate legt fällige Vorlagen-To-dos an und pflegt das eine To-do „Zeiten nachtragen“.
// Eine Sperre je Person verhindert doppelte Zeilen bei gleichzeitigen Aufrufen.
func (a *App) todoGenerate(ctx context.Context, uid int64, today string, cal workCal) error {
	gap, err := a.monthEndStatus(ctx, uid, false)
	if err != nil {
		return err
	}
	return a.inTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, int64(7270040000000000)+uid); err != nil {
			return err
		}
		if err := a.runTemplatesTx(ctx, tx, uid, today, cal); err != nil {
			return err
		}
		var dismissed *string // nach der Sperre lesen (gleichzeitiges „erledigt“)
		if err := tx.QueryRow(ctx, `SELECT value FROM user_settings WHERE user_id=$1 AND key='gaps_todo_dismissed'`, uid).Scan(&dismissed); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		return syncGapTodoTx(ctx, tx, uid, today, gap, deref(dismissed))
	})
}

func (a *App) runTemplatesTx(ctx context.Context, tx pgx.Tx, uid int64, t string, cal workCal) error {
	type tpl struct {
		id              int64
		title, rule     string
		project         *int64
		ticket, lastFor *string
		est, prio       *int32
	}
	rows, err := tx.Query(ctx, `SELECT id, title, rule, project_id, jira_ticket, last_for, estimated_minutes, priority FROM todo_templates WHERE user_id=$1 AND active=1`, uid)
	if err != nil {
		return err
	}
	var list []tpl
	for rows.Next() {
		var x tpl
		if err := rows.Scan(&x.id, &x.title, &x.rule, &x.project, &x.ticket, &x.lastFor, &x.est, &x.prio); err != nil {
			rows.Close()
			return err
		}
		list = append(list, x)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	horizon := cal.next(cal.next(t)) // bis übermorgen anlegen
	for _, tp := range list {
		last := ""
		if tp.lastFor != nil {
			last = *tp.lastFor
		}
		for _, ref := range []string{t, horizon} {
			due := todoDueFor(tp.rule, ref, cal)
			if due == "" || due < t || due > weekEnd(horizon) || (last != "" && last >= due) {
				continue
			}
			est, prio := int32(30), int32(2)
			if tp.est != nil {
				est = *tp.est
			}
			if tp.prio != nil {
				prio = *tp.prio
			}
			if _, err := tx.Exec(ctx, `INSERT INTO plan_tasks(user_id,title,project_id,jira_ticket,estimated_minutes,priority,status,scheduled_date,scheduled_week,due_date,source,template_id)
				VALUES($1,$2,(SELECT id FROM projects WHERE id=$3 AND user_id=$1),$4,$5,$6,'open',$7,$8,$7,'recurring',$9)`, uid, tp.title, tp.project, tp.ticket, est, prio, due, isoWeekOf(due), tp.id); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE todo_templates SET last_for=$3 WHERE id=$1 AND user_id=$2`, tp.id, uid, due); err != nil {
				return err
			}
			last = due
		}
	}
	return nil
}

// gapTodoText: Titel, Notiz und Schätzung des To-dos „Zeiten nachtragen“ (wie syncGapTodo in todo.js)
func gapTodoText(days []missDay) (string, string, int) {
	n := len(days)
	title := fmt.Sprintf("Zeiten nachtragen (%d Tag%s)", n, map[bool]string{true: "", false: "e"}[n == 1])
	var rec []string
	for i := n - 1; i >= 0 && i >= n-3; i-- {
		rec = append(rec, ddmm(days[i].Date)+" fehlen "+fmtDE(days[i].MissingH)+" h")
	}
	notes := strings.Join(rec, " · ")
	if more := n - len(rec); more > 0 {
		notes += fmt.Sprintf(" + %d weitere", more)
	}
	return title, notes, min(120, 10*n)
}

func syncGapTodoTx(ctx context.Context, tx pgx.Tx, uid int64, t string, st *monthEnd, dismissed string) error {
	seen := map[string]bool{}
	var days []missDay
	var src []missDay
	if st.Prev != nil {
		src = append(src, st.Prev.Days...)
	}
	src = append(src, st.OpenDays...)
	for _, d := range src {
		if !seen[d.Date] {
			seen[d.Date] = true
			days = append(days, d)
		}
	}
	sort.Slice(days, func(i, j int) bool { return days[i].Date < days[j].Date })
	var curID int64
	err := tx.QueryRow(ctx, `SELECT id FROM plan_tasks WHERE user_id=$1 AND source='gaps' AND COALESCE(status,'open')='open' ORDER BY id DESC LIMIT 1`, uid).Scan(&curID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if len(days) == 0 {
		if curID != 0 {
			_, err = tx.Exec(ctx, `UPDATE plan_tasks SET status='done', completed_at=$3 WHERE id=$1 AND user_id=$2`, curID, uid, time.Now().UTC().Format(isoMillis))
		}
		return err
	}
	dates := make([]string, len(days))
	for i, d := range days {
		dates[i] = d.Date
	}
	if curID == 0 && dismissed != "" { // bewusst abgehakt, nichts Neues
		dl := strings.Split(dismissed, ",")
		all := true
		for _, d := range dates {
			if !contains(dl, d) {
				all = false
				break
			}
		}
		if all {
			return nil
		}
	}
	title, notes, est := gapTodoText(days)
	sig := strings.Join(dates, ",")
	if curID != 0 {
		_, err = tx.Exec(ctx, `UPDATE plan_tasks SET title=$3, notes=$4, ticket_title=$5, scheduled_date=CASE WHEN scheduled_date<$6 THEN $6 ELSE scheduled_date END,
			scheduled_week=$7, estimated_minutes=$8 WHERE id=$1 AND user_id=$2`, curID, uid, title, notes, sig, t, isoWeekOf(t), est)
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO plan_tasks(user_id,title,notes,ticket_title,priority,status,scheduled_date,scheduled_week,estimated_minutes,source)
		VALUES($1,$2,$3,$4,1,'open',$5,$6,$7,'gaps')`, uid, title, notes, sig, t, isoWeekOf(t), est)
	return err
}

/* ── Liste in Spalten ─────────────────────────────────────────────── */

func (a *App) todoBoard(w http.ResponseWriter, r *http.Request, u User) {
	ctx := r.Context()
	t := a.today()
	cal, err := a.todoCal(ctx, u.ID, t)
	if err != nil {
		a.fail(w, err)
		return
	}
	if err := a.todoGenerate(ctx, u.ID, t, cal); err != nil {
		a.Log.Warn("To-dos erzeugen", "err", err) // wie Node: die Liste kommt trotzdem
	}
	tm, we, wk := cal.next(t), weekEnd(t), isoWeekOf(t)
	rows, err := a.maps(ctx, todoSelect+` WHERE p.user_id=$1 AND `+todoOpen+` NOT IN ('done','template')
		ORDER BY p.priority ASC, p.sort_order ASC, COALESCE(p.due_date,'9999') ASC, p.created_at ASC`, u.ID)
	if err == nil {
		rows, err = a.todoShape(ctx, u.ID, rows)
	}
	if err != nil {
		a.fail(w, err)
		return
	}
	col := map[string][]map[string]any{"overdue": {}, "today": {}, "tomorrow": {}, "week": {}, "later": {}, "inbox": {}}
	for _, x := range rows {
		k := todoColumn(asStr(x["scheduled_date"]), asStr(x["scheduled_week"]), asStr(x["due_date"]), t, tm, we, wk)
		col[k] = append(col[k], x)
	}
	done, err := a.maps(ctx, todoSelect+` WHERE p.user_id=$1 AND p.status='done' AND substr(p.completed_at,1,10)>=$2 ORDER BY p.completed_at DESC LIMIT 30`, u.ID, addDays(t, -7))
	if err == nil {
		done, err = a.todoShape(ctx, u.ID, done)
	}
	if err != nil {
		a.fail(w, err)
		return
	}
	st, err := a.userSettings(ctx, u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	capToday, err := a.todoCapacity(ctx, u.ID, st, t, t)
	if err != nil {
		a.fail(w, err)
		return
	}
	capTm, err := a.todoCapacity(ctx, u.ID, st, tm, t)
	if err != nil {
		a.fail(w, err)
		return
	}
	capToday["planned"], capTm["planned"] = plannedMinutes(col["today"]), plannedMinutes(col["tomorrow"])
	on := func(k string) bool { return st[k] != "0" }
	writeJSON(w, http.StatusOK, map[string]any{"today": t, "tomorrow": tm, "week": wk, "columns": col, "done": done,
		"capacity": map[string]any{"today": capToday, "tomorrow": capTm},
		"settings": map[string]any{"sync_due": on("todo_sync_due"), "sync_tempo_plan": on("todo_sync_tempo_plan"), "sync_plane_start": on("todo_sync_plane_start"),
			"sync_available": false}}) // Abgleich nach Plane/Jira/Tempo im Go-Server noch nicht portiert
}

func plannedMinutes(list []map[string]any) int {
	n := 0
	for _, x := range list {
		n += max(0, int(asFloat(x["estimated_minutes"]))-int(math.Round(asFloat(x["tracked_seconds"])/60)))
	}
	return n
}

// todoCapacity: Soll − Abwesenheit (Termine bleiben im Client: 0) und heute Gebuchtes, in Minuten
func (a *App) todoCapacity(ctx context.Context, uid int64, st map[string]string, d, today string) (map[string]any, error) {
	day, _ := parseDay(d)
	target := dayTargets(st)[int(day.Weekday())] * 60
	abs, err := a.maps(ctx, `SELECT type, hours FROM absences WHERE user_id=$1 AND date_from<=$2 AND date_to>=$2`, uid, d)
	if err != nil {
		return nil, err
	}
	free, sum := target, 0.0
	for _, x := range abs {
		h, ok := x["hours"].(float64)
		if asStr(x["type"]) == "feiertag" || !ok {
			free = 0
		}
		sum += h * 60
	}
	if free > 0 {
		free = math.Max(0, target-sum)
	}
	booked := 0.0
	if d == today {
		if err := a.DB.QueryRow(ctx, `SELECT COALESCE(SUM(duration_seconds),0)::float8 FROM time_entries WHERE user_id=$1 AND date=$2 AND COALESCE(is_absence,0)=0`, uid, d).Scan(&booked); err != nil {
			return nil, err
		}
	}
	return map[string]any{"date": d, "available": int(math.Round(free)), "meetings": 0, "booked": int(math.Round(booked / 60))}, nil
}

/* ── Kalender: geplante und fällige To-dos im Zeitraum ───────────── */

func (a *App) todoRange(w http.ResponseWriter, r *http.Request, u User) {
	from, to := r.URL.Query().Get("from"), r.URL.Query().Get("to")
	_, ok1 := parseDay(from)
	_, ok2 := parseDay(to)
	if !ok1 || !ok2 {
		badRequest(w, "from/to")
		return
	}
	ctx := r.Context()
	rows, err := a.maps(ctx, todoSelect+` WHERE p.user_id=$1 AND `+todoOpen+` NOT IN ('template')
		AND ((p.scheduled_date BETWEEN $2 AND $3) OR (p.due_date BETWEEN $2 AND $3 AND `+todoOpen+`='open'))`, u.ID, from, to)
	if err == nil {
		rows, err = a.todoShape(ctx, u.ID, rows)
	}
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

/* ── Anlegen, Ändern, Löschen ────────────────────────────────────── */

// todoValue prüft und wandelt ein Feld; ok=false mit Text = 400
func todoValue(field string, v any) (any, string) {
	if field == "title" {
		s := strings.TrimSpace(asStr(v))
		if s == "" || len(s) > 500 {
			return nil, "Was ist zu tun?"
		}
		return s, ""
	}
	if s, isStr := v.(string); v == nil || (isStr && strings.TrimSpace(s) == "") { // "" → NULL wie in Node
		return nil, ""
	}
	switch field {
	case "jira_ticket":
		k := strings.ToUpper(strings.TrimSpace(asStr(v)))
		if len(k) > 64 {
			return nil, "Ticketnummer zu lang"
		}
		return nz(k), ""
	case "ticket_title", "notes":
		s := asStr(v)
		if len(s) > 4000 {
			return nil, field + " zu lang"
		}
		return nz(s), ""
	case "due_date":
		s := asStr(v)
		if _, ok := parseDay(s); !ok {
			return nil, "due_date: JJJJ-MM-TT"
		}
		return s, ""
	case "block_start":
		s := asStr(v)
		if !clockRE.MatchString(s) {
			return nil, "block_start: HH:MM"
		}
		return s, ""
	case "estimated_minutes":
		n := asFloat(v)
		if _, num := v.(float64); (!num && asStr(v) == "") || n < 0 || n > 10080 {
			return nil, "estimated_minutes ungültig"
		}
		return int(n), ""
	case "priority":
		n := int(asFloat(v))
		if n < 1 || n > 3 {
			return nil, "priority: 1–3"
		}
		return n, ""
	case "sort_order":
		return int(math.Max(-1e6, math.Min(1e6, asFloat(v)))), ""
	case "project_id", "task_id":
		if invalidID(v) {
			return nil, field + " ungültig"
		}
		return asID(v), ""
	}
	return nil, "Feld unbekannt"
}

func (a *App) todoCreate(w http.ResponseWriter, r *http.Request, u User) {
	var in body
	if !readJSON(w, r, &in) {
		return
	}
	ctx := r.Context()
	title := strings.TrimSpace(jsOr(asStr(in["title"]), jsOr(asStr(in["ticket_title"]), asStr(in["jira_ticket"]))))
	if title == "" {
		badRequest(w, "Was ist zu tun?")
		return
	}
	vals := map[string]any{}
	for _, f := range []string{"title", "project_id", "task_id", "jira_ticket", "ticket_title", "estimated_minutes", "priority", "due_date", "block_start", "notes"} {
		v := in[f]
		if f == "title" {
			v = title
		}
		x, msg := todoValue(f, v)
		if msg != "" {
			badRequest(w, msg)
			return
		}
		vals[f] = x
	}
	if !a.owned(ctx, "projects", vals["project_id"], u.ID) || !a.owned(ctx, "tasks", vals["task_id"], u.ID) {
		badRequest(w, "Projekt oder Ticket nicht gefunden")
		return
	}
	source := jsOr(asStr(in["source"]), "manual")
	if !todoSourceRE.MatchString(source) {
		badRequest(w, "source ungültig")
		return
	}
	eventUID := nz(asStr(in["event_uid"]))
	if len(asStr(in["event_uid"])) > 300 {
		badRequest(w, "event_uid zu lang")
		return
	}
	t := a.today()
	cal, err := a.todoCal(ctx, u.ID, t)
	if err != nil {
		a.fail(w, err)
		return
	}
	sd, sw := todoWhen(asStr(in["when"]), t, cal)
	key, _ := vals["jira_ticket"].(string)
	if key != "" && !truthy(in["force"]) {
		var exists bool
		if err := a.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM plan_tasks p WHERE p.user_id=$1 AND UPPER(p.jira_ticket)=$2 AND `+todoOpen+` NOT IN ('done','template'))`, u.ID, key).Scan(&exists); err != nil {
			a.fail(w, err)
			return
		}
		if exists {
			writeJSON(w, http.StatusConflict, map[string]any{"error": key + " steht schon auf der Liste"})
			return
		}
	}
	pid := vals["project_id"]
	if pid == nil && key != "" {
		pid = a.projectByJiraKey(ctx, u.ID, strings.SplitN(key, "-", 2)[0])
		if pid == nil {
			var p int64
			if a.DB.QueryRow(ctx, `SELECT project_id FROM tasks WHERE user_id=$1 AND UPPER(jira_ticket)=$2 ORDER BY id LIMIT 1`, u.ID, key).Scan(&p) == nil {
				pid = p
			}
		}
	}
	est, prio := vals["estimated_minutes"], vals["priority"]
	if est == nil {
		est = 60
	}
	if prio == nil {
		prio = 2
	}
	var syncHash any
	if truthy(in["due_from_ticket"]) { // Fälligkeit kam aus dem Ticket → nicht zurückschreiben
		b, _ := json.Marshal(map[string]any{"due": vals["due_date"]})
		syncHash = string(b)
	}
	var id int64
	if err := a.DB.QueryRow(ctx, `INSERT INTO plan_tasks(user_id,title,project_id,task_id,jira_ticket,ticket_title,estimated_minutes,priority,status,scheduled_date,scheduled_week,due_date,block_start,notes,source,event_uid,sync_hash)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,'open',$9,$10,$11,$12,$13,$14,$15,$16) RETURNING id`,
		u.ID, title, pid, vals["task_id"], vals["jira_ticket"], vals["ticket_title"], est, prio, sd, sw, vals["due_date"], vals["block_start"], vals["notes"], source, eventUID, syncHash).Scan(&id); err != nil {
		a.fail(w, err)
		return
	}
	todoSuggest.Delete(u.ID)
	a.todoReply(w, r, u, id)
}

func (a *App) todoReply(w http.ResponseWriter, r *http.Request, u User, id int64) {
	row, err := a.todoByID(r.Context(), u.ID, id)
	if err != nil {
		a.fail(w, err)
		return
	}
	if row == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "nicht gefunden"})
		return
	}
	writeJSON(w, http.StatusOK, row)
}

func (a *App) todoUpdate(w http.ResponseWriter, r *http.Request, u User) {
	id, ok := pathID(r)
	if !ok {
		notFound(w)
		return
	}
	var in body
	if !readJSON(w, r, &in) {
		return
	}
	ctx := r.Context()
	var curDate, curSource, curSig *string
	if err := a.DB.QueryRow(ctx, `SELECT scheduled_date, source, ticket_title FROM plan_tasks WHERE id=$1 AND user_id=$2`, id, u.ID).Scan(&curDate, &curSource, &curSig); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "nicht gefunden"})
			return
		}
		a.fail(w, err)
		return
	}
	var sets []string
	var args []any
	set := func(col string, v any) {
		args = append(args, v)
		sets = append(sets, fmt.Sprintf("%s=$%d", col, len(args)+2))
	}
	for _, f := range []string{"title", "project_id", "task_id", "jira_ticket", "ticket_title", "estimated_minutes", "priority", "due_date", "block_start", "notes", "sort_order"} {
		v, has := in[f]
		if !has {
			continue
		}
		x, msg := todoValue(f, v)
		if msg != "" {
			badRequest(w, msg)
			return
		}
		if f == "title" && x == nil {
			badRequest(w, "Was ist zu tun?")
			return
		}
		if (f == "project_id" && !a.owned(ctx, "projects", x, u.ID)) || (f == "task_id" && !a.owned(ctx, "tasks", x, u.ID)) {
			badRequest(w, "Projekt oder Ticket nicht gefunden")
			return
		}
		set(f, x)
	}
	if v, has := in["when"]; has {
		t := a.today()
		cal, err := a.todoCal(ctx, u.ID, t)
		if err != nil {
			a.fail(w, err)
			return
		}
		sd, sw := todoWhen(asStr(v), t, cal)
		set("scheduled_date", sd)
		set("scheduled_week", sw)
		if curDate != nil && *curDate != "" && asStr(sd) != *curDate {
			sets = append(sets, "moved_count=COALESCE(moved_count,0)+1")
		}
	}
	doneV, hasDone := in["done"]
	if hasDone {
		if truthy(doneV) {
			set("status", "done")
			set("completed_at", time.Now().UTC().Format(isoMillis))
		} else {
			set("status", "open")
			set("completed_at", nil)
		}
	}
	err := a.inTx(ctx, func(tx pgx.Tx) error {
		if hasDone && truthy(doneV) && curSource != nil && *curSource == "gaps" {
			if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, int64(7270040000000000)+u.ID); err != nil {
				return err
			}
			sig := ""
			if curSig != nil {
				sig = *curSig
			}
			if err := a.setUserSetting(ctx, tx, u.ID, "gaps_todo_dismissed", sig); err != nil {
				return err
			}
		}
		if len(sets) == 0 {
			return nil
		}
		_, err := tx.Exec(ctx, "UPDATE plan_tasks SET "+strings.Join(sets, ",")+" WHERE id=$1 AND user_id=$2", append([]any{id, u.ID}, args...)...)
		return err
	})
	if err != nil {
		a.fail(w, err)
		return
	}
	todoSuggest.Delete(u.ID)
	a.todoReply(w, r, u, id)
}

func (a *App) todoDelete(w http.ResponseWriter, r *http.Request, u User) {
	id, ok := pathID(r)
	if !ok {
		notFound(w)
		return
	}
	// Buchungen behalten ihren Eintrag, verlieren nur den Verweis (FK ON DELETE SET NULL). Liegt eine davon in einem
	// eingereichten/freigegebenen Monat, lehnt die Sperre der Datenbank das ab – dann bleibt das To-do stehen.
	// Läuft der Timer auf diesem To-do, verliert er nur den Verweis (timer_state hat keinen Fremdschlüssel)
	err := a.inTx(r.Context(), func(tx pgx.Tx) error {
		if _, err := tx.Exec(r.Context(), `UPDATE timer_state SET plan_task_id=NULL WHERE user_id=$1 AND plan_task_id=$2`, u.ID, id); err != nil {
			return err
		}
		_, err := tx.Exec(r.Context(), `DELETE FROM plan_tasks WHERE id=$1 AND user_id=$2`, id, u.ID)
		return err
	})
	if isMonthLocked(err) {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "Auf dieses To-do ist in einem eingereichten oder freigegebenen Monat gebucht – bitte als erledigt markieren statt löschen", "code": "month_locked"})
		return
	}
	if err != nil {
		a.fail(w, err)
		return
	}
	todoSuggest.Delete(u.ID)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// todoStart: Timer auf dem To-do starten; lokale Aufgabe zum Ticket sicherstellen (Plane oder Jira)
func (a *App) todoStart(w http.ResponseWriter, r *http.Request, u User) {
	id, ok := pathID(r)
	if !ok {
		notFound(w)
		return
	}
	ctx := r.Context()
	var title string
	var ticket, ticketTitle, sched *string
	var pid, tid *int64
	err := a.DB.QueryRow(ctx, `SELECT title, jira_ticket, ticket_title, scheduled_date, project_id, task_id FROM plan_tasks WHERE id=$1 AND user_id=$2`, id, u.ID).
		Scan(&title, &ticket, &ticketTitle, &sched, &pid, &tid)
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "nicht gefunden"})
		return
	}
	if err != nil {
		a.fail(w, err)
		return
	}
	key := ""
	if ticket != nil {
		key = strings.ToUpper(strings.TrimSpace(*ticket))
	}
	var p, t any
	if pid != nil {
		p = *pid
	}
	if tid != nil {
		t = *tid
	}
	if t == nil && key != "" {
		sys, err := a.ticketSystems(ctx, u.ID, []string{key})
		if err != nil {
			a.fail(w, err)
			return
		}
		if sys.of(key) == "plane" {
			nt, np, msg, err := a.ensurePlaneTask(ctx, u.ID, key)
			if err != nil {
				a.planeFail(w, err)
				return
			}
			if msg != "" {
				badRequest(w, msg)
				return
			}
			t, p = nt, np
		} else if keyRE.MatchString(key) {
			if p == nil {
				p = a.projectByJiraKey(ctx, u.ID, strings.SplitN(key, "-", 2)[0])
			}
			if p == nil {
				badRequest(w, "Kein Projekt für "+key+" – bitte am To-do ein Projekt wählen")
				return
			}
			var nt int64
			err := a.DB.QueryRow(ctx, `SELECT id FROM tasks WHERE user_id=$1 AND project_id=$2 AND UPPER(jira_ticket)=$3 ORDER BY id LIMIT 1`, u.ID, p, key).Scan(&nt)
			if errors.Is(err, pgx.ErrNoRows) {
				tt := title
				if ticketTitle != nil && *ticketTitle != "" {
					tt = *ticketTitle
				}
				err = a.DB.QueryRow(ctx, `INSERT INTO tasks(user_id,project_id,title,jira_ticket,status) VALUES($1,$2,$3,$4,'open') RETURNING id`, u.ID, p, tt, key).Scan(&nt)
			}
			if err != nil {
				a.fail(w, err)
				return
			}
			t = nt
		}
		if t != nil {
			if _, err := a.DB.Exec(ctx, `UPDATE plan_tasks SET task_id=$3, project_id=$4 WHERE id=$1 AND user_id=$2`, id, u.ID, t, p); err != nil {
				a.fail(w, err)
				return
			}
		}
	}
	if p == nil {
		badRequest(w, "Bitte am To-do ein Projekt oder Ticket wählen")
		return
	}
	var comment any
	if label := jsOr(func() string {
		if ticketTitle != nil {
			return *ticketTitle
		}
		return ""
	}(), key); title != label {
		comment = title
	}
	_, ae, err := a.startTimer(ctx, u.ID, p, t, id, comment, nil, "")
	if ae != nil {
		writeJSON(w, ae.Code, ae.Body)
		return
	}
	if err != nil {
		a.fail(w, err)
		return
	}
	if td := a.today(); sched == nil || *sched != td {
		if _, err := a.DB.Exec(ctx, `UPDATE plan_tasks SET scheduled_date=$3, scheduled_week=$4 WHERE id=$1 AND user_id=$2`, id, u.ID, td, isoWeekOf(td)); err != nil {
			a.fail(w, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *App) todoSettings(w http.ResponseWriter, r *http.Request, u User) {
	var in body
	if !readJSON(w, r, &in) {
		return
	}
	for _, k := range []string{"todo_sync_due", "todo_sync_tempo_plan", "todo_sync_plane_start"} {
		v, has := in[k]
		if !has {
			continue
		}
		if err := a.setUserSetting(r.Context(), a.DB, u.ID, k, map[bool]string{true: "1", false: "0"}[truthy(v)]); err != nil {
			a.fail(w, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

/* ── Vorschläge („Light“: Regeln, keine KI) ──────────────────────── */

var todoPrio = map[string]int{"Dringend": 1, "Highest": 1, "Hoch": 1, "High": 1, "Mittel": 2, "Medium": 2, "Niedrig": 3, "Low": 3, "Lowest": 3}

// ticketSuggestion: Score/Begründung/Spalte eines zugewiesenen Tickets (wie todo.js)
func ticketSuggestion(due, priority, today, tomorrow, weekEndD string) (int, string, any) {
	pr := todoPrio[priority]
	if pr == 0 {
		pr = 2
	}
	soon := addDays(today, 7)
	score, why := 0, []string{}
	switch {
	case due != "" && due < today:
		score += 100
		why = append(why, "überfällig seit "+due)
	case due != "" && due <= tomorrow:
		score += 80
		why = append(why, map[bool]string{true: "heute fällig", false: "morgen fällig"}[due == today])
	case due != "" && due <= soon:
		score += 50
		why = append(why, "fällig "+due)
	case due != "":
		score += 10
		why = append(why, "fällig "+due)
	}
	if pr == 1 {
		score += 40
		why = append(why, priority)
	}
	if len(why) == 0 {
		why = append(why, "dir zugewiesen")
	}
	var when any = "later"
	switch {
	case due != "" && due <= today:
		when = "today"
	case due != "" && due <= tomorrow:
		when = "tomorrow"
	case due != "" && due <= weekEndD, pr == 1:
		when = "week"
	}
	return score, strings.Join(why, " · "), when
}

// todoTickets: mir zugewiesene Tickets (Plane „meine“, Jira assignee = currentUser()); Fehler werden übergangen wie in Node
func (a *App) todoTickets(ctx context.Context, uid int64) []map[string]any {
	var out []map[string]any
	pctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if cfgs, err := a.planeCfgs(pctx, uid); err == nil {
		seen := map[string]bool{}
		for _, c := range cfgs {
			if !c.ok() {
				continue
			}
			for _, i := range a.planeMineCached(pctx, uid, c) {
				k := strings.ToUpper(asStr(i["key"]))
				if k == "" || seen[k] {
					continue
				}
				seen[k] = true
				out = append(out, map[string]any{"system": "plane", "key": k, "summary": i["name"], "project_id": i["local_project_id"],
					"project_name": nil, "due": "", "priority": ""})
			}
		}
	}
	if !a.tempoModuleOn(ctx) {
		return out
	}
	s, st, err := a.tempoSetup(ctx, uid)
	if err != nil || !jiraOn(st) {
		return out
	}
	jql := jiraSearchJQL("", "", "1", "")
	for _, ti := range s.usable() {
		jctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		res, err := a.jiraDo(jctx, uid, ti.C.jira(), "GET", "/rest/api/2/search?startAt=0&maxResults=50&fields="+jiraFields+"&jql="+url.QueryEscape(jql), nil)
		cancel()
		if err != nil {
			continue
		}
		for _, i := range listOf(jm(res)) {
			sh := shapeIssue(i, nil)
			if strings.Contains(strings.ToLower(asStr(sh["status_cat"])), "done") {
				continue
			}
			out = append(out, map[string]any{"system": "jira", "key": strings.ToUpper(asStr(sh["key"])), "summary": sh["summary"],
				"project_id": a.projectByJiraKey(ctx, uid, asStr(sh["project"])), "project_name": sh["project_name"], "due": asStr(sh["due"]), "priority": asStr(sh["priority"])})
		}
	}
	return out
}

// planeMineCached: „meine Work-Items“ aus dem Zwischenspeicher von /integrations/plane/my-items (bis 1 h), sonst einmal laden
func (a *App) planeMineCached(ctx context.Context, uid int64, c planeCfg) []map[string]any {
	ck := c.Base + "|" + c.WS + "|" + descHash(c.Key)[:8]
	v, _ := myItemsByUser.LoadOrStore(uid, &myItemsState{})
	ms := v.(*myItemsState)
	ms.mu.Lock()
	cached, busy := ms.cache, ms.inflight != nil
	ms.mu.Unlock()
	if cached != nil && cached.key == ck && time.Since(cached.at) < time.Hour {
		return cached.items
	}
	if busy { // läuft gerade für die Oberfläche: nicht doppelt anfragen
		return nil
	}
	items, err := a.loadMyItems(ctx, uid, c)
	if err != nil {
		return nil
	}
	ms.mu.Lock()
	ms.cache = &myItemsCached{at: time.Now(), key: ck, items: items}
	ms.mu.Unlock()
	return items
}

func (a *App) todoSuggestRoute(w http.ResponseWriter, r *http.Request, u User) {
	ctx := r.Context()
	if on, _ := a.suggestAllowedFor(ctx, u.ID, "To-do-Vorschläge"); !on { // aus: keine Abfragen, keine Aufrufe nach außen
		todoSuggest.Delete(u.ID)
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	if v, ok := todoSuggest.Load(u.ID); ok && r.URL.Query().Get("fresh") != "1" && time.Since(v.(todoSugCached).at) < 5*time.Minute {
		out, err := a.todoFilterDismissed(ctx, u.ID, v.(todoSugCached).data)
		if err != nil {
			a.fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, out)
		return
	}
	t := a.today()
	cal, err := a.todoCal(ctx, u.ID, t)
	if err != nil {
		a.fail(w, err)
		return
	}
	tm, we := cal.next(t), weekEnd(t)
	onList := map[string]bool{}
	rows, err := a.DB.Query(ctx, `SELECT UPPER(jira_ticket) FROM plan_tasks WHERE user_id=$1 AND jira_ticket IS NOT NULL AND COALESCE(status,'open')<>'template'
		AND (COALESCE(status,'open')<>'done' OR substr(completed_at,1,10)>=$2)`, u.ID, addDays(t, -2))
	if err != nil {
		a.fail(w, err)
		return
	}
	for rows.Next() {
		var k string
		if rows.Scan(&k) == nil {
			onList[k] = true
		}
	}
	rows.Close()
	out := []map[string]any{}
	// 1. Mir zugewiesene Tickets (Plane + Jira) – fällig/dringend zuerst
	for _, i := range a.todoTickets(ctx, u.ID) {
		k := asStr(i["key"])
		if k == "" || onList[k] {
			continue
		}
		score, why, when := ticketSuggestion(asStr(i["due"]), asStr(i["priority"]), t, tm, we)
		pr := todoPrio[asStr(i["priority"])]
		if pr == 0 {
			pr = 2
		}
		out = append(out, map[string]any{"kind": "ticket", "key": "t:" + k, "title": jsOr(asStr(i["summary"]), k), "ticket": k, "ticket_title": i["summary"],
			"system": i["system"], "project_id": i["project_id"], "project_name": i["project_name"], "due": nz(asStr(i["due"])), "priority": pr, "score": score, "why": why, "when": when})
	}
	// 2. Termine ohne Vorbereitung: entfällt (Kalender bleibt im Client)
	// 3. Liegengebliebenes: oft verschoben oder lange im Eingang
	stale, err := a.maps(ctx, `SELECT id, title, jira_ticket, moved_count FROM plan_tasks WHERE user_id=$1 AND COALESCE(status,'open')='open'
		AND (COALESCE(moved_count,0)>=3 OR (scheduled_date IS NULL AND scheduled_week IS NULL AND created_at < now() - interval '14 days')) ORDER BY id`, u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	for _, x := range stale {
		why := "seit über 2 Wochen im Eingang"
		if n := int(asFloat(x["moved_count"])); n >= 3 {
			why = fmt.Sprintf("%d× verschoben – heute machen, abgeben oder streichen?", n)
		}
		out = append(out, map[string]any{"kind": "stale", "key": fmt.Sprintf("s:%v", x["id"]), "todo_id": x["id"], "title": x["title"], "ticket": x["jira_ticket"], "score": 30, "why": why, "when": "today"})
	}
	// 4. Wiederkehrendes: Vorschläge für Vorlagen, die es noch nicht gibt
	have := map[string]bool{}
	trs, err := a.DB.Query(ctx, `SELECT title FROM todo_templates WHERE user_id=$1`, u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	for trs.Next() {
		var s string
		if trs.Scan(&s) == nil {
			have[s] = true
		}
	}
	trs.Close()
	for _, x := range [][3]string{{"Stundenzettel prüfen und einreichen", "month-last-workday", "jeden letzten Arbeitstag im Monat"}, {"Woche planen", "week-1", "jeden Montag"}, {"Monatsabschluss: Zeiten nachtragen", "month-25", "vor dem Monatsende"}} {
		if !have[x[0]] {
			out = append(out, map[string]any{"kind": "recurring", "key": "r:" + x[1], "title": x[0], "rule": x[1], "why": x[2], "score": 5, "when": nil})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return asFloat(out[i]["score"]) > asFloat(out[j]["score"]) })
	keys := []string{}
	for _, x := range out {
		if k := asStr(x["ticket"]); k != "" {
			keys = append(keys, strings.ToUpper(k))
		}
	}
	sys, err := a.ticketSystems(ctx, u.ID, keys)
	if err != nil {
		a.fail(w, err)
		return
	}
	seen, dedup := map[string]bool{}, []map[string]any{}
	for _, x := range out {
		k := asStr(x["key"])
		if tk := asStr(x["ticket"]); tk != "" {
			k = "t:" + strings.ToUpper(tk)
			if s := sys.of(tk); s != nil {
				x["system"] = s
			}
		}
		if seen[k] {
			continue
		}
		seen[k] = true
		dedup = append(dedup, x)
	}
	if len(dedup) > 40 {
		dedup = dedup[:40]
	}
	todoSuggest.Store(u.ID, todoSugCached{time.Now(), dedup})
	res, err := a.todoFilterDismissed(ctx, u.ID, dedup)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// todoFilterDismissed: Verworfenes, erledigte Liegengebliebene und schon geplante Tickets herausnehmen (gespeicherte Liste bleibt unverändert)
func (a *App) todoFilterDismissed(ctx context.Context, uid int64, list []map[string]any) ([]map[string]any, error) {
	dis, open, openTickets := map[string]bool{}, map[int64]bool{}, map[string]bool{}
	rows, err := a.DB.Query(ctx, `SELECT key FROM todo_dismissed WHERE user_id=$1 AND (until IS NULL OR until>=$2)`, uid, a.today())
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var k string
		if rows.Scan(&k) == nil {
			dis[k] = true
		}
	}
	rows.Close()
	rows, err = a.DB.Query(ctx, `SELECT id, UPPER(COALESCE(jira_ticket,'')) FROM plan_tasks WHERE user_id=$1 AND COALESCE(status,'open')='open'`, uid)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id int64
		var k string
		if rows.Scan(&id, &k) == nil {
			open[id] = true
			if k != "" {
				openTickets[k] = true
			}
		}
	}
	rows.Close()
	out := []map[string]any{}
	for _, x := range list {
		if dis[asStr(x["key"])] {
			continue
		}
		if id, ok := x["todo_id"].(int64); ok && !open[id] {
			continue
		}
		if x["kind"] == "ticket" && openTickets[strings.ToUpper(asStr(x["ticket"]))] {
			continue
		}
		out = append(out, x)
	}
	return out, nil
}

func (a *App) todoDismiss(w http.ResponseWriter, r *http.Request, u User) {
	var in body
	if !readJSON(w, r, &in) {
		return
	}
	k := asStr(in["key"])
	if len(k) > 200 {
		badRequest(w, "key zu lang")
		return
	}
	if k != "" {
		var until any
		if d := int(asFloat(in["days"])); d > 0 {
			until = addDays(a.today(), min(d, 3650))
		}
		if _, err := a.DB.Exec(r.Context(), `INSERT INTO todo_dismissed(user_id,key,until) VALUES($1,$2,$3) ON CONFLICT (user_id,key) DO UPDATE SET until=EXCLUDED.until`, u.ID, k, until); err != nil {
			a.fail(w, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

/* ── Vorlagen (wiederkehrend) ─────────────────────────────────────── */

func (a *App) todoTemplates(w http.ResponseWriter, r *http.Request, u User) {
	rs, err := a.maps(r.Context(), `SELECT * FROM todo_templates WHERE user_id=$1 ORDER BY id`, u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rs)
}

func (a *App) todoTemplateCreate(w http.ResponseWriter, r *http.Request, u User) {
	var in body
	if !readJSON(w, r, &in) {
		return
	}
	title, rule := strings.TrimSpace(asStr(in["title"])), asStr(in["rule"])
	if title == "" || rule == "" {
		badRequest(w, "Titel und Rhythmus fehlen")
		return
	}
	if len(title) > 500 || !todoRuleRE.MatchString(rule) {
		badRequest(w, "Titel zu lang oder Rhythmus unbekannt")
		return
	}
	ctx := r.Context()
	pid, msg := todoValue("project_id", in["project_id"])
	if msg != "" || !a.owned(ctx, "projects", pid, u.ID) {
		badRequest(w, "Projekt nicht gefunden")
		return
	}
	ticket, msg := todoValue("jira_ticket", in["jira_ticket"])
	est, msg2 := todoValue("estimated_minutes", in["estimated_minutes"])
	prio, msg3 := todoValue("priority", in["priority"])
	if m := msg + msg2 + msg3; m != "" {
		badRequest(w, m)
		return
	}
	if est == nil {
		est = 30
	}
	if prio == nil {
		prio = 2
	}
	var id int64
	if err := a.DB.QueryRow(ctx, `INSERT INTO todo_templates(user_id,title,rule,project_id,jira_ticket,estimated_minutes,priority) VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING id`,
		u.ID, title, rule, pid, ticket, est, prio).Scan(&id); err != nil {
		a.fail(w, err)
		return
	}
	todoSuggest.Delete(u.ID)
	t := a.today()
	if cal, err := a.todoCal(ctx, u.ID, t); err == nil {
		if err := a.todoGenerate(ctx, u.ID, t, cal); err != nil {
			a.Log.Warn("Vorlage anwenden", "err", err)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id})
}

func (a *App) todoTemplateDelete(w http.ResponseWriter, r *http.Request, u User) {
	id, ok := pathID(r)
	if !ok {
		notFound(w)
		return
	}
	if _, err := a.DB.Exec(r.Context(), `UPDATE todo_templates SET active=0 WHERE id=$1 AND user_id=$2`, id, u.ID); err != nil {
		a.fail(w, err)
		return
	}
	todoSuggest.Delete(u.ID)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

/* ── Für den Assistenten: was ist bei der Planung zu tun? ────────── */

type hint struct {
	ID    string `json:"id"`
	Level string `json:"level"`
	Title string `json:"title"`
	Text  string `json:"text"`
	Count int    `json:"count"`
}

func (a *App) planningHints(ctx context.Context, uid int64) ([]hint, error) {
	t := a.today()
	cal, err := a.todoCal(ctx, uid, t)
	if err != nil {
		return nil, err
	}
	now := time.Now().In(a.Loc)
	h, wd := now.Hour(), int(now.Weekday())
	tm := cal.next(t)
	n := func(sql string, args ...any) int {
		var c int
		_ = a.DB.QueryRow(ctx, sql, append([]any{uid}, args...)...).Scan(&c)
		return c
	}
	const open = `SELECT COUNT(*) FROM plan_tasks WHERE user_id=$1 AND COALESCE(status,'open')='open' AND `
	var out []hint
	if c := n(open+`((scheduled_date IS NOT NULL AND scheduled_date<$2) OR (due_date IS NOT NULL AND due_date<$2))`, t); c > 0 {
		out = append(out, hint{"todo_overdue", "warn", "Überfällige To-dos", "neu planen, erledigen oder streichen", c})
	}
	if h >= 14 && cal.workday(t) && n(open+`scheduled_date=$2`, tm) == 0 {
		d, _ := parseDay(tm)
		out = append(out, hint{"todo_plan_tomorrow", "info", weekdaysDE[d.Weekday()] + " planen", "noch nichts geplant – Vorschläge liegen bereit", 1})
	}
	if (wd == 1 && h < 12) || (wd == 5 && h >= 12) {
		ref := t
		if wd == 5 {
			ref = cal.next(t)
		}
		if n(open+`scheduled_week=$2`, isoWeekOf(ref)) < 3 {
			title := "Woche planen"
			if wd == 5 {
				title = "Nächste Woche planen"
			}
			out = append(out, hint{"todo_plan_week", "info", title, "weniger als 3 To-dos für die Woche", 1})
		}
	}
	return out, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

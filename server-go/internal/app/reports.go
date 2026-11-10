package app

import (
	"math"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

/* Bericht, Timesheet und Wochenraster – wie backend/server.js (/api/report, /api/timesheet, /api/grid), je Benutzer getrennt. */

const maxReportDays = 800

func (a *App) routesReports(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/report", a.auth(a.asTarget(a.report)))
	mux.HandleFunc("GET /api/timesheet", a.auth(a.asTarget(a.timesheet)))
	mux.HandleFunc("GET /api/grid", a.auth(a.asTarget(a.gridGet)))
	mux.HandleFunc("PUT /api/grid/cell", a.auth(a.gridCell))
}

func rangeParams(w http.ResponseWriter, r *http.Request) (from, to string, ok bool) {
	from, to = r.URL.Query().Get("from"), r.URL.Query().Get("to")
	if from == "" || to == "" {
		badRequest(w, "from + to erforderlich")
		return
	}
	f, ok1 := parseDay(from)
	t, ok2 := parseDay(to)
	if !ok1 || !ok2 || t.Before(f) || t.Sub(f) > maxReportDays*24*time.Hour {
		badRequest(w, "Zeitraum ungültig (JJJJ-MM-TT, höchstens 800 Tage)")
		return
	}
	return from, to, true
}

/* ── Bericht ─────────────────────────────────────────────────────── */

func (a *App) report(w http.ResponseWriter, r *http.Request, u User) {
	from, to, ok := rangeParams(w, r)
	if !ok {
		return
	}
	entries, err := a.maps(r.Context(), `SELECT e.*, p.name AS project_name, p.hourly_rate, p.budget_hours AS p_budget, p.jira_key AS p_jira_key,
		t.title AS task_title, t.jira_ticket, pt.title AS plan_task_title
		FROM time_entries e LEFT JOIN projects p ON p.id=e.project_id LEFT JOIN tasks t ON t.id=e.task_id LEFT JOIN plan_tasks pt ON pt.id=e.plan_task_id
		WHERE e.user_id=$1 AND e.date>=$2 AND e.date<=$3 ORDER BY e.date, e.start_time`, u.ID, from, to)
	if err != nil {
		a.fail(w, err)
		return
	}
	type sum struct {
		ProjectID    int64   `json:"project_id"`
		ProjectName  any     `json:"project_name"`
		HourlyRate   any     `json:"hourly_rate"`
		BudgetHours  float64 `json:"budget_hours"`
		JiraKey      any     `json:"jira_key"`
		TotalSeconds int64   `json:"total_seconds"`
		BillableSecs int64   `json:"billable_seconds"`
		EntryCount   int     `json:"entry_count"`
	}
	byProj := map[int64]*sum{}
	var summary []*sum // Reihenfolge des ersten Auftretens (Datum, Startzeit), wie Node
	for _, e := range entries {
		budget, _ := e["p_budget"].(float64)
		jk := e["p_jira_key"]
		delete(e, "p_budget")
		delete(e, "p_jira_key")
		pid, ok := e["project_id"].(int64)
		if !ok {
			continue
		}
		s := byProj[pid]
		if s == nil {
			s = &sum{ProjectID: pid, ProjectName: e["project_name"], HourlyRate: e["hourly_rate"], BudgetHours: budget, JiraKey: jk}
			byProj[pid] = s
			summary = append(summary, s)
		}
		d := int64(asFloat(e["duration_seconds"]))
		s.TotalSeconds += d
		if b := asFloat(e["billable"]); b != 0 {
			s.BillableSecs += d
		}
		s.EntryCount++
	}
	if summary == nil {
		summary = []*sum{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"summary": summary, "entries": entries, "from": from, "to": to})
}

/* ── Timesheet ───────────────────────────────────────────────────── */

type tsActivity struct {
	Label   string `json:"label"`
	Seconds int64  `json:"seconds"`
}

type tsDay struct {
	Date         string         `json:"date"`
	Dow          int            `json:"dow"`
	TargetH      float64        `json:"target_h"`
	FirstStart   *string        `json:"first_start"`
	LastEnd      *string        `json:"last_end"`
	WorkSeconds  int64          `json:"work_seconds"`
	PauseSeconds int64          `json:"pause_seconds"`
	SSHSeconds   int64          `json:"ssh_seconds"`
	Absence      map[string]any `json:"absence"`
	AbsenceH     float64        `json:"absence_h"`
	Activities   []tsActivity   `json:"activities"`
	Future       bool           `json:"future"`
	IstH         float64        `json:"ist_h"`
	BalanceH     float64        `json:"balance_h"`
	acts         map[string]*tsActivity
}

type tsRow struct {
	Key          string              `json:"key"`
	ProjectID    any                 `json:"project_id"`
	TaskID       any                 `json:"task_id"`
	Project      string              `json:"project"`
	ProjectKey   string              `json:"project_key"`
	Customer     string              `json:"customer"`
	Ticket       string              `json:"ticket"`
	Days         map[string]int64    `json:"days"`
	Comments     map[string][]string `json:"comments"`
	TotalSeconds int64               `json:"total_seconds"`
}

func shiftDay(s string, n int) string {
	t, ok := parseDay(s)
	if !ok {
		return s
	}
	return dayStr(t.AddDate(0, 0, n))
}

func str(v any) string { return asStr(v) }

// rowKey: „Projekt|Ticket“, fehlende Ids als 0
func rowKey(pid, tid any) string {
	p, t := str(pid), str(tid)
	if p == "" {
		p = "0"
	}
	if t == "" {
		t = "0"
	}
	return p + "|" + t
}

func (a *App) timesheet(w http.ResponseWriter, r *http.Request, u User) {
	from, to, ok := rangeParams(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	st, err := a.userSettings(ctx, u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	targets := dayTargets(st)
	var dates []string
	days := map[string]*tsDay{}
	f, _ := parseDay(from)
	t, _ := parseDay(to)
	for d := f; !d.After(t); d = d.AddDate(0, 0, 1) {
		ds := dayStr(d)
		dates = append(dates, ds)
		days[ds] = &tsDay{Date: ds, Dow: int(d.Weekday()), TargetH: targets[int(d.Weekday())], acts: map[string]*tsActivity{}}
	}

	// Einträge ±1 Tag holen, dann nach lokalem Datum der Startzeit zuordnen
	entries, err := a.maps(ctx, `SELECT e.project_id, e.task_id, e.start_time, e.end_time, e.duration_seconds, e.comment, e.is_absence,
		p.name AS project_name, p.jira_key AS project_key, t.title AS task_title, t.jira_ticket AS task_ticket
		FROM time_entries e LEFT JOIN projects p ON p.id=e.project_id LEFT JOIN tasks t ON t.id=e.task_id
		WHERE e.user_id=$1 AND e.date BETWEEN $2 AND $3 ORDER BY e.start_time`, u.ID, shiftDay(from, -1), shiftDay(to, 1))
	if err != nil {
		a.fail(w, err)
		return
	}
	rows := map[string]*tsRow{}
	for _, e := range entries {
		if asFloat(e["is_absence"]) != 0 { // Tempo-Abwesenheitsprojekt zählt nicht als Arbeitszeit
			continue
		}
		date := a.dateOf(str(e["start_time"]))
		day := days[date]
		if day == nil {
			continue
		}
		sec := int64(asFloat(e["duration_seconds"]))
		if sec == 0 && str(e["end_time"]) != "" {
			if d, ok := a.seconds(str(e["start_time"]), str(e["end_time"])); ok {
				sec = d
			}
		}
		day.WorkSeconds += sec
		stt := str(e["start_time"])
		if day.FirstStart == nil || stt < *day.FirstStart {
			day.FirstStart = &stt
		}
		end := str(e["end_time"])
		if end == "" {
			end = stt
		}
		if day.LastEnd == nil || end > *day.LastEnd {
			day.LastEnd = &end
		}
		key := rowKey(e["project_id"], e["task_id"])
		row := rows[key]
		if row == nil {
			proj := str(e["project_name"])
			if proj == "" {
				proj = "Ohne Projekt"
			}
			ticket := str(e["task_ticket"])
			if ticket == "" {
				ticket = str(e["task_title"])
			}
			row = &tsRow{Key: key, ProjectID: e["project_id"], TaskID: e["task_id"], Project: proj, ProjectKey: str(e["project_key"]),
				Ticket: ticket, Days: map[string]int64{}, Comments: map[string][]string{}}
			rows[key] = row
		}
		row.Days[date] += sec
		row.TotalSeconds += sec
		if c := str(e["comment"]); c != "" && !contains(row.Comments[date], c) {
			row.Comments[date] = append(row.Comments[date], c)
		}
		act := day.acts[key]
		if act == nil {
			label := row.Project
			if row.Ticket != "" {
				label += " · " + row.Ticket
			}
			act = &tsActivity{Label: label}
			day.acts[key] = act
		}
		act.Seconds += sec
	}

	// Pausen (nach lokalem Datum der Startzeit)
	prow, err := a.DB.Query(ctx, `SELECT start_time, end_time FROM pauses WHERE user_id=$1 AND date BETWEEN $2 AND $3 AND end_time IS NOT NULL`, u.ID, shiftDay(from, -1), shiftDay(to, 1))
	if err != nil {
		a.fail(w, err)
		return
	}
	for prow.Next() {
		var ps, pe string
		if err := prow.Scan(&ps, &pe); err != nil {
			prow.Close()
			a.fail(w, err)
			return
		}
		if day := days[a.dateOf(ps)]; day != nil {
			if secs, ok := a.seconds(ps, pe); ok {
				day.PauseSeconds += secs
			}
		}
	}
	prow.Close()

	// Abwesenheiten
	abs, err := a.maps(ctx, `SELECT * FROM absences WHERE user_id=$1 AND date_to >= $2 AND date_from <= $3 ORDER BY id`, u.ID, from, to)
	if err != nil {
		a.fail(w, err)
		return
	}
	for _, ab := range abs {
		for _, date := range dates {
			if date >= str(ab["date_from"]) && date <= str(ab["date_to"]) {
				d := days[date]
				d.Absence = map[string]any{"type": ab["type"], "note": ab["note"]}
				if h, ok := ab["hours"].(float64); ok {
					d.AbsenceH = h
				} else {
					d.AbsenceH = d.TargetH
				}
			}
		}
	}

	today := a.today()
	list := make([]*tsDay, 0, len(dates))
	var wsec, psec int64
	var tgt, tgtTo, ist, bal float64
	for _, date := range dates {
		d := days[date]
		istH := float64(d.WorkSeconds) / 3600
		if d.Absence != nil {
			istH += d.AbsenceH
		}
		d.Future = date > today // Zukunft zählt nicht ins +/−
		d.IstH = r2(istH)
		if !d.Future {
			d.BalanceH = r2(istH - d.TargetH)
		}
		acts := make([]tsActivity, 0, len(d.acts))
		for _, x := range d.acts {
			acts = append(acts, *x)
		}
		sort.Slice(acts, func(i, j int) bool {
			if acts[i].Seconds != acts[j].Seconds {
				return acts[i].Seconds > acts[j].Seconds
			}
			return acts[i].Label < acts[j].Label
		})
		d.Activities = acts
		list = append(list, d)
		wsec += d.WorkSeconds
		psec += d.PauseSeconds
		tgt += d.TargetH
		if !d.Future {
			tgtTo += d.TargetH
		}
		ist += d.IstH
		bal += d.BalanceH
	}
	rowList := make([]*tsRow, 0, len(rows))
	for _, x := range rows {
		rowList = append(rowList, x)
	}
	sort.Slice(rowList, func(i, j int) bool {
		x, y := strings.ToLower(rowList[i].Project+rowList[i].Ticket), strings.ToLower(rowList[j].Project+rowList[j].Ticket)
		if x != y {
			return x < y
		}
		return rowList[i].Key < rowList[j].Key
	})
	writeJSON(w, http.StatusOK, map[string]any{"from": from, "to": to, "days": list, "rows": rowList,
		"totals": map[string]any{"work_seconds": wsec, "pause_seconds": psec, "target_h": tgt, "target_to_date_h": tgtTo,
			"ist_h": r2(ist), "balance_h": r2(bal)}})
}

/* ── Wochenraster (wie Tempo-Timesheet): Zeilen = Ticket/Projekt, Spalten = Tage ── */

var gridLocked = map[string]bool{"tempo": true, "tempo_csv": true, "plane": true, "plane_import": true} // importierte Buchungen nicht über das Raster ändern
var ticketKey = regexp.MustCompile(`^[A-Z][A-Z0-9_]*-\d+$`)

func (a *App) gridGet(w http.ResponseWriter, r *http.Request, u User) {
	from, to, ok := rangeParams(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	planeIdents := map[string]bool{}
	ir, err := a.DB.Query(ctx, `SELECT UPPER(plane_identifier) FROM projects WHERE user_id=$1 AND plane_project_id IS NOT NULL AND plane_identifier IS NOT NULL`, u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	for ir.Next() {
		var s string
		if err := ir.Scan(&s); err != nil {
			ir.Close()
			a.fail(w, err)
			return
		}
		planeIdents[s] = true
	}
	ir.Close()

	entries, err := a.maps(ctx, `SELECT e.id, e.project_id, e.task_id, e.date, e.start_time, e.end_time, e.duration_seconds, e.comment, e.source, e.is_absence,
		e.tempo_push_id, e.tempo_account, t.jira_ticket, t.title AS task_title, t.plane_work_item_id AS task_wi, p.name AS project_name
		FROM time_entries e LEFT JOIN tasks t ON t.id=e.task_id LEFT JOIN projects p ON p.id=e.project_id
		WHERE e.user_id=$1 AND e.date>=$2 AND e.date<=$3 AND e.end_time IS NOT NULL AND e.duration_seconds > 0 ORDER BY e.date, e.start_time`, u.ID, from, to)
	if err != nil {
		a.fail(w, err)
		return
	}
	type row struct {
		Key         string                      `json:"key"`
		ProjectID   any                         `json:"project_id"`
		TaskID      any                         `json:"task_id"`
		Ticket      any                         `json:"ticket"`
		System      any                         `json:"system"`
		Absence     bool                        `json:"absence"`
		Title       any                         `json:"title"`
		ProjectName any                         `json:"project_name"`
		Cells       map[string]int64            `json:"cells"`
		Locked      map[string]bool             `json:"locked"`
		Items       map[string][]map[string]any `json:"items"`
		Total       int64                       `json:"total"`
	}
	rows := map[string]*row{}
	var order []string
	for _, e := range entries {
		k := rowKey(e["project_id"], e["task_id"])
		tk := strings.ToUpper(str(e["jira_ticket"]))
		var system any
		if ticketKey.MatchString(tk) {
			if str(e["task_wi"]) != "" || planeIdents[strings.SplitN(tk, "-", 2)[0]] {
				system = "plane"
			} else {
				system = "jira"
			}
		}
		x := rows[k]
		if x == nil {
			x = &row{Key: k, ProjectID: e["project_id"], TaskID: e["task_id"], Ticket: nzAny(e["jira_ticket"]), System: system,
				Absence: asFloat(e["is_absence"]) != 0, Title: e["task_title"], ProjectName: e["project_name"],
				Cells: map[string]int64{}, Locked: map[string]bool{}, Items: map[string][]map[string]any{}}
			rows[k] = x
			order = append(order, k)
		}
		date, dur := str(e["date"]), int64(asFloat(e["duration_seconds"]))
		x.Cells[date] += dur
		x.Items[date] = append(x.Items[date], map[string]any{"id": e["id"], "start_time": e["start_time"], "end_time": e["end_time"],
			"duration_seconds": dur, "comment": str(e["comment"]), "source": str(e["source"]), "tempo": e["tempo_push_id"] != nil && str(e["tempo_push_id"]) != "",
			"account": e["tempo_account"]})
		if gridLocked[str(e["source"])] {
			x.Locked[date] = true
		}
		x.Total += dur
	}
	list := make([]*row, 0, len(order))
	for _, k := range order {
		list = append(list, rows[k])
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].Total > list[j].Total })
	writeJSON(w, http.StatusOK, map[string]any{"from": from, "to": to, "rows": list})
}

func nzAny(v any) any {
	if str(v) == "" {
		return nil
	}
	return v
}

// gridCell setzt eine Zelle auf eine Gesamtdauer: mehr → neuer Eintrag, weniger → letzte Einträge kürzen/löschen.
func (a *App) gridCell(w http.ResponseWriter, r *http.Request, u User) {
	var in body
	if !readJSON(w, r, &in) {
		return
	}
	date := str(in["date"])
	if _, ok := parseDay(date); !ok {
		badRequest(w, "Datum fehlt")
		return
	}
	if invalidID(in["project_id"]) || invalidID(in["task_id"]) {
		badRequest(w, "project_id/task_id ungültig")
		return
	}
	pid, tid := asID(in["project_id"]), asID(in["task_id"])
	if pid == nil {
		badRequest(w, "Projekt fehlt")
		return
	}
	ctx := r.Context()
	if !a.owned(ctx, "projects", pid, u.ID) || !a.owned(ctx, "tasks", tid, u.ID) {
		badRequest(w, "Projekt oder Ticket nicht gefunden")
		return
	}
	target := int64(math.Max(0, math.Round(asFloat(in["seconds"]))))
	if target > 24*3600 {
		badRequest(w, "Mehr als 24 Stunden an einem Tag")
		return
	}
	type ent struct {
		id         int64
		start, src string
		dur        int64
	}
	var tooLow string
	err := a.inTx(ctx, func(tx pgx.Tx) error {
		// Einträge der Zelle sperren, damit zwei gleichzeitige Änderungen nicht mit veralteten Daten arbeiten
		rs, err := tx.Query(ctx, `SELECT id, start_time, COALESCE(source,''), COALESCE(duration_seconds,0) FROM time_entries
			WHERE user_id=$1 AND date=$2 AND project_id=$3 AND task_id IS NOT DISTINCT FROM $4 AND end_time IS NOT NULL ORDER BY start_time FOR UPDATE`, u.ID, date, pid, tid)
		if err != nil {
			return err
		}
		var list []ent
		for rs.Next() {
			var e ent
			if err := rs.Scan(&e.id, &e.start, &e.src, &e.dur); err != nil {
				rs.Close()
				return err
			}
			list = append(list, e)
		}
		rs.Close()
		if err := rs.Err(); err != nil {
			return err
		}
		var fixed, current int64
		var editable []ent
		for _, e := range list {
			current += e.dur
			if gridLocked[e.src] {
				fixed += e.dur
			} else {
				editable = append(editable, e)
			}
		}
		if target < fixed {
			tooLow = "Mindestens " + str(math.Round(float64(fixed)/60)) + " min stammen aus Tempo/Plane und lassen sich hier nicht ändern"
			return nil
		}
		if target > current {
			// neuen Block hinter die letzte Buchung des Tages legen (sonst 08:00 Uhr)
			var last *string
			if err := tx.QueryRow(ctx, `SELECT MAX(end_time) FROM time_entries WHERE user_id=$1 AND date=$2 AND end_time IS NOT NULL`, u.ID, date).Scan(&last); err != nil {
				return err
			}
			day, _ := parseDay(date)
			start := time.Date(day.Year(), day.Month(), day.Day(), 8, 0, 0, 0, a.Loc)
			if last != nil {
				if t, ok := a.parseTime(*last); ok && a.dateOf(*last) == date {
					start = t
				}
			}
			add := target - current
			end := start.Add(time.Duration(add) * time.Second)
			_, err := tx.Exec(ctx, `INSERT INTO time_entries(user_id,project_id,task_id,start_time,end_time,duration_seconds,comment,billable,date,source,activity)
				VALUES($1,$2,$3,$4,$5,$6,NULL,1,$7,'grid',$8)`, u.ID, pid, tid, start.UTC().Format(isoMillis), end.UTC().Format(isoMillis), add, date, nz(in["activity"]))
			return err
		}
		cut := current - target
		for i := len(editable) - 1; i >= 0 && cut > 0; i-- {
			e := editable[i]
			if e.dur <= cut {
				if err := a.deleteEntryTx(ctx, tx, u.ID, e.id); err != nil {
					return err
				}
				cut -= e.dur
				continue
			}
			nd := e.dur - cut
			st, ok := a.parseTime(e.start)
			if !ok {
				tooLow = "Startzeit eines Eintrags nicht lesbar"
				return nil
			}
			if _, err := tx.Exec(ctx, `UPDATE time_entries SET duration_seconds=$3, end_time=$4, version=version+1, updated_at=now() WHERE id=$1 AND user_id=$2`,
				e.id, u.ID, nd, st.Add(time.Duration(nd)*time.Second).UTC().Format(isoMillis)); err != nil {
				return err
			}
			cut = 0
		}
		return nil
	})
	if err != nil {
		a.fail(w, err)
		return
	}
	if tooLow != "" {
		badRequest(w, tooLow)
		return
	}
	a.syncAuto(u.ID)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "seconds": target})
}

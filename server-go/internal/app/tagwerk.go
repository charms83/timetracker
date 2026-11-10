package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

/* Tagesansicht, Teil 1: Planung (plan_tasks), freie Pausen, Tagesnotizen, Tätigkeitsarten, Health.
   Verhalten wie backend/server.js; je Benutzer getrennt. Das Arbeitstag-Konzept (work_days) entfällt,
   Pausen hängen daher nie an einem Arbeitstag. */

func (a *App) routesTagwerk(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "version": ServerVersion()})
	})
	mux.HandleFunc("GET /api/plan/tasks", a.auth(a.planList))
	mux.HandleFunc("GET /api/plan/today", a.auth(a.planToday))
	mux.HandleFunc("POST /api/plan/tasks", a.auth(a.planCreate))
	mux.HandleFunc("PUT /api/plan/tasks/{id}", a.auth(a.planUpdate))
	mux.HandleFunc("DELETE /api/plan/tasks/{id}", a.auth(a.planDelete))
	mux.HandleFunc("POST /api/plan/tasks/{id}/move", a.auth(a.planMove))
	mux.HandleFunc("POST /api/plan/rollover", a.auth(a.planRollover))
	mux.HandleFunc("POST /api/pauses", a.auth(a.pauseCreate))
	mux.HandleFunc("PUT /api/pauses/{id}", a.auth(a.pauseUpdate))
	mux.HandleFunc("DELETE /api/pauses/{id}", a.auth(a.pauseDelete))
	mux.HandleFunc("POST /api/daily-log", a.auth(a.logCreate))
	mux.HandleFunc("PUT /api/daily-log/{id}", a.auth(a.logUpdate))
	mux.HandleFunc("DELETE /api/daily-log/{id}", a.auth(a.logDelete))
	mux.HandleFunc("GET /api/activities", a.auth(a.activitiesGet))
	mux.HandleFunc("PUT /api/activities", a.auth(a.activitiesPut))
}

/* ── Planung ─────────────────────────────────────────────────────── */

const planSelect = `SELECT p.*, pr.name AS project_name,
	(SELECT COALESCE(SUM(e.duration_seconds),0) FROM time_entries e WHERE e.plan_task_id=p.id) AS tracked_seconds
	FROM plan_tasks p LEFT JOIN projects pr ON pr.id=p.project_id`

func (a *App) planList(w http.ResponseWriter, r *http.Request, u User) {
	q := r.URL.Query()
	sql := planSelect + ` WHERE p.user_id=$1`
	args := []any{u.ID}
	add := func(cond, v string) {
		args = append(args, v)
		sql += " AND " + fmt.Sprintf(cond, len(args))
	}
	if v := q.Get("date"); v != "" {
		add("(p.scheduled_date=$%d OR (p.scheduled_date IS NULL AND p.scheduled_week IS NULL))", v)
	}
	if v := q.Get("week"); v != "" {
		add("p.scheduled_week=$%d", v)
	}
	if v := q.Get("status"); v != "" {
		add("p.status=$%d", v)
	} else {
		sql += " AND p.status <> 'done'"
	}
	rs, err := a.maps(r.Context(), sql+" ORDER BY p.priority ASC, p.sort_order ASC, p.created_at ASC", args...)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rs)
}

func (a *App) planToday(w http.ResponseWriter, r *http.Request, u User) {
	d := r.URL.Query().Get("date")
	if d == "" {
		d = a.today()
	}
	ctx := r.Context()
	todayTasks, err := a.maps(ctx, planSelect+` WHERE p.user_id=$1 AND p.scheduled_date=$2 AND p.status<>'done' ORDER BY p.priority, p.sort_order`, u.ID, d)
	if err != nil {
		a.fail(w, err)
		return
	}
	inbox, err := a.maps(ctx, planSelect+` WHERE p.user_id=$1 AND p.scheduled_date IS NULL AND p.scheduled_week IS NULL AND p.status='open'
		ORDER BY p.priority, p.sort_order LIMIT 10`, u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"date": d, "todayTasks": todayTasks, "inbox": inbox})
}

func intOr(v any, def int64) int64 {
	if n := int64(asFloat(v)); n != 0 {
		return n
	}
	return def
}

func (a *App) planCreate(w http.ResponseWriter, r *http.Request, u User) {
	var in body
	if !readJSON(w, r, &in) {
		return
	}
	title := strings.TrimSpace(asStr(in["title"]))
	if title == "" {
		badRequest(w, "Titel fehlt")
		return
	}
	if invalidID(in["project_id"]) {
		badRequest(w, "project_id ungültig")
		return
	}
	pid := asID(in["project_id"])
	if !a.owned(r.Context(), "projects", pid, u.ID) {
		badRequest(w, "Projekt nicht gefunden")
		return
	}
	row, err := a.one(r.Context(), `INSERT INTO plan_tasks(user_id,title,project_id,jira_ticket,estimated_minutes,priority,scheduled_date,scheduled_week,recurrence,notes)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING id`,
		u.ID, title, pid, nz(in["jira_ticket"]), intOr(in["estimated_minutes"], 60), intOr(in["priority"], 2),
		nz(in["scheduled_date"]), nz(in["scheduled_week"]), nz(in["recurrence"]), nz(in["notes"]))
	if err != nil {
		a.fail(w, err)
		return
	}
	a.planReply(w, r, u, row["id"].(int64))
}

func (a *App) planReply(w http.ResponseWriter, r *http.Request, u User, id int64) {
	row, err := a.one(r.Context(), planSelect+` WHERE p.id=$1 AND p.user_id=$2`, id, u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	if row == nil {
		notFound(w)
		return
	}
	writeJSON(w, http.StatusOK, row)
}

func (a *App) planUpdate(w http.ResponseWriter, r *http.Request, u User) {
	id, ok := pathID(r)
	if !ok {
		notFound(w)
		return
	}
	var in body
	if !readJSON(w, r, &in) {
		return
	}
	title := strings.TrimSpace(asStr(in["title"]))
	if title == "" {
		badRequest(w, "Titel fehlt")
		return
	}
	if invalidID(in["project_id"]) {
		badRequest(w, "project_id ungültig")
		return
	}
	pid := asID(in["project_id"])
	if !a.owned(r.Context(), "projects", pid, u.ID) {
		badRequest(w, "Projekt nicht gefunden")
		return
	}
	status := asStr(in["status"])
	if status == "" {
		status = "open"
	}
	tag, err := a.DB.Exec(r.Context(), `UPDATE plan_tasks SET title=$3,project_id=$4,jira_ticket=$5,estimated_minutes=$6,priority=$7,status=$8,
		scheduled_date=$9,scheduled_week=$10,recurrence=$11,notes=$12,completed_at=$13 WHERE id=$1 AND user_id=$2`,
		id, u.ID, title, pid, nz(in["jira_ticket"]), intOr(in["estimated_minutes"], 60), intOr(in["priority"], 2), status,
		nz(in["scheduled_date"]), nz(in["scheduled_week"]), nz(in["recurrence"]), nz(in["notes"]), nz(in["completed_at"]))
	if err != nil {
		a.fail(w, err)
		return
	}
	if tag.RowsAffected() == 0 {
		notFound(w)
		return
	}
	a.planReply(w, r, u, id)
}

func (a *App) planDelete(w http.ResponseWriter, r *http.Request, u User) {
	id, ok := pathID(r)
	if !ok {
		notFound(w)
		return
	}
	if _, err := a.DB.Exec(r.Context(), `DELETE FROM plan_tasks WHERE id=$1 AND user_id=$2`, id, u.ID); err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// nextWorkday: der Tag danach, Wochenende wird übersprungen
func nextWorkday(from string) (string, bool) {
	t, ok := parseDay(from)
	if !ok {
		return "", false
	}
	t = t.AddDate(0, 0, 1)
	for t.Weekday() == time.Saturday || t.Weekday() == time.Sunday {
		t = t.AddDate(0, 0, 1)
	}
	return dayStr(t), true
}

// nextWeek: „2026-W45“ → „2026-W46“ (ISO-Wochen, Jahreswechsel richtig)
func nextWeek(s string) (string, bool) {
	parts := strings.SplitN(s, "-W", 2)
	if len(parts) != 2 {
		return "", false
	}
	y, e1 := strconv.Atoi(parts[0])
	wk, e2 := strconv.Atoi(parts[1])
	if e1 != nil || e2 != nil || wk < 1 || wk > 53 {
		return "", false
	}
	jan4 := time.Date(y, 1, 4, 0, 0, 0, 0, time.UTC)
	monday1 := jan4.AddDate(0, 0, -((int(jan4.Weekday()) + 6) % 7)) // Montag der KW 1
	ny, nw := monday1.AddDate(0, 0, wk*7).ISOWeek()
	return fmt.Sprintf("%d-W%02d", ny, nw), true
}

func (a *App) planMove(w http.ResponseWriter, r *http.Request, u User) {
	id, ok := pathID(r)
	if !ok {
		notFound(w)
		return
	}
	var date, week *string
	err := a.DB.QueryRow(r.Context(), `SELECT scheduled_date, scheduled_week FROM plan_tasks WHERE id=$1 AND user_id=$2`, id, u.ID).Scan(&date, &week)
	if err != nil {
		notFound(w)
		return
	}
	if date != nil && *date != "" {
		if nd, ok := nextWorkday(*date); ok {
			_, err = a.DB.Exec(r.Context(), `UPDATE plan_tasks SET scheduled_date=$3,status='open' WHERE id=$1 AND user_id=$2`, id, u.ID, nd)
		}
	} else if week != nil && *week != "" {
		if nw, ok := nextWeek(*week); ok {
			_, err = a.DB.Exec(r.Context(), `UPDATE plan_tasks SET scheduled_week=$3,status='open' WHERE id=$1 AND user_id=$2`, id, u.ID, nw)
		}
	}
	if err != nil {
		a.fail(w, err)
		return
	}
	row, err := a.one(r.Context(), `SELECT * FROM plan_tasks WHERE id=$1 AND user_id=$2`, id, u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, row)
}

// planRollover (Tagesabschluss): offene Aufgaben des Tages auf den nächsten Arbeitstag schieben
func (a *App) planRollover(w http.ResponseWriter, r *http.Request, u User) {
	var in body
	if !readJSON(w, r, &in) {
		return
	}
	from := asStr(in["from_date"])
	if from == "" {
		badRequest(w, "from_date fehlt")
		return
	}
	next, ok := nextWorkday(from)
	if !ok {
		badRequest(w, "from_date ungültig")
		return
	}
	tag, err := a.DB.Exec(r.Context(), `UPDATE plan_tasks SET scheduled_date=$3,status='open' WHERE user_id=$1 AND scheduled_date=$2 AND status='open'`, u.ID, from, next)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"moved": tag.RowsAffected(), "to": next})
}

/* ── Freie Pausen ────────────────────────────────────────────────── */

func clock(iso string) string {
	if len(iso) >= 16 {
		return iso[11:16]
	}
	return iso
}

// pauseOverlap liefert die überschneidende Pause desselben Tages (Zeiten als Text im ISO-Format verglichen)
func (a *App) pauseOverlap(r *http.Request, u User, date, start, end string, exclude int64) (string, error) {
	var s, e string
	err := a.DB.QueryRow(r.Context(), `SELECT start_time, end_time FROM pauses WHERE user_id=$1 AND date=$2 AND end_time IS NOT NULL AND id<>$3
		AND $4 < end_time AND start_time < $5 LIMIT 1`, u.ID, date, exclude, start, end).Scan(&s, &e)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	return fmt.Sprintf("Überschneidet sich mit bestehender Pause %s–%s Uhr", clock(s), clock(e)), nil
}

func (a *App) pauseCreate(w http.ResponseWriter, r *http.Request, u User) {
	var in body
	if !readJSON(w, r, &in) {
		return
	}
	start, end := asStr(in["start_time"]), asStr(in["end_time"])
	if start == "" {
		badRequest(w, "start_time erforderlich")
		return
	}
	if end == "" {
		badRequest(w, "end_time erforderlich")
		return
	}
	if end <= start {
		badRequest(w, "Ende liegt vor Start")
		return
	}
	secs, ok := a.seconds(start, end)
	if !ok {
		badRequest(w, "Zeit nicht lesbar")
		return
	}
	if secs < 300 {
		badRequest(w, "Pause muss mindestens 5 Minuten lang sein")
		return
	}
	date := asStr(in["date"])
	if date == "" {
		date = a.dateOf(start)
	}
	if msg, err := a.pauseOverlap(r, u, date, start, end, -1); err != nil {
		a.fail(w, err)
		return
	} else if msg != "" {
		badRequest(w, msg)
		return
	}
	row, err := a.one(r.Context(), `INSERT INTO pauses(user_id,work_day_id,start_time,end_time,date) VALUES($1,NULL,$2,$3,$4) RETURNING *`, u.ID, start, end, date)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, row)
}

func (a *App) pauseUpdate(w http.ResponseWriter, r *http.Request, u User) {
	id, ok := pathID(r)
	if !ok {
		notFound(w)
		return
	}
	var in body
	if !readJSON(w, r, &in) {
		return
	}
	start, end := asStr(in["start_time"]), asStr(in["end_time"])
	if start == "" {
		badRequest(w, "start_time fehlt")
		return
	}
	var date string
	if err := a.DB.QueryRow(r.Context(), `SELECT date FROM pauses WHERE id=$1 AND user_id=$2`, id, u.ID).Scan(&date); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "Pause nicht gefunden"})
		return
	}
	if end != "" {
		if end <= start {
			badRequest(w, "Ende liegt vor Start")
			return
		}
		if msg, err := a.pauseOverlap(r, u, date, start, end, id); err != nil {
			a.fail(w, err)
			return
		} else if msg != "" {
			badRequest(w, msg)
			return
		}
	}
	row, err := a.one(r.Context(), `UPDATE pauses SET start_time=$3, end_time=$4 WHERE id=$1 AND user_id=$2 RETURNING *`, id, u.ID, start, nz(end))
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, row)
}

func (a *App) pauseDelete(w http.ResponseWriter, r *http.Request, u User) {
	id, ok := pathID(r)
	if !ok {
		notFound(w)
		return
	}
	if _, err := a.DB.Exec(r.Context(), `DELETE FROM pauses WHERE id=$1 AND user_id=$2`, id, u.ID); err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

/* ── Tagesnotizen ────────────────────────────────────────────────── */

func (a *App) logCreate(w http.ResponseWriter, r *http.Request, u User) {
	var in body
	if !readJSON(w, r, &in) {
		return
	}
	text := strings.TrimSpace(asStr(in["text"]))
	if text == "" {
		badRequest(w, "text fehlt")
		return
	}
	row, err := a.one(r.Context(), `INSERT INTO daily_logs(user_id,work_day_id,timestamp,text,date) VALUES($1,NULL,$2,$3,$4) RETURNING *`,
		u.ID, time.Now().UTC().Format(isoMillis), text, a.today())
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, row)
}

func (a *App) logUpdate(w http.ResponseWriter, r *http.Request, u User) {
	id, ok := pathID(r)
	var in body
	if !ok {
		notFound(w)
		return
	}
	if !readJSON(w, r, &in) {
		return
	}
	text := strings.TrimSpace(asStr(in["text"]))
	if text == "" {
		badRequest(w, "text fehlt")
		return
	}
	row, err := a.one(r.Context(), `UPDATE daily_logs SET text=$3 WHERE id=$1 AND user_id=$2 RETURNING *`, id, u.ID, text)
	if err != nil {
		a.fail(w, err)
		return
	}
	if row == nil {
		notFound(w)
		return
	}
	writeJSON(w, http.StatusOK, row)
}

func (a *App) logDelete(w http.ResponseWriter, r *http.Request, u User) {
	id, ok := pathID(r)
	if !ok {
		notFound(w)
		return
	}
	if _, err := a.DB.Exec(r.Context(), `DELETE FROM daily_logs WHERE id=$1 AND user_id=$2`, id, u.ID); err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

/* ── Tätigkeitsarten (Liste in user_settings.activities als JSON) ── */

var defaultActivities = []string{"Entwicklung", "Konzeption", "Meeting", "Support", "Administration", "Dokumentation", "Fahrzeit"}

func (a *App) activityList(r *http.Request, u User) ([]string, error) {
	var raw *string
	if err := a.DB.QueryRow(r.Context(), `SELECT value FROM user_settings WHERE user_id=$1 AND key='activities'`, u.ID).Scan(&raw); err != nil || raw == nil {
		return defaultActivities, nil
	}
	var list []string
	if json.Unmarshal([]byte(*raw), &list) != nil {
		return defaultActivities, nil
	}
	out := list[:0]
	for _, x := range list {
		if strings.TrimSpace(x) != "" {
			out = append(out, x)
		}
	}
	return out, nil
}

func (a *App) activitiesGet(w http.ResponseWriter, r *http.Request, u User) {
	list, _ := a.activityList(r, u)
	writeJSON(w, http.StatusOK, list)
}

func (a *App) activitiesPut(w http.ResponseWriter, r *http.Request, u User) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
	var in []any
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		badRequest(w, "Liste erwartet")
		return
	}
	seen := map[string]bool{}
	list := []string{}
	for _, v := range in {
		s := strings.TrimSpace(asStr(v))
		if s != "" && !seen[s] && len(list) < 40 {
			seen[s] = true
			list = append(list, s)
		}
	}
	raw, _ := json.Marshal(list)
	if _, err := a.DB.Exec(r.Context(), `INSERT INTO user_settings(user_id,key,value) VALUES($1,'activities',$2)
		ON CONFLICT (user_id,key) DO UPDATE SET value=EXCLUDED.value`, u.ID, string(raw)); err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
)

/* Zeiteinträge – Verhalten wie backend/server.js (/api/time-entries), je Benutzer getrennt.
   Die Schreibwege (entryCreate/entryUpdate/entryDelete) nutzt auch das Nachtragen durch Teamleitung/Personal (nachtrag.go):
   gleiche Prüfungen, Ziel ist dann eine andere Person (entryWriter). */

const entrySelect = `SELECT e.*, p.name AS project_name, p.hourly_rate,
	t.title AS task_title, t.jira_ticket, pt.title AS plan_task_title
	FROM time_entries e
	LEFT JOIN projects p ON p.id=e.project_id
	LEFT JOIN tasks t ON t.id=e.task_id
	LEFT JOIN plan_tasks pt ON pt.id=e.plan_task_id`

// entrySelectEB: wie entrySelect, zusätzlich Name derjenigen, die nachgetragen hat (entered_by, Migration 018)
const entrySelectEB = `SELECT e.*, p.name AS project_name, p.hourly_rate,
	t.title AS task_title, t.jira_ticket, pt.title AS plan_task_title, eb.name AS entered_by_name
	FROM time_entries e
	LEFT JOIN projects p ON p.id=e.project_id
	LEFT JOIN tasks t ON t.id=e.task_id
	LEFT JOIN plan_tasks pt ON pt.id=e.plan_task_id
	LEFT JOIN users eb ON eb.id=e.entered_by`

func (a *App) routesEntries(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/time-entries", a.auth(a.asTarget(a.entriesList)))
	mux.HandleFunc("POST /api/time-entries", a.auth(a.entriesCreate))
	mux.HandleFunc("PUT /api/time-entries/{id}", a.auth(a.entriesUpdate))
	mux.HandleFunc("DELETE /api/time-entries/{id}", a.auth(a.entriesDelete))
}

func (a *App) entryByID(r *http.Request, uid, id int64) (map[string]any, error) {
	return a.one(r.Context(), entrySelectEB+` WHERE e.id=$1 AND e.user_id=$2`, id, uid)
}

func (a *App) entriesList(w http.ResponseWriter, r *http.Request, u User) {
	q := r.URL.Query()
	sql := entrySelectEB + ` WHERE e.user_id=$1`
	args := []any{u.ID}
	add := func(cond, val string) {
		args = append(args, val)
		sql += fmt.Sprintf(" AND "+cond, len(args))
	}
	if v := q.Get("from"); v != "" {
		add("e.date>=$%d", v)
	}
	if v := q.Get("to"); v != "" {
		add("e.date<=$%d", v)
	}
	if v := q.Get("date"); v != "" {
		add("e.date=$%d", v)
	}
	if invalidID(q.Get("projectId")) {
		badRequest(w, "projectId ungültig")
		return
	}
	if v := asID(q.Get("projectId")); v != nil {
		args = append(args, v)
		sql += fmt.Sprintf(" AND e.project_id=$%d", len(args))
	}
	rs, err := a.maps(r.Context(), sql+" ORDER BY e.start_time DESC", args...)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rs)
}

// seconds liefert end−start in Sekunden (nicht negativ); ok=false, wenn ein Wert nicht lesbar ist.
func (a *App) seconds(start, end string) (int64, bool) {
	s, ok1 := a.parseTime(start)
	e, ok2 := a.parseTime(end)
	if !ok1 || !ok2 {
		return 0, false
	}
	d := int64(e.Sub(s).Seconds() + 0.5)
	if d < 0 {
		d = 0
	}
	return d, true
}

func (a *App) entriesCreate(w http.ResponseWriter, r *http.Request, u User) {
	var in body
	if !readJSON(w, r, &in) {
		return
	}
	a.entryCreate(w, r, selfWriter(u), in)
}

// entryCreate: neuer Eintrag für wr.uid (eigene Daten oder Nachtrag)
func (a *App) entryCreate(w http.ResponseWriter, r *http.Request, wr entryWriter, in body) {
	start := asStr(in["start_time"])
	if start == "" {
		badRequest(w, "start_time erforderlich")
		return
	}
	ctx := r.Context()
	if invalidID(in["project_id"]) || invalidID(in["task_id"]) {
		badRequest(w, "project_id/task_id ungültig")
		return
	}
	if _, ok := a.parseTime(start); !ok {
		badRequest(w, "start_time nicht lesbar")
		return
	}
	if e := asStr(in["end_time"]); e != "" {
		if _, ok := a.parseTime(e); !ok {
			badRequest(w, "end_time nicht lesbar")
			return
		}
	}
	pid, tid := asID(in["project_id"]), asID(in["task_id"])
	if !a.owned(ctx, "projects", pid, wr.uid) || !a.owned(ctx, "tasks", tid, wr.uid) {
		badRequest(w, "Projekt oder Ticket nicht gefunden")
		return
	}
	pid, tid, _ = a.redirect(ctx, wr.uid, pid, tid) // abgeschlossenes Projekt → Nachfolger
	end := asStr(in["end_time"])
	dur := int64(asFloat(in["duration_seconds"]))
	// Dauer immer aus Beginn/Ende berechnen, wenn nicht angegeben
	if end != "" && dur == 0 {
		if d, ok := a.seconds(start, end); ok {
			dur = d
		}
	}
	tooShort := end != "" && dur > 0 && dur < 300
	date := asStr(in["date"])
	if date == "" {
		date = a.dateOf(start)
	}
	if wr.nachtrag() && !isoDateRE.MatchString(date) {
		badRequest(w, "Datum ungültig")
		return
	}
	cr, stop := a.guard(w, r, wr.uid, in, candidate{Date: date, Start: start, End: end})
	if stop {
		return
	}
	billable := 1
	if b, ok := in["billable"].(bool); ok && !b {
		billable = 0
	}
	var durArg any
	if dur != 0 {
		durArg = dur
	}
	var newID int64
	err := a.writeTx(ctx, wr, func(tx pgx.Tx) error {
		if err := a.requireLocked(ctx, tx, wr, date); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `INSERT INTO time_entries(user_id,project_id,task_id,start_time,end_time,duration_seconds,comment,billable,date,activity,entered_by)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) RETURNING id`,
			wr.uid, pid, tid, start, nz(end), durArg, nz(in["comment"]), billable, date, nz(in["activity"]), wr.enteredBy()).Scan(&newID); err != nil {
			return err
		}
		return a.nachtragLog(ctx, tx, wr, "Zeiteintrag angelegt", fmt.Sprintf("#%d", newID), date)
	})
	if err != nil {
		a.writeFail(w, err)
		return
	}
	a.stamp(ctx, wr.uid, newID, cr)
	a.afterWrite(ctx, wr, date)
	e, err := a.entryByID(r, wr.uid, newID)
	if err != nil {
		a.fail(w, err)
		return
	}
	e["warning"] = nil
	if tooShort {
		e["warning"] = "Eintrag unter 5 Minuten – bitte prüfen"
	}
	writeJSON(w, http.StatusOK, e)
}

func (a *App) entriesUpdate(w http.ResponseWriter, r *http.Request, u User) {
	id, ok := pathID(r)
	if !ok {
		notFound(w)
		return
	}
	var in body
	if !readJSON(w, r, &in) {
		return
	}
	a.entryUpdate(w, r, selfWriter(u), id, in)
}

// entryUpdate: Eintrag id von wr.uid ändern (eigene Daten oder Nachtrag)
func (a *App) entryUpdate(w http.ResponseWriter, r *http.Request, wr entryWriter, id int64, in body) {
	ctx := r.Context()
	cur, err := a.entryByID(r, wr.uid, id)
	if err != nil {
		a.fail(w, err)
		return
	}
	if cur == nil {
		notFound(w)
		return
	}
	for _, k := range []string{"project_id", "task_id"} {
		if invalidID(in[k]) {
			badRequest(w, k+" ungültig")
			return
		}
	}
	if v, has := in["start_time"]; has {
		if _, ok := a.parseTime(asStr(v)); !ok {
			badRequest(w, "start_time nicht lesbar")
			return
		}
	}
	if v, has := in["end_time"]; has && asStr(v) != "" {
		if _, ok := a.parseTime(asStr(v)); !ok {
			badRequest(w, "end_time nicht lesbar")
			return
		}
	}
	// Plausibilität nur, wenn sich die Zeit ändert (Text/Ticket ändern fragt nie nach)
	var cr *checkResult
	_, sg := in["start_time"]
	_, eg := in["end_time"]
	_, dg := in["duration_seconds"]
	if sg || eg || dg {
		st, en := asStr(cur["start_time"]), asStr(cur["end_time"])
		if sg {
			st = asStr(in["start_time"])
		}
		if eg {
			en = asStr(in["end_time"])
		}
		d := asStr(in["date"])
		if d == "" {
			if sg {
				d = a.dateOf(st)
			} else {
				d = asStr(cur["date"])
			}
		}
		var stop bool
		if cr, stop = a.guard(w, r, wr.uid, in, candidate{ID: id, Date: d, Start: st, End: en}); stop {
			return
		}
	}
	var sets []string
	var args []any
	set := func(col string, v any) {
		args = append(args, v)
		sets = append(sets, fmt.Sprintf("%s=$%d", col, len(args)+2))
	}
	if v, has := in["project_id"]; has {
		pid := asID(v)
		if !a.owned(ctx, "projects", pid, wr.uid) {
			badRequest(w, "Projekt nicht gefunden")
			return
		}
		set("project_id", pid)
	}
	if v, has := in["task_id"]; has {
		tid := asID(v)
		if !a.owned(ctx, "tasks", tid, wr.uid) {
			badRequest(w, "Ticket nicht gefunden")
			return
		}
		set("task_id", tid)
	}
	oldDate := asStr(cur["date"])
	newDate := oldDate
	start, startGiven := in["start_time"]
	if startGiven {
		s := asStr(start)
		set("start_time", s)
		d := asStr(in["date"])
		if d == "" {
			d = a.dateOf(s)
		}
		set("date", d)
		newDate = d
	}
	if wr.nachtrag() && !isoDateRE.MatchString(newDate) {
		badRequest(w, "Datum ungültig")
		return
	}
	endV, endGiven := in["end_time"]
	if endGiven {
		set("end_time", nz(endV))
	}
	durV, durGiven := in["duration_seconds"]
	if durGiven {
		if n := int64(asFloat(durV)); n != 0 {
			set("duration_seconds", n)
		} else {
			set("duration_seconds", nil)
		}
	}
	if v, has := in["comment"]; has {
		set("comment", nz(v))
	}
	if v, has := in["billable"]; has {
		b, _ := v.(bool)
		n := 0
		if b {
			n = 1
		}
		set("billable", n)
	}
	if v, has := in["activity"]; has {
		set("activity", nz(v))
	}
	if len(sets) > 0 {
		// Dauer passend zu Beginn/Ende halten, wenn sie nicht ausdrücklich gesetzt wird
		if !durGiven && (startGiven || endGiven) {
			s := asStr(cur["start_time"])
			if startGiven {
				s = asStr(start)
			}
			e := asStr(cur["end_time"])
			if endGiven {
				e = asStr(endV)
			}
			if e != "" {
				if d, ok := a.seconds(s, e); ok {
					set("duration_seconds", d)
				}
			}
		}
		if wr.nachtrag() {
			set("entered_by", wr.actor)
		}
		sets = append(sets, "version=version+1", "updated_at=now()")
		args = append([]any{id, wr.uid}, args...)
		if err := a.writeTx(ctx, wr, func(tx pgx.Tx) error {
			if err := a.requireLocked(ctx, tx, wr, oldDate, newDate); err != nil {
				return err
			}
			tag, err := tx.Exec(ctx, "UPDATE time_entries SET "+strings.Join(sets, ",")+" WHERE id=$1 AND user_id=$2", args...)
			if err != nil {
				return err
			}
			if tag.RowsAffected() == 0 {
				return pgx.ErrNoRows
			}
			return a.nachtragLog(ctx, tx, wr, "Zeiteintrag geändert", fmt.Sprintf("#%d", id), oldDate, newDate)
		}); err != nil {
			a.writeFail(w, err)
			return
		}
	} else if wr.nachtrag() {
		badRequest(w, "Keine Änderung angegeben")
		return
	}
	a.stamp(ctx, wr.uid, id, cr)
	a.afterWrite(ctx, wr, oldDate, newDate)
	e, err := a.entryByID(r, wr.uid, id)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, e)
}

// deleteEntryTx löscht nur den lokalen Eintrag. War er schon in Plane/Tempo gebucht, wird das Löschen dort
// vorgemerkt (plane_deletions / tempo_push_deletions) und später vom jeweiligen Modul ausgeführt.
func (a *App) deleteEntryTx(ctx context.Context, tx pgx.Tx, uid, id int64) error {
	var planeWL, planeItem, planeProj, tempoPush, label *string
	var tempoInst *int64
	var importID *string
	// FOR UPDATE: sperrt gegen gleichzeitiges Speichern der Tempo-/Plane-Id
	err := tx.QueryRow(ctx, `SELECT plane_worklog_id, plane_work_item_id, plane_project_id, tempo_push_id,
		date || ' ' || LEFT(COALESCE(comment,''),60), tempo_push_instance, plane_import_id FROM time_entries WHERE id=$1 AND user_id=$2 FOR UPDATE`, id, uid).
		Scan(&planeWL, &planeItem, &planeProj, &tempoPush, &label, &tempoInst, &importID)
	if err != nil {
		return err
	}
	if planeWL != nil && *planeWL != "" {
		if _, err := tx.Exec(ctx, `INSERT INTO plane_deletions(user_id,worklog_id,work_item_id,project_id,label) VALUES($1,$2,$3,$4,$5)`,
			uid, *planeWL, planeItem, planeProj, label); err != nil {
			return err
		}
	}
	if tempoPush != nil && *tempoPush != "" {
		if _, err := tx.Exec(ctx, `INSERT INTO tempo_push_deletions(user_id,worklog_id,label,instance_id) VALUES($1,$2,$3,$4)`, uid, *tempoPush, label, tempoInst); err != nil {
			return err
		}
	}
	if importID != nil && *importID != "" { // aus Plane importiert: Grabstein, damit der nächste Import ihn nicht zurückholt
		if _, err := tx.Exec(ctx, `INSERT INTO plane_import_ignored(user_id,plane_import_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, uid, *importID); err != nil {
			return err
		}
	}
	_, err = tx.Exec(ctx, `DELETE FROM time_entries WHERE id=$1 AND user_id=$2`, id, uid)
	return err
}

func (a *App) entriesDelete(w http.ResponseWriter, r *http.Request, u User) {
	id, ok := pathID(r)
	if !ok {
		notFound(w)
		return
	}
	a.entryDelete(w, r, selfWriter(u), id)
}

// entryDelete: Eintrag id von wr.uid löschen (eigene Daten oder Nachtrag)
func (a *App) entryDelete(w http.ResponseWriter, r *http.Request, wr entryWriter, id int64) {
	ctx := r.Context()
	var date string
	err := a.writeTx(ctx, wr, func(tx pgx.Tx) error {
		if wr.nachtrag() { // Datum vorab: der Monat muss gesperrt sein (sonst normaler Weg der Person)
			if err := tx.QueryRow(ctx, `SELECT date FROM time_entries WHERE id=$1 AND user_id=$2`, id, wr.uid).Scan(&date); err != nil {
				return err
			}
			if err := a.requireLocked(ctx, tx, wr, date); err != nil {
				return err
			}
		}
		if err := a.deleteEntryTx(ctx, tx, wr.uid, id); err != nil {
			return err
		}
		return a.nachtragLog(ctx, tx, wr, "Zeiteintrag gelöscht", fmt.Sprintf("#%d", id), date)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		notFound(w)
		return
	}
	if err != nil {
		a.writeFail(w, err)
		return
	}
	a.afterWrite(ctx, wr, date)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

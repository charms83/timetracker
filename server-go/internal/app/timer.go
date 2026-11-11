package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

/* Timer und Live-Pause – Verhalten wie backend/server.js (/api/timer, /api/pause), je Benutzer getrennt.
   Noch nicht portiert: Quellen-Umleitung (abgeschlossenes Projekt → Nachfolger), Plausibilitäts-Markierung. */

const isoMillis = "2006-01-02T15:04:05.000Z"

func (a *App) routesTimer(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/timer/status", a.authSessionOrDevice(a.timerStatus)) // Add-on/Mac-Token
	mux.HandleFunc("POST /api/timer/start", a.auth(a.timerStart))
	mux.HandleFunc("POST /api/timer/stop", a.authSessionOrDevice(a.timerStop))
	mux.HandleFunc("POST /api/timer/assign", a.auth(a.timerAssign)) // Ticket/Projekt nachträglich am laufenden Timer
	mux.HandleFunc("GET /api/pause/status", a.authSessionOrDevice(a.pauseStatus))
	mux.HandleFunc("POST /api/pause/start", a.authSessionOrDevice(a.pauseStart))
	mux.HandleFunc("POST /api/pause/stop", a.authSessionOrDevice(a.pauseStop))
}

func (a *App) inTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	tx, err := a.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// stopTimerTx beendet den laufenden Timer und speichert den Eintrag.
// Unter 1 Minute (z. B. aus Versehen gestartet) wird nicht gespeichert.
func (a *App) stopTimerTx(ctx context.Context, tx pgx.Tx, uid int64, end time.Time) (int64, error) {
	var newID int64
	var proj, task, plan *int64
	var comment, start, activity *string
	err := tx.QueryRow(ctx, `SELECT project_id,task_id,plan_task_id,comment,start_time,activity FROM timer_state WHERE user_id=$1 FOR UPDATE`, uid).
		Scan(&proj, &task, &plan, &comment, &start, &activity)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if start == nil || *start == "" {
		return 0, nil
	}
	if st, ok := a.parseTime(*start); ok {
		// Monat des Beginns inzwischen gesperrt (z. B. nach langer Laufzeit automatisch geschlossen): der Teil im gesperrten Monat wird
		// nicht gespeichert (Systemlog, Nachtrag durch Teamleitung/Personal), der Teil ab Beginn des Monats von „end“ schon, wenn dieser offen ist.
		// Der Timer endet in jedem Fall – er hängt nie am Trigger.
		startIso := *start
		if locked, err := monthLockedTx(ctx, tx, uid, a.dateOf(*start)); err != nil {
			return 0, err
		} else if locked {
			el := end.In(a.Loc)
			cut := time.Date(el.Year(), el.Month(), 1, 0, 0, 0, 0, a.Loc)
			a.sysLog(ctx, uid, "warn", "pruefung", fmt.Sprintf("Timer ab %s: Teil bis %s nicht gespeichert – Monat %s ist gesperrt, bitte Teamleitung/Personal um Nachtrag",
				*start, cut.UTC().Format(isoMillis), a.dateOf(*start)[:7]))
			origSt := st
			st = end // ohne offenen Teil: nichts speichern
			if cut.After(origSt) && !end.Before(cut) {
				l2, err := monthLockedTx(ctx, tx, uid, dayStr(cut))
				if err != nil {
					return 0, err
				}
				if !l2 {
					st, startIso = cut, cut.UTC().Format(isoMillis)
				}
			}
		}
		if dur := int64(end.Sub(st).Seconds()); dur >= 60 {
			if err := tx.QueryRow(ctx, `INSERT INTO time_entries(user_id,project_id,task_id,plan_task_id,start_time,end_time,duration_seconds,comment,date,source,activity)
				VALUES($1,$2,$3,(SELECT id FROM plan_tasks WHERE id=$4 AND user_id=$1),$5,$6,$7,$8,$9,'manual',$10) RETURNING id`,
				uid, proj, task, plan, startIso, end.UTC().Format(isoMillis), dur, comment, a.dateOf(startIso), activity).Scan(&newID); err != nil {
				return 0, err
			}
		}
	}
	_, err = tx.Exec(ctx, `UPDATE timer_state SET project_id=NULL,task_id=NULL,plan_task_id=NULL,comment=NULL,start_time=NULL,activity=NULL WHERE user_id=$1`, uid)
	return newID, err
}

// stopPauseTx beendet die laufende Pause; sie wird als freie Pause gespeichert (über 5 s).
func (a *App) stopPauseTx(ctx context.Context, tx pgx.Tx, uid int64, end time.Time) error {
	var start *string
	err := tx.QueryRow(ctx, `SELECT start_time FROM pause_state WHERE user_id=$1 FOR UPDATE`, uid).Scan(&start)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if start == nil || *start == "" {
		return nil
	}
	if st, ok := a.parseTime(*start); ok && end.Sub(st).Seconds() > 5 {
		// Pausen haben keinen eigenen Trigger, verändern aber das Ist: in einem eingereichten/freigegebenen Monat wird
		// die Pause nicht gespeichert (Monat bleibt unverändert), die laufende Pause endet trotzdem (nichts bleibt hängen)
		if locked, err := monthLockedTx(ctx, tx, uid, a.dateOf(*start)); err != nil {
			return err
		} else if locked {
			_, err = tx.Exec(ctx, `UPDATE pause_state SET start_time=NULL WHERE user_id=$1`, uid)
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO pauses(user_id,work_day_id,start_time,end_time,date) VALUES($1,NULL,$2,$3,$4)`,
			uid, *start, end.UTC().Format(isoMillis), a.dateOf(*start)); err != nil {
			return err
		}
	}
	_, err = tx.Exec(ctx, `UPDATE pause_state SET start_time=NULL WHERE user_id=$1`, uid)
	return err
}

func (a *App) timerStatus(w http.ResponseWriter, r *http.Request, u User) {
	ctx := r.Context()
	s, err := a.one(ctx, `SELECT project_id,task_id,plan_task_id,comment,start_time,activity FROM timer_state WHERE user_id=$1`, u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	if s == nil || s["start_time"] == nil || s["start_time"] == "" {
		writeJSON(w, http.StatusOK, map[string]any{"active": false})
		return
	}
	get := func(table string, id any) map[string]any {
		if id == nil {
			return nil
		}
		m, _ := a.one(ctx, "SELECT * FROM "+table+" WHERE id=$1 AND user_id=$2", id, u.ID)
		return m
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"active": true, "project": get("projects", s["project_id"]), "task": get("tasks", s["task_id"]),
		"planTask": get("plan_tasks", s["plan_task_id"]), "comment": s["comment"], "start_time": s["start_time"], "activity": s["activity"],
	})
}

func (a *App) timerStart(w http.ResponseWriter, r *http.Request, u User) {
	var in body
	if !readJSON(w, r, &in) {
		return
	}
	for _, k := range []string{"project_id", "task_id", "plan_task_id"} {
		if invalidID(in[k]) {
			badRequest(w, k+" ungültig")
			return
		}
	}
	res, ae, err := a.startTimer(r.Context(), u.ID, asID(in["project_id"]), asID(in["task_id"]), asID(in["plan_task_id"]), nz(in["comment"]), nz(in["activity"]), asStr(in["start_time"]))
	switch {
	case ae != nil:
		writeJSON(w, ae.Code, ae.Body)
	case err != nil:
		a.fail(w, err)
	default:
		writeJSON(w, http.StatusOK, res)
	}
}

// apiErr: fachliche Ablehnung mit Status und Antwort (z. B. 400, 409 month_locked)
type apiErr struct {
	Code int
	Body map[string]any
}

// startTimer: gemeinsamer Weg für POST /api/timer/start und den Start aus einem To-do (gleiche Prüfungen, gleiche Sperre).
// Projekt/Ticket optional – Tracken ohne Ticket; später per /api/timer/assign oder nach Stopp zuordnen.
func (a *App) startTimer(ctx context.Context, uid int64, pid, tid, plid, comment, activity any, wantStart string) (map[string]any, *apiErr, error) {
	var monthLocked bool
	if err := a.DB.QueryRow(ctx, `SELECT zp_month_locked($1, $2)`, uid, time.Now().In(a.Loc).Format("2006-01-02")).Scan(&monthLocked); err == nil && monthLocked {
		return nil, &apiErr{http.StatusConflict, map[string]any{"error": "Der laufende Monat ist geschlossen, eingereicht oder freigegeben – der Timer kann nicht starten", "code": "month_locked"}}, nil
	}
	if !a.owned(ctx, "projects", pid, uid) || !a.owned(ctx, "tasks", tid, uid) || !a.owned(ctx, "plan_tasks", plid, uid) {
		return nil, &apiErr{http.StatusBadRequest, map[string]any{"error": "Projekt, Ticket oder Plan-Aufgabe nicht gefunden"}}, nil
	}
	var redirectedFrom any
	if np, nt, from := a.redirect(ctx, uid, pid, tid); from != "" { // abgeschlossenes Projekt → Nachfolger
		pid, tid, redirectedFrom = np, nt, from
	}
	now := time.Now().UTC()
	startIso := now.Format(isoMillis)
	// optional rückwirkend starten (z. B. ab Terminbeginn): höchstens 12 h zurück, nie in der Zukunft,
	// und nie vor das Ende der letzten Buchung (keine Überschneidung)
	if want, ok := a.parseTime(strings.TrimSpace(wantStart)); ok && want.Before(now) && now.Sub(want) < 12*time.Hour {
		var lastEnd *string
		if err := a.DB.QueryRow(ctx, `SELECT MAX(end_time) FROM time_entries WHERE user_id=$1 AND end_time IS NOT NULL AND date=$2`,
			uid, want.In(a.Loc).Format("2006-01-02")).Scan(&lastEnd); err != nil {
			return nil, nil, err
		}
		start := want
		if lastEnd != nil {
			if le, ok := a.parseTime(*lastEnd); ok && le.After(start) {
				start = le
			}
		}
		// nie rückwirkend in einen gesperrten (geschlossenen/eingereichten) Monat: dann ab Beginn des laufenden Monats
		if nl := now.In(a.Loc); start.In(a.Loc).Month() != nl.Month() {
			var locked bool
			if err := a.DB.QueryRow(ctx, `SELECT zp_month_locked($1, $2)`, uid, start.In(a.Loc).Format("2006-01-02")).Scan(&locked); err != nil {
				return nil, nil, err
			}
			if locked {
				start = time.Date(nl.Year(), nl.Month(), 1, 0, 0, 0, 0, a.Loc)
			}
		}
		startIso = start.UTC().Format(isoMillis)
	}
	var stoppedID int64
	err := a.inTx(ctx, func(tx pgx.Tx) error {
		if err := a.stopPauseTx(ctx, tx, uid, now); err != nil {
			return err
		}
		id, err := a.stopTimerTx(ctx, tx, uid, now)
		if err != nil {
			return err
		}
		stoppedID = id
		_, err = tx.Exec(ctx, `INSERT INTO timer_state(user_id,project_id,task_id,plan_task_id,comment,start_time,activity) VALUES($1,$2,$3,$4,$5,$6,$7)
			ON CONFLICT (user_id) DO UPDATE SET project_id=EXCLUDED.project_id, task_id=EXCLUDED.task_id, plan_task_id=EXCLUDED.plan_task_id,
			comment=EXCLUDED.comment, start_time=EXCLUDED.start_time, activity=EXCLUDED.activity`,
			uid, pid, tid, plid, comment, startIso, activity)
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	a.flagAfterID(ctx, uid, stoppedID)
	a.syncAuto(uid)
	return map[string]any{"ok": true, "start_time": startIso, "redirected_from": redirectedFrom}, nil, nil
}

func (a *App) timerStop(w http.ResponseWriter, r *http.Request, u User) {
	ctx := r.Context()
	var stoppedID int64
	if err := a.inTx(ctx, func(tx pgx.Tx) (err error) {
		stoppedID, err = a.stopTimerTx(ctx, tx, u.ID, time.Now().UTC())
		return err
	}); err != nil {
		a.fail(w, err)
		return
	}
	a.flagAfterID(ctx, u.ID, stoppedID)
	a.syncAuto(u.ID)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// timerAssign: laufenden Timer nachträglich mit Projekt/Ticket/Kommentar versehen (oder Ticket anlegen lassen).
func (a *App) timerAssign(w http.ResponseWriter, r *http.Request, u User) {
	var in body
	if !readJSON(w, r, &in) {
		return
	}
	for _, k := range []string{"project_id", "task_id", "plan_task_id"} {
		if invalidID(in[k]) {
			badRequest(w, k+" ungültig")
			return
		}
	}
	ctx := r.Context()
	pid, tid, plid := asID(in["project_id"]), asID(in["task_id"]), asID(in["plan_task_id"])
	if !a.owned(ctx, "projects", pid, u.ID) || !a.owned(ctx, "tasks", tid, u.ID) || !a.owned(ctx, "plan_tasks", plid, u.ID) {
		badRequest(w, "Projekt, Ticket oder Plan-Aufgabe nicht gefunden")
		return
	}
	if pid != nil || tid != nil {
		pid, tid, _ = a.redirect(ctx, u.ID, pid, tid)
	}
	var start *string
	err := a.DB.QueryRow(ctx, `SELECT start_time FROM timer_state WHERE user_id=$1`, u.ID).Scan(&start)
	if err != nil || start == nil || *start == "" {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "Kein Timer aktiv", "code": "no_timer"})
		return
	}
	sets := []string{}
	args := []any{u.ID}
	add := func(col string, v any) {
		args = append(args, v)
		sets = append(sets, fmt.Sprintf("%s=$%d", col, len(args)))
	}
	if _, has := in["project_id"]; has {
		add("project_id", pid)
	}
	if _, has := in["task_id"]; has {
		add("task_id", tid)
	}
	if _, has := in["plan_task_id"]; has {
		add("plan_task_id", plid)
	}
	if _, has := in["comment"]; has {
		add("comment", nz(in["comment"]))
	}
	if _, has := in["activity"]; has {
		add("activity", nz(in["activity"]))
	}
	if len(sets) == 0 {
		badRequest(w, "nichts zu setzen")
		return
	}
	_, err = a.DB.Exec(ctx, `UPDATE timer_state SET `+strings.Join(sets, ",")+` WHERE user_id=$1 AND start_time IS NOT NULL`, args...)
	if err != nil {
		a.fail(w, err)
		return
	}
	a.timerStatus(w, r, u)
}

func (a *App) pauseStatus(w http.ResponseWriter, r *http.Request, u User) {
	var start *string
	err := a.DB.QueryRow(r.Context(), `SELECT start_time FROM pause_state WHERE user_id=$1`, u.ID).Scan(&start)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		a.fail(w, err)
		return
	}
	if start == nil || *start == "" {
		writeJSON(w, http.StatusOK, map[string]any{"active": false})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"active": true, "start_time": *start})
}

func (a *App) pauseStart(w http.ResponseWriter, r *http.Request, u User) {
	ctx := r.Context()
	now := time.Now().UTC()
	var stoppedID int64
	err := a.inTx(ctx, func(tx pgx.Tx) error {
		id, err := a.stopTimerTx(ctx, tx, u.ID, now) // laufenden Projekt-Timer beenden, Eintrag wird gespeichert
		if err != nil {
			return err
		}
		stoppedID = id
		if err := a.stopPauseTx(ctx, tx, u.ID, now); err != nil { // Sicherheit: falls schon eine Pause offen war
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO pause_state(user_id,start_time) VALUES($1,$2)
			ON CONFLICT (user_id) DO UPDATE SET start_time=EXCLUDED.start_time`, u.ID, now.Format(isoMillis))
		return err
	})
	if err != nil {
		a.fail(w, err)
		return
	}
	a.flagAfterID(ctx, u.ID, stoppedID)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *App) pauseStop(w http.ResponseWriter, r *http.Request, u User) {
	ctx := r.Context()
	if err := a.inTx(ctx, func(tx pgx.Tx) error { return a.stopPauseTx(ctx, tx, u.ID, time.Now().UTC()) }); err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// flagAfterID markiert den vom Timer gespeicherten Eintrag (id 0 = nichts gespeichert)
func (a *App) flagAfterID(ctx context.Context, uid, id int64) {
	if id != 0 {
		a.flagAfter(ctx, uid, id)
	}
}

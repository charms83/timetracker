package app

import (
	"context"
	"errors"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

/* Arbeitstag, Ticket-Schätzungen und Abwesenheits-Hinweise – Verhalten wie backend/server.js (/api/workday*, /api/ticket-estimates)
   und backend/absence-sync.js (/api/absence-hints).
   Bewusst anders:
   • Arbeitstag: work_days bleibt nur Anzeige (Beginn/Ende); Pausen hängen nie an einem Arbeitstag (work_day_id NULL) und zählen
     damit wie freie Pausen im Konto (konto.go). Pausen-Schreibwege prüfen die Monatssperre in der Transaktion (Pausen haben keinen
     eigenen Trigger, verändern aber das Ist eines Monats).
   • Abwesenheits-Hinweise: erkannt wird im Server nichts (Kalender bleibt im Client, Tempo-Abgleich fehlt noch); geliefert werden die
     gespeicherten Hinweise, übernommen wird unter der Monatssperre (Trigger absences_lock). */

func (a *App) routesWorkday(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/workday", a.auth(a.workdayGet))
	mux.HandleFunc("POST /api/workday/start", a.auth(a.workdayStart))
	mux.HandleFunc("PUT /api/workday/{id}", a.auth(a.workdayPut))
	mux.HandleFunc("POST /api/workday/end", a.auth(a.workdayEnd))
	mux.HandleFunc("POST /api/workday/reopen", a.auth(a.workdayReopen))
	mux.HandleFunc("POST /api/workday/pause/start", a.auth(a.workdayPauseStart))
	mux.HandleFunc("POST /api/workday/pause/end", a.auth(a.workdayPauseEnd))
	mux.HandleFunc("GET /api/ticket-estimates", a.auth(a.estimatesGet))
	mux.HandleFunc("PUT /api/ticket-estimates/{ticket}", a.auth(a.estimatePut))
	mux.HandleFunc("GET /api/absence-hints", a.auth(a.absenceHints))
	mux.HandleFunc("POST /api/absence-hints/{key}", a.auth(a.absenceHintAct))
}

// errLocked: Monat gesperrt – Transaktion zurückrollen, Antwort 409 month_locked
var errLocked = errors.New("Monat gesperrt")

/* ── Arbeitstag ──────────────────────────────────────────────────── */

func (a *App) workdayGet(w http.ResponseWriter, r *http.Request, u User) {
	ctx := r.Context()
	date := r.URL.Query().Get("date")
	if date == "" {
		date = a.today()
	}
	if _, ok := parseDay(date); !ok {
		badRequest(w, "date: JJJJ-MM-TT")
		return
	}
	wd, err := a.one(ctx, `SELECT * FROM work_days WHERE user_id=$1 AND date=$2`, u.ID, date)
	if err != nil {
		a.fail(w, err)
		return
	}
	logs, err := a.maps(ctx, `SELECT * FROM daily_logs WHERE user_id=$1 AND date=$2 ORDER BY timestamp`, u.ID, date)
	if err != nil {
		a.fail(w, err)
		return
	}
	entries, err := a.maps(ctx, entrySelect+` WHERE e.user_id=$1 AND e.date=$2 ORDER BY e.start_time`, u.ID, date)
	if err != nil {
		a.fail(w, err)
		return
	}
	pauses, err := a.maps(ctx, `SELECT * FROM pauses WHERE user_id=$1 AND date=$2 ORDER BY start_time`, u.ID, date)
	if err != nil {
		a.fail(w, err)
		return
	}
	var wdv any
	if wd != nil {
		wdv = wd
	}
	writeJSON(w, http.StatusOK, map[string]any{"workday": wdv, "dailyLogs": logs, "timeEntries": entries, "pauses": pauses})
}

func (a *App) workdayStart(w http.ResponseWriter, r *http.Request, u User) {
	ctx := r.Context()
	t := a.today()
	row, err := a.one(ctx, `INSERT INTO work_days(user_id,date,start_time) VALUES($1,$2,$3) ON CONFLICT (user_id,date) DO NOTHING RETURNING *`,
		u.ID, t, time.Now().UTC().Format(isoMillis))
	if err != nil {
		a.fail(w, err)
		return
	}
	if row != nil {
		writeJSON(w, http.StatusOK, map[string]any{"workday": row})
		return
	}
	cur, err := a.one(ctx, `SELECT * FROM work_days WHERE user_id=$1 AND date=$2`, u.ID, t)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"workday": cur, "alreadyRunning": true})
}

func (a *App) workdayPut(w http.ResponseWriter, r *http.Request, u User) {
	id, ok := pathID(r)
	if !ok {
		notFound(w)
		return
	}
	var in body
	if !readJSON(w, r, &in) {
		return
	}
	var sets []string
	args := []any{id, u.ID}
	for _, k := range []string{"start_time", "end_time", "notes"} {
		v, has := in[k]
		if !has {
			continue
		}
		if k != "notes" && asStr(v) != "" {
			if _, ok := a.parseTime(asStr(v)); !ok {
				badRequest(w, k+" nicht lesbar")
				return
			}
		}
		if k == "start_time" && asStr(v) == "" {
			badRequest(w, "start_time erforderlich")
			return
		}
		args = append(args, nz(v))
		sets = append(sets, k+"=$"+itoa(int64(len(args))))
	}
	if len(sets) > 0 {
		if _, err := a.DB.Exec(r.Context(), `UPDATE work_days SET `+strings.Join(sets, ",")+` WHERE id=$1 AND user_id=$2`, args...); err != nil {
			a.fail(w, err)
			return
		}
	}
	row, err := a.one(r.Context(), `SELECT * FROM work_days WHERE id=$1 AND user_id=$2`, id, u.ID)
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

// workdayEnd: Feierabend – Timer und Pause beenden, offene Pausen schließen, Arbeitstag beenden
func (a *App) workdayEnd(w http.ResponseWriter, r *http.Request, u User) {
	var in body
	if !readJSON(w, r, &in) {
		return
	}
	ctx := r.Context()
	now := time.Now().UTC()
	t := a.today()
	var stoppedID int64
	locked := false
	err := a.inTx(ctx, func(tx pgx.Tx) error {
		var err error
		if locked, err = monthLockedTx(ctx, tx, u.ID, t); err != nil {
			return err
		} else if locked {
			return errLocked // zurückrollen: nichts beenden, nichts speichern
		}
		id, err := a.stopTimerTx(ctx, tx, u.ID, now) // Eintrag unterliegt zusätzlich dem Trigger der Monatssperre
		if err != nil {
			return err
		}
		stoppedID = id
		if err := a.stopPauseTx(ctx, tx, u.ID, now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE pauses SET end_time=$3 WHERE user_id=$1 AND date=$2 AND end_time IS NULL`, u.ID, t, now.Format(isoMillis)); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE work_days SET end_time=$3, notes=$4 WHERE user_id=$1 AND date=$2 AND end_time IS NULL`, u.ID, t, now.Format(isoMillis), nz(in["notes"]))
		return err
	})
	if locked || errors.Is(err, errLocked) {
		writeJSON(w, http.StatusConflict, monthLockedBody(t))
		return
	}
	if err != nil {
		a.fail(w, err)
		return
	}
	a.flagAfterID(ctx, u.ID, stoppedID)
	a.syncAuto(u.ID)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// workdayReopen: Arbeitstag wieder öffnen; die Unterbrechung seit Feierabend wird als Pause eingetragen
func (a *App) workdayReopen(w http.ResponseWriter, r *http.Request, u User) {
	var in body
	if !readJSON(w, r, &in) {
		return
	}
	ctx := r.Context()
	d := asStr(in["date"])
	if d == "" {
		d = a.today()
	}
	if _, ok := parseDay(d); !ok {
		badRequest(w, "date: JJJJ-MM-TT")
		return
	}
	var out map[string]any
	code := http.StatusOK
	err := a.inTx(ctx, func(tx pgx.Tx) error {
		var id int64
		var end *string
		err := tx.QueryRow(ctx, `SELECT id, end_time FROM work_days WHERE user_id=$1 AND date=$2 FOR UPDATE`, u.ID, d).Scan(&id, &end)
		if errors.Is(err, pgx.ErrNoRows) {
			code, out = http.StatusNotFound, map[string]any{"error": "Kein Arbeitstag gefunden"}
			return nil
		}
		if err != nil {
			return err
		}
		if end == nil || *end == "" {
			out = map[string]any{"ok": true, "message": "Bereits offen"}
			return nil
		}
		if locked, err := monthLockedTx(ctx, tx, u.ID, d); err != nil || locked {
			if locked {
				code, out = http.StatusConflict, monthLockedBody(d)
			}
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO pauses(user_id,work_day_id,start_time,date) VALUES($1,NULL,$2,$3)`, u.ID, *end, d); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE work_days SET end_time=NULL WHERE id=$1 AND user_id=$2`, id, u.ID); err != nil {
			return err
		}
		out = map[string]any{"ok": true, "pauseFrom": *end}
		return nil
	})
	if err != nil {
		a.fail(w, err)
		return
	}
	if code == http.StatusOK && out["pauseFrom"] != nil {
		a.sysLog(ctx, u.ID, "info", "system", "Arbeitstag "+d+" wieder geöffnet (Pause seit "+clock(asStr(out["pauseFrom"]))+" eingetragen)")
	}
	writeJSON(w, code, out)
}

func (a *App) workdayPauseStart(w http.ResponseWriter, r *http.Request, u User) {
	ctx := r.Context()
	t := a.today()
	var out map[string]any
	code := http.StatusOK
	err := a.inTx(ctx, func(tx pgx.Tx) error {
		var open bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM work_days WHERE user_id=$1 AND date=$2 AND end_time IS NULL)`, u.ID, t).Scan(&open); err != nil {
			return err
		}
		if !open {
			code, out = http.StatusBadRequest, map[string]any{"error": "Kein aktiver Arbeitstag"}
			return nil
		}
		if locked, err := monthLockedTx(ctx, tx, u.ID, t); err != nil || locked {
			if locked {
				code, out = http.StatusConflict, monthLockedBody(t)
			}
			return err
		}
		cur, err := txOne(ctx, tx, `SELECT * FROM pauses WHERE user_id=$1 AND date=$2 AND end_time IS NULL ORDER BY id LIMIT 1`, u.ID, t)
		if err != nil {
			return err
		}
		if cur != nil {
			out = map[string]any{"pause": cur, "alreadyPaused": true}
			return nil
		}
		p, err := txOne(ctx, tx, `INSERT INTO pauses(user_id,work_day_id,start_time,date) VALUES($1,NULL,$2,$3) RETURNING *`, u.ID, time.Now().UTC().Format(isoMillis), t)
		out = map[string]any{"pause": p}
		return err
	})
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, code, out)
}

// workdayPauseEnd: Pause beenden; unter 5 Minuten wird sie verworfen
func (a *App) workdayPauseEnd(w http.ResponseWriter, r *http.Request, u User) {
	ctx := r.Context()
	t := a.today()
	var out map[string]any
	code := http.StatusOK
	err := a.inTx(ctx, func(tx pgx.Tx) error {
		var open bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM work_days WHERE user_id=$1 AND date=$2 AND end_time IS NULL)`, u.ID, t).Scan(&open); err != nil {
			return err
		}
		if !open {
			code, out = http.StatusBadRequest, map[string]any{"error": "Kein aktiver Arbeitstag"}
			return nil
		}
		var id int64
		var start string
		err := tx.QueryRow(ctx, `SELECT id, start_time FROM pauses WHERE user_id=$1 AND date=$2 AND end_time IS NULL ORDER BY id LIMIT 1 FOR UPDATE`, u.ID, t).Scan(&id, &start)
		if errors.Is(err, pgx.ErrNoRows) {
			out = map[string]any{"ok": true, "skipped": true}
			return nil
		}
		if err != nil {
			return err
		}
		if locked, err := monthLockedTx(ctx, tx, u.ID, t); err != nil || locked {
			if locked {
				code, out = http.StatusConflict, monthLockedBody(t)
			}
			return err
		}
		now := time.Now().UTC()
		if st, ok := a.parseTime(start); ok && now.Sub(st) < 5*time.Minute {
			_, err = tx.Exec(ctx, `DELETE FROM pauses WHERE id=$1 AND user_id=$2`, id, u.ID)
			out = map[string]any{"ok": true, "discarded": true, "reason": "unter 5 Minuten"}
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE pauses SET end_time=$3 WHERE id=$1 AND user_id=$2`, id, u.ID, now.Format(isoMillis))
		out = map[string]any{"ok": true}
		return err
	})
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, code, out)
}

// txOne: erste Zeile als Objekt innerhalb einer Transaktion (wie a.one)
func txOne(ctx context.Context, tx pgx.Tx, sql string, args ...any) (map[string]any, error) {
	rows, err := tx.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, rows.Err()
	}
	vals, err := rows.Values()
	if err != nil {
		return nil, err
	}
	m := map[string]any{}
	for i, fd := range rows.FieldDescriptions() {
		if fd.Name == "user_id" || fd.Name == "legacy_id" {
			continue
		}
		if t, ok := vals[i].(time.Time); ok {
			m[fd.Name] = t.UTC().Format("2006-01-02 15:04:05")
		} else {
			m[fd.Name] = vals[i]
		}
	}
	return m, nil
}

/* ── Schätzung je Ticket (tasks.estimated_hours) ─────────────────── */

func (a *App) estimatesGet(w http.ResponseWriter, r *http.Request, u User) {
	rows, err := a.DB.Query(r.Context(), `WITH k AS (SELECT UPPER(TRIM(jira_ticket)) AS ticket, COALESCE(MAX(estimated_hours),0)::float8 AS est
			FROM tasks WHERE user_id=$1 AND jira_ticket IS NOT NULL AND TRIM(jira_ticket)<>'' GROUP BY 1),
		s AS (SELECT UPPER(TRIM(t.jira_ticket)) AS ticket, COALESCE(SUM(e.duration_seconds),0)::float8 AS secs
			FROM time_entries e JOIN tasks t ON t.id=e.task_id AND t.user_id=e.user_id
			WHERE e.user_id=$1 AND e.end_time IS NOT NULL AND t.jira_ticket IS NOT NULL GROUP BY 1)
		SELECT k.ticket, k.est, COALESCE(s.secs,0) FROM k LEFT JOIN s ON s.ticket=k.ticket`, u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	defer rows.Close()
	out := map[string]any{}
	for rows.Next() {
		var k string
		var est, secs float64
		if err := rows.Scan(&k, &est, &secs); err != nil {
			a.fail(w, err)
			return
		}
		out[k] = map[string]any{"estimate_h": est, "total_secs": int64(secs)}
	}
	if err := rows.Err(); err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *App) estimatePut(w http.ResponseWriter, r *http.Request, u User) {
	key := strings.ToUpper(strings.TrimSpace(r.PathValue("ticket")))
	var in body
	if !readJSON(w, r, &in) {
		return
	}
	h := asFloat(strings.Replace(asStr(in["hours"]), ",", ".", 1))
	if math.IsNaN(h) || math.IsInf(h, 0) {
		h = 0
	}
	h = math.Max(0, math.Min(10000, h))
	tag, err := a.DB.Exec(r.Context(), `UPDATE tasks SET estimated_hours=$3 WHERE user_id=$1 AND UPPER(TRIM(jira_ticket))=$2`, u.ID, key, h)
	if err != nil {
		a.fail(w, err)
		return
	}
	if tag.RowsAffected() == 0 {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "Ticket " + key + " ist lokal noch nicht bekannt – erst einmal darauf buchen"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "ticket": key, "estimate_h": h})
}

/* ── Abwesenheits-Hinweise ───────────────────────────────────────── */

var absenceHintTypes = []string{"urlaub", "krankheit", "fortbildung", "sonstiges"}

func (a *App) absenceHints(w http.ResponseWriter, r *http.Request, u User) {
	ctx := r.Context()
	cal := []map[string]any{}
	on, why := a.suggestAllowedFor(ctx, u.ID, "Abwesenheits-Vorschläge")
	if on {
		var err error
		if cal, err = a.maps(ctx, `SELECT * FROM absence_hints WHERE user_id=$1 AND status='new' ORDER BY date_from`, u.ID); err != nil {
			a.fail(w, err)
			return
		}
	}
	mt, err := a.missingInTempo(r, u)
	if err != nil {
		a.fail(w, err)
		return
	}
	out := map[string]any{"calendar": cal, "missing_in_tempo": mt}
	if !on {
		out["suggest"], out["reason"] = false, why
	}
	writeJSON(w, http.StatusOK, out)
}

// missingInTempo: Urlaub/Krankheit hier eingetragen, aber an keinem Werktag auf dem Abwesenheitsprojekt gebucht.
// Wie Node nur, wenn Zeiten aus Tempo übernommen werden (tempo_sync_from gesetzt) – sonst wäre jede Abwesenheit „fehlend“.
func (a *App) missingInTempo(r *http.Request, u User) ([]map[string]any, error) {
	ctx := r.Context()
	st, err := a.userSettings(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	since := st["tempo_sync_from"]
	if !isoDateRE.MatchString(since) || !a.tempoModuleOn(ctx) {
		return out, nil
	}
	key := strings.ToUpper(jsOr(strings.TrimSpace(st["tempo_absence_project"]), "HAP"))
	booked := map[string]bool{}
	rows, err := a.DB.Query(ctx, `SELECT DISTINCT e.date FROM time_entries e JOIN tasks t ON t.id=e.task_id AND t.user_id=e.user_id
		WHERE e.user_id=$1 AND e.date>=$2 AND UPPER(t.jira_ticket) LIKE $3`, u.ID, since, key+"-%")
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var d string
		if rows.Scan(&d) == nil {
			booked[d] = true
		}
	}
	rows.Close()
	abs, err := a.maps(ctx, `SELECT id, date_from, date_to, type FROM absences WHERE user_id=$1 AND type IN ('urlaub','krankheit') AND COALESCE(source,'')<>'tempo' AND date_to>=$2 ORDER BY date_from`, u.ID, since)
	if err != nil {
		return nil, err
	}
	limit := addDays(a.today(), 60)
	for _, x := range abs {
		f, ok1 := parseDay(asStr(x["date_from"]))
		t, ok2 := parseDay(asStr(x["date_to"]))
		if !ok1 || !ok2 || t.Sub(f) > 800*24*time.Hour {
			continue
		}
		days, missing := 0, 0
		for d := f; !d.After(t); d = d.AddDate(0, 0, 1) {
			if wd := d.Weekday(); wd == time.Saturday || wd == time.Sunday || dayStr(d) > limit {
				continue
			}
			days++
			if !booked[dayStr(d)] {
				missing++
			}
		}
		if missing > 0 && missing == days {
			out = append(out, map[string]any{"id": x["id"], "from": x["date_from"], "to": x["date_to"], "type": x["type"], "days": days})
		}
	}
	return out, nil
}

// absenceHintAct: Hinweis übernehmen (take) oder verwerfen. Übernehmen legt die Abwesenheit an – die Monatssperre der
// Datenbank gilt (eingereichter/freigegebener Monat → 409 month_locked, Hinweis bleibt offen).
func (a *App) absenceHintAct(w http.ResponseWriter, r *http.Request, u User) {
	key := r.PathValue("key")
	var in body
	if !readJSON(w, r, &in) {
		return
	}
	ctx := r.Context()
	take := truthy(in["take"])
	typ := asStr(in["type"])
	if typ != "" && !contains(absenceHintTypes, typ) {
		badRequest(w, "type ungültig")
		return
	}
	found := true
	err := a.inTx(ctx, func(tx pgx.Tx) error {
		var title, from, to string
		var htype *string
		err := tx.QueryRow(ctx, `SELECT COALESCE(title,''), COALESCE(date_from,''), COALESCE(date_to,''), type FROM absence_hints WHERE user_id=$1 AND key=$2 FOR UPDATE`, u.ID, key).Scan(&title, &from, &to, &htype)
		if errors.Is(err, pgx.ErrNoRows) {
			found = false
			return nil
		}
		if err != nil {
			return err
		}
		if take {
			if !validRange(from, to) {
				return errUser("Zeitraum des Hinweises ungültig")
			}
			if typ == "" && htype != nil {
				typ = *htype
			}
			if !contains(absenceHintTypes, typ) {
				typ = "sonstiges"
			}
			var covered bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM absences WHERE user_id=$1 AND type<>'feiertag' AND date_from<=$2 AND date_to>=$3)`, u.ID, from, to).Scan(&covered); err != nil {
				return err
			}
			if !covered {
				if _, err := tx.Exec(ctx, `INSERT INTO absences(user_id,date_from,date_to,type,hours,note,source,ext_key) VALUES($1,$2,$3,$4,NULL,$5,'kalender',$6)`,
					u.ID, from, to, typ, truncate(title+" (aus Kalender)", 200), key); err != nil {
					return err // ZP001 → 409 month_locked
				}
			}
		}
		_, err = tx.Exec(ctx, `UPDATE absence_hints SET status=$3 WHERE user_id=$1 AND key=$2`, u.ID, key, map[bool]string{true: "taken", false: "dismissed"}[take])
		return err
	})
	var ue errUser
	if errors.As(err, &ue) {
		badRequest(w, string(ue))
		return
	}
	if err != nil {
		a.fail(w, err)
		return
	}
	if !found {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "nicht gefunden"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

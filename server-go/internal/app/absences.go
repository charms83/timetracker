package app

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

/* Abwesenheiten und Saldo-Korrekturen – Verhalten wie backend/server.js, je Benutzer getrennt.
   Noch nicht portiert: Kalender-Abo (.ics), Feiertage importieren. */

func (a *App) routesAbsences(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/absences", a.auth(a.asTarget(a.absencesList)))
	mux.HandleFunc("POST /api/absences", a.auth(a.absencesCreate))
	mux.HandleFunc("PUT /api/absences/{id}", a.auth(a.absencesUpdate))
	mux.HandleFunc("DELETE /api/absences/{id}", a.auth(a.absencesDelete))
	mux.HandleFunc("GET /api/balance-corrections", a.auth(a.asTarget(a.corrList)))
	mux.HandleFunc("POST /api/balance-corrections", a.auth(a.corrCreate))
	mux.HandleFunc("PUT /api/balance-corrections/{id}", a.auth(a.corrUpdate))
	mux.HandleFunc("DELETE /api/balance-corrections/{id}", a.auth(a.corrDelete))
}

func (a *App) absencesList(w http.ResponseWriter, r *http.Request, u User) {
	q := r.URL.Query()
	sql := `SELECT * FROM absences WHERE user_id=$1`
	args := []any{u.ID}
	if v := q.Get("from"); v != "" {
		args = append(args, v)
		sql += " AND date_to >= $" + strconv.Itoa(len(args))
	}
	if v := q.Get("to"); v != "" {
		args = append(args, v)
		sql += " AND date_from <= $" + strconv.Itoa(len(args))
	}
	rs, err := a.maps(r.Context(), sql+" ORDER BY date_from, id", args...)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rs)
}

// badHours: angegeben, aber keine Zahl
func badHours(v any) bool {
	if v == nil || asStr(v) == "" {
		return false
	}
	_, err := strconv.ParseFloat(strings.TrimSpace(asStr(v)), 64)
	return err != nil
}

// hoursArg: leer/fehlend → NULL, sonst Zahl (Stunden je Tag)
func hoursArg(v any) any {
	if v == nil || asStr(v) == "" {
		return nil
	}
	return asFloat(v)
}

func (a *App) absencesCreate(w http.ResponseWriter, r *http.Request, u User) {
	var in body
	if !readJSON(w, r, &in) {
		return
	}
	from, typ := asStr(in["date_from"]), asStr(in["type"])
	if from == "" || typ == "" {
		badRequest(w, "date_from + type erforderlich")
		return
	}
	to := asStr(in["date_to"])
	if to == "" {
		to = from
	}
	if !validRange(from, to) {
		badRequest(w, "Zeitraum ungültig (JJJJ-MM-TT, Ende nicht vor Beginn, höchstens 800 Tage)")
		return
	}
	if badHours(in["hours"]) {
		badRequest(w, "hours ist keine Zahl")
		return
	}
	row, err := a.one(r.Context(), `INSERT INTO absences(user_id,date_from,date_to,type,hours,note) VALUES($1,$2,$3,$4,$5,$6) RETURNING *`,
		u.ID, from, to, typ, hoursArg(in["hours"]), nz(in["note"]))
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, row)
}

func (a *App) absencesUpdate(w http.ResponseWriter, r *http.Request, u User) {
	id, ok := pathID(r)
	if !ok {
		notFound(w)
		return
	}
	var in body
	if !readJSON(w, r, &in) {
		return
	}
	from, typ := asStr(in["date_from"]), asStr(in["type"])
	if from == "" || typ == "" {
		badRequest(w, "date_from + type erforderlich")
		return
	}
	to := asStr(in["date_to"])
	if to == "" {
		to = from
	}
	if !validRange(from, to) {
		badRequest(w, "Zeitraum ungültig (JJJJ-MM-TT, Ende nicht vor Beginn, höchstens 800 Tage)")
		return
	}
	if badHours(in["hours"]) {
		badRequest(w, "hours ist keine Zahl")
		return
	}
	row, err := a.one(r.Context(), `UPDATE absences SET date_from=$3,date_to=$4,type=$5,hours=$6,note=$7 WHERE id=$1 AND user_id=$2 RETURNING *`,
		id, u.ID, from, to, typ, hoursArg(in["hours"]), nz(in["note"]))
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

func (a *App) absencesDelete(w http.ResponseWriter, r *http.Request, u User) {
	id, ok := pathID(r)
	if !ok {
		notFound(w)
		return
	}
	if _, err := a.DB.Exec(r.Context(), `DELETE FROM absences WHERE id=$1 AND user_id=$2`, id, u.ID); err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

/* ── Saldo-Korrekturen: Konto an einem Stichtag auf einen Wert setzen ── */

func (a *App) corrList(w http.ResponseWriter, r *http.Request, u User) {
	rs, err := a.maps(r.Context(), `SELECT * FROM balance_corrections WHERE user_id=$1 ORDER BY date DESC, id DESC`, u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rs)
}

func (a *App) corrCreate(w http.ResponseWriter, r *http.Request, u User) {
	var in body
	if !readJSON(w, r, &in) {
		return
	}
	date := asStr(in["date"])
	if date == "" {
		badRequest(w, "date erforderlich")
		return
	}
	if in["balance_hours"] == nil || asStr(in["balance_hours"]) == "" {
		badRequest(w, "balance_hours erforderlich")
		return
	}
	row, err := a.one(r.Context(), `INSERT INTO balance_corrections(user_id,date,balance_hours,note) VALUES($1,$2,$3,$4) RETURNING *`,
		u.ID, date, asFloat(in["balance_hours"]), nz(in["note"]))
	if err != nil {
		a.fail(w, err)
		return
	}
	a.audit(r.Context(), u.ID, "balance.correction", date+" → "+asStr(in["balance_hours"])+" h")
	writeJSON(w, http.StatusOK, row)
}

func (a *App) corrUpdate(w http.ResponseWriter, r *http.Request, u User) {
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
	add := func(col string, v any) {
		args = append(args, v)
		sets = append(sets, col+"=$"+strconv.Itoa(len(args)))
	}
	if v, has := in["date"]; has {
		if strings.TrimSpace(asStr(v)) == "" {
			badRequest(w, "date darf nicht leer sein")
			return
		}
		add("date", asStr(v))
	}
	if v, has := in["balance_hours"]; has {
		if asStr(v) == "" {
			badRequest(w, "balance_hours darf nicht leer sein")
			return
		}
		add("balance_hours", asFloat(v))
	}
	if v, has := in["note"]; has {
		add("note", nz(v))
	}
	if len(sets) > 0 {
		if _, err := a.DB.Exec(r.Context(), "UPDATE balance_corrections SET "+strings.Join(sets, ",")+" WHERE id=$1 AND user_id=$2", args...); err != nil {
			a.fail(w, err)
			return
		}
	}
	row, err := a.one(r.Context(), `SELECT * FROM balance_corrections WHERE id=$1 AND user_id=$2`, id, u.ID)
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

func (a *App) corrDelete(w http.ResponseWriter, r *http.Request, u User) {
	id, ok := pathID(r)
	if !ok {
		notFound(w)
		return
	}
	if _, err := a.DB.Exec(r.Context(), `DELETE FROM balance_corrections WHERE id=$1 AND user_id=$2`, id, u.ID); err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// validRange: gültige Tage, Ende nicht vor Beginn, höchstens 800 Tage (schützt auch die Monatssperre in der Datenbank)
func validRange(from, to string) bool {
	f, ok1 := parseDay(from)
	t, ok2 := parseDay(to)
	return ok1 && ok2 && !t.Before(f) && t.Sub(f) <= 800*24*time.Hour
}

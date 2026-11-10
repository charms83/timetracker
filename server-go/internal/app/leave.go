package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

/* Urlaubsantrag mit Genehmigung (Spezifikation §16.8):
   Person stellt Antrag → Teamleitung entscheidet; Vertretung: Personal.
   Technischer Admin entscheidet nicht (sieht keine Zeitdaten).
   Genehmigt → Abwesenheit type=urlaub; abgelehnt/storniert ohne Abwesenheit. */

func (a *App) routesLeave(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/leave", a.auth(a.leaveList))
	mux.HandleFunc("POST /api/leave", a.auth(a.leaveCreate))
	mux.HandleFunc("POST /api/leave/{id}/cancel", a.auth(a.leaveCancel))
	mux.HandleFunc("GET /api/leave/pending", a.auth(a.leavePending))
	mux.HandleFunc("POST /api/leave/{id}/decide", a.auth(a.leaveDecide))
}

// mayDecideLeave: Teamleitung des Antragstellers oder Personal; nie die eigene Person, nie Notfallzugang.
func (a *App) mayDecideLeave(ctx context.Context, approver User, target int64) bool {
	if approver.ID <= 0 || approver.ID == target || approver.Role == "breakglass" || !approver.timeAccount() {
		return false
	}
	if approver.IsHR {
		return true
	}
	if !approver.IsLead {
		return false
	}
	return a.canSee(ctx, User{ID: approver.ID, IsLead: true}, target)
}

func (a *App) leaveWorkdays(ctx context.Context, uid int64, from, to string) (float64, error) {
	st, err := a.userSettings(ctx, uid)
	if err != nil {
		return 0, err
	}
	targets := dayTargets(st)
	hol := map[string]bool{}
	rs, err := a.maps(ctx, `SELECT date_from FROM absences WHERE user_id=$1 AND type='feiertag' AND date_from>=$2 AND date_from<=$3`, uid, from, to)
	if err != nil {
		return 0, err
	}
	for _, r := range rs {
		hol[asStr(r["date_from"])] = true
	}
	f, ok1 := parseDay(from)
	t, ok2 := parseDay(to)
	if !ok1 || !ok2 {
		return 0, errors.New("Zeitraum ungültig")
	}
	var days float64
	for d := f; !d.After(t); d = d.AddDate(0, 0, 1) {
		w := int(d.Weekday())
		if w > 0 && w < 6 && !hol[dayStr(d)] && targets[w] > 0 {
			days++
		}
	}
	return days, nil
}

func (a *App) leaveList(w http.ResponseWriter, r *http.Request, u User) {
	ctx := r.Context()
	sql := `SELECT l.*, d.name AS decided_by_name FROM leave_requests l
		LEFT JOIN users d ON d.id=l.decided_by WHERE l.user_id=$1`
	args := []any{u.ID}
	if st := r.URL.Query().Get("status"); st != "" {
		args = append(args, st)
		sql += " AND l.status=$" + strconv.Itoa(len(args))
	}
	rs, err := a.maps(ctx, sql+" ORDER BY l.date_from DESC, l.id DESC LIMIT 200", args...)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rs)
}

func (a *App) leaveCreate(w http.ResponseWriter, r *http.Request, u User) {
	var in body
	if !readJSON(w, r, &in) {
		return
	}
	from, to := asStr(in["date_from"]), asStr(in["date_to"])
	if to == "" {
		to = from
	}
	if !validRange(from, to) {
		badRequest(w, "Zeitraum ungültig (JJJJ-MM-TT, Ende nicht vor Beginn)")
		return
	}
	if from < a.today() {
		badRequest(w, "Urlaubsantrag nur ab heute (Vergangenheit bitte als Abwesenheit eintragen)")
		return
	}
	note := truncRunes(strings.TrimSpace(asStr(in["note"])), 500)
	ctx := r.Context()
	days, err := a.leaveWorkdays(ctx, u.ID, from, to)
	if err != nil {
		a.fail(w, err)
		return
	}
	if days <= 0 {
		badRequest(w, "Im Zeitraum liegen keine Werktage")
		return
	}
	if v := asFloat(in["days"]); v > 0 {
		days = v
	}
	var overlap int
	_ = a.DB.QueryRow(ctx, `SELECT COUNT(*) FROM leave_requests WHERE user_id=$1 AND status IN ('pending','approved')
		AND date_from<=$3 AND date_to>=$2`, u.ID, from, to).Scan(&overlap)
	if overlap > 0 {
		badRequest(w, "Es gibt schon einen offenen oder genehmigten Antrag in diesem Zeitraum")
		return
	}
	rs, err := a.maps(ctx, `INSERT INTO leave_requests(user_id,date_from,date_to,days,note,status)
		VALUES($1,$2,$3,$4,$5,'pending') RETURNING *`, u.ID, from, to, days, note)
	if err != nil {
		a.fail(w, err)
		return
	}
	a.audit(ctx, u.ID, "leave.submit", fmt.Sprintf("%s–%s (%.1f Tage)", from, to, days))
	a.sysLog(ctx, u.ID, "info", "urlaub", fmt.Sprintf("Antrag %s–%s gestellt", from, to))
	writeJSON(w, http.StatusOK, rs[0])
}

func (a *App) leaveCancel(w http.ResponseWriter, r *http.Request, u User) {
	id, ok := pathID(r)
	if !ok {
		notFound(w)
		return
	}
	ctx := r.Context()
	tag, err := a.DB.Exec(ctx, `UPDATE leave_requests SET status='cancelled' WHERE id=$1 AND user_id=$2 AND status='pending'`, id, u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	if tag.RowsAffected() == 0 {
		badRequest(w, "Nur eigene offene Anträge können storniert werden")
		return
	}
	a.audit(ctx, u.ID, "leave.cancel", "Antrag #"+strconv.FormatInt(id, 10))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *App) leavePending(w http.ResponseWriter, r *http.Request, u User) {
	if !u.IsLead && !u.IsHR {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "Nur Teamleitung oder Personal"})
		return
	}
	ctx := r.Context()
	rs, err := a.maps(ctx, `SELECT l.*, u.name AS user_name, u.email AS user_email FROM leave_requests l
		JOIN users u ON u.id=l.user_id WHERE l.status='pending' ORDER BY l.date_from, l.id`)
	if err != nil {
		a.fail(w, err)
		return
	}
	var out []map[string]any
	for _, row := range rs {
		tid := int64(asFloat(row["user_id"]))
		if a.mayDecideLeave(ctx, u, tid) {
			out = append(out, row)
		}
	}
	if out == nil {
		out = []map[string]any{}
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *App) leaveDecide(w http.ResponseWriter, r *http.Request, u User) {
	id, ok := pathID(r)
	if !ok {
		notFound(w)
		return
	}
	var in body
	if !readJSON(w, r, &in) {
		return
	}
	decision := strings.ToLower(asStr(in["decision"]))
	if decision != "approved" && decision != "rejected" {
		badRequest(w, "decision: approved oder rejected")
		return
	}
	note := truncRunes(strings.TrimSpace(asStr(in["note"])), 500)
	ctx := r.Context()

	tx, err := a.DB.Begin(ctx)
	if err != nil {
		a.fail(w, err)
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var uid int64
	var from, to, st string
	var days float64
	var leaveNote string
	err = tx.QueryRow(ctx, `SELECT user_id, date_from, date_to, days, note, status FROM leave_requests WHERE id=$1 FOR UPDATE`, id).
		Scan(&uid, &from, &to, &days, &leaveNote, &st)
	if errors.Is(err, pgx.ErrNoRows) {
		notFound(w)
		return
	}
	if err != nil {
		a.fail(w, err)
		return
	}
	if st != "pending" {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "Antrag ist nicht mehr offen"})
		return
	}
	if !a.mayDecideLeave(ctx, u, uid) {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "Keine Berechtigung für diesen Antrag"})
		return
	}

	var absenceID any
	if decision == "approved" {
		var aid int64
		err = tx.QueryRow(ctx, `INSERT INTO absences(user_id,date_from,date_to,type,note)
			VALUES($1,$2,$3,'urlaub',$4) RETURNING id`, uid, from, to, nz(leaveNote)).Scan(&aid)
		if err != nil {
			a.fail(w, err)
			return
		}
		absenceID = aid
	}
	rs, err := mapsQ(ctx, tx, `UPDATE leave_requests SET status=$2, decided_by=$3, decided_at=now(), decision_note=$4, absence_id=$5
		WHERE id=$1 AND status='pending' RETURNING *`, id, decision, u.ID, note, absenceID)
	if err != nil {
		a.fail(w, err)
		return
	}
	if len(rs) == 0 {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "Antrag ist nicht mehr offen"})
		return
	}
	if err = tx.Commit(ctx); err != nil {
		a.fail(w, err)
		return
	}
	a.audit(ctx, u.ID, "leave."+decision, fmt.Sprintf("Antrag #%d Person %d %s–%s", id, uid, from, to))
	a.sysLog(ctx, uid, "info", "urlaub", fmt.Sprintf("Antrag %s–%s: %s (durch %s)", from, to, decision, u.Name))
	writeJSON(w, http.StatusOK, rs[0])
}

// leaveNotices: offene eigene Anträge + ausstehende Entscheidungen für Lead/HR
func (a *App) leaveNotices(ctx context.Context, u User) []string {
	var out []string
	var n int
	if err := a.DB.QueryRow(ctx, `SELECT COUNT(*) FROM leave_requests WHERE user_id=$1 AND status='pending' AND date_from>=$2`, u.ID, a.today()).Scan(&n); err == nil && n > 0 {
		out = append(out, fmt.Sprintf("%d Urlaubsantrag%s wartet auf Genehmigung.", n, map[bool]string{true: "", false: "e"}[n == 1]))
	}
	if u.IsLead || u.IsHR {
		rs, err := a.maps(ctx, `SELECT user_id FROM leave_requests WHERE status='pending'`)
		if err == nil {
			c := 0
			for _, r := range rs {
				if a.mayDecideLeave(ctx, u, int64(asFloat(r["user_id"]))) {
					c++
				}
			}
			if c > 0 {
				out = append(out, fmt.Sprintf("%d Urlaubsantrag%s zur Entscheidung.", c, map[bool]string{true: "", false: "e"}[c == 1]))
			}
		}
	}
	var rejected int
	since := time.Now().Add(-14 * 24 * time.Hour).Format(time.RFC3339)
	if err := a.DB.QueryRow(ctx, `SELECT COUNT(*) FROM leave_requests WHERE user_id=$1 AND status='rejected' AND decided_at>=$2::timestamptz`, u.ID, since).Scan(&rejected); err == nil && rejected > 0 {
		out = append(out, "Ein Urlaubsantrag wurde abgelehnt – bitte unter Abwesenheiten nachsehen.")
	}
	return out
}

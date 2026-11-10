package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

/* Monatsfreigabe (wie der Stundenzettel in Tempo): Person reicht den Monat ein, Teamleiter oder Personal geben frei oder lehnen mit Kommentar ab.
   Eingereichte und freigegebene Monate sind schreibgeschützt (Sperre in der Datenbank, Migration 013). Eigener Monat: nie selbst freigeben.
   Einstellung „approval_mode“: either (Standard: Teamleiter ODER Personal), lead (nur Teamleiter), hr (nur Personal). */

var monthRE = regexp.MustCompile(`^[0-9]{4}-(0[1-9]|1[0-2])$`)

func (a *App) routesApproval(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/approval/status", a.auth(a.asTarget(a.approvalStatus)))
	mux.HandleFunc("POST /api/approval/submit", a.auth(a.approvalSubmit))
	mux.HandleFunc("POST /api/approval/withdraw", a.auth(a.approvalWithdraw))
	mux.HandleFunc("GET /api/approval/pending", a.auth(a.approvalPending))
	mux.HandleFunc("POST /api/approval/decide", a.auth(a.approvalDecide))
	mux.HandleFunc("POST /api/approval/reopen", a.auth(a.approvalReopen))
}

func (a *App) approvalMode(ctx context.Context) string {
	switch m := a.conf(ctx, "approval_mode"); m {
	case "lead", "hr":
		return m
	}
	return "either"
}

// mayDecide: darf approver den Monat von target freigeben/ablehnen? (nie die eigene Person)
func (a *App) mayDecide(ctx context.Context, approver User, target int64) bool {
	if approver.ID == target || approver.Role == "breakglass" {
		return false
	}
	mode := a.approvalMode(ctx)
	if approver.IsHR && mode != "lead" {
		return true
	}
	if mode == "hr" || !approver.IsLead {
		return false
	}
	return a.canSee(ctx, User{ID: approver.ID, IsLead: true}, target)
}

func (a *App) approvalStatus(w http.ResponseWriter, r *http.Request, u User) {
	month := r.URL.Query().Get("month")
	if !monthRE.MatchString(month) {
		badRequest(w, "month: JJJJ-MM")
		return
	}
	row, err := a.one(r.Context(), `SELECT a.status, a.submitted_at, a.decided_at, a.comment, d.name AS decided_by_name
		FROM month_approvals a LEFT JOIN users d ON d.id=a.decided_by WHERE a.user_id=$1 AND a.month=$2`, u.ID, month)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		a.fail(w, err)
		return
	}
	hist, err := a.maps(r.Context(), `SELECT l.at, l.action, l.comment, x.name AS actor FROM month_approval_log l LEFT JOIN users x ON x.id=l.actor_id
		WHERE l.user_id=$1 AND l.month=$2 ORDER BY l.id DESC LIMIT 50`, u.ID, month)
	if err != nil {
		a.fail(w, err)
		return
	}
	out := map[string]any{"month": month, "status": "open", "history": hist}
	for k, v := range row {
		out[k] = v
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *App) approvalSubmit(w http.ResponseWriter, r *http.Request, u User) {
	var in struct {
		Month string `json:"month"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if !monthRE.MatchString(in.Month) {
		badRequest(w, "month: JJJJ-MM")
		return
	}
	if in.Month > time.Now().In(a.Loc).Format("2006-01") {
		badRequest(w, "Ein Monat in der Zukunft kann nicht eingereicht werden")
		return
	}
	ctx := r.Context()
	var running bool // Ein laufender Timer würde beim Stoppen an der Sperre scheitern
	_ = a.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM timer_state WHERE user_id=$1 AND start_time IS NOT NULL AND start_time<>'')`, u.ID).Scan(&running)
	if running {
		badRequest(w, "Bitte erst den laufenden Timer beenden")
		return
	}
	tx, err := a.DB.Begin(ctx)
	if err != nil {
		a.fail(w, err)
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if err = lockApproval(ctx, tx, u.ID); err != nil {
		a.fail(w, err)
		return
	}
	var st string
	err = tx.QueryRow(ctx, `SELECT status FROM month_approvals WHERE user_id=$1 AND month=$2 FOR UPDATE`, u.ID, in.Month).Scan(&st)
	if err == nil && (st == "submitted" || st == "approved") {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "Der Monat ist schon eingereicht oder freigegeben"})
		return
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		a.fail(w, err)
		return
	}
	if _, err = tx.Exec(ctx, `INSERT INTO month_approvals(user_id,month,status,submitted_at) VALUES($1,$2,'submitted',now())
		ON CONFLICT (user_id,month) DO UPDATE SET status='submitted', submitted_at=now(), decided_at=NULL, decided_by=NULL, comment=NULL`, u.ID, in.Month); err != nil {
		a.fail(w, err)
		return
	}
	if _, err = tx.Exec(ctx, `INSERT INTO month_approval_log(user_id,month,action,actor_id) VALUES($1,$2,'submit',$1)`, u.ID, in.Month); err != nil {
		a.fail(w, err)
		return
	}
	if err = tx.Commit(ctx); err != nil {
		a.fail(w, err)
		return
	}
	a.audit(ctx, u.ID, "approval.submit", in.Month)
	a.tempoMonthHook(u.ID)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "status": "submitted"})
}

func (a *App) approvalWithdraw(w http.ResponseWriter, r *http.Request, u User) {
	var in struct {
		Month string `json:"month"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if !monthRE.MatchString(in.Month) {
		badRequest(w, "month: JJJJ-MM")
		return
	}
	ctx := r.Context()
	tx, err := a.DB.Begin(ctx)
	if err != nil {
		a.fail(w, err)
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if err = lockApproval(ctx, tx, u.ID); err != nil {
		a.fail(w, err)
		return
	}
	tag, err := tx.Exec(ctx, `DELETE FROM month_approvals WHERE user_id=$1 AND month=$2 AND status='submitted'`, u.ID, in.Month)
	if err != nil {
		a.fail(w, err)
		return
	}
	if tag.RowsAffected() == 0 {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "Zurückziehen geht nur für eingereichte, noch nicht entschiedene Monate"})
		return
	}
	if _, err = tx.Exec(ctx, `INSERT INTO month_approval_log(user_id,month,action,actor_id) VALUES($1,$2,'withdraw',$1)`, u.ID, in.Month); err != nil {
		a.fail(w, err)
		return
	}
	if err = tx.Commit(ctx); err != nil {
		a.fail(w, err)
		return
	}
	a.audit(ctx, u.ID, "approval.withdraw", in.Month)
	a.tempoMonthHook(u.ID)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "status": "open"})
}

func (a *App) approvalPending(w http.ResponseWriter, r *http.Request, u User) {
	mode := a.approvalMode(r.Context())
	hrOK := u.IsHR && mode != "lead"
	leadOK := u.IsLead && mode != "hr"
	if !hrOK && !leadOK {
		writeJSON(w, http.StatusOK, map[string]any{"items": []any{}})
		return
	}
	rs, err := a.maps(r.Context(), `SELECT a.user_id, u.name, a.month, a.submitted_at FROM month_approvals a JOIN users u ON u.id=a.user_id
		WHERE a.status='submitted' AND a.user_id<>$1 AND u.role<>'breakglass'
		  AND ($2 OR a.user_id IN (SELECT m2.user_id FROM team_members m1 JOIN team_members m2 ON m1.team_id=m2.team_id WHERE m1.user_id=$1 AND m1.is_lead))
		ORDER BY a.month, u.name LIMIT 500`, u.ID, hrOK)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": rs})
}

func (a *App) approvalDecide(w http.ResponseWriter, r *http.Request, u User) {
	var in struct {
		UserID   int64  `json:"user_id"`
		Month    string `json:"month"`
		Decision string `json:"decision"`
		Comment  string `json:"comment"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	in.Comment = strings.TrimSpace(in.Comment)
	if !monthRE.MatchString(in.Month) || in.UserID <= 0 || (in.Decision != "approve" && in.Decision != "reject") || len(in.Comment) > 500 {
		badRequest(w, "Eingabe ungültig")
		return
	}
	if in.Decision == "reject" && len([]rune(in.Comment)) < 3 {
		badRequest(w, "Ablehnen braucht einen Kommentar")
		return
	}
	ctx := r.Context()
	if !a.mayDecide(ctx, u, in.UserID) {
		a.audit(ctx, u.ID, "approval.denied", fmt.Sprintf("Person %d, Monat %s", in.UserID, in.Month))
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "nicht gefunden"})
		return
	}
	status := "approved"
	if in.Decision == "reject" {
		status = "rejected"
	}
	tx, err := a.DB.Begin(ctx)
	if err != nil {
		a.fail(w, err)
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if err = lockApproval(ctx, tx, in.UserID); err != nil {
		a.fail(w, err)
		return
	}
	var st string
	if err = tx.QueryRow(ctx, `SELECT status FROM month_approvals WHERE user_id=$1 AND month=$2 FOR UPDATE`, in.UserID, in.Month).Scan(&st); err != nil || st != "submitted" {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "Der Monat ist nicht (mehr) zur Freigabe eingereicht"})
		return
	}
	if _, err = tx.Exec(ctx, `UPDATE month_approvals SET status=$3, decided_at=now(), decided_by=$4, comment=NULLIF($5,'') WHERE user_id=$1 AND month=$2`,
		in.UserID, in.Month, status, u.ID, in.Comment); err != nil {
		a.fail(w, err)
		return
	}
	if _, err = tx.Exec(ctx, `INSERT INTO month_approval_log(user_id,month,action,actor_id,comment) VALUES($1,$2,$3,$4,NULLIF($5,''))`, in.UserID, in.Month, in.Decision, u.ID, in.Comment); err != nil {
		a.fail(w, err)
		return
	}
	if err = tx.Commit(ctx); err != nil {
		a.fail(w, err)
		return
	}
	a.audit(ctx, u.ID, "approval."+in.Decision, fmt.Sprintf("Person %d, Monat %s", in.UserID, in.Month))
	a.tempoMonthHook(in.UserID) // Übergangs-Ticket schließen/wiederöffnen (Hintergrund, blockiert nie)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "status": status})
}

// approvalReopen: Personal öffnet einen eingereichten/freigegebenen Monat wieder (mit Begründung, protokolliert)
func (a *App) approvalReopen(w http.ResponseWriter, r *http.Request, u User) {
	var in struct {
		UserID  int64  `json:"user_id"`
		Month   string `json:"month"`
		Comment string `json:"comment"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	in.Comment = strings.TrimSpace(in.Comment)
	if !monthRE.MatchString(in.Month) || in.UserID <= 0 || len([]rune(in.Comment)) < 3 || len(in.Comment) > 500 {
		badRequest(w, "Monat, Person und eine Begründung sind nötig")
		return
	}
	ctx := r.Context()
	if !u.IsHR || u.Role == "breakglass" || u.ID == in.UserID {
		a.audit(ctx, u.ID, "approval.denied", fmt.Sprintf("Wiedereröffnen Person %d, Monat %s", in.UserID, in.Month))
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "nicht gefunden"})
		return
	}
	tx, err := a.DB.Begin(ctx)
	if err != nil {
		a.fail(w, err)
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if err = lockApproval(ctx, tx, in.UserID); err != nil {
		a.fail(w, err)
		return
	}
	tag, err := tx.Exec(ctx, `UPDATE month_approvals SET status='rejected', decided_at=now(), decided_by=$3, comment=$4 WHERE user_id=$1 AND month=$2 AND status IN ('submitted','approved')`,
		in.UserID, in.Month, u.ID, in.Comment)
	if err != nil {
		a.fail(w, err)
		return
	}
	if tag.RowsAffected() == 0 {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "Der Monat ist nicht eingereicht oder freigegeben"})
		return
	}
	if _, err = tx.Exec(ctx, `INSERT INTO month_approval_log(user_id,month,action,actor_id,comment) VALUES($1,$2,'reopen',$3,$4)`, in.UserID, in.Month, u.ID, in.Comment); err != nil {
		a.fail(w, err)
		return
	}
	if err = tx.Commit(ctx); err != nil {
		a.fail(w, err)
		return
	}
	a.audit(ctx, u.ID, "approval.reopen", fmt.Sprintf("Person %d, Monat %s", in.UserID, in.Month))
	a.tempoMonthHook(in.UserID) // Übergangs-Ticket wieder öffnen (Hintergrund, blockiert nie)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "status": "rejected"})
}

// workdayOfMonth: wievielter Werktag (Mo–Fr, Feiertage nicht berücksichtigt) ist t im Monat?
func workdayOfMonth(t time.Time) int {
	n := 0
	for d := time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, t.Location()); !d.After(t); d = d.AddDate(0, 0, 1) {
		if wd := d.Weekday(); wd != time.Saturday && wd != time.Sunday {
			n++
		}
	}
	return n
}

// approvalNotices: Erinnerungen (Vormonat nicht eingereicht/abgelehnt, offene Freigaben)
func (a *App) approvalNotices(ctx context.Context, u User) []string {
	if u.Role == "breakglass" || !u.timeAccount() { // Notfall- und reines Administrationskonto erfassen keine Zeiten: keine Fristen-Erinnerung
		return nil
	}
	var out []string
	now := time.Now().In(a.Loc)
	prev := now.AddDate(0, -1, 0).Format("2006-01")
	submitBy := a.confInt(ctx, "approval_submit_workday", 3)
	var st, cm string
	err := a.DB.QueryRow(ctx, `SELECT status, COALESCE(comment,'') FROM month_approvals WHERE user_id=$1 AND month=$2`, u.ID, prev).Scan(&st, &cm)
	switch {
	case err == nil && st == "rejected":
		out = append(out, "Monat "+prev+" wurde abgelehnt: "+cm+" – bitte korrigieren und neu einreichen.")
	case errors.Is(err, pgx.ErrNoRows) && workdayOfMonth(now) >= submitBy:
		out = append(out, fmt.Sprintf("Monat %s ist noch nicht eingereicht (Frist: %d. Werktag).", prev, submitBy))
	}
	if u.IsHR || u.IsLead {
		var n int
		mode := a.approvalMode(ctx)
		if err := a.DB.QueryRow(ctx, `SELECT COUNT(*) FROM month_approvals a WHERE a.status='submitted' AND a.user_id<>$1
			AND ($2 OR a.user_id IN (SELECT m2.user_id FROM team_members m1 JOIN team_members m2 ON m1.team_id=m2.team_id WHERE m1.user_id=$1 AND m1.is_lead))`,
			u.ID, u.IsHR && mode != "lead").Scan(&n); err == nil && n > 0 && (mode != "hr" || u.IsHR) && (mode != "lead" || u.IsLead) {
			out = append(out, fmt.Sprintf("%d Monat(e) warten auf deine Freigabe.", n))
		}
	}
	return out
}

func (a *App) confInt(ctx context.Context, key string, def int) int {
	var n int
	if _, err := fmt.Sscan(a.conf(ctx, key), &n); err != nil || n < 1 || n > 31 {
		return def
	}
	return n
}

// lockApproval nimmt die Personen-Sperre exklusiv (Gegenstück zur gemeinsamen Sperre in zp_month_locked, Migration 013):
// wartet, bis laufende Schreibzugriffe dieser Person beendet sind, und hält neue bis zum Ende der Transaktion an.
func lockApproval(ctx context.Context, tx pgx.Tx, uid int64) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, 7270030000000000+uid)
	return err
}

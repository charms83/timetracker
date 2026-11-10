package app

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"
)

/* Integrations-API für Abrechnungsprogramme (Lohn u. a.).
   Token zpint_… nur vom Administrator; Scope payroll → /api/integrations/payroll/*.
   Kein Mitarbeiter-Zugang, kein /api/admin. Klartext einmal bei Erstellung. */

const integrationTokenPrefix = "zpint_"
const integrationHMACPrefix = "v1:"

func (a *App) routesIntegrations(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/admin/integrations", a.strictAdmin(a.integrationsList))
	mux.HandleFunc("POST /api/admin/integrations", a.strictAdmin(a.integrationsCreate))
	mux.HandleFunc("DELETE /api/admin/integrations/{id}", a.strictAdmin(a.integrationsRevoke))
	mux.HandleFunc("GET /api/integrations/payroll", a.integrationScope("payroll", a.payrollMonth))
	mux.HandleFunc("GET /api/integrations/payroll/entries", a.integrationScope("payroll", a.payrollEntries))
}

func (a *App) integrationTokenHash(raw string) string {
	key := a.Key
	if len(key) == 0 {
		key = make([]byte, 32)
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte("integration_token|v1|"))
	_, _ = mac.Write([]byte(raw))
	return integrationHMACPrefix + hex.EncodeToString(mac.Sum(nil))
}

func (a *App) integrationsList(w http.ResponseWriter, r *http.Request, u User) {
	rs, err := a.maps(r.Context(), `SELECT id, label, scopes, created_at, last_used, revoked_at,
		(revoked_at IS NULL) AS active FROM integration_tokens ORDER BY created_at DESC`)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rs)
}

func (a *App) integrationsCreate(w http.ResponseWriter, r *http.Request, u User) {
	var in body
	if !readJSON(w, r, &in) {
		return
	}
	label := truncRunes(strings.TrimSpace(asStr(in["label"])), 80)
	if label == "" {
		label = "Abrechnung"
	}
	scopes := []string{"payroll"}
	if raw, ok := in["scopes"].([]any); ok && len(raw) > 0 {
		scopes = scopes[:0]
		for _, x := range raw {
			s := strings.ToLower(strings.TrimSpace(fmt.Sprint(x)))
			if s == "payroll" {
				scopes = append(scopes, s)
			}
		}
		if len(scopes) == 0 {
			badRequest(w, "scopes: derzeit nur payroll")
			return
		}
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		a.fail(w, err)
		return
	}
	raw := integrationTokenPrefix + base64.RawURLEncoding.EncodeToString(b)
	ctx := r.Context()
	var id int64
	err := a.DB.QueryRow(ctx, `INSERT INTO integration_tokens(token_hash,label,scopes,created_by) VALUES($1,$2,$3,$4) RETURNING id`,
		a.integrationTokenHash(raw), label, scopes, u.ID).Scan(&id)
	if err != nil {
		a.fail(w, err)
		return
	}
	a.audit(ctx, u.ID, "integration.create", fmt.Sprintf("#%d %s %v", id, label, scopes))
	writeJSON(w, http.StatusOK, map[string]any{
		"id": id, "label": label, "scopes": scopes, "token": raw,
		"hint": "Token nur einmal sichtbar – im Abrechnungsprogramm hinterlegen. Header: Authorization: Bearer zpint_…",
		"usage": map[string]string{
			"header":  "Authorization: Bearer <token>",
			"example": "curl -sH 'Authorization: Bearer zpint_…' 'https://zeit.example/api/integrations/payroll?month=2026-11'",
		},
	})
}

func (a *App) integrationsRevoke(w http.ResponseWriter, r *http.Request, u User) {
	id, ok := pathID(r)
	if !ok {
		notFound(w)
		return
	}
	ctx := r.Context()
	tag, err := a.DB.Exec(ctx, `UPDATE integration_tokens SET revoked_at=now() WHERE id=$1 AND revoked_at IS NULL`, id)
	if err != nil {
		a.fail(w, err)
		return
	}
	if tag.RowsAffected() == 0 {
		notFound(w)
		return
	}
	a.audit(ctx, u.ID, "integration.revoke", "Integration #"+fmt.Sprint(id))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *App) integrationFromRequest(r *http.Request) (id int64, scopes []string, ok bool) {
	raw := r.Header.Get("Authorization")
	if strings.HasPrefix(raw, "Bearer ") {
		raw = strings.TrimSpace(strings.TrimPrefix(raw, "Bearer "))
	} else {
		raw = r.Header.Get("X-ZP-Integration-Token")
	}
	if raw == "" || !strings.HasPrefix(raw, integrationTokenPrefix) || len(raw) > 200 {
		return 0, nil, false
	}
	err := a.DB.QueryRow(r.Context(), `SELECT id, scopes FROM integration_tokens
		WHERE token_hash=$1 AND revoked_at IS NULL`, a.integrationTokenHash(raw)).Scan(&id, &scopes)
	if err != nil {
		return 0, nil, false
	}
	_, _ = a.DB.Exec(r.Context(), `UPDATE integration_tokens SET last_used=now()
		WHERE id=$1 AND (last_used IS NULL OR last_used < now() - interval '1 minute')`, id)
	return id, scopes, true
}

func (a *App) integrationScope(need string, h func(http.ResponseWriter, *http.Request, int64)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, scopes, ok := a.integrationFromRequest(r)
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]any{
				"error": "integration_token_required",
				"hint":  "Authorization: Bearer zpint_… (Admin legt Token unter /admin an)",
			})
			return
		}
		allowed := false
		for _, s := range scopes {
			if s == need {
				allowed = true
				break
			}
		}
		if !allowed {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": "scope fehlt: " + need, "code": "scope"})
			return
		}
		h(w, r, id)
	}
}

// payrollMonth: Monatssummen je Person für Abrechnung (Standard: nur freigegeben/geschlossen).
func (a *App) payrollMonth(w http.ResponseWriter, r *http.Request, _ int64) {
	month, from, to, ok := a.overviewMonth(r)
	if !ok {
		badRequest(w, "month: JJJJ-MM")
		return
	}
	onlyReady := r.URL.Query().Get("all") != "1" // all=1 → alle Status (Vollständigkeitsprüfung)
	ctx := r.Context()
	type payPerson struct {
		ID          int64   `json:"id"`
		Name        string  `json:"name"`
		Email       string  `json:"email"`
		Status      string  `json:"status"`
		SubmittedAt any     `json:"submitted_at"`
		DecidedAt   any     `json:"decided_at"`
		SollH       float64 `json:"soll_h"`
		IstH        float64 `json:"ist_h"`
		SaldoH      float64 `json:"saldo_h"`
		KontoH      any     `json:"konto_h"`
	}
	q, err := a.DB.Query(ctx, `SELECT u.id, u.name, u.email,
		a.status, a.submitted_at, a.decided_at
		FROM users u LEFT JOIN month_approvals a ON a.user_id=u.id AND a.month=$1
		WHERE `+activeSQL+`
		ORDER BY u.name, u.id`, month)
	if err != nil {
		a.fail(w, err)
		return
	}
	rows := []*personRow{}
	emails := map[int64]string{}
	func() {
		defer q.Close()
		for q.Next() {
			p := &personRow{}
			var st *string
			var sub, dec *time.Time
			var email string
			if err = q.Scan(&p.ID, &p.Name, &email, &st, &sub, &dec); err != nil {
				return
			}
			p.Status, p.StatusRaw = normStatus(st)
			p.SubmittedAt, p.DecidedAt = tsOrNil(sub), tsOrNil(dec)
			p.setTeams(nil)
			if onlyReady && p.Status != "approved" && p.Status != "closed" {
				continue
			}
			rows = append(rows, p)
			emails[p.ID] = email
		}
		err = q.Err()
	}()
	if err != nil {
		a.fail(w, err)
		return
	}
	if err := a.fillFigures(ctx, rows, month, from, to); err != nil {
		a.fail(w, err)
		return
	}
	out := make([]payPerson, 0, len(rows))
	for _, p := range rows {
		out = append(out, payPerson{
			ID: p.ID, Name: p.Name, Email: emails[p.ID], Status: p.Status,
			SubmittedAt: p.SubmittedAt, DecidedAt: p.DecidedAt,
			SollH: p.SollH, IstH: p.IstH, SaldoH: p.SaldoH, KontoH: p.KontoH,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"month": month, "from": from, "to": to,
		"filter": map[string]any{"ready_only": onlyReady, "hint": "Standard nur approved/closed; ?all=1 für alle Status"},
		"people": out,
	})
	a.auditNil(ctx, "integration.payroll", fmt.Sprintf("month=%s n=%d token", month, len(out)))
}

// payrollEntries: Einzelbuchungen einer Person im Monat (nur wenn Monat freigegeben/geschlossen, außer ?all=1).
func (a *App) payrollEntries(w http.ResponseWriter, r *http.Request, _ int64) {
	month, from, to, ok := a.overviewMonth(r)
	if !ok {
		badRequest(w, "month: JJJJ-MM")
		return
	}
	uid, err := strconvParseID(r.URL.Query().Get("user_id"))
	if err != nil || uid <= 0 {
		badRequest(w, "user_id erforderlich")
		return
	}
	ctx := r.Context()
	var st string
	_ = a.DB.QueryRow(ctx, `SELECT COALESCE(status,'open') FROM month_approvals WHERE user_id=$1 AND month=$2`, uid, month).Scan(&st)
	if st == "" {
		st = "open"
	}
	if r.URL.Query().Get("all") != "1" && st != "approved" && st != "closed" {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error": "Monat nicht freigegeben", "code": "month_not_ready", "status": st, "month": month, "user_id": uid,
		})
		return
	}
	rs, err := a.maps(ctx, `SELECT e.id, e.date, e.start_time, e.end_time, e.duration_seconds,
		e.comment, e.activity, e.source, e.is_absence,
		p.name AS project_name, COALESCE(p.jira_key,'') AS project_key,
		t.name AS task_name
		FROM time_entries e
		LEFT JOIN projects p ON p.id=e.project_id
		LEFT JOIN tasks t ON t.id=e.task_id
		WHERE e.user_id=$1 AND e.date>=$2 AND e.date<=$3 AND e.end_time IS NOT NULL
		ORDER BY e.date, e.start_time, e.id`, uid, from, to)
	if err != nil {
		a.fail(w, err)
		return
	}
	var name, email string
	_ = a.DB.QueryRow(ctx, `SELECT name, email FROM users WHERE id=$1`, uid).Scan(&name, &email)
	writeJSON(w, http.StatusOK, map[string]any{
		"month": month, "from": from, "to": to, "user_id": uid, "name": name, "email": email,
		"status": st, "entries": rs,
	})
}

func strconvParseID(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("leer")
	}
	var n int64
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("keine Zahl")
		}
		n = n*10 + int64(c-'0')
	}
	return n, nil
}

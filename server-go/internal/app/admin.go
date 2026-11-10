package app

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

/* Admin-Oberfläche (/admin) und Admin-API (/api/admin/*): Status, Logs, Benutzer, Einstellungen.
   Nur Rolle admin. Dieselben Funktionen gibt es als Befehle am Server (cmd/server/cli.go). */

//go:embed admin.html
var adminHTML []byte

var userRoles = []string{"user", "lead", "admin"}

// adminOnly schützt eine Route: angemeldet UND Rolle admin
func (a *App) adminOnly(h func(http.ResponseWriter, *http.Request, User)) http.HandlerFunc {
	return a.auth(func(w http.ResponseWriter, r *http.Request, u User) {
		if u.Role != "admin" && u.Role != "breakglass" { // Notfallzugang: die erlaubten Routen filtert auth() vorher (Allow-List)
			writeJSON(w, http.StatusForbidden, map[string]any{"error": "Nur für Administratoren"})
			return
		}
		h(w, r, u)
	})
}

func (a *App) routesAdmin(mux *http.ServeMux) {
	mux.HandleFunc("GET /admin", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(adminHTML)
	})
	mux.HandleFunc("GET /api/admin/status", a.adminOnly(a.adminStatus))
	mux.HandleFunc("GET /api/admin/selfcheck", a.adminOnly(a.adminSelfcheck))
	mux.HandleFunc("POST /api/admin/selfcheck", a.adminOnly(a.adminSelfcheck))
	mux.HandleFunc("GET /api/admin/logs", a.adminOnly(a.adminLogs))
	mux.HandleFunc("GET /api/admin/audit", a.adminOnly(a.adminAudit))
	mux.HandleFunc("GET /api/admin/users", a.adminOnly(a.adminUsers))
	mux.HandleFunc("PUT /api/admin/users/{id}", a.adminOnly(a.adminUserUpdate))
	mux.HandleFunc("POST /api/admin/emergency/close-month", a.adminOnly(a.emergencyCloseMonth))
	mux.HandleFunc("GET /api/admin/config", a.adminOnly(a.adminConfigGet))
	mux.HandleFunc("PUT /api/admin/config", a.adminOnly(a.adminConfigPut))
	a.routesAdminAuth(mux)
}

func (a *App) adminStatus(w http.ResponseWriter, r *http.Request, u User) {
	ctx := r.Context()
	count := func(sql string, args ...any) int64 {
		var n int64
		_ = a.DB.QueryRow(ctx, sql, args...).Scan(&n)
		return n
	}
	dbOK := a.DB.Ping(ctx) == nil
	migs := []string{}
	if rows, err := a.DB.Query(ctx, `SELECT name FROM schema_migrations ORDER BY name`); err == nil {
		for rows.Next() {
			var n string
			if rows.Scan(&n) == nil {
				migs = append(migs, n)
			}
		}
		rows.Close()
	}
	since := time.Now().Add(-24 * time.Hour).UTC().Format(isoMillis)
	_, planeOK := a.primaryPlaneWorkspace(ctx)
	writeJSON(w, http.StatusOK, map[string]any{
		"version": serverVersion, "go": runtime.Version(), "started": a.Started.UTC().Format(time.RFC3339),
		"uptime_seconds": int64(time.Since(a.Started).Seconds()), "timezone": a.Loc.String(),
		"database": map[string]any{"ok": dbOK, "migrations": migs},
		"setup": map[string]any{"done": a.conf(ctx, "setup_done") == "1", "auth_mode": a.conf(ctx, "auth_mode"),
			"allow_http": a.AllowHTTP, "plane_configured": planeOK},
		"counts": map[string]any{
			"users": count(`SELECT COUNT(*) FROM users`), "users_disabled": count(`SELECT COUNT(*) FROM users WHERE disabled`),
			"projects": count(`SELECT COUNT(*) FROM projects`), "time_entries": count(`SELECT COUNT(*) FROM time_entries`),
			"plane_pending_deletions": count(`SELECT COUNT(*) FROM plane_deletions`),
			"errors_24h":              count(`SELECT COUNT(*) FROM system_logs WHERE level='error' AND timestamp >= $1`, since),
			"warnings_24h":            count(`SELECT COUNT(*) FROM system_logs WHERE level='warn' AND timestamp >= $1`, since),
		},
	})
}

// adminLogs: Anwendungs-Log (system_logs), neueste zuerst; Filter level, category, q (Text), user, after (nur neuere Id, zum Mitlesen)
func (a *App) adminLogs(w http.ResponseWriter, r *http.Request, u User) {
	q := r.URL.Query()
	sql := `SELECT l.id, l.timestamp, l.level, l.category, l.message, COALESCE(u.email,'') AS user FROM system_logs l LEFT JOIN users u ON u.id=l.user_id WHERE TRUE`
	var args []any
	add := func(cond string, v any) {
		args = append(args, v)
		sql += " AND " + strings.Replace(cond, "$#", "$"+strconv.Itoa(len(args)), 1)
	}
	if v := q.Get("level"); v != "" {
		switch v { // „warn“ zeigt auch Fehler
		case "warn":
			sql += " AND l.level IN ('warn','error')"
		default:
			add("l.level=$#", v)
		}
	}
	if v := q.Get("category"); v != "" {
		add("l.category=$#", v)
	}
	if v := strings.TrimSpace(q.Get("q")); v != "" {
		add("l.message ILIKE '%' || $# || '%'", v)
	}
	if v := strings.TrimSpace(q.Get("user")); v != "" {
		add("u.email ILIKE '%' || $# || '%'", v)
	}
	if v, err := strconv.ParseInt(q.Get("after"), 10, 64); err == nil && v > 0 {
		add("l.id > $#", v)
	}
	if u.Role == "breakglass" { // keine Buchungsnotizen, Zeiten oder Ticket-Titel: nur Kategorien des Betriebs
		sql += " AND l.category IN ('notfall','system','betrieb')"
	}
	limit := 200
	if v, err := strconv.Atoi(q.Get("limit")); err == nil && v > 0 && v <= 1000 {
		limit = v
	}
	rs, err := a.maps(r.Context(), sql+" ORDER BY l.id DESC LIMIT "+strconv.Itoa(limit), args...)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rs)
}

// adminAudit: Änderungsprotokoll (Anmeldung, Zugangsdaten, Monatsabschluss …)
func (a *App) adminAudit(w http.ResponseWriter, r *http.Request, u User) {
	limit := 200
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 && v <= 1000 {
		limit = v
	}
	where := ""
	if u.Role == "breakglass" { // keine Stunden (Korrekturen) und keine Monatsabschlüsse: nur Anmeldung, Betrieb, Konten
		where = ` WHERE g.action ~ '^(login|breakglass\.|setup\.|admin\.|emergency\.|password\.|plane\.host_changed|secret\.|user\.|cli\.)'`
	}
	rs, err := a.maps(r.Context(), `SELECT g.id, g.at, g.action, g.detail, COALESCE(u.email,'') AS user FROM audit_log g LEFT JOIN users u ON u.id=g.user_id`+where+` ORDER BY g.id DESC LIMIT `+strconv.Itoa(limit))
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rs)
}

func (a *App) adminUsers(w http.ResponseWriter, r *http.Request, u User) {
	rs, err := a.maps(r.Context(), `SELECT u.id, u.email, u.name, u.role, u.disabled, u.kind, u.productive, u.suggest_blocked, u.armed_until, u.created_at, u.last_login,
		EXISTS(SELECT 1 FROM user_secrets s WHERE s.user_id=u.id AND s.kind='plane_key') AS plane_key,
		EXISTS(SELECT 1 FROM user_secrets s WHERE s.user_id=u.id AND s.kind='jira_token') AS jira_token,
		(SELECT COUNT(*) FROM time_entries e WHERE e.user_id=u.id) AS entries
		FROM users u ORDER BY u.id`)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rs)
}

// adminUserUpdate: Rolle ändern, sperren/entsperren. Es muss immer mindestens ein aktiver Administrator bleiben.
func (a *App) adminUserUpdate(w http.ResponseWriter, r *http.Request, u User) {
	id, ok := pathID(r)
	var in body
	if !ok {
		notFound(w)
		return
	}
	if !readJSON(w, r, &in) {
		return
	}
	ctx := r.Context()
	var role, kind string
	var disabled, productive bool
	if err := a.DB.QueryRow(ctx, `SELECT role, disabled, kind, productive FROM users WHERE id=$1`, id).Scan(&role, &disabled, &kind, &productive); err != nil {
		notFound(w)
		return
	}
	if role == "breakglass" { // Notfallzugang nur per Befehl am Server (server breakglass …)
		badRequest(w, "Der Notfallzugang wird nur per Befehl am Server verwaltet")
		return
	}
	if v, has := in["role"]; has {
		if !contains(userRoles, asStr(v)) {
			badRequest(w, "Rolle: user, lead oder admin")
			return
		}
		role = asStr(v)
	}
	if v, has := in["disabled"]; has {
		disabled = truthy(v)
	}
	if v, has := in["productive"]; has && kind == "local" {
		productive = truthy(v)
	}
	var suggestBlocked *bool // Ticket-Vorschläge für diese Person sperren (nur Administrator)
	if v, has := in["suggest_blocked"]; has {
		b := truthy(v)
		suggestBlocked = &b
	}
	err := a.inTx(ctx, func(tx pgx.Tx) error {
		// alle aktiven Administratoren sperren: parallele Änderungen warten, dann stimmt die Zählung
		if _, err := tx.Exec(ctx, `SELECT id FROM users WHERE role='admin' AND NOT disabled FOR UPDATE`); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(727002)`); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE users SET role=$2, disabled=$3, productive=$4 WHERE id=$1`, id, role, disabled, productive); err != nil {
			return err
		}
		if suggestBlocked != nil {
			if _, err := tx.Exec(ctx, `UPDATE users SET suggest_blocked=$2 WHERE id=$1`, id, *suggestBlocked); err != nil {
				return err
			}
		}
		if kind == "local" { // Einzelbetrieb: nach der Änderung prüfen (höchstens 1 produktiv, 1 Testkonto)
			if err := a.checkLimitsIfLocal(ctx, tx); err != nil {
				if errors.Is(err, ErrLocalLimits) {
					return errUser(err.Error())
				}
				return err
			}
		}
		var admins int
		if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM users WHERE role='admin' AND NOT disabled`).Scan(&admins); err != nil {
			return err
		}
		if admins == 0 {
			return errUser("Es muss mindestens ein aktiver Administrator bleiben")
		}
		return nil
	})
	if ue, ok := err.(errUser); ok {
		badRequest(w, string(ue))
		return
	}
	if err != nil {
		a.fail(w, err)
		return
	}
	detail := "id " + strconv.FormatInt(id, 10) + ": Rolle " + role + ", gesperrt " + strconv.FormatBool(disabled)
	if suggestBlocked != nil {
		detail += ", Vorschläge gesperrt " + strconv.FormatBool(*suggestBlocked)
	}
	a.audit(ctx, u.ID, "admin.user", detail)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// Einstellungen, die der Administrator hier ändern darf (Anmeldung bleibt der Einrichtung mit Token vorbehalten)
var adminConfigKeys = []string{"plane_base_url", "plane_workspace", "approval_mode", "approval_submit_workday", "approval_decide_workday", "month_grace_workdays", "module_tempo", "module_suggest",
	mailEnabledKey, mailHostKey, mailPortKey, mailUserKey, mailPassKey, mailTLSKey, mailFromKey, mailFromNameKey}

func (a *App) adminConfigGet(w http.ResponseWriter, r *http.Request, u User) {
	ctx := r.Context()
	out := map[string]any{"auth_mode": a.conf(ctx, "auth_mode"), "oidc_issuer": a.conf(ctx, "oidc_issuer"), "oidc_client_id": a.conf(ctx, "oidc_client_id")}
	for _, k := range adminConfigKeys {
		if k == mailPassKey {
			out["mail_smtp_password_set"] = a.conf(ctx, mailPassKey) != ""
			continue // Passwort nie ausliefern
		}
		out[k] = a.conf(ctx, k)
	}
	writeJSON(w, http.StatusOK, out)
}

// SetAdminConfig setzt eine erlaubte Servereinstellung (prüft den Wert); auch für den Befehl „config set“
func (a *App) SetAdminConfig(ctx context.Context, key, raw string) error {
	val, err := a.checkAdminConfig(key, raw)
	if err != nil {
		return err
	}
	return a.applyAdminConfig(ctx, key, val)
}

// applyAdminConfig speichert einen geprüften Wert; eine neue Plane-Adresse löscht alle Plane-Schlüssel (siehe setPlaneBase)
func (a *App) applyAdminConfig(ctx context.Context, key, val string) error {
	switch key {
	case "plane_base_url":
		_, err := a.setPlaneBase(ctx, val)
		if err == nil {
			a.mirrorPlaneConfigToWorkspace(ctx)
		}
		return err
	case "plane_workspace":
		if err := a.setConf(ctx, key, val); err != nil {
			return err
		}
		a.mirrorPlaneConfigToWorkspace(ctx)
		return nil
	}
	if contains(mailConfigKeys, key) {
		return a.applyMailConfig(ctx, key, val)
	}
	return a.setConf(ctx, key, val)
}

func (a *App) checkAdminConfig(key, raw string) (string, error) {
	if !contains(adminConfigKeys, key) {
		return "", errUser("Nicht änderbar: " + key + " (erlaubt: " + strings.Join(adminConfigKeys, ", ") + ")")
	}
	val := strings.TrimSpace(raw)
	if contains(mailConfigKeys, key) {
		return a.checkMailConfig(key, val)
	}
	switch key {
	case "approval_mode":
		if val != "either" && val != "lead" && val != "hr" {
			return "", errUser("approval_mode: hr (Standard), either oder lead")
		}
		return val, nil
	case "month_grace_workdays": // Nachfrist nach Monatsende in Werktagen (monthclose.go)
		var n int
		if _, err := fmt.Sscan(val, &n); err != nil || n < 0 || n > 5 || fmt.Sprint(n) != val {
			return "", errUser("month_grace_workdays: Zahl von 0 bis 5 (Werktage nach Monatsende)")
		}
		return val, nil
	case "module_tempo", "module_suggest": // Modul-Schalter Jira/Tempo bzw. Ticket-Vorschläge
		if val != "0" && val != "1" {
			return "", errUser(key + ": 0 (aus) oder 1 (ein)")
		}
		return val, nil
	case "approval_submit_workday", "approval_decide_workday":
		var n int
		if _, err := fmt.Sscan(val, &n); err != nil || n < 1 || n > 20 || fmt.Sprint(n) != val {
			return "", errUser(key + ": Zahl von 1 bis 20 (Werktag im Folgemonat)")
		}
		return val, nil
	}
	if key == "plane_workspace" {
		return normWS(val), nil
	}
	val, err := a.validatePlaneURL(val)
	if err != nil {
		return "", errUser(err.Error())
	}
	return val, nil
}

func (a *App) adminConfigPut(w http.ResponseWriter, r *http.Request, u User) {
	var in body
	if !readJSON(w, r, &in) {
		return
	}
	ctx := r.Context()
	clean := map[string]string{}
	for k := range in {
		if u.Role == "breakglass" && (strings.HasPrefix(k, "approval_") || k == "month_grace_workdays") {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": "Freigabe-Einstellungen sind mit dem Notfallzugang nicht änderbar"})
			return
		}
	}
	for k, v := range in { // erst alles prüfen, dann speichern (kein Teil-Ergebnis)
		val, err := a.checkAdminConfig(k, asStr(v))
		if err != nil {
			badRequest(w, err.Error())
			return
		}
		clean[k] = val
	}
	for k, val := range clean {
		if err := a.applyAdminConfig(ctx, k, val); err != nil {
			a.fail(w, err)
			return
		}
		a.audit(ctx, u.ID, "admin.config", k+" geändert")
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// emergencyCloseMonth: Monatsabschluss für eine Person auslösen. Auch der Notfallzugang darf das – ohne die Zahlen zu sehen.
func (a *App) emergencyCloseMonth(w http.ResponseWriter, r *http.Request, u User) {
	var in body
	if !readJSON(w, r, &in) {
		return
	}
	uid, year, month := int64(asFloat(in["user_id"])), int(asFloat(in["year"])), int(asFloat(in["month"]))
	if uid == 0 || month < 1 || month > 12 || year < 2000 || year > 2200 {
		badRequest(w, "user_id, year, month erforderlich")
		return
	}
	var exists bool
	if err := a.DB.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM users WHERE id=$1)`, uid).Scan(&exists); err != nil || !exists {
		notFound(w)
		return
	}
	_, err := a.closeMonth(r.Context(), uid, year, month, nil)
	switch e := err.(type) {
	case nil:
		a.audit(r.Context(), u.ID, "emergency.close_month", fmt.Sprintf("Benutzer %d, %d-%02d", uid, year, month))
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "year": year, "month": month}) // bewusst ohne Zahlen
	case errConflict:
		writeJSON(w, http.StatusConflict, map[string]any{"error": string(e)})
	default:
		a.fail(w, err)
	}
}

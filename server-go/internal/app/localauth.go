package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

/* Lokale Konten: Anmeldung mit Benutzername und Passwort (Modus „local“), Sitzungen per Cookie.
   Dazu der Notfallzugang („Break the glass“, Rolle breakglass): normalerweise gesperrt, nur per Befehl am Server für eine
   begrenzte Zeit scharf zu schalten, nur Betrieb – nie Zeitdaten. Im Modus „oidc“ (authentik) melden sich Mitarbeitende
   über authentik an; per Passwort geht dann nur der (scharf geschaltete) Notfallzugang. */

//go:embed login.html
var loginHTML []byte

const (
	sessionCookie   = "zp_session"
	sessionAbsolute = 12 * time.Hour
	breakGlassMax   = 60 * time.Minute // längste Sitzung des Notfallzugangs
)

func (a *App) routesLocalAuth(mux *http.ServeMux) {
	mux.HandleFunc("GET /login", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(loginHTML)
	})
	mux.HandleFunc("GET /api/auth/state", a.authState)
	mux.HandleFunc("POST /api/auth/login", a.authLogin)
	mux.HandleFunc("POST /api/auth/logout", a.authLogout)
	mux.HandleFunc("POST /api/auth/password", a.auth(a.authPassword))
}

// emergencyAllowed: Allow-List des Notfallzugangs (alles andere ist verboten: keine Einträge, Projekte, Tickets, Berichte)
var emergencyAllowed = map[string]bool{
	"GET /api/me": true, "POST /api/auth/logout": true,
	"GET /api/admin/status": true, "GET /api/admin/selfcheck": true, "POST /api/admin/selfcheck": true,
	"GET /api/admin/logs": true, "GET /api/admin/audit": true,
	"GET /api/admin/config": true, "PUT /api/admin/config": true,
	"POST /api/admin/emergency/close-month": true,
}

func (a *App) secureCookie(r *http.Request) bool {
	return r.TLS != nil || (a.TrustProxy && r.Header.Get("X-Forwarded-Proto") == "https") || strings.HasPrefix(a.PublicURL, "https://")
}

func (a *App) setSessionCookie(w http.ResponseWriter, r *http.Request, token string, ttl time.Duration) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode,
		Secure: a.secureCookie(r), MaxAge: int(ttl.Seconds())})
}

func clearSessionCookie(w http.ResponseWriter, r *http.Request, secure bool) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: secure, MaxAge: -1})
}

func sessionID(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

// newSession legt eine Sitzung an. In der Datenbank steht nur der Hash des Tokens.
func (a *App) newSession(ctx context.Context, uid int64, ip, ua string, ttl time.Duration) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(b)
	if len(ua) > 200 {
		ua = ua[:200]
	}
	_, err := a.DB.Exec(ctx, `INSERT INTO sessions(id,user_id,expires_at,ip,user_agent) VALUES($1,$2,$3,$4,$5)`,
		sessionID(token), uid, time.Now().Add(ttl), ip, ua)
	return token, err
}

// sessionUser liefert den angemeldeten Benutzer zum Cookie. Abgelaufene, gesperrte und (beim Notfallzugang) nicht scharfe Konten gelten nicht.
func (a *App) sessionUser(r *http.Request) (User, bool) {
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" || len(c.Value) > 100 {
		return User{}, false
	}
	id := sessionID(c.Value)
	var u User
	err = a.DB.QueryRow(r.Context(), `SELECT u.id, u.email, u.name, u.role, u.kind, u.productive, u.must_change_password, u.is_hr,
		EXISTS(SELECT 1 FROM team_members m WHERE m.user_id=u.id AND m.is_lead)
		FROM sessions s JOIN users u ON u.id=s.user_id
		WHERE s.id=$1 AND s.expires_at > now() AND s.last_seen > now() - interval '2 hours' AND NOT u.disabled
		  AND (u.role <> 'breakglass' OR (u.armed_until IS NOT NULL AND u.armed_until > now()))`, id).
		Scan(&u.ID, &u.Email, &u.Name, &u.Role, &u.Kind, &u.Productive, &u.MustChange, &u.IsHR, &u.IsLead)
	if err != nil {
		return User{}, false
	}
	_, _ = a.DB.Exec(r.Context(), `UPDATE sessions SET last_seen=now() WHERE id=$1 AND last_seen < now() - interval '1 minute'`, id)
	return u, true
}

func (a *App) authState(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"mode": a.conf(r.Context(), "auth_mode"), "setup_done": a.conf(r.Context(), "setup_done") == "1"})
}

func trim(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

func (a *App) authLogin(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	mode := a.conf(ctx, "auth_mode")
	if mode != "local" && mode != "oidc" && mode != "ldap" {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "Anmeldung mit Passwort ist in dieser Betriebsart nicht aktiv"})
		return
	}
	a.initLimits()
	ip := a.clientIP(r)
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	username := strings.ToLower(strings.TrimSpace(in.Username))
	if len(username) > 64 || len(in.Password) > 400 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "ungültige Eingabe"})
		return
	}
	const fail = "Anmeldung fehlgeschlagen"
	var (
		uid        int64
		hash       *string
		role, name string
		kind       string
		disabled   bool
		mustChange bool
		armedUntil *time.Time
	)
	// 1) Benutzer nachschlagen (günstig)
	err := a.DB.QueryRow(ctx, `SELECT id, password_hash, role, name, kind, disabled, must_change_password, armed_until FROM users
		WHERE lower(username)=$1 AND kind='local'`, username).Scan(&uid, &hash, &role, &name, &kind, &disabled, &mustChange, &armedUntil)
	found := err == nil
	armed := found && role == "breakglass" && armedUntil != nil && armedUntil.After(time.Now())
	if mode == "ldap" && !(found && role == "breakglass") { // Mitarbeitende: Prüfung im Verzeichnis; nur der Notfallzugang geht per lokalem Passwort
		a.ldapLogin(w, r, username, in.Password, ip)
		return
	}
	// 2) Versuche VOR der langsamen Passwortprüfung atomar eintragen (sonst kämen parallele Anfragen alle durch die Sperre).
	//    Zähler je Benutzer gibt es nur für BESTEHENDE Konten (erfundene Namen füllen den Speicher nicht);
	//    der Notfallzugang zählt nur, solange er scharf ist, und je Adresse (Fremde können ihn im Notfall nicht aussperren).
	tooMany := func() {
		writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": "Zu viele Fehlversuche – bitte später erneut versuchen"})
	}
	if !a.loginFailsIP.reserve(ip) {
		tooMany()
		return
	}
	var userLim *failLimiter
	userKey := ""
	// Schlüssel Name|Adresse für bekannte UND erfundene Namen: keine Auskunft, ob es den Benutzer gibt, und niemand sperrt ein Konto
	// für alle anderen aus. (Verteiltes Raten bremsen Passwortrichtlinie und Adress-Limit.)
	userLim, userKey = a.loginFailsUser, username+"|"+ip
	if armed {
		userLim = a.loginFailsBG
	}
	if userLim != nil && !userLim.reserve(userKey) {
		a.loginFailsIP.release(ip)
		tooMany()
		return
	}
	// 3) prüfen (immer rechnen, auch bei unbekanntem Benutzer: gleiche Antwortzeit)
	stored := dummyHash
	if found && hash != nil {
		stored = *hash
	}
	pwOK := verifyPassword(in.Password, stored)
	allowed := found && hash != nil && pwOK && !disabled &&
		(mode == "local" || role == "breakglass") && // im Modus oidc melden sich nur der Notfallzugang per Passwort an
		(role != "breakglass" || armed)
	if !allowed {
		if found && role == "breakglass" {
			a.auditNil(ctx, "breakglass.login_failed", "Von "+ip+map[bool]string{true: "", false: " – nicht scharf geschaltet"}[armed])
		}
		a.auditNil(ctx, "login.failed", fmt.Sprintf("Benutzer „%s“ von %s", trim(username, 64), ip))
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": fail})
		return
	}
	a.loginFailsIP.release(ip) // Erfolg zählt nicht als Fehlversuch
	if userLim != nil {
		userLim.release(userKey)
	}
	ttl := sessionAbsolute
	if role == "breakglass" {
		ttl = min(breakGlassMax, time.Until(*armedUntil))
	}
	token, err := a.newSession(ctx, uid, ip, r.UserAgent(), ttl) // jede Anmeldung bekommt eine NEUE Sitzung (keine Fixierung)
	if err != nil {
		a.fail(w, err)
		return
	}
	_, _ = a.DB.Exec(ctx, `UPDATE users SET last_login=now() WHERE id=$1`, uid)
	a.setSessionCookie(w, r, token, ttl)
	if role == "breakglass" {
		a.Log.Error("NOTFALLZUGANG ANGEMELDET", "von", ip)
		a.sysLog(ctx, uid, "error", "notfall", "Notfallzugang angemeldet von "+ip)
		a.audit(ctx, uid, "breakglass.login", "von "+ip)
	} else {
		a.audit(ctx, uid, "login", "von "+ip)
	}
	u := User{ID: uid, Name: name, Role: role, Kind: kind, MustChange: mustChange}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "user": u, "must_change_password": mustChange})
}

func (a *App) authLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
		if u, ok := a.sessionUser(r); ok && u.Role == "breakglass" {
			a.audit(r.Context(), u.ID, "breakglass.logout", "")
		}
		_, _ = a.DB.Exec(r.Context(), `DELETE FROM sessions WHERE id=$1`, sessionID(c.Value))
	}
	clearSessionCookie(w, r, a.secureCookie(r))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// authPassword: eigenes Passwort ändern (nur lokale Konten). Beendet alle anderen Sitzungen der Person.
func (a *App) authPassword(w http.ResponseWriter, r *http.Request, u User) {
	if u.Kind != "local" {
		badRequest(w, "Das Passwort wird bei diesem Konto im Verzeichnis (authentik/LDAP) geändert")
		return
	}
	var in struct {
		Old string `json:"old"`
		New string `json:"new"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	ctx := r.Context()
	a.initLimits()
	key := fmt.Sprintf("pw:%d", u.ID)
	if a.loginFailsUser.blocked(key) {
		writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": "Zu viele Fehlversuche – bitte später erneut versuchen"})
		return
	}
	var hash *string
	var username string
	if err := a.DB.QueryRow(ctx, `SELECT password_hash, COALESCE(username,'') FROM users WHERE id=$1`, u.ID).Scan(&hash, &username); err != nil {
		a.fail(w, err)
		return
	}
	if hash == nil || !verifyPassword(in.Old, *hash) {
		a.loginFailsUser.fail(key)
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "Aktuelles Passwort falsch"})
		return
	}
	if in.New == in.Old {
		badRequest(w, "Das neue Passwort muss sich vom alten unterscheiden")
		return
	}
	if err := ValidatePassword(in.New, username); err != nil {
		badRequest(w, err.Error())
		return
	}
	nh, err := HashPassword(in.New)
	if err != nil {
		a.fail(w, err)
		return
	}
	cur := ""
	if c, err := r.Cookie(sessionCookie); err == nil {
		cur = sessionID(c.Value)
	}
	err = a.inTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE users SET password_hash=$2, must_change_password=false WHERE id=$1`, u.ID, nh); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `DELETE FROM sessions WHERE user_id=$1 AND id <> $2`, u.ID, cur)
		return err
	})
	if err != nil {
		a.fail(w, err)
		return
	}
	a.audit(ctx, u.ID, "password.changed", "")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

/* ── Hinweise (Einzelbetrieb, Testkonto, Notfallzugang) ───────────── */

func (a *App) notices(ctx context.Context, u User) []string {
	var out []string
	mode := a.conf(ctx, "auth_mode")
	switch {
	case u.Role == "breakglass":
		out = append(out, "NOTFALLZUGANG: nur Betrieb (Status, Selbsttest, Logs, Einstellungen, Monat abschließen). Zeitdaten sind gesperrt. Alle Aktionen werden protokolliert.")
	case mode == "local" && u.Kind == "local" && !u.Productive:
		out = append(out, "Lokales Testkonto (Administrator): nicht für produktive Zeiten. Wird entfernt, sobald weitere Mitarbeitende mit authentik arbeiten.")
	case mode == "local" && u.Kind == "local":
		out = append(out, "Einzelbetrieb: Solange authentik nicht eingerichtet ist, darf nur eine Person produktiv arbeiten. Weitere Mitarbeitende brauchen die Anmeldung über authentik (OIDC).")
	case mode == "none":
		out = append(out, "Testmodus ohne Anmeldung – nur für lokale Tests, nicht für den Betrieb.")
	}
	if u.MustChange {
		out = append(out, "Bitte ändere jetzt das Passwort.")
	}
	var until *time.Time
	if err := a.DB.QueryRow(ctx, `SELECT armed_until FROM users WHERE role='breakglass' AND armed_until > now() LIMIT 1`).Scan(&until); err == nil && until != nil {
		out = append(out, "ACHTUNG: Der Notfallzugang ist scharf geschaltet bis "+until.In(a.Loc).Format("02.01. 15:04")+" Uhr.")
	}
	out = append(out, a.approvalNotices(ctx, u)...)
	out = append(out, a.leaveNotices(ctx, u)...)
	return out
}

/* ── Notfallzugang: Wächter ───────────────────────────────────────── */

// breakGlassTick: abgelaufene Notfall-Freigaben zurücknehmen und beenden; neue Freigaben laut melden.
func (a *App) breakGlassTick(ctx context.Context) {
	if tag, err := a.DB.Exec(ctx, `UPDATE users SET armed_until=NULL, armed_reason=NULL WHERE role='breakglass' AND armed_until IS NOT NULL AND armed_until <= now()`); err == nil && tag.RowsAffected() > 0 {
		_, _ = a.DB.Exec(ctx, `DELETE FROM sessions WHERE user_id IN (SELECT id FROM users WHERE role='breakglass')`)
		a.Log.Warn("Notfallzugang abgelaufen und gesperrt")
		a.auditNil(ctx, "breakglass.expired", "Freigabe abgelaufen, Sitzungen beendet")
	}
	var until time.Time
	var reason string
	if err := a.DB.QueryRow(ctx, `SELECT armed_until, COALESCE(armed_reason,'') FROM users WHERE role='breakglass' AND armed_until > now() LIMIT 1`).Scan(&until, &reason); err == nil {
		if a.bgReportedUntil != until.Unix() {
			a.bgReportedUntil = until.Unix()
			a.Log.Error("NOTFALLZUGANG SCHARF GESCHALTET", "bis", until.In(a.Loc).Format(time.RFC3339), "grund", reason)
			a.sysLog(ctx, 0, "error", "notfall", "Notfallzugang scharf geschaltet bis "+until.In(a.Loc).Format("02.01. 15:04")+": "+reason)
		}
	} else {
		a.bgReportedUntil = 0
	}
}

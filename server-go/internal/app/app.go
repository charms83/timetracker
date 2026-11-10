// Package app enthält die HTTP-Schicht: Einrichtung, Anmeldung (Testmodus/OIDC) und Profil.
package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type App struct {
	DB          *pgxpool.Pool
	Key         []byte
	Log         *slog.Logger
	SetupToken  string
	Started     time.Time
	KeyFromFile bool // Schlüssel stammt aus der Schlüsseldatei (nur Test)
	lastCheck   atomic.Pointer[CheckReport]
	AllowHTTP   bool           // ZP_ALLOW_HTTP=1: Plane-Adresse darf http:// sein
	Loc         *time.Location // Zeitzone für lokale Datumswerte (ZP_TZ)

	Production      bool   // Betrieb: Testmodus verboten (ZP_PRODUCTION=1 oder Kubernetes)
	SetupEnabled    bool   // ZP_SETUP_ENABLED=1: /setup bleibt nach der Einrichtung offen
	TrustProxy      bool   // ZP_TRUST_PROXY=1: Aufrufer-Adresse aus X-Forwarded-For (hinter dem Router)
	TrustedProxies  int    // Anzahl vorgeschalteter Proxys (von rechts gezählt)
	PublicURL       string // ZP_PUBLIC_URL: andere Host-Köpfe werden abgelehnt
	publicHost      string
	once            sync.Once
	rl              *rateLimiter
	setupFails      *failLimiter
	loginFailsIP    *failLimiter
	loginFailsUser  *failLimiter
	loginFailsBG    *failLimiter
	bgReportedUntil int64

	limiters sync.Map // Benutzer-Id → *limiter (Plane-Anfragebremse)
	planeRun sync.Map // Benutzer-Id → *planeState (läuft gerade / Wiederholung / Entprellung)

	ExtraCAFile   string    // ZP_EXTRA_CA_FILE: zusätzliche Zertifikate (Firmen-CA) für Jira/Tempo
	jiraInit      sync.Once // Jira/Tempo-HTTP-Client einmal bauen
	jiraClient    *http.Client
	jiraCAErr     error
	jiraDialHook  func(ctx context.Context, network, addr string) (net.Conn, error) // nur Tests: Verbindung im Speicher
	jiraGates     sync.Map                                                          // Benutzer-Id → *jiraGate (Pause nach HTTP 429)
	tempoRun      sync.Map                                                          // Benutzer-Id → *tempoState
	tempoAccCache sync.Map                                                          // Benutzer|Adresse|Projekt → Standard-Account
}

type User struct {
	ID         int64  `json:"id"`
	Email      string `json:"email"`
	Name       string `json:"name"`
	Role       string `json:"role"`
	Kind       string `json:"kind,omitempty"`
	Productive bool   `json:"productive"`
	MustChange bool   `json:"must_change_password,omitempty"`
	IsHR       bool   `json:"is_hr,omitempty"`   // Personal: sieht alle
	IsLead     bool   `json:"is_lead,omitempty"` // Teamleiter: leitet mindestens ein Team
}

type ctxKey struct{}

// initLimits legt die Begrenzer und die erlaubte Host-Adresse einmalig an
func (a *App) initLimits() {
	a.once.Do(func() {
		a.rl = newRateLimiter(30, 120) // je Adresse: 30 Anfragen/s, Spitze 120
		a.setupFails = newFailLimiter(5, 15*time.Minute)
		a.loginFailsIP = newFailLimiter(20, 15*time.Minute)
		a.loginFailsUser = newFailLimiter(5, 15*time.Minute)
		a.loginFailsBG = newFailLimiter(3, 15*time.Minute)
		if a.PublicURL != "" {
			a.publicHost = strings.ToLower(hostOf(a.PublicURL))
		}
	})
}

// Protect: Sicherheits-Köpfe, Host-Prüfung, Begrenzung je Adresse und Schutz vor Aufrufen fremder Webseiten (CSRF).
// Ein ändernder Aufruf mit Origin-Kopf muss von derselben Adresse kommen. Browser senden Origin bei jedem
// seitenübergreifenden POST/PUT/DELETE mit; curl und die Mac-App senden keinen.
func (a *App) Protect(next http.Handler) http.Handler {
	a.initLimits()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.securityHeaders(w, r)
		probe := r.URL.Path == "/healthz" || r.URL.Path == "/api/health"
		if a.publicHost != "" && !probe && strings.ToLower(r.Host) != a.publicHost {
			writeJSON(w, http.StatusMisdirectedRequest, map[string]any{"error": "Unbekannte Adresse"})
			return
		}
		if !probe && !a.rl.allow(a.clientIP(r)) {
			w.Header().Set("Retry-After", "2")
			writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": "Zu viele Anfragen"})
			return
		}
		switch r.Method {
		case http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch:
			if r.Header.Get("Sec-Fetch-Site") == "cross-site" { // Browser meldet: Aufruf von fremder Webseite
				writeJSON(w, http.StatusForbidden, map[string]any{"error": "Aufruf von fremder Adresse abgelehnt"})
				return
			}
			if o := r.Header.Get("Origin"); o != "" {
				if o == "null" {
					writeJSON(w, http.StatusForbidden, map[string]any{"error": "Aufruf von fremder Adresse abgelehnt"})
					return
				}
				if u, err := url.Parse(o); err != nil || u.Host != r.Host {
					writeJSON(w, http.StatusForbidden, map[string]any{"error": "Aufruf von fremder Adresse abgelehnt"})
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (a *App) securityHeaders(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Cross-Origin-Opener-Policy", "same-origin")
	h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
	switch p := r.URL.Path; {
	case strings.HasPrefix(p, "/api/"):
		h.Set("Cache-Control", "no-store")
		h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
	case p == "/setup" || p == "/admin" || p == "/login" || p == "/profile": // Seiten mit eigenem Skript (inline) brauchen script-src
		h.Set("Content-Security-Policy", "default-src 'none'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; connect-src 'self'; img-src 'self' data:; frame-ancestors 'none'; base-uri 'none'; form-action 'self'; object-src 'none'")
	default:
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; font-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'; object-src 'none'")
	}
	if r.TLS != nil || (a.TrustProxy && r.Header.Get("X-Forwarded-Proto") == "https") || strings.HasPrefix(a.PublicURL, "https://") {
		h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
	}
}

func (a *App) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /setup", a.setupPage)
	mux.HandleFunc("GET /api/setup/status", a.setupStatus)
	mux.HandleFunc("POST /api/setup", a.setupSave)
	mux.HandleFunc("GET /api/me", a.auth(a.me))
	mux.HandleFunc("PUT /api/profile/secrets/{kind}", a.auth(a.secretPut))
	mux.HandleFunc("DELETE /api/profile/secrets/{kind}", a.auth(a.secretDelete))
	a.routesStammdaten(mux)
	a.routesEntries(mux)
	a.routesTimer(mux)
	a.routesSettings(mux)
	a.routesAbsences(mux)
	a.routesKonto(mux)
	a.routesTagwerk(mux)
	a.routesChecks(mux)
	a.routesPresence(mux)
	a.routesReports(mux)
	a.routesHolidays(mux)
	a.routesPlane(mux)
	a.routesTempo(mux)
	a.routesJiraInstances(mux)
	a.routesTempoTickets(mux)
	a.routesSources(mux)
	a.routesPlaneTickets(mux)
	a.routesPlaneImport(mux)
	a.routesPlaneUI(mux)
	a.routesPlaneMap(mux)
	a.routesJiraUI(mux)
	a.routesJiraActions(mux)
	a.routesTempoSetup(mux)
	a.routesAdmin(mux)
	a.routesLocalAuth(mux)
	a.routesOIDC(mux)
	a.routesApproval(mux)
	a.routesNachtrag(mux) // Nachtragen nach Monatsschluss (Teamleitung/Personal)
	a.routesProfilePage(mux)
	a.routesTeams(mux)
	a.routesLocalUsers(mux)
	a.routesTodo(mux) // Arbeitsalltag (Parität Gruppe D)
	a.routesMonthEnd(mux)
	a.routesGapSuggest(mux)
	a.routesAssistant(mux)
	a.routesWorkday(mux)
	a.routesUserOps(mux) // Team-/Personal-Übersichten, Gruppe E (userops.go)
}

/* ── Servereinstellungen ─────────────────────────────────────────── */

func (a *App) conf(ctx context.Context, key string) string {
	var v string
	if err := a.DB.QueryRow(ctx, `SELECT value FROM server_config WHERE key=$1`, key).Scan(&v); err != nil {
		return ""
	}
	return v
}

func (a *App) setConf(ctx context.Context, key, value string) error {
	_, err := a.DB.Exec(ctx, `INSERT INTO server_config(key,value) VALUES($1,$2)
		ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value, updated_at=now()`, key, value)
	return err
}

// ApplyEnvDefaults richtet den Server ohne Assistenten ein, wenn ZP_AUTH_MODE=none gesetzt ist
// (Test, Container ohne Klicken) und noch nichts eingerichtet wurde.
func (a *App) ApplyEnvDefaults(ctx context.Context, authMode string) error {
	if authMode != "none" || a.conf(ctx, "setup_done") == "1" {
		return nil
	}
	if a.Production {
		return fmt.Errorf("ZP_AUTH_MODE=none ist im Betrieb nicht erlaubt")
	}
	if err := a.setConf(ctx, "auth_mode", "none"); err != nil {
		return err
	}
	return a.setConf(ctx, "setup_done", "1")
}

/* ── Anmeldung ───────────────────────────────────────────────────── */

// auth schützt eine Route. Testmodus "none": nur Zugriff von diesem Rechner (kein Proxy-Header),
// ein lokaler Benutzer mit Rolle admin. Modus "oidc" folgt.
func (a *App) auth(h func(http.ResponseWriter, *http.Request, User)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch a.conf(r.Context(), "auth_mode") {
		case "":
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "setup_required"})
		case "none":
			if a.Production {
				writeJSON(w, http.StatusForbidden, map[string]any{"error": "Testmodus ohne Anmeldung ist im Betrieb nicht erlaubt"})
				return
			}
			if !isLoopback(r) || !hostLoopback(r.Host) { // Host-Kopf: gegen DNS-Rebinding
				writeJSON(w, http.StatusForbidden, map[string]any{"error": "Testmodus ohne Anmeldung nur von localhost"})
				return
			}
			var u User
			var disabled bool
			err := a.DB.QueryRow(r.Context(), `
				INSERT INTO users(oidc_subject,email,name,role,kind,productive) VALUES('local','lokal@localhost','Lokaler Test','admin','local',false)
				ON CONFLICT (oidc_subject) DO UPDATE SET last_login=now()
				RETURNING id,email,name,role,disabled`).Scan(&u.ID, &u.Email, &u.Name, &u.Role, &disabled)
			if err != nil {
				a.fail(w, err)
				return
			}
			if disabled {
				writeJSON(w, http.StatusForbidden, map[string]any{"error": "Benutzer gesperrt"})
				return
			}
			h(w, r, u)
		default: // local und oidc: Anmeldung per Sitzung (oidc: Anmeldung über authentik folgt; bis dahin nur der scharfe Notfallzugang)
			u, ok := a.sessionUser(r)
			if !ok {
				writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "login_required"})
				return
			}
			if u.Role == "breakglass" && !emergencyAllowed[r.Pattern] { // Notfallzugang: nur Betrieb, nie Zeitdaten
				a.auditNil(r.Context(), "breakglass.denied", r.Method+" "+r.URL.Path)
				writeJSON(w, http.StatusForbidden, map[string]any{"error": "Notfallzugang: nur Betrieb erlaubt, keine Zeitdaten"})
				return
			}
			if u.Role == "breakglass" {
				a.audit(r.Context(), u.ID, "breakglass.access", r.Pattern)
			}
			if !u.timeAccount() && !adminAccountAllowed(r.Pattern) { // reines Administrationskonto: nur Verwaltung, keine Zeiterfassung/Vorschläge/Zugangsdaten
				writeJSON(w, http.StatusForbidden, map[string]any{"error": "Dieses Konto ist nur für die Administration", "code": "admin_only_account"})
				return
			}
			if u.MustChange && r.Pattern != "GET /api/me" && r.Pattern != "POST /api/auth/password" && r.Pattern != "POST /api/auth/logout" {
				writeJSON(w, http.StatusForbidden, map[string]any{"error": "password_change_required"})
				return
			}
			h(w, r, u)
		}
	}
}

// timeAccount: Konto, das Zeiten erfasst. Das lokale Testkonto „Administrator“ (kind local, nicht produktiv) ist nur für die Verwaltung da;
// Konten aus LDAP/authentik und produktive lokale Konten erfassen Zeiten (auch wenn sie zusätzlich Administrator sind).
func (u User) timeAccount() bool {
	return !(u.Kind == "local" && !u.Productive) && u.Role != "breakglass"
}

// adminAccountAllowed: was ein reines Administrationskonto aufrufen darf (alles andere ist verboten)
func adminAccountAllowed(pattern string) bool {
	_, path, ok := strings.Cut(pattern, " ")
	if !ok {
		return false
	}
	return strings.HasPrefix(path, "/api/admin/") || strings.HasPrefix(path, "/api/auth/") || path == "/api/me"
}

func isLoopback(r *http.Request) bool {
	if r.Header.Get("X-Forwarded-For") != "" || r.Header.Get("Forwarded") != "" {
		return false
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

/* ── Hilfen ──────────────────────────────────────────────────────── */

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "ungültige Eingabe"})
		return false
	}
	return true
}

func (a *App) fail(w http.ResponseWriter, err error) {
	var pe *pgconn.PgError
	if errors.As(err, &pe) && pe.Code == "ZP001" { // Monat eingereicht/freigegeben (Sperre in der Datenbank)
		writeJSON(w, http.StatusConflict, map[string]any{"error": pe.Message, "code": "month_locked"})
		return
	}
	a.Log.Error("Anfrage fehlgeschlagen", "err", err)
	writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "interner Fehler"})
}

func (a *App) audit(ctx context.Context, userID int64, action, detail string) {
	if _, err := a.DB.Exec(ctx, `INSERT INTO audit_log(user_id,action,detail) VALUES($1,$2,$3)`, userID, action, detail); err != nil {
		a.Log.Error("Audit-Log", "err", err)
	}
}

package app

import (
	"context"
	"crypto/subtle"
	_ "embed"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/jackc/pgx/v5"

	"example/zeitprotokoll-server/internal/crypto"
)

//go:embed setup.html
var setupHTML []byte

func (a *App) setupPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(setupHTML)
}

// setupOpen: Einrichtung ist möglich, solange sie nicht abgeschlossen ist (oder ZP_SETUP_ENABLED=1 gesetzt ist).
// Danach laufen Änderungen über /admin (angemeldeter Administrator) oder den Befehl „server config set“.
func (a *App) setupOpen(ctx context.Context) bool {
	if a.SetupEnabled {
		return true
	}
	var done string
	err := a.DB.QueryRow(ctx, `SELECT value FROM server_config WHERE key='setup_done'`).Scan(&done)
	if err != nil && err != pgx.ErrNoRows {
		return false // Datenbankfehler: im Zweifel geschlossen
	}
	return done != "1"
}

// SetupOpen für den Start (Hinweis im Log nur, wenn die Einrichtung offen ist)
func (a *App) SetupOpen(ctx context.Context) bool { return a.setupOpen(ctx) }

func (a *App) tokenOK(tok string) bool {
	return a.SetupToken != "" && subtle.ConstantTimeCompare([]byte(tok), []byte(a.SetupToken)) == 1
}

// setupStatus: ohne Einrichtungs-Token nur das Nötigste (kein Issuer, keine Client-Id, keine Plane-Adresse)
func (a *App) setupStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	out := map[string]any{"setup_done": a.conf(ctx, "setup_done") == "1", "setup_open": a.setupOpen(ctx)}
	a.initLimits()
	ip := a.clientIP(r)
	if tok := r.URL.Query().Get("token"); tok != "" {
		if a.setupFails.blocked(ip) {
			writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": "Zu viele Fehlversuche – bitte später erneut versuchen"})
			return
		}
		if !a.tokenOK(tok) {
			a.setupFails.fail(ip)
		}
	}
	if a.tokenOK(r.URL.Query().Get("token")) && a.setupOpen(ctx) {
		out["auth_mode"] = a.conf(ctx, "auth_mode")
		out["oidc_issuer"], out["oidc_client"] = a.conf(ctx, "oidc_issuer"), a.conf(ctx, "oidc_client_id")
		out["oidc_secret"] = a.conf(ctx, "oidc_client_secret") != ""
		out["plane_base_url"], out["plane_workspace"] = a.conf(ctx, "plane_base_url"), a.conf(ctx, "plane_workspace")
	}
	writeJSON(w, http.StatusOK, out)
}

// validateIssuer: OIDC-Issuer nur über https (http nur für localhost/Test)
func (a *App) validateIssuer(raw string) (string, error) {
	v := strings.TrimSpace(raw)
	u, err := url.Parse(v)
	if err != nil || u.Host == "" || u.User != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return "", fmt.Errorf("Issuer-URL ungültig")
	}
	if u.Scheme == "http" && !hostLoopback(u.Host) && !a.AllowHTTP {
		return "", fmt.Errorf("Issuer muss mit https:// beginnen")
	}
	return v, nil
}

// setupSave schreibt die Servereinstellungen – nur mit Einrichtungs-Token und nur solange die Einrichtung offen ist.
// Erst wird ALLES geprüft, dann gespeichert (kein Teil-Ergebnis).
func (a *App) setupSave(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if !a.setupOpen(ctx) {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "Die Einrichtung ist abgeschlossen. Änderungen: Admin-Oberfläche (/admin) oder Befehl „server config set“."})
		return
	}
	ip := a.clientIP(r)
	a.initLimits()
	if a.setupFails.blocked(ip) {
		writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": "Zu viele Fehlversuche – bitte später erneut versuchen"})
		return
	}
	var in struct {
		Token            string  `json:"token"`
		AuthMode         string  `json:"auth_mode"`
		OIDCIssuer       string  `json:"oidc_issuer"`
		OIDCClientID     string  `json:"oidc_client_id"`
		OIDCClientSecret string  `json:"oidc_client_secret"`
		AdminPassword    string  `json:"admin_password"`
		PlaneBaseURL     *string `json:"plane_base_url"`
		PlaneWorkspace   *string `json:"plane_workspace"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if !a.tokenOK(in.Token) {
		a.setupFails.fail(ip)
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "Einrichtungs-Token falsch"})
		return
	}
	bad := func(msg string) { writeJSON(w, http.StatusBadRequest, map[string]any{"error": msg}) }

	// 1) prüfen
	var planeURL string
	if in.PlaneBaseURL != nil {
		v, err := a.validatePlaneURL(*in.PlaneBaseURL)
		if err != nil {
			bad(err.Error())
			return
		}
		planeURL = v
	}
	var issuer, clientID string
	switch in.AuthMode {
	case "none":
		if a.Production {
			bad("Testmodus ohne Anmeldung ist im Betrieb nicht erlaubt")
			return
		}
		if !isLoopback(r) || !hostLoopback(r.Host) {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": "Testmodus ohne Anmeldung nur von localhost einstellbar"})
			return
		}
	case "local":
		if err := ValidatePassword(in.AdminPassword, "admin"); err != nil {
			bad("Passwort des lokalen Administrators: " + strings.TrimPrefix(err.Error(), "Passwort: "))
			return
		}
	case "oidc":
		var err error
		if issuer, err = a.validateIssuer(in.OIDCIssuer); err != nil {
			bad(err.Error())
			return
		}
		if clientID = strings.TrimSpace(in.OIDCClientID); clientID == "" {
			bad("Client-ID fehlt")
			return
		}
		if in.OIDCClientSecret == "" && a.conf(ctx, "oidc_client_secret") == "" {
			bad("Client-Secret fehlt")
			return
		}
	default:
		bad("auth_mode: local, oidc oder none")
		return
	}

	// 2) speichern
	cleared := int64(0)
	if in.PlaneBaseURL != nil {
		n, err := a.setPlaneBase(ctx, planeURL)
		if err != nil {
			a.fail(w, err)
			return
		}
		cleared = n
	}
	if in.PlaneWorkspace != nil {
		if err := a.setConf(ctx, "plane_workspace", normWS(*in.PlaneWorkspace)); err != nil {
			a.fail(w, err)
			return
		}
	}
	if in.AuthMode == "oidc" {
		if err := a.setConf(ctx, "oidc_issuer", issuer); err != nil {
			a.fail(w, err)
			return
		}
		if err := a.setConf(ctx, "oidc_client_id", clientID); err != nil {
			a.fail(w, err)
			return
		}
		if in.OIDCClientSecret != "" { // leer = bisherigen Wert behalten
			blob, err := crypto.SealAAD(a.Key, []byte(in.OIDCClientSecret), []byte("server|oidc_client_secret"))
			if err != nil {
				a.fail(w, err)
				return
			}
			if err := a.setConf(ctx, "oidc_client_secret", base64.StdEncoding.EncodeToString(blob)); err != nil {
				a.fail(w, err)
				return
			}
		}
	}
	if in.AuthMode == "local" {
		if err := a.upsertLocalAdmin(ctx, in.AdminPassword); err != nil {
			if errors.Is(err, ErrLocalLimits) {
				bad(err.Error())
				return
			}
			a.fail(w, err)
			return
		}
	}
	if err := a.setConf(ctx, "auth_mode", in.AuthMode); err != nil {
		a.fail(w, err)
		return
	}
	if err := a.setConf(ctx, "setup_done", "1"); err != nil {
		a.fail(w, err)
		return
	}
	a.auditNil(ctx, "setup.save", fmt.Sprintf("Anmeldung %s, Plane-Adresse geändert: %v, Plane-Schlüssel gelöscht: %d, von %s", in.AuthMode, in.PlaneBaseURL != nil, cleared, ip))
	a.Log.Info("Einrichtung gespeichert", "auth_mode", in.AuthMode)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// auditNil schreibt ins Änderungsprotokoll ohne angemeldete Person (Einrichtung, Befehle)
func (a *App) auditNil(ctx context.Context, action, detail string) {
	if _, err := a.DB.Exec(ctx, `INSERT INTO audit_log(user_id,action,detail) VALUES(NULL,$1,$2)`, action, detail); err != nil {
		a.Log.Error("Audit-Log", "err", err)
	}
}

func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Host)
}

// setPlaneBase setzt die Plane-Adresse. Ändert sich dabei der Host, werden ALLE gespeicherten Plane-Schlüssel gelöscht:
// Sonst würde der Server die Schlüssel aller Personen an den neuen Host schicken – wer die Adresse ändern kann
// (Einrichtung, Admin), könnte sie so einsammeln. Die Personen tragen ihren Schlüssel dann neu ein.
func (a *App) setPlaneBase(ctx context.Context, newURL string) (int64, error) {
	old := a.conf(ctx, "plane_base_url")
	if planeScope(old) == planeScope(newURL) { // Schema und Host (https→http auf demselben Host zählt als Wechsel)
		return 0, a.setConf(ctx, "plane_base_url", newURL)
	}
	var cleared int64
	err := a.inTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM user_secrets WHERE kind='plane_key'`)
		if err != nil {
			return err
		}
		cleared = tag.RowsAffected()
		if _, err := tx.Exec(ctx, `DELETE FROM user_settings WHERE key='plane_me_id'`); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO server_config(key,value) VALUES('plane_base_url',$1) ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value, updated_at=now()`, newURL)
		return err
	})
	if err == nil && cleared > 0 {
		a.Log.Warn("Plane-Adresse geändert: gespeicherte Plane-Schlüssel gelöscht", "anzahl", cleared)
		a.auditNil(ctx, "plane.host_changed", fmt.Sprintf("%d Plane-Schlüssel gelöscht (Host geändert)", cleared))
	}
	return cleared, err
}

// upsertLocalAdmin legt den lokalen Administrator „admin“ an oder setzt sein Passwort (Testkonto, nicht produktiv)
func (a *App) upsertLocalAdmin(ctx context.Context, password string) error {
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	return a.inTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(727002)`); err != nil {
			return err
		}
		// Das Testkonto aus dem Testmodus ohne Anmeldung (oidc_subject 'local', ohne Benutzername) wird zum Konto „admin“ – samt seinen Daten
		if _, err := tx.Exec(ctx, `UPDATE users SET username='admin' WHERE oidc_subject='local' AND username IS NULL
			AND NOT EXISTS (SELECT 1 FROM users WHERE lower(username)='admin')`); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `UPDATE users SET password_hash=$1, must_change_password=false, disabled=false, kind='local', role='admin', productive=false WHERE lower(username)='admin'`, hash)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			if _, err := tx.Exec(ctx, `INSERT INTO users(oidc_subject,email,name,role,kind,username,password_hash,productive)
				VALUES('local:admin','admin@lokal','Lokaler Administrator','admin','local','admin',$1,false)`, hash); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `DELETE FROM sessions WHERE user_id IN (SELECT id FROM users WHERE lower(username)='admin')`); err != nil {
			return err
		}
		return CheckLocalLimits(ctx, tx)
	})
}

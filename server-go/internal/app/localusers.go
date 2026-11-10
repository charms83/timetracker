package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

/* Lokale Konten verwalten (nur Rolle admin, nie der Notfallzugang): anlegen, Passwort zurücksetzen, entfernen.
   Einzelbetrieb: Solange authentik nicht eingerichtet ist (Modus local), darf höchstens EINE Person produktiv arbeiten.
   Der lokale Administrator ist ein Testkonto (nicht produktiv) und wird entfernt, sobald weitere Mitarbeitende mit authentik arbeiten. */

var usernameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{2,31}$`)

func (a *App) routesLocalUsers(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/admin/local-users", a.strictAdmin(a.localUserCreate))
	mux.HandleFunc("POST /api/admin/users/{id}/reset-password", a.strictAdmin(a.localUserResetPassword))
	mux.HandleFunc("DELETE /api/admin/local-users/{id}", a.strictAdmin(a.localUserDelete))
}

// strictAdmin: nur Rolle admin (auch der Notfallzugang nicht)
func (a *App) strictAdmin(h func(http.ResponseWriter, *http.Request, User)) http.HandlerFunc {
	return a.auth(func(w http.ResponseWriter, r *http.Request, u User) {
		if u.Role != "admin" {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": "Nur für Administratoren"})
			return
		}
		h(w, r, u)
	})
}

// checkLimitsIfLocal: im Modus local nach einer Änderung (in derselben Transaktion) die Regeln des Einzelbetriebs prüfen
func (a *App) checkLimitsIfLocal(ctx context.Context, q Queryer) error {
	if a.conf(ctx, "auth_mode") != "local" {
		return nil
	}
	return CheckLocalLimits(ctx, q)
}

func (a *App) localUserCreate(w http.ResponseWriter, r *http.Request, u User) {
	var in body
	if !readJSON(w, r, &in) {
		return
	}
	ctx := r.Context()
	username := strings.ToLower(strings.TrimSpace(asStr(in["username"])))
	if !usernameRE.MatchString(username) {
		badRequest(w, "Benutzername: 3–32 Zeichen aus a–z, 0–9, Punkt, Bindestrich, Unterstrich")
		return
	}
	role := asStr(in["role"])
	if !contains([]string{"user", "lead", "admin"}, role) {
		badRequest(w, "Rolle: user, lead oder admin")
		return
	}
	email := strings.TrimSpace(asStr(in["email"]))
	if email == "" {
		email = username + "@lokal"
	}
	if len(email) > 200 || !strings.Contains(email, "@") || strings.HasSuffix(strings.ToLower(email), "@lokal") && email != username+"@lokal" {
		badRequest(w, "E-Mail ungültig")
		return
	}
	name := trim(strings.TrimSpace(asStr(in["name"])), 100)
	if name == "" {
		name = username
	}
	productive := role != "admin"
	if v, has := in["productive"]; has {
		productive = truthy(v)
	}
	pw, err := GeneratePassword()
	if err != nil {
		a.fail(w, err)
		return
	}
	hash, err := HashPassword(pw)
	if err != nil {
		a.fail(w, err)
		return
	}
	if !productive && role != "admin" {
		badRequest(w, "Ein Testkonto (nicht produktiv) muss Administrator sein")
		return
	}
	var id int64
	err = a.inTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(727002)`); err != nil { // gleichzeitige Änderungen dürfen die Grenze nicht umgehen
			return err
		}
		if err := tx.QueryRow(ctx, `INSERT INTO users(oidc_subject,email,name,role,kind,username,password_hash,productive,must_change_password)
			VALUES('local:'||$1,$2,$3,$4,'local',$1,$5,$6,true) RETURNING id`, username, email, name, role, hash, productive).Scan(&id); err != nil {
			return err
		}
		return a.checkLimitsIfLocal(ctx, tx)
	})
	if errors.Is(err, ErrLocalLimits) {
		writeJSON(w, http.StatusConflict, map[string]any{"error": ErrLocalLimits.Error()})
		return
	}
	if err != nil {
		if strings.Contains(err.Error(), "users_username_idx") || strings.Contains(err.Error(), "oidc_subject") {
			writeJSON(w, http.StatusConflict, map[string]any{"error": "Benutzername existiert schon"})
			return
		}
		a.fail(w, err)
		return
	}
	a.audit(ctx, u.ID, "user.created", fmt.Sprintf("lokal „%s“, Rolle %s, produktiv %v", username, role, productive))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id, "username": username, "password": pw,
		"hinweis": "Das Passwort wird nur jetzt angezeigt. Beim ersten Anmelden muss es geändert werden."})
}

func (a *App) localUserResetPassword(w http.ResponseWriter, r *http.Request, u User) {
	id, ok := pathID(r)
	if !ok {
		notFound(w)
		return
	}
	ctx := r.Context()
	var kind, role, username string
	if err := a.DB.QueryRow(ctx, `SELECT kind, role, COALESCE(username,'') FROM users WHERE id=$1`, id).Scan(&kind, &role, &username); err != nil {
		notFound(w)
		return
	}
	if kind != "local" || role == "breakglass" {
		badRequest(w, "Nur für lokale Konten (der Notfallzugang wird per Befehl am Server verwaltet)")
		return
	}
	pw, err := GeneratePassword()
	if err != nil {
		a.fail(w, err)
		return
	}
	hash, err := HashPassword(pw)
	if err != nil {
		a.fail(w, err)
		return
	}
	err = a.inTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE users SET password_hash=$2, must_change_password=true WHERE id=$1`, id, hash); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `DELETE FROM sessions WHERE user_id=$1`, id)
		return err
	})
	if err != nil {
		a.fail(w, err)
		return
	}
	a.audit(ctx, u.ID, "user.password_reset", "Benutzer „"+username+"“")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "username": username, "password": pw, "hinweis": "Das Passwort wird nur jetzt angezeigt."})
}

// localUserDelete entfernt ein lokales Konto. Hat es Daten (Einträge, Projekte), nur mit ?delete_data=1 – dann wird ALLES der Person gelöscht.
func (a *App) localUserDelete(w http.ResponseWriter, r *http.Request, u User) {
	id, ok := pathID(r)
	if !ok {
		notFound(w)
		return
	}
	if id == u.ID {
		badRequest(w, "Das eigene Konto lässt sich nicht entfernen")
		return
	}
	ctx := r.Context()
	var kind, role, username string
	if err := a.DB.QueryRow(ctx, `SELECT kind, role, COALESCE(username,'') FROM users WHERE id=$1`, id).Scan(&kind, &role, &username); err != nil {
		notFound(w)
		return
	}
	if kind != "local" || role == "breakglass" {
		badRequest(w, "Nur lokale Konten (der Notfallzugang wird per Befehl am Server verwaltet)")
		return
	}
	var entries, projects int64
	_ = a.DB.QueryRow(ctx, `SELECT (SELECT COUNT(*) FROM time_entries WHERE user_id=$1), (SELECT COUNT(*) FROM projects WHERE user_id=$1)`, id).Scan(&entries, &projects)
	withData := r.URL.Query().Get("delete_data") == "1"
	if (entries > 0 || projects > 0) && !withData {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "Das Konto hat Daten. Zum Löschen aller Daten dieser Person ?delete_data=1 angeben – oder das Konto stattdessen sperren.",
			"entries": entries, "projects": projects})
		return
	}
	err := a.inTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT id FROM users WHERE role='admin' AND NOT disabled FOR UPDATE`); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM users WHERE id=$1`, id); err != nil {
			return err
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
	a.audit(ctx, u.ID, "user.deleted", "lokal „"+username+"“, Daten gelöscht: "+strconv.FormatBool(withData)+fmt.Sprintf(" (%d Einträge, %d Projekte)", entries, projects))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

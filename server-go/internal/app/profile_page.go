package app

import (
	_ "embed"
	"net/http"
	"strconv"
)

/* „Mein Profil“: eigene Zugangsdaten (Plane-Schlüssel, Jira-Token je Instanz), Vorschläge, Passwort – im Browser, ohne Befehle.
   Die Seite spricht nur die bestehenden Routen an (/api/me, /api/profile/secrets/…, /api/settings, /api/jira/instances). */

//go:embed profile.html
var profileHTML []byte

func (a *App) routesProfilePage(mux *http.ServeMux) {
	mux.HandleFunc("GET /profile", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(profileHTML)
	})
	// Administrator: gespeicherte Zugangsdaten einer Person löschen (z. B. nach Austritt oder Verlust). Lesen kann niemand.
	mux.HandleFunc("DELETE /api/admin/users/{id}/secrets", a.strictAdmin(a.adminSecretsDelete))
}

// adminSecretsDelete löscht plane_key und/oder jira_token (alle Instanzen) einer Person; ?kind= schränkt ein.
func (a *App) adminSecretsDelete(w http.ResponseWriter, r *http.Request, u User) {
	id, ok := pathID(r)
	if !ok {
		badRequest(w, "id ungültig")
		return
	}
	kind := r.URL.Query().Get("kind")
	if kind != "" && !secretKinds[kind] {
		badRequest(w, "kind: plane_key oder jira_token")
		return
	}
	ctx := r.Context()
	var isBG bool
	if err := a.DB.QueryRow(ctx, `SELECT role='breakglass' FROM users WHERE id=$1`, id).Scan(&isBG); err != nil || isBG {
		notFound(w)
		return
	}
	tag, err := a.DB.Exec(ctx, `DELETE FROM user_secrets WHERE user_id=$1 AND ($2='' OR kind=$2)`, id, kind)
	if err != nil {
		a.fail(w, err)
		return
	}
	a.audit(ctx, u.ID, "admin.secrets_deleted", "Person "+strconv.FormatInt(id, 10)+", Art "+firstNonEmpty(kind, "alle")+", "+strconv.FormatInt(tag.RowsAffected(), 10)+" gelöscht")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "deleted": tag.RowsAffected()})
}

package app

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"example/zeitprotokoll-server/internal/crypto"
)

// secretAAD bindet ein Chiffrat an Person und Art (siehe crypto.SealAAD)
func secretAAD(uid int64, kind string) []byte { return []byte(fmt.Sprintf("u%d|%s", uid, kind)) }

// secretAADFor: der Plane-Schlüssel ist zusätzlich an die aktuelle Plane-Adresse (Schema + Host) gebunden. Wechselt die Adresse,
// lässt sich ein alter Schlüssel nicht mehr entschlüsseln – er kann nie an einen anderen Host gehen, auch nicht im Zeitfenster des Wechsels.
func (a *App) secretAADFor(ctx context.Context, uid int64, kind string) []byte {
	if kind == "plane_key" {
		return []byte(fmt.Sprintf("u%d|%s|%s", uid, kind, planeScope(a.conf(ctx, "plane_base_url"))))
	}
	return secretAAD(uid, kind)
}

// planeScope: kleingeschrieben Schema://Host(:Port) einer Adresse
func planeScope(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Host)
}

var secretKinds = map[string]bool{"jira_token": true, "plane_key": true}

func (a *App) me(w http.ResponseWriter, r *http.Request, u User) {
	rows, err := a.DB.Query(r.Context(), `SELECT kind FROM user_secrets WHERE user_id=$1`, u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	defer rows.Close()
	have := map[string]bool{"jira_token": false, "plane_key": false}
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			a.fail(w, err)
			return
		}
		have[k] = true
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": u, "auth_mode": a.conf(r.Context(), "auth_mode"), "secrets": have, "notices": a.notices(r.Context(), u)})
}

// secretPut speichert Jira-Token bzw. Plane-Schlüssel der Person verschlüsselt. Werte werden nie zurückgegeben.
func (a *App) secretPut(w http.ResponseWriter, r *http.Request, u User) {
	kind := r.PathValue("kind")
	if !secretKinds[kind] {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "unbekannte Art"})
		return
	}
	var in struct {
		Value string `json:"value"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	v := strings.TrimSpace(in.Value)
	if v == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "Wert fehlt"})
		return
	}
	if kind == "jira_token" { // je Jira-Instanz (?instance=Id; ohne Angabe nur bei genau einer eingeschalteten)
		inst, err := a.tokenInstance(r.Context(), r.URL.Query().Get("instance"), true)
		if err != nil {
			a.instFail(w, err)
			return
		}
		if err := a.setJiraToken(r.Context(), u.ID, inst, v); err != nil {
			a.fail(w, err)
			return
		}
		a.audit(r.Context(), u.ID, "secret.set", fmt.Sprintf("jira_token (Instanz %d)", inst.ID))
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "instance": inst.ID})
		return
	}
	blob, err := crypto.SealAAD(a.Key, []byte(v), a.secretAADFor(r.Context(), u.ID, kind))
	if err != nil {
		a.fail(w, err)
		return
	}
	if _, err := a.DB.Exec(r.Context(), `INSERT INTO user_secrets(user_id,kind,ciphertext) VALUES($1,$2,$3)
		ON CONFLICT (user_id,kind,scope) DO UPDATE SET ciphertext=EXCLUDED.ciphertext, updated_at=now()`, u.ID, kind, blob); err != nil {
		a.fail(w, err)
		return
	}
	a.clearPlaneMe(r.Context(), u.ID, kind)
	a.audit(r.Context(), u.ID, "secret.set", kind)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *App) secretDelete(w http.ResponseWriter, r *http.Request, u User) {
	kind := r.PathValue("kind")
	if !secretKinds[kind] {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "unbekannte Art"})
		return
	}
	if kind == "jira_token" {
		inst, err := a.tokenInstance(r.Context(), r.URL.Query().Get("instance"), false)
		if err != nil {
			a.instFail(w, err)
			return
		}
		if err := a.deleteJiraToken(r.Context(), u.ID, inst.ID); err != nil {
			a.fail(w, err)
			return
		}
		a.audit(r.Context(), u.ID, "secret.delete", fmt.Sprintf("jira_token (Instanz %d)", inst.ID))
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "instance": inst.ID})
		return
	}
	if _, err := a.DB.Exec(r.Context(), `DELETE FROM user_secrets WHERE user_id=$1 AND kind=$2 AND scope=0`, u.ID, kind); err != nil {
		a.fail(w, err)
		return
	}
	a.clearPlaneMe(r.Context(), u.ID, kind)
	a.audit(r.Context(), u.ID, "secret.delete", kind)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// Secret liefert den entschlüsselten Wert für interne Aufrufe (Plane/Jira/Tempo); leer, wenn nicht gesetzt.
func (a *App) Secret(r *http.Request, userID int64, kind string) (string, error) {
	return a.secretFor(r.Context(), userID, kind)
}

// secretFor: Zugangsdaten ohne Instanz (plane_key). Jira-Tokens je Instanz liest jiraToken.
func (a *App) secretFor(ctx context.Context, userID int64, kind string) (string, error) {
	var blob []byte
	if err := a.DB.QueryRow(ctx, `SELECT ciphertext FROM user_secrets WHERE user_id=$1 AND kind=$2 AND scope=0`, userID, kind).Scan(&blob); err != nil {
		return "", nil
	}
	b, err := crypto.OpenAAD(a.Key, blob, a.secretAADFor(ctx, userID, kind))
	return string(b), err
}

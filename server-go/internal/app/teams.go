package app

import (
	"net/http"
	"strconv"
	"strings"
)

/* Teams und Merkmale – nur für Administratoren (nicht für den Notfallzugang). Teamleiter sind Mitglieder mit is_lead.
   Personal (is_hr) sieht alle. Jede Änderung wird protokolliert. Hinweis: Wer Personal-Rechte vergibt (auch sich selbst), gibt damit Zugriff auf alle Zeitdaten. */

func (a *App) routesTeams(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/admin/teams", a.strictAdmin(a.teamsList))
	mux.HandleFunc("POST /api/admin/teams", a.strictAdmin(a.teamsCreate))
	mux.HandleFunc("DELETE /api/admin/teams/{id}", a.strictAdmin(a.teamsDelete))
	mux.HandleFunc("PUT /api/admin/teams/{id}/members/{uid}", a.strictAdmin(a.teamMemberSet))
	mux.HandleFunc("DELETE /api/admin/teams/{id}/members/{uid}", a.strictAdmin(a.teamMemberDelete))
	mux.HandleFunc("PUT /api/admin/users/{id}/hr", a.strictAdmin(a.userHR))
}

func (a *App) teamsList(w http.ResponseWriter, r *http.Request, u User) {
	rs, err := a.maps(r.Context(), `SELECT t.id, t.name,
		COALESCE((SELECT json_agg(json_build_object('user_id', m.user_id, 'name', x.name, 'is_lead', m.is_lead) ORDER BY x.name)
		          FROM team_members m JOIN users x ON x.id=m.user_id WHERE m.team_id=t.id), '[]'::json) AS members
		FROM teams t ORDER BY lower(t.name)`)
	if err != nil {
		a.fail(w, err)
		return
	}
	hr, err := a.maps(r.Context(), `SELECT id FROM users WHERE is_hr ORDER BY id`)
	if err != nil {
		a.fail(w, err)
		return
	}
	ids := make([]any, 0, len(hr))
	for _, h := range hr {
		ids = append(ids, h["id"])
	}
	writeJSON(w, http.StatusOK, map[string]any{"teams": rs, "hr": ids})
}

func (a *App) teamsCreate(w http.ResponseWriter, r *http.Request, u User) {
	var in struct {
		Name string `json:"name"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	name := strings.TrimSpace(in.Name)
	if n := len([]rune(name)); n < 1 || n > 80 {
		badRequest(w, "Name: 1 bis 80 Zeichen")
		return
	}
	var id int64
	if err := a.DB.QueryRow(r.Context(), `INSERT INTO teams(name) VALUES($1) ON CONFLICT DO NOTHING RETURNING id`, name).Scan(&id); err != nil {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "Ein Team mit diesem Namen gibt es schon"})
		return
	}
	a.audit(r.Context(), u.ID, "admin.team", "Team „"+name+"“ angelegt")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id})
}

func (a *App) teamsDelete(w http.ResponseWriter, r *http.Request, u User) {
	id, ok := pathID(r)
	if !ok {
		badRequest(w, "id ungültig")
		return
	}
	var name string
	if err := a.DB.QueryRow(r.Context(), `DELETE FROM teams WHERE id=$1 RETURNING name`, id).Scan(&name); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "nicht gefunden"})
		return
	}
	a.audit(r.Context(), u.ID, "admin.team", "Team „"+name+"“ gelöscht")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *App) teamMemberSet(w http.ResponseWriter, r *http.Request, u User) {
	tid, ok := pathID(r)
	uid, err := strconv.ParseInt(r.PathValue("uid"), 10, 64)
	if !ok || err != nil || uid <= 0 {
		badRequest(w, "id ungültig")
		return
	}
	var in struct {
		IsLead bool `json:"is_lead"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if in.IsLead && uid == u.ID {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "Teamleitung kann sich niemand selbst geben"})
		return
	}
	ctx := r.Context()
	var exists bool
	if e := a.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM teams WHERE id=$1) AND EXISTS(SELECT 1 FROM users WHERE id=$2 AND role<>'breakglass')`, tid, uid).Scan(&exists); e != nil || !exists {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "nicht gefunden"})
		return
	}
	if _, e := a.DB.Exec(ctx, `INSERT INTO team_members(team_id,user_id,is_lead) VALUES($1,$2,$3) ON CONFLICT (team_id,user_id) DO UPDATE SET is_lead=EXCLUDED.is_lead`, tid, uid, in.IsLead); e != nil {
		a.fail(w, e)
		return
	}
	a.audit(ctx, u.ID, "admin.team", "Team "+strconv.FormatInt(tid, 10)+": Person "+strconv.FormatInt(uid, 10)+", Teamleiter "+strconv.FormatBool(in.IsLead))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *App) teamMemberDelete(w http.ResponseWriter, r *http.Request, u User) {
	tid, ok := pathID(r)
	uid, err := strconv.ParseInt(r.PathValue("uid"), 10, 64)
	if !ok || err != nil {
		badRequest(w, "id ungültig")
		return
	}
	if _, e := a.DB.Exec(r.Context(), `DELETE FROM team_members WHERE team_id=$1 AND user_id=$2`, tid, uid); e != nil {
		a.fail(w, e)
		return
	}
	a.audit(r.Context(), u.ID, "admin.team", "Team "+strconv.FormatInt(tid, 10)+": Person "+strconv.FormatInt(uid, 10)+" entfernt")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// userHR: Personal-Merkmal setzen. Bei Konten aus dem Verzeichnis (ldap/oidc) überschreibt die Gruppe es beim nächsten Login.
func (a *App) userHR(w http.ResponseWriter, r *http.Request, u User) {
	id, ok := pathID(r)
	if !ok {
		badRequest(w, "id ungültig")
		return
	}
	var in struct {
		IsHR bool `json:"is_hr"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if in.IsHR && id == u.ID {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "Das Personal-Recht kann sich niemand selbst geben (Vier-Augen-Prinzip; sonst per Befehl am Server oder über die Verzeichnisgruppe)"})
		return
	}
	tag, err := a.DB.Exec(r.Context(), `UPDATE users SET is_hr=$2 WHERE id=$1 AND role<>'breakglass'`, id, in.IsHR)
	if err != nil {
		a.fail(w, err)
		return
	}
	if tag.RowsAffected() == 0 {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "nicht gefunden"})
		return
	}
	a.audit(r.Context(), u.ID, "admin.hr", "Person "+strconv.FormatInt(id, 10)+": Personal "+strconv.FormatBool(in.IsHR))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

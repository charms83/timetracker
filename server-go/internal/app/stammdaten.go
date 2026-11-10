package app

import (
	"fmt"
	"net/http"
	"strings"
)

/* Projekte, Tickets – Verhalten wie backend/server.js, je Benutzer getrennt. */

func (a *App) routesStammdaten(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/projects", a.auth(a.asTarget(a.projectsList))) // ?user= nur lesend (z. B. Nachtragen durch Teamleitung)
	mux.HandleFunc("GET /api/projects/{id}", a.auth(a.projectsGet))
	mux.HandleFunc("POST /api/projects", a.auth(a.projectsCreate))
	mux.HandleFunc("PUT /api/projects/{id}", a.auth(a.projectsUpdate))
	mux.HandleFunc("DELETE /api/projects/{id}", a.auth(a.projectsDelete))
	mux.HandleFunc("GET /api/tasks", a.auth(a.asTarget(a.tasksList)))
	mux.HandleFunc("POST /api/tasks", a.auth(a.tasksCreate))
	mux.HandleFunc("PUT /api/tasks/{id}", a.auth(a.tasksUpdate))
	mux.HandleFunc("DELETE /api/tasks/{id}", a.auth(a.tasksDelete))
}

/* ── Projekte ───────────────────────────────────────────────────── */

func (a *App) projectsList(w http.ResponseWriter, r *http.Request, u User) {
	q := r.URL.Query()
	sql := `SELECT p.*,
		(SELECT COUNT(*) FROM tasks t WHERE t.project_id=p.id) AS task_count,
		(SELECT COALESCE(SUM(e.duration_seconds),0) FROM time_entries e WHERE e.project_id=p.id) AS booked_seconds
		FROM projects p WHERE p.user_id=$1`
	args := []any{u.ID}
	if s := q.Get("status"); s != "" && s != "all" {
		args = append(args, s)
		sql += " AND p.status=$2"
	}
	if q.Get("with_muted") != "1" { // stumm geschaltete Projekte tauchen in Auswahllisten nicht auf
		sql += " AND COALESCE(p.muted,0)=0"
	}
	sql += " ORDER BY p.name"
	rs, err := a.maps(r.Context(), sql, args...)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rs)
}

func (a *App) projectsGet(w http.ResponseWriter, r *http.Request, u User) {
	id, ok := pathID(r)
	if !ok {
		notFound(w)
		return
	}
	p, err := a.one(r.Context(), `SELECT p.*,
		(SELECT COALESCE(SUM(e.duration_seconds),0) FROM time_entries e WHERE e.project_id=p.id) AS booked_seconds
		FROM projects p WHERE p.id=$1 AND p.user_id=$2`, id, u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	if p == nil {
		notFound(w)
		return
	}
	tasks, err := a.tasksOf(r, u, id)
	if err != nil {
		a.fail(w, err)
		return
	}
	p["tasks"] = tasks
	writeJSON(w, http.StatusOK, p)
}

func (a *App) projectsCreate(w http.ResponseWriter, r *http.Request, u User) {
	var in body
	if !readJSON(w, r, &in) {
		return
	}
	name := strings.TrimSpace(asStr(in["name"]))
	if name == "" {
		badRequest(w, "Name fehlt")
		return
	}
	status := asStr(in["status"])
	if status == "" {
		status = "active"
	}
	p, err := a.one(r.Context(), `INSERT INTO projects(user_id,name,jira_key,status,budget_hours,hourly_rate,description)
		VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING *`,
		u.ID, name, nz(in["jira_key"]), status, asFloat(in["budget_hours"]), asFloat(in["hourly_rate"]), nz(in["description"]))
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (a *App) projectsUpdate(w http.ResponseWriter, r *http.Request, u User) {
	id, ok := pathID(r)
	var in body
	if !ok {
		notFound(w)
		return
	}
	if !readJSON(w, r, &in) {
		return
	}
	name := strings.TrimSpace(asStr(in["name"]))
	if name == "" {
		badRequest(w, "Name fehlt")
		return
	}
	status := asStr(in["status"])
	if status == "" {
		status = "active"
	}
	p, err := a.one(r.Context(), `UPDATE projects SET name=$3,jira_key=$4,status=$5,budget_hours=$6,hourly_rate=$7,description=$8
		WHERE id=$1 AND user_id=$2 RETURNING *`,
		id, u.ID, name, nz(in["jira_key"]), status, asFloat(in["budget_hours"]), asFloat(in["hourly_rate"]), nz(in["description"]))
	if err != nil {
		a.fail(w, err)
		return
	}
	if p == nil {
		notFound(w)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (a *App) projectsDelete(w http.ResponseWriter, r *http.Request, u User) {
	id, ok := pathID(r)
	if !ok {
		notFound(w)
		return
	}
	if _, err := a.DB.Exec(r.Context(), `DELETE FROM projects WHERE id=$1 AND user_id=$2`, id, u.ID); err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

/* ── Tickets (tasks) ────────────────────────────────────────────── */

func (a *App) tasksOf(r *http.Request, u User, projectID int64) ([]map[string]any, error) {
	return a.maps(r.Context(), `SELECT t.*,
		(SELECT COALESCE(SUM(e.duration_seconds),0) FROM time_entries e WHERE e.task_id=t.id) AS booked_seconds
		FROM tasks t WHERE t.project_id=$1 AND t.user_id=$2 ORDER BY t.sort_order, t.created_at`, projectID, u.ID)
}

func (a *App) tasksList(w http.ResponseWriter, r *http.Request, u User) {
	id := asID(r.URL.Query().Get("projectId"))
	if id == nil {
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	rs, err := a.tasksOf(r, u, id.(int64))
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rs)
}

// tasksCreate legt ein Ticket an. local_only markiert ein Entwurfs-Ticket: Es entsteht erst bei der
// Freigabe in Plane/Tempo. Das sofortige Anlegen in Plane (Node: linkTaskToPlane) folgt mit dem Plane-Modul.
func (a *App) tasksCreate(w http.ResponseWriter, r *http.Request, u User) {
	var in body
	if !readJSON(w, r, &in) {
		return
	}
	title := strings.TrimSpace(asStr(in["title"]))
	pid := asID(in["project_id"])
	if title == "" || pid == nil {
		badRequest(w, "title + project_id fehlen")
		return
	}
	if !a.owned(r.Context(), "projects", pid, u.ID) {
		badRequest(w, "Projekt nicht gefunden")
		return
	}
	status := asStr(in["status"])
	if status == "" {
		status = "open"
	}
	localOnly, _ := in["local_only"].(bool)
	t, err := a.one(r.Context(), `INSERT INTO tasks(user_id,project_id,title,jira_ticket,status,estimated_hours,description,sort_order,local_only)
		VALUES($1,$2,$3,$4,$5,$6,$7,(SELECT COALESCE(MAX(sort_order),0)+1 FROM tasks WHERE project_id=$2),$8) RETURNING *`,
		u.ID, pid, title, nz(in["jira_ticket"]), status, asFloat(in["estimated_hours"]), nz(in["description"]), localOnly)
	if err != nil {
		a.fail(w, err)
		return
	}
	// Freier Titel ohne Ticket in einem Plane-Projekt → gleich als Work-Item in Plane anlegen (außer Entwurf)
	if !localOnly && strings.TrimSpace(asStr(in["jira_ticket"])) == "" {
		if tid, ok := t["id"].(int64); ok {
			ctx := r.Context()
			done, lerr := a.linkTaskToPlane(ctx, u.ID, tid)
			if lerr != nil {
				a.sysLog(ctx, u.ID, "warn", "plane", fmt.Sprintf("Work-Item für „%s“ nicht angelegt (bleibt lokal): %v", title, lerr))
				t["plane_error"] = lerr.Error()
			} else if done {
				if t2, err := a.one(ctx, `SELECT * FROM tasks WHERE id=$1 AND user_id=$2`, tid, u.ID); err == nil && t2 != nil {
					t = t2
				}
			}
		}
	}
	writeJSON(w, http.StatusOK, t)
}

func (a *App) tasksUpdate(w http.ResponseWriter, r *http.Request, u User) {
	id, ok := pathID(r)
	var in body
	if !ok {
		notFound(w)
		return
	}
	if !readJSON(w, r, &in) {
		return
	}
	title := strings.TrimSpace(asStr(in["title"]))
	if title == "" {
		badRequest(w, "title fehlt")
		return
	}
	status := asStr(in["status"])
	if status == "" {
		status = "open"
	}
	ticket := nz(in["jira_ticket"])
	// Ticketnummer geändert → alte Plane-Zuordnung lösen (sonst landet Zeit am alten Work-Item)
	t, err := a.one(r.Context(), `UPDATE tasks SET title=$3, status=$5, estimated_hours=$6, description=$7,
		plane_work_item_id = CASE WHEN UPPER(TRIM(COALESCE(jira_ticket,''))) <> UPPER(TRIM(COALESCE($4::text,''))) THEN NULL ELSE plane_work_item_id END,
		plane_project_id   = CASE WHEN UPPER(TRIM(COALESCE(jira_ticket,''))) <> UPPER(TRIM(COALESCE($4::text,''))) THEN NULL ELSE plane_project_id END,
		jira_ticket=$4
		WHERE id=$1 AND user_id=$2 RETURNING *`,
		id, u.ID, title, ticket, status, asFloat(in["estimated_hours"]), nz(in["description"]))
	if err != nil {
		a.fail(w, err)
		return
	}
	if t == nil {
		notFound(w)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (a *App) tasksDelete(w http.ResponseWriter, r *http.Request, u User) {
	id, ok := pathID(r)
	if !ok {
		notFound(w)
		return
	}
	if _, err := a.DB.Exec(r.Context(), `DELETE FROM tasks WHERE id=$1 AND user_id=$2`, id, u.ID); err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"example/zeitprotokoll-server/internal/crypto"
)

/* Plane-Workspaces (mehrere), vom Administrator verwaltet (Migration 023).
   Quellen/Sync/Tickets/Import laufen je eingeschaltetem Workspace.
   API-Schlüssel je Person und Plane-Adresse (Schema+Host); gleiche Adresse = gemeinsamer Schlüssel (scope=0 Primär, sonst Workspace-Id). */

type planeWorkspace struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	BaseURL   string `json:"base_url"`
	Workspace string `json:"workspace"`
	Enabled   bool   `json:"enabled"`
	CreatedAt string `json:"created_at,omitempty"`
}

type PlaneWorkspaceInput struct {
	Name, BaseURL, Workspace *string
	Enabled                  *bool
}

func (a *App) routesPlaneWorkspaces(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/admin/plane-workspaces", a.strictAdmin(a.adminPlaneList))
	mux.HandleFunc("POST /api/admin/plane-workspaces", a.strictAdmin(a.adminPlaneCreate))
	mux.HandleFunc("PUT /api/admin/plane-workspaces/{id}", a.strictAdmin(a.adminPlaneUpdate))
	mux.HandleFunc("DELETE /api/admin/plane-workspaces/{id}", a.strictAdmin(a.adminPlaneDelete))
	mux.HandleFunc("GET /api/plane/workspaces", a.auth(a.planeWorkspacesMine))
}

func (a *App) PlaneWorkspaces(ctx context.Context) ([]planeWorkspace, error) {
	rs, err := a.maps(ctx, `SELECT id, name, base_url, workspace, enabled, created_at::text FROM plane_workspaces ORDER BY id`)
	if err != nil {
		return nil, err
	}
	out := make([]planeWorkspace, 0, len(rs))
	for _, r := range rs {
		out = append(out, planeWorkspace{
			ID: int64(asFloat(r["id"])), Name: asStr(r["name"]), BaseURL: asStr(r["base_url"]),
			Workspace: asStr(r["workspace"]), Enabled: truthy(r["enabled"]), CreatedAt: asStr(r["created_at"]),
		})
	}
	return out, nil
}

func (a *App) planeWorkspaceByID(ctx context.Context, id int64) (planeWorkspace, error) {
	var w planeWorkspace
	err := a.DB.QueryRow(ctx, `SELECT id, name, base_url, workspace, enabled, created_at::text FROM plane_workspaces WHERE id=$1`, id).
		Scan(&w.ID, &w.Name, &w.BaseURL, &w.Workspace, &w.Enabled, &w.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return w, errUser("Plane-Workspace nicht gefunden")
	}
	return w, err
}

func (a *App) primaryPlaneWorkspace(ctx context.Context) (planeWorkspace, bool) {
	list, err := a.PlaneWorkspaces(ctx)
	if err != nil {
		return planeWorkspace{}, false
	}
	for _, w := range list {
		if w.Enabled && w.BaseURL != "" && w.Workspace != "" {
			return w, true
		}
	}
	if len(list) > 0 {
		return list[0], true
	}
	// Fallback: alte server_config (vor Migration / leere Tabelle)
	base := strings.TrimRight(strings.TrimSpace(a.conf(ctx, "plane_base_url")), "/")
	ws := normWS(a.conf(ctx, "plane_workspace"))
	if base != "" && ws != "" {
		return planeWorkspace{Name: "Plane", BaseURL: base, Workspace: ws, Enabled: true}, true
	}
	return planeWorkspace{}, false
}

func (a *App) checkPlaneWorkspace(in planeWorkspace) (planeWorkspace, error) {
	in.Name = strings.TrimSpace(in.Name)
	if n := len([]rune(in.Name)); n < 1 || n > 60 {
		return in, errUser("Name: 1 bis 60 Zeichen")
	}
	u, err := a.validatePlaneURL(in.BaseURL)
	if err != nil {
		return in, errUser(err.Error())
	}
	if u == "" {
		return in, errUser("Adresse fehlt")
	}
	in.BaseURL = u
	in.Workspace = normWS(in.Workspace)
	if in.Workspace == "" || len(in.Workspace) > 120 {
		return in, errUser("Workspace-Kennung fehlt oder zu lang")
	}
	return in, nil
}

func applyPlaneInput(cur planeWorkspace, in PlaneWorkspaceInput) planeWorkspace {
	if in.Name != nil {
		cur.Name = *in.Name
	}
	if in.BaseURL != nil {
		cur.BaseURL = *in.BaseURL
	}
	if in.Workspace != nil {
		cur.Workspace = *in.Workspace
	}
	if in.Enabled != nil {
		cur.Enabled = *in.Enabled
	}
	return cur
}

func (a *App) syncPlaneConfigMirror(ctx context.Context) {
	if w, ok := a.primaryPlaneWorkspace(ctx); ok && w.ID > 0 {
		_ = a.setConf(ctx, "plane_base_url", w.BaseURL)
		_ = a.setConf(ctx, "plane_workspace", w.Workspace)
	}
}

// mirrorPlaneConfigToWorkspace: CLI/alte Config-Keys → Primärzeile in plane_workspaces (falls Tabelle schon da).
func (a *App) mirrorPlaneConfigToWorkspace(ctx context.Context) {
	base := strings.TrimRight(strings.TrimSpace(a.conf(ctx, "plane_base_url")), "/")
	ws := normWS(a.conf(ctx, "plane_workspace"))
	if base == "" || ws == "" {
		return
	}
	var id int64
	err := a.DB.QueryRow(ctx, `SELECT id FROM plane_workspaces ORDER BY id LIMIT 1`).Scan(&id)
	if err != nil {
		_, _ = a.DB.Exec(ctx, `INSERT INTO plane_workspaces(name,base_url,workspace,enabled) VALUES('Plane',$1,$2,true)`, base, ws)
		return
	}
	_, _ = a.DB.Exec(ctx, `UPDATE plane_workspaces SET base_url=$2, workspace=$3 WHERE id=$1`, id, base, ws)
}

func (a *App) PlaneWorkspaceCreate(ctx context.Context, in PlaneWorkspaceInput) (planeWorkspace, error) {
	cur := applyPlaneInput(planeWorkspace{Enabled: true}, in)
	cur, err := a.checkPlaneWorkspace(cur)
	if err != nil {
		return cur, err
	}
	err = a.DB.QueryRow(ctx, `INSERT INTO plane_workspaces(name,base_url,workspace,enabled) VALUES($1,$2,$3,$4)
		RETURNING id, name, base_url, workspace, enabled, created_at::text`, cur.Name, cur.BaseURL, cur.Workspace, cur.Enabled).
		Scan(&cur.ID, &cur.Name, &cur.BaseURL, &cur.Workspace, &cur.Enabled, &cur.CreatedAt)
	if err != nil {
		return cur, uniquePlaneErr(err)
	}
	a.syncPlaneConfigMirror(ctx)
	return cur, nil
}

func (a *App) PlaneWorkspaceUpdate(ctx context.Context, id int64, in PlaneWorkspaceInput) (planeWorkspace, int64, error) {
	cur, err := a.planeWorkspaceByID(ctx, id)
	if err != nil {
		return cur, 0, err
	}
	oldScope := planeScope(cur.BaseURL)
	cur = applyPlaneInput(cur, in)
	cur, err = a.checkPlaneWorkspace(cur)
	if err != nil {
		return cur, 0, err
	}
	_, err = a.DB.Exec(ctx, `UPDATE plane_workspaces SET name=$2, base_url=$3, workspace=$4, enabled=$5 WHERE id=$1`,
		id, cur.Name, cur.BaseURL, cur.Workspace, cur.Enabled)
	if err != nil {
		return cur, 0, uniquePlaneErr(err)
	}
	cur.ID = id
	var cleared int64
	if planeScope(cur.BaseURL) != oldScope {
		// Adresse gewechselt: Schlüssel dieser Workspace-Id ungültig (AAD); Primär (scope=0) nur wenn das der einzige WS mit altem Host war
		tag, _ := a.DB.Exec(ctx, `DELETE FROM user_secrets WHERE kind='plane_key' AND scope=$1`, id)
		cleared = tag.RowsAffected()
	}
	a.syncPlaneConfigMirror(ctx)
	return cur, cleared, nil
}

func (a *App) PlaneWorkspaceDelete(ctx context.Context, id int64) (planeWorkspace, int64, error) {
	cur, err := a.planeWorkspaceByID(ctx, id)
	if err != nil {
		return cur, 0, err
	}
	var refs int64
	_ = a.DB.QueryRow(ctx, `SELECT COUNT(*) FROM project_sources WHERE workspace_id=$1`, id).Scan(&refs)
	if refs > 0 {
		return cur, 0, errUser(fmt.Sprintf("Workspace hat noch %d Quellen – zuerst abschalten oder Quellen entfernen", refs))
	}
	tag, err := a.DB.Exec(ctx, `DELETE FROM user_secrets WHERE kind='plane_key' AND scope=$1`, id)
	if err != nil {
		return cur, 0, err
	}
	if _, err := a.DB.Exec(ctx, `DELETE FROM plane_workspaces WHERE id=$1`, id); err != nil {
		return cur, 0, err
	}
	a.syncPlaneConfigMirror(ctx)
	return cur, tag.RowsAffected(), nil
}

func uniquePlaneErr(err error) error {
	var pe *pgconn.PgError
	if errors.As(err, &pe) && pe.Code == "23505" {
		return errUser("Name oder Adresse+Workspace schon vergeben")
	}
	return err
}

func (a *App) adminPlaneList(w http.ResponseWriter, r *http.Request, u User) {
	ctx := r.Context()
	list, err := a.PlaneWorkspaces(ctx)
	if err != nil {
		a.fail(w, err)
		return
	}
	out := []map[string]any{}
	for _, i := range list {
		var tokens, refs int64
		_ = a.DB.QueryRow(ctx, `SELECT COUNT(*) FROM user_secrets WHERE kind='plane_key' AND (scope=$1 OR ($1=(SELECT id FROM plane_workspaces ORDER BY id LIMIT 1) AND scope=0))`, i.ID).Scan(&tokens)
		_ = a.DB.QueryRow(ctx, `SELECT COUNT(*) FROM project_sources WHERE workspace_id=$1`, i.ID).Scan(&refs)
		out = append(out, map[string]any{"id": i.ID, "name": i.Name, "base_url": i.BaseURL, "workspace": i.Workspace,
			"enabled": i.Enabled, "created_at": i.CreatedAt, "tokens": tokens, "refs": refs, "host": planeScope(i.BaseURL)})
	}
	writeJSON(w, http.StatusOK, out)
}

func readPlaneWSInput(w http.ResponseWriter, r *http.Request) (PlaneWorkspaceInput, bool) {
	var in body
	if !readJSON(w, r, &in) {
		return PlaneWorkspaceInput{}, false
	}
	var out PlaneWorkspaceInput
	str := func(k string) *string {
		if v, ok := in[k]; ok {
			s := asStr(v)
			return &s
		}
		return nil
	}
	out.Name, out.BaseURL, out.Workspace = str("name"), str("base_url"), str("workspace")
	if v, ok := in["enabled"]; ok {
		b := truthy(v)
		out.Enabled = &b
	}
	return out, true
}

func (a *App) adminPlaneCreate(w http.ResponseWriter, r *http.Request, u User) {
	in, ok := readPlaneWSInput(w, r)
	if !ok {
		return
	}
	i, err := a.PlaneWorkspaceCreate(r.Context(), in)
	if err != nil {
		var ue errUser
		if errors.As(err, &ue) {
			badRequest(w, string(ue))
			return
		}
		a.fail(w, err)
		return
	}
	a.audit(r.Context(), u.ID, "admin.plane_workspace", fmt.Sprintf("angelegt #%d „%s“ (%s / %s)", i.ID, i.Name, planeScope(i.BaseURL), i.Workspace))
	writeJSON(w, http.StatusOK, i)
}

func (a *App) adminPlaneUpdate(w http.ResponseWriter, r *http.Request, u User) {
	id, ok := pathID(r)
	if !ok {
		notFound(w)
		return
	}
	in, ok := readPlaneWSInput(w, r)
	if !ok {
		return
	}
	i, cleared, err := a.PlaneWorkspaceUpdate(r.Context(), id, in)
	if err != nil {
		var ue errUser
		if errors.As(err, &ue) {
			badRequest(w, string(ue))
			return
		}
		a.fail(w, err)
		return
	}
	a.audit(r.Context(), u.ID, "admin.plane_workspace", fmt.Sprintf("geändert #%d „%s“ (%s / %s)", i.ID, i.Name, planeScope(i.BaseURL), i.Workspace))
	writeJSON(w, http.StatusOK, map[string]any{"workspace": i, "tokens_cleared": cleared})
}

func (a *App) adminPlaneDelete(w http.ResponseWriter, r *http.Request, u User) {
	id, ok := pathID(r)
	if !ok {
		notFound(w)
		return
	}
	i, tokens, err := a.PlaneWorkspaceDelete(r.Context(), id)
	if err != nil {
		var ue errUser
		if errors.As(err, &ue) {
			badRequest(w, string(ue))
			return
		}
		a.fail(w, err)
		return
	}
	a.audit(r.Context(), u.ID, "admin.plane_workspace", fmt.Sprintf("gelöscht #%d „%s“ (%d Tokens)", i.ID, i.Name, tokens))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "tokens_cleared": tokens})
}

func (a *App) planeWorkspacesMine(w http.ResponseWriter, r *http.Request, u User) {
	ctx := r.Context()
	list, err := a.PlaneWorkspaces(ctx)
	if err != nil {
		a.fail(w, err)
		return
	}
	haveHost := map[string]bool{}
	if key, err := a.secretFor(ctx, u.ID, "plane_key"); err == nil && key != "" {
		if w, ok := a.primaryPlaneWorkspace(ctx); ok {
			haveHost[planeScope(w.BaseURL)] = true
		}
	}
	// zusätzliche Schlüssel je Workspace-Scope
	if rows, err := a.DB.Query(ctx, `SELECT scope FROM user_secrets WHERE user_id=$1 AND kind='plane_key' AND scope<>0`, u.ID); err == nil {
		for rows.Next() {
			var sid int64
			if rows.Scan(&sid) == nil {
				if w, e := a.planeWorkspaceByID(ctx, sid); e == nil {
					haveHost[planeScope(w.BaseURL)] = true
				}
			}
		}
		rows.Close()
	}
	out := []map[string]any{}
	for _, i := range list {
		if !i.Enabled {
			continue
		}
		out = append(out, map[string]any{"id": i.ID, "name": i.Name, "base_url": i.BaseURL, "workspace": i.Workspace,
			"host": planeScope(i.BaseURL), "key_set": haveHost[planeScope(i.BaseURL)]})
	}
	writeJSON(w, http.StatusOK, out)
}

func planeMeSettingKey(wsID int64) string {
	if wsID > 0 {
		return fmt.Sprintf("plane_me_id:%d", wsID)
	}
	return "plane_me_id"
}

// planeCfgFor: Konfiguration für einen Workspace (Schlüssel über Host teilen).
func (a *App) planeCfgFor(ctx context.Context, uid int64, w planeWorkspace) (planeCfg, error) {
	st, err := a.userSettings(ctx, uid)
	if err != nil {
		return planeCfg{}, err
	}
	key, err := a.planeKeyForHost(ctx, uid, w)
	if err != nil {
		return planeCfg{}, err
	}
	mode := "manual"
	if st["plane_sync_mode"] == "auto" {
		mode = "auto"
	}
	me := st[planeMeSettingKey(w.ID)]
	if me == "" {
		me = st["plane_me_id"] // Legacy (global)
	}
	return planeCfg{
		Base: strings.TrimRight(strings.TrimSpace(w.BaseURL), "/"),
		Key:  strings.TrimSpace(key),
		WS:   normWS(w.Workspace),
		Mode: mode,
		MeID: me,
		ID:   w.ID,
	}, nil
}

// planeCfgSelect: ?workspace=<id>; ohne Angabe Primär-Workspace (Abwärtskompatibilität).
func (a *App) planeCfgSelect(ctx context.Context, uid int64, raw string) (planeCfg, error) {
	raw = strings.TrimSpace(raw)
	if raw != "" {
		id := int64(asFloat(raw))
		if id <= 0 {
			return planeCfg{}, errUser("workspace: Id des Plane-Workspace")
		}
		w, err := a.planeWorkspaceByID(ctx, id)
		if err != nil {
			return planeCfg{}, err
		}
		if !w.Enabled {
			return planeCfg{}, errUser("Plane-Workspace „" + w.Name + "“ ist ausgeschaltet")
		}
		return a.planeCfgFor(ctx, uid, w)
	}
	return a.planeCfg(ctx, uid)
}

// planeCfgForProject: Workspace aus projects.plane_workspace_id bzw. verknüpfter Quelle.
func (a *App) planeCfgForProject(ctx context.Context, uid, projectID int64) (planeCfg, error) {
	var wsID int64
	_ = a.DB.QueryRow(ctx, `SELECT COALESCE(p.plane_workspace_id, (
		SELECT s.workspace_id FROM project_sources s
		WHERE s.user_id=$1 AND s.project_id=$2 AND s.system='plane' AND s.status='linked' AND s.workspace_id IS NOT NULL
		ORDER BY s.id LIMIT 1), 0)
		FROM projects p WHERE p.id=$2 AND p.user_id=$1`, uid, projectID).Scan(&wsID)
	if wsID > 0 {
		w, err := a.planeWorkspaceByID(ctx, wsID)
		if err != nil {
			return planeCfg{}, err
		}
		return a.planeCfgFor(ctx, uid, w)
	}
	return a.planeCfg(ctx, uid)
}

// planeCfgForTicket: Workspace der lokalen Aufgabe mit diesem Plane-Kürzel, sonst Primär.
func (a *App) planeCfgForTicket(ctx context.Context, uid int64, ticket string) (planeCfg, error) {
	ticket = strings.ToUpper(strings.TrimSpace(ticket))
	if ticket == "" {
		return a.planeCfg(ctx, uid)
	}
	var wsID int64
	_ = a.DB.QueryRow(ctx, `SELECT COALESCE(t.plane_workspace_id, p.plane_workspace_id, 0)
		FROM tasks t LEFT JOIN projects p ON p.id=t.project_id
		WHERE t.user_id=$1 AND UPPER(TRIM(t.jira_ticket))=$2 AND t.plane_work_item_id IS NOT NULL
		ORDER BY t.id DESC LIMIT 1`, uid, ticket).Scan(&wsID)
	if wsID > 0 {
		w, err := a.planeWorkspaceByID(ctx, wsID)
		if err != nil {
			return planeCfg{}, err
		}
		return a.planeCfgFor(ctx, uid, w)
	}
	return a.planeCfg(ctx, uid)
}

// planeKeyForHost: Primärschlüssel (scope=0) wenn Host passt, sonst scope=Workspace-Id mit passendem Host.
func (a *App) planeKeyForHost(ctx context.Context, uid int64, w planeWorkspace) (string, error) {
	want := planeScope(w.BaseURL)
	try := func(scope int64, base string) (string, bool) {
		var blob []byte
		err := a.DB.QueryRow(ctx, `SELECT ciphertext FROM user_secrets WHERE user_id=$1 AND kind='plane_key' AND scope=$2`, uid, scope).Scan(&blob)
		if err != nil || len(blob) == 0 {
			return "", false
		}
		aad := []byte(fmt.Sprintf("u%d|plane_key|%s", uid, planeScope(base)))
		pt, err := crypto.OpenAAD(a.Key, blob, aad)
		if err != nil {
			return "", false
		}
		return string(pt), true
	}
	if prim, ok := a.primaryPlaneWorkspace(ctx); ok && planeScope(prim.BaseURL) == want {
		if s, ok := try(0, prim.BaseURL); ok {
			return s, nil
		}
		if prim.ID > 0 {
			if s, ok := try(prim.ID, prim.BaseURL); ok {
				return s, nil
			}
		}
	}
	if w.ID > 0 {
		if s, ok := try(w.ID, w.BaseURL); ok {
			return s, nil
		}
	}
	// Geschwister mit gleichem Host
	list, _ := a.PlaneWorkspaces(ctx)
	for _, x := range list {
		if planeScope(x.BaseURL) != want {
			continue
		}
		if s, ok := try(x.ID, x.BaseURL); ok {
			return s, nil
		}
	}
	if s, ok := try(0, w.BaseURL); ok { // Legacy: AAD schon mit dieser Adresse
		return s, nil
	}
	return "", nil
}

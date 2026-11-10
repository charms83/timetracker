package app

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

/* Plane-Tickets (wie backend/plane.js): suchen, ansehen, kommentieren, Status ändern, als Aufgabe übernehmen,
   Work-Items anlegen (auch für Entwurfs-Tickets bei der Freigabe). Lesende Aufrufe sind zwischengespeichert. */

var uuidRE = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func (a *App) routesPlaneTickets(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/integrations/plane/search", a.auth(a.planeSearch))
	mux.HandleFunc("GET /api/plane/tickets", a.auth(a.planeTickets))
	mux.HandleFunc("GET /api/plane/ticket/{key}", a.auth(a.planeTicket))
	mux.HandleFunc("POST /api/plane/ticket/{key}/comment", a.auth(a.planeComment))
	mux.HandleFunc("POST /api/plane/ticket/{key}/state", a.auth(a.planeState))
	mux.HandleFunc("POST /api/plane/ticket/{key}/task", a.auth(a.planeTicketTask))
	mux.HandleFunc("GET /api/plane/resolve", a.auth(a.planeResolve))
	mux.HandleFunc("POST /api/integrations/plane/work-items", a.auth(a.planeWorkItemCreate))
	mux.HandleFunc("GET /api/integrations/plane/local-only", a.auth(a.planeLocalOnly))
	mux.HandleFunc("POST /api/integrations/plane/local-only/{id}/push", a.auth(a.planeLocalOnlyPush))
}

/* ── Hilfen ─────────────────────────────────────────────────────── */

var (
	reBR     = regexp.MustCompile(`(?i)<br\s*/?>`)
	reBlock  = regexp.MustCompile(`(?i)</(p|li|h\d|div)>`)
	reLi     = regexp.MustCompile(`(?i)<li[^>]*>`)
	reTag    = regexp.MustCompile(`<[^>]+>`)
	reBlanks = regexp.MustCompile(`\n{3,}`)
)

func stripHTML(h string) string {
	s := reBR.ReplaceAllString(h, "\n")
	s = reBlock.ReplaceAllString(s, "\n")
	s = reLi.ReplaceAllString(s, "• ")
	s = reTag.ReplaceAllString(s, "")
	s = html.UnescapeString(strings.ReplaceAll(s, "&nbsp;", " "))
	return strings.TrimSpace(reBlanks.ReplaceAllString(s, "\n\n"))
}

func toHTML(text string) string {
	var out []string
	for _, par := range regexp.MustCompile(`\n{2,}`).Split(text, -1) {
		out = append(out, "<p>"+strings.ReplaceAll(html.EscapeString(par), "\n", "<br>")+"</p>")
	}
	return strings.Join(out, "")
}

// normKey prüft und normalisiert „prja-12“ → „PRJA-12“ (bevor der Wert in eine Adresse eingesetzt wird)
func normKey(s string) (string, bool) {
	k := strings.ToUpper(strings.TrimSpace(s))
	return k, keyRE.MatchString(k)
}

type stateInfo struct{ Group, Name string }

var stateCache sync.Map // "uid|projekt" → {at, map}

type stateEntry struct {
	at time.Time
	m  map[string]stateInfo
}

func (a *App) statesOf(ctx context.Context, uid int64, c planeCfg, pid string) map[string]stateInfo {
	key := fmt.Sprintf("%d|%s", uid, pid)
	if v, ok := stateCache.Load(key); ok && time.Since(v.(stateEntry).at) < time.Hour {
		return v.(stateEntry).m
	}
	r, err := a.planeDo(ctx, uid, c, "GET", c.wsPath()+"/projects/"+url.PathEscape(pid)+"/states/", nil, planeOpts{})
	if err != nil { // Fehler 5 Minuten merken, damit nicht für jedes Ticket neu angefragt wird
		empty := map[string]stateInfo{}
		stateCache.Store(key, stateEntry{time.Now().Add(-55 * time.Minute), empty})
		return empty
	}
	m := map[string]stateInfo{}
	for _, s := range listOf(r) {
		m[asStr(s["id"])] = stateInfo{asStr(s["group"]), asStr(s["name"])}
	}
	stateCache.Store(key, stateEntry{time.Now(), m})
	return m
}

type localProj struct {
	ID, WorkspaceID                    int64
	Name, PlaneProject, Ident, JiraKey string
}

func (a *App) planeProjectsLocal(ctx context.Context, uid int64) ([]localProj, error) {
	return a.planeProjectsLocalWS(ctx, uid, 0)
}

func (a *App) planeProjectsLocalWS(ctx context.Context, uid, workspaceID int64) ([]localProj, error) {
	sql := `SELECT id, name, COALESCE(plane_project_id,''), COALESCE(plane_identifier,''), COALESCE(jira_key,''), COALESCE(plane_workspace_id,0)
		FROM projects WHERE user_id=$1 AND plane_project_id IS NOT NULL AND COALESCE(muted,0)=0`
	args := []any{uid}
	if workspaceID > 0 {
		sql += ` AND (plane_workspace_id=$2 OR plane_workspace_id IS NULL)`
		args = append(args, workspaceID)
	}
	rows, err := a.DB.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []localProj
	for rows.Next() {
		var p localProj
		if err := rows.Scan(&p.ID, &p.Name, &p.PlaneProject, &p.Ident, &p.JiraKey, &p.WorkspaceID); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// planeCfgForKey: Workspace aus Query, sonst aus lokaler Aufgabe, sonst Primär; Work-Item laden (bei Miss alle Workspaces).
func (a *App) planeCfgForKey(ctx context.Context, uid int64, key, workspaceRaw string) (planeCfg, map[string]any, string, *localProj, error) {
	try := func(c planeCfg) (map[string]any, string, *localProj, error) {
		if !c.ok() {
			return nil, "", nil, &planeErr{Status: 400, Msg: "Plane ist nicht eingerichtet"}
		}
		return a.workItemByKey(ctx, uid, c, key)
	}
	if strings.TrimSpace(workspaceRaw) != "" {
		c, err := a.planeCfgSelect(ctx, uid, workspaceRaw)
		if err != nil {
			return planeCfg{}, nil, "", nil, err
		}
		wi, pid, p, err := try(c)
		return c, wi, pid, p, err
	}
	c, err := a.planeCfgForTicket(ctx, uid, key)
	if err != nil {
		return planeCfg{}, nil, "", nil, err
	}
	wi, pid, p, err := try(c)
	if err == nil {
		return c, wi, pid, p, nil
	}
	cfgs, cerr := a.planeCfgs(ctx, uid)
	if cerr != nil {
		return c, nil, "", nil, err
	}
	for _, alt := range cfgs {
		if alt.ID == c.ID && alt.WS == c.WS {
			continue
		}
		if wi, pid, p, e2 := try(alt); e2 == nil {
			return alt, wi, pid, p, nil
		}
	}
	return c, nil, "", nil, err
}

func findProj(ps []localProj, ppid string) *localProj {
	for i := range ps {
		if ps[i].PlaneProject == ppid {
			return &ps[i]
		}
	}
	return nil
}

func (a *App) shapeItem(ctx context.Context, uid int64, c planeCfg, i map[string]any, p *localProj, ppid, identFallback, meID string) map[string]any {
	pid := asStr(i["project"])
	if pid == "" {
		pid = asStr(i["project_id"])
	}
	if pid == "" {
		pid = ppid
	}
	states := a.statesOf(ctx, uid, c, pid)
	stateID := asStr(i["state"])
	if m := jm(i["state"]); m != nil {
		stateID = asStr(m["id"])
	}
	st := states[stateID]
	ident, name := strings.ToUpper(identFallback), identFallback
	var localID any
	if p != nil {
		localID, name = p.ID, p.Name
		ident = strings.ToUpper(firstNonEmpty(p.Ident, p.JiraKey, identFallback))
	}
	if ident == "" {
		ident = strings.ToUpper(asStr(i["project__identifier"]))
	}
	if p == nil {
		name = ident
	}
	mine := false
	if as, ok := i["assignees"].([]any); ok && meID != "" {
		for _, x := range as {
			if asStr(x) == meID || asStr(jm(x)["id"]) == meID {
				mine = true
			}
		}
	}
	prio := map[string]string{"urgent": "Dringend", "high": "Hoch", "medium": "Mittel", "low": "Niedrig"}[asStr(i["priority"])]
	return map[string]any{
		"id": i["id"], "key": fmt.Sprintf("%s-%s", ident, asStr(i["sequence_id"])), "summary": i["name"], "status": nzAny(st.Name), "state_group": nzAny(st.Group),
		"priority": nzAny(prio), "project_name": name, "plane_project_id": pid, "local_project_id": localID, "updated": i["updated_at"], "mine": mine,
		"due": nzAny(i["target_date"]),
	}
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

func (a *App) planeMeID(ctx context.Context, uid int64, c planeCfg) string {
	if c.MeID != "" {
		return c.MeID
	}
	if me, err := a.planeMe(ctx, uid, c, planeOpts{}); err == nil {
		return asStr(me["id"])
	}
	return ""
}

type bookedSum struct {
	Secs   int64
	TaskID int64
}

func (a *App) planeSums(ctx context.Context, uid int64, keys []string) map[string]*bookedSum {
	out := map[string]*bookedSum{}
	if len(keys) == 0 {
		return out
	}
	rows, err := a.DB.Query(ctx, `SELECT UPPER(t.jira_ticket), t.id, COALESCE(SUM(COALESCE(e.duration_seconds,0)),0)::bigint FROM tasks t
		LEFT JOIN time_entries e ON e.task_id=t.id AND e.user_id=t.user_id
		WHERE t.user_id=$1 AND UPPER(t.jira_ticket) = ANY($2) AND t.plane_work_item_id IS NOT NULL GROUP BY t.id`, uid, keys)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var k string
		var id, secs int64
		if rows.Scan(&k, &id, &secs) != nil {
			continue
		}
		o := out[k]
		if o == nil {
			o = &bookedSum{TaskID: id}
			out[k] = o
		}
		o.Secs += secs
	}
	return out
}

/* ── Suche und Liste ────────────────────────────────────────────── */

func (a *App) planeSearch(w http.ResponseWriter, r *http.Request, u User) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len([]rune(q)) < 2 {
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	ctx := r.Context()
	cfgs, err := a.planeCfgsForQuery(ctx, u.ID, r.URL.Query().Get("workspace"))
	if err != nil {
		a.fail(w, err)
		return
	}
	locals, _ := a.planeProjectsLocal(ctx, u.ID)
	out := []map[string]any{}
	seen := map[string]bool{}
	for _, c := range cfgs {
		if !c.ok() {
			continue
		}
		res, err := a.planeDo(ctx, u.ID, c, "GET", c.wsPath()+"/work-items/search/?search="+url.QueryEscape(q)+"&limit=15&workspace_search=true", nil, planeOpts{Interactive: true})
		if err != nil {
			if len(cfgs) == 1 {
				a.planeFail(w, err)
				return
			}
			continue
		}
		for _, i := range listOf(res) {
			ident := asStr(i["project__identifier"])
			key := strings.ToUpper(ident + "-" + asStr(i["sequence_id"]))
			id := asStr(i["id"])
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			p := findProj(locals, asStr(i["project_id"]))
			if p == nil {
				for k := range locals {
					if strings.EqualFold(firstNonEmpty(locals[k].Ident, locals[k].JiraKey), ident) && ident != "" {
						p = &locals[k]
						break
					}
				}
			}
			var lid any
			if p != nil {
				lid = p.ID
			}
			row := map[string]any{"id": i["id"], "key": key, "name": i["name"], "plane_project_id": i["project_id"], "local_project_id": lid}
			if c.ID > 0 {
				row["workspace_id"] = c.ID
			}
			out = append(out, row)
			if len(out) >= 30 {
				writeJSON(w, http.StatusOK, out)
				return
			}
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// planeCfgsForQuery: ein Workspace aus ?workspace=, sonst alle mit Schlüssel (leer → Primär-Versuch).
func (a *App) planeCfgsForQuery(ctx context.Context, uid int64, workspaceRaw string) ([]planeCfg, error) {
	if strings.TrimSpace(workspaceRaw) != "" {
		c, err := a.planeCfgSelect(ctx, uid, workspaceRaw)
		if err != nil {
			return nil, err
		}
		return []planeCfg{c}, nil
	}
	cfgs, err := a.planeCfgs(ctx, uid)
	if err != nil {
		return nil, err
	}
	if len(cfgs) == 0 {
		c, err := a.planeCfg(ctx, uid)
		if err != nil {
			return nil, err
		}
		return []planeCfg{c}, nil
	}
	return cfgs, nil
}

func (a *App) planeTickets(w http.ResponseWriter, r *http.Request, u User) {
	ctx := r.Context()
	wsRaw := r.URL.Query().Get("workspace")
	cfgs, err := a.planeCfgsForQuery(ctx, u.ID, wsRaw)
	if err != nil {
		a.fail(w, err)
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	mine, open := r.URL.Query().Get("mine") != "0", r.URL.Query().Get("open") != "0"
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	cacheKey := fmt.Sprintf("%d|%s|%v|%v", u.ID, wsRaw, mine, open)
	if q == "" {
		if v, ok := ticketListCache.Load(cacheKey); ok && time.Since(v.(listCached).at) < 2*time.Minute {
			writeJSON(w, http.StatusOK, v.(listCached).v)
			return
		}
	}
	projects, err := a.planeProjectsLocal(ctx, u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	var items []map[string]any
	seen := map[string]bool{}
	primary := cfgs[0]
	if q != "" {
		for _, c := range cfgs {
			if !c.ok() {
				continue
			}
			meID := a.planeMeID(ctx, u.ID, c)
			res, err := a.planeDo(ctx, u.ID, c, "GET", c.wsPath()+"/work-items/search/?search="+url.QueryEscape(q)+"&limit=15&workspace_search=true", nil, planeOpts{Interactive: true})
			if err != nil {
				if len(cfgs) == 1 {
					a.planeFail(w, err)
					return
				}
				continue
			}
			hits := listOf(res)
			if len(hits) > 15 {
				hits = hits[:15]
			}
			for _, hit := range hits {
				id := asStr(hit["id"])
				if id != "" && seen[id] {
					continue
				}
				seen[id] = true
				ppid := asStr(hit["project_id"])
				p := findProj(projects, ppid)
				wi := hit
				if uuidRE.MatchString(ppid) && uuidRE.MatchString(id) {
					if full, err := a.planeDo(ctx, u.ID, c, "GET", c.wsPath()+"/projects/"+ppid+"/work-items/"+id+"/", nil, planeOpts{}); err == nil && jm(full) != nil {
						wi = jm(full)
					}
				}
				wi["project__identifier"] = hit["project__identifier"]
				row := a.shapeItem(ctx, u.ID, c, wi, p, ppid, asStr(hit["project__identifier"]), meID)
				if c.ID > 0 {
					row["workspace_id"] = c.ID
				}
				items = append(items, row)
			}
		}
	} else {
		for k := range projects {
			p := &projects[k]
			if wsRaw != "" && p.WorkspaceID > 0 && int64(asFloat(wsRaw)) != p.WorkspaceID {
				continue
			}
			c, err := a.planeCfgForProject(ctx, u.ID, p.ID)
			if err != nil || !c.ok() {
				continue
			}
			if wsRaw != "" && c.ID > 0 && int64(asFloat(wsRaw)) != c.ID {
				continue
			}
			meID := a.planeMeID(ctx, u.ID, c)
			cursor := ""
			for page := 0; page < 4; page++ {
				path := c.wsPath() + "/projects/" + url.PathEscape(p.PlaneProject) + "/work-items/?per_page=100&order_by=-updated_at"
				if cursor != "" {
					path += "&cursor=" + url.QueryEscape(cursor)
				}
				res, err := a.planeDo(ctx, u.ID, c, "GET", path, nil, planeOpts{})
				if err != nil {
					if len(projects) == 1 {
						a.planeFail(w, err)
						return
					}
					break
				}
				for _, i := range listOf(res) {
					id := asStr(i["id"])
					if id != "" && seen[id] {
						continue
					}
					seen[id] = true
					row := a.shapeItem(ctx, u.ID, c, i, p, p.PlaneProject, "", meID)
					if c.ID > 0 {
						row["workspace_id"] = c.ID
					}
					items = append(items, row)
				}
				m := jm(res)
				if m == nil || m["next_page_results"] != true || asStr(m["next_cursor"]) == "" {
					break
				}
				cursor = asStr(m["next_cursor"])
			}
		}
	}
	filtered := items[:0]
	for _, i := range items {
		if mine && q == "" && i["mine"] != true {
			continue
		}
		if g := asStr(i["state_group"]); open && (g == "completed" || g == "cancelled") {
			continue
		}
		filtered = append(filtered, i)
	}
	items = filtered
	sort.SliceStable(items, func(x, y int) bool { return asStr(items[x]["updated"]) > asStr(items[y]["updated"]) })
	if len(items) > 200 {
		items = items[:200]
	}
	keys := make([]string, 0, len(items))
	for _, i := range items {
		keys = append(keys, asStr(i["key"]))
	}
	sums := a.planeSums(ctx, u.ID, keys)
	for _, i := range items {
		i["booked_seconds"], i["task_id"] = int64(0), nil
		if s := sums[asStr(i["key"])]; s != nil {
			i["booked_seconds"], i["task_id"] = s.Secs, s.TaskID
		}
	}
	if items == nil {
		items = []map[string]any{}
	}
	out := map[string]any{"total": len(items), "issues": items, "base": primary.Base, "ws": primary.WS}
	if q == "" {
		ticketListCache.Store(cacheKey, listCached{time.Now(), out})
	}
	writeJSON(w, http.StatusOK, out)
}

/* ── Ein Ticket ─────────────────────────────────────────────────── */

func (a *App) workItemByKey(ctx context.Context, uid int64, c planeCfg, key string) (map[string]any, string, *localProj, error) {
	r, err := a.planeDo(ctx, uid, c, "GET", c.wsPath()+"/work-items/"+url.PathEscape(key)+"/", nil, planeOpts{})
	if err != nil {
		return nil, "", nil, err
	}
	wi := jm(r)
	pid := firstNonEmpty(asStr(wi["project"]), asStr(wi["project_id"]))
	if asStr(wi["id"]) == "" || pid == "" {
		return nil, "", nil, fmt.Errorf("Work-Item %s nicht gefunden", key)
	}
	locals, _ := a.planeProjectsLocal(ctx, uid)
	return wi, pid, findProj(locals, pid), nil
}

func (a *App) planeTicket(w http.ResponseWriter, r *http.Request, u User) {
	key, ok := normKey(r.PathValue("key"))
	if !ok {
		badRequest(w, "Ticketnummer ungültig")
		return
	}
	ctx := r.Context()
	c, wi, pid, p, err := a.planeCfgForKey(ctx, u.ID, key, r.URL.Query().Get("workspace"))
	if err != nil {
		a.planeFail(w, err)
		return
	}
	item := a.shapeItem(ctx, u.ID, c, wi, p, pid, strings.SplitN(key, "-", 2)[0], c.MeID)
	states := a.statesOf(ctx, u.ID, c, pid)
	stateList := []map[string]any{}
	for id, s := range states {
		stateList = append(stateList, map[string]any{"id": id, "name": s.Name, "group": s.Group})
	}
	sort.Slice(stateList, func(i, j int) bool { return asStr(stateList[i]["name"]) < asStr(stateList[j]["name"]) })
	comments := []map[string]any{}
	if cr, err := a.planeDo(ctx, u.ID, c, "GET", c.wsPath()+"/projects/"+url.PathEscape(pid)+"/work-items/"+url.PathEscape(asStr(wi["id"]))+"/comments/", nil, planeOpts{}); err == nil {
		all := listOf(cr)
		if len(all) > 20 {
			all = all[len(all)-20:]
		}
		for _, x := range all {
			author := firstNonEmpty(asStr(jm(x["actor_detail"])["display_name"]), asStr(jm(x["created_by_detail"])["display_name"]))
			comments = append(comments, map[string]any{"id": x["id"], "author": author, "created": x["created_at"],
				"body": stripHTML(firstNonEmpty(asStr(x["comment_html"]), asStr(x["comment_stripped"])))})
		}
	}
	members := map[string]string{}
	if mr, err := a.planeDo(ctx, u.ID, c, "GET", c.wsPath()+"/members/", nil, planeOpts{}); err == nil {
		for _, m := range listOf(mr) {
			id := firstNonEmpty(asStr(m["id"]), asStr(jm(m["member"])["id"]))
			members[id] = firstNonEmpty(asStr(m["display_name"]), asStr(jm(m["member"])["display_name"]), asStr(m["first_name"]))
		}
	}
	var assignees []string
	if as, ok := wi["assignees"].([]any); ok {
		for _, x := range as {
			n := ""
			if id := asStr(x); id != "" {
				n = firstNonEmpty(members[id], id)
			} else {
				n = asStr(jm(x)["display_name"])
			}
			if n != "" {
				assignees = append(assignees, n)
			}
		}
	}
	sums := a.planeSums(ctx, u.ID, []string{key})
	entries, _ := a.maps(ctx, `SELECT e.id, e.date, e.start_time, e.end_time, e.duration_seconds, e.comment FROM time_entries e JOIN tasks t ON t.id=e.task_id
		WHERE e.user_id=$1 AND UPPER(t.jira_ticket)=$2 AND t.plane_work_item_id IS NOT NULL ORDER BY e.start_time DESC LIMIT 30`, u.ID, key)
	stateID := asStr(wi["state"])
	if m := jm(wi["state"]); m != nil {
		stateID = asStr(m["id"])
	}
	var booked int64
	if s := sums[key]; s != nil {
		booked = s.Secs
	}
	item["description"] = stripHTML(firstNonEmpty(asStr(wi["description_html"]), asStr(wi["description_stripped"])))
	item["assignee"] = nzAny(strings.Join(assignees, ", "))
	item["states"], item["state_id"], item["comments"], item["entries"] = stateList, stateID, comments, entries
	item["booked_seconds"], item["url"] = booked, fmt.Sprintf("%s/%s/browse/%s/", c.Base, c.WS, key)
	writeJSON(w, http.StatusOK, item)
}

func (a *App) planeComment(w http.ResponseWriter, r *http.Request, u User) {
	key, ok := normKey(r.PathValue("key"))
	var in body
	if !ok {
		badRequest(w, "Ticketnummer ungültig")
		return
	}
	if !readJSON(w, r, &in) {
		return
	}
	text := strings.TrimSpace(asStr(in["body"]))
	if text == "" {
		badRequest(w, "Kommentar fehlt")
		return
	}
	ctx := r.Context()
	c, wi, pid, _, err := a.planeCfgForKey(ctx, u.ID, key, r.URL.Query().Get("workspace"))
	if err != nil {
		a.planeFail(w, err)
		return
	}
	if _, err := a.planeDo(ctx, u.ID, c, "POST", c.wsPath()+"/projects/"+url.PathEscape(pid)+"/work-items/"+url.PathEscape(asStr(wi["id"]))+"/comments/", map[string]any{"comment_html": toHTML(text)}, planeOpts{}); err != nil {
		a.planeFail(w, err)
		return
	}
	a.sysLog(ctx, u.ID, "info", "plane", "Kommentar zu "+key+" geschrieben")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *App) planeState(w http.ResponseWriter, r *http.Request, u User) {
	key, ok := normKey(r.PathValue("key"))
	var in body
	if !ok {
		badRequest(w, "Ticketnummer ungültig")
		return
	}
	if !readJSON(w, r, &in) {
		return
	}
	sid := asStr(in["state_id"])
	if !uuidRE.MatchString(sid) {
		badRequest(w, "state_id ungültig")
		return
	}
	ctx := r.Context()
	c, wi, pid, _, err := a.planeCfgForKey(ctx, u.ID, key, r.URL.Query().Get("workspace"))
	if err != nil {
		a.planeFail(w, err)
		return
	}
	if _, err := a.planeDo(ctx, u.ID, c, "PATCH", c.wsPath()+"/projects/"+url.PathEscape(pid)+"/work-items/"+url.PathEscape(asStr(wi["id"]))+"/", map[string]any{"state": sid}, planeOpts{}); err != nil {
		a.planeFail(w, err)
		return
	}
	a.sysLog(ctx, u.ID, "info", "plane", fmt.Sprintf("Status von %s geändert (%s)", key, asStr(in["name"])))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// planeTicketTask: Ticket für die Zeiterfassung auswählen → lokale Aufgabe sicherstellen
func (a *App) planeTicketTask(w http.ResponseWriter, r *http.Request, u User) {
	key, ok := normKey(r.PathValue("key"))
	if !ok {
		badRequest(w, "Ticketnummer ungültig")
		return
	}
	tid, projID, msg, err := a.ensurePlaneTask(r.Context(), u.ID, key)
	switch {
	case err != nil:
		a.planeFail(w, err) // Nicht-Plane-Fehler gehen dort an a.fail
	case msg != "":
		badRequest(w, msg)
	default:
		writeJSON(w, http.StatusOK, map[string]any{"task_id": tid, "project_id": projID})
	}
}

// ensurePlaneTask: lokale Aufgabe zum Plane-Ticket finden oder anlegen (auch für den To-do-Start). msg ≠ "" = fachlich abgelehnt (400).
func (a *App) ensurePlaneTask(ctx context.Context, uid int64, key string) (int64, int64, string, error) {
	var tid, projID int64
	if err := a.DB.QueryRow(ctx, `SELECT id, project_id FROM tasks WHERE user_id=$1 AND UPPER(jira_ticket)=$2 AND plane_work_item_id IS NOT NULL ORDER BY id LIMIT 1`, uid, key).Scan(&tid, &projID); err == nil {
		return tid, projID, "", nil
	}
	c, wi, pid, p, err := a.planeCfgForKey(ctx, uid, key, "")
	if err != nil {
		return 0, 0, "", err
	}
	if p == nil {
		return 0, 0, "Das Plane-Projekt ist noch keinem lokalen Projekt zugeordnet (Projekte › Quellen)", nil
	}
	var ws any
	if c.ID > 0 {
		ws = c.ID
	}
	if err := a.DB.QueryRow(ctx, `INSERT INTO tasks(user_id,project_id,title,jira_ticket,status,plane_work_item_id,plane_project_id,plane_workspace_id) VALUES($1,$2,$3,$4,'open',$5,$6,$7) RETURNING id`,
		uid, p.ID, asStr(wi["name"]), key, asStr(wi["id"]), pid, ws).Scan(&tid); err != nil {
		return 0, 0, "", err
	}
	return tid, p.ID, "", nil
}

/* ── Adresse ohne Ticketnummer → „PRJA-581“ + Titel (für Browser-Erkennung und Add-on; nur lesen, zwischengespeichert) ── */

type resolved struct {
	at time.Time
	v  map[string]any
	ok bool
}

var resolveCache sync.Map

type listCached struct {
	at time.Time
	v  map[string]any
}

var ticketListCache sync.Map // Liste „meine Tickets“ je Person 2 Minuten (Node: myCache)

// purgeCaches räumt abgelaufene Einträge der Zwischenspeicher auf (läuft mit dem Selbsttest)
func purgeCaches() {
	now := time.Now()
	purgeIntegrationCaches(now)
	resolveCache.Range(func(k, v any) bool {
		if now.Sub(v.(resolved).at) > time.Hour {
			resolveCache.Delete(k)
		}
		return true
	})
	stateCache.Range(func(k, v any) bool {
		if now.Sub(v.(stateEntry).at) > 2*time.Hour {
			stateCache.Delete(k)
		}
		return true
	})
	ticketListCache.Range(func(k, v any) bool {
		if now.Sub(v.(listCached).at) > 10*time.Minute {
			ticketListCache.Delete(k)
		}
		return true
	})
}

// clearPendingClaims: nach einem Absturz mitten im Anlegen steht noch „pending:…“ an der Aufgabe – beim Start freigeben
func (a *App) clearPendingClaims(ctx context.Context) {
	// Nur Ansprüche, die älter als 10 Minuten sind (die Zahl nach „pending:“ ist der Zeitstempel in ns): ein anderer Pod, der gerade anlegt, behält seinen.
	cutoff := time.Now().Add(-10 * time.Minute).UnixNano()
	if tag, err := a.DB.Exec(ctx, `UPDATE tasks SET plane_work_item_id=NULL WHERE plane_work_item_id ~ '^pending:[0-9]+$'
		AND substring(plane_work_item_id from 9)::numeric < $1`, cutoff); err == nil && tag.RowsAffected() > 0 {
		a.Log.Warn("Selbsttest: unfertiges Anlegen von Plane-Tickets freigegeben", "anzahl", tag.RowsAffected())
	}
}

func (a *App) planeResolve(w http.ResponseWriter, r *http.Request, u User) {
	pid, iid := r.URL.Query().Get("project"), r.URL.Query().Get("item")
	if !uuidRE.MatchString(iid) || (pid != "" && !uuidRE.MatchString(pid)) {
		badRequest(w, "Work-Item-Kennung nötig")
		return
	}
	ck := fmt.Sprintf("%d|%s/%s", u.ID, firstNonEmpty(pid, "*"), iid)
	if v, ok := resolveCache.Load(ck); ok && time.Since(v.(resolved).at) < 30*time.Minute {
		if rv := v.(resolved); rv.ok {
			writeJSON(w, http.StatusOK, rv.v)
		} else {
			writeJSON(w, http.StatusNotFound, rv.v)
		}
		return
	}
	ctx := r.Context()
	// zuerst lokal: kennen wir das Work-Item schon? (keine Plane-Anfrage)
	var lk, lt string
	if err := a.DB.QueryRow(ctx, `SELECT jira_ticket, COALESCE(title,'') FROM tasks WHERE user_id=$1 AND plane_work_item_id=$2 AND jira_ticket IS NOT NULL LIMIT 1`, u.ID, iid).Scan(&lk, &lt); err == nil {
		v := map[string]any{"key": strings.ToUpper(lk), "title": lt}
		resolveCache.Store(ck, resolved{time.Now(), v, true})
		writeJSON(w, http.StatusOK, v)
		return
	}
	cfgs, err := a.planeCfgsForQuery(ctx, u.ID, r.URL.Query().Get("workspace"))
	if err != nil {
		a.fail(w, err)
		return
	}
	var wi map[string]any
	found := pid
	var c planeCfg
	for _, cand := range cfgs {
		if !cand.ok() {
			continue
		}
		c = cand
		if pid != "" {
			res, err := a.planeDo(ctx, u.ID, c, "GET", c.wsPath()+"/projects/"+pid+"/work-items/"+iid+"/", nil, planeOpts{})
			if err != nil {
				if len(cfgs) == 1 {
					a.planeFail(w, err)
					return
				}
				continue
			}
			wi = jm(res)
			break
		}
		locals, _ := a.planeProjectsLocalWS(ctx, u.ID, c.ID)
		for _, p := range locals {
			if res, err := a.planeDo(ctx, u.ID, c, "GET", c.wsPath()+"/projects/"+url.PathEscape(p.PlaneProject)+"/work-items/"+iid+"/", nil, planeOpts{}); err == nil && jm(res) != nil {
				wi, found = jm(res), p.PlaneProject
				break
			}
		}
		if wi != nil {
			break
		}
	}
	if wi == nil {
		v := map[string]any{"error": "Work-Item in den verknüpften Plane-Projekten nicht gefunden"}
		resolveCache.Store(ck, resolved{time.Now().Add(-20 * time.Minute), v, false}) // „nicht gefunden“ 10 min merken
		writeJSON(w, http.StatusNotFound, v)
		return
	}
	ident := ""
	_ = a.DB.QueryRow(ctx, `SELECT COALESCE(plane_identifier,'') FROM projects WHERE user_id=$1 AND plane_project_id=$2`, u.ID, found).Scan(&ident)
	if ident == "" {
		if pr, err := a.planeDo(ctx, u.ID, c, "GET", c.wsPath()+"/projects/"+url.PathEscape(found)+"/", nil, planeOpts{}); err == nil {
			ident = asStr(jm(pr)["identifier"])
		}
	}
	if ident == "" || wi["sequence_id"] == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "Work-Item nicht gefunden"})
		return
	}
	v := map[string]any{"key": strings.ToUpper(ident) + "-" + asStr(wi["sequence_id"]), "title": asStr(wi["name"])}
	resolveCache.Store(ck, resolved{time.Now(), v, true})
	writeJSON(w, http.StatusOK, v)
}

/* ── Work-Items anlegen ─────────────────────────────────────────── */

type planeProject struct {
	ID                                 int64
	WorkspaceID                        int64
	Name, PlaneProject, Ident, JiraKey string
}

func (a *App) planeProjectOf(ctx context.Context, uid, id int64) (*planeProject, error) {
	var p planeProject
	var ws *int64
	err := a.DB.QueryRow(ctx, `SELECT id, name, plane_project_id, COALESCE(plane_identifier,''), COALESCE(jira_key,''), plane_workspace_id FROM projects
		WHERE id=$1 AND user_id=$2 AND plane_project_id IS NOT NULL`, id, uid).Scan(&p.ID, &p.Name, &p.PlaneProject, &p.Ident, &p.JiraKey, &ws)
	if err != nil {
		return nil, err
	}
	if ws != nil {
		p.WorkspaceID = *ws
	}
	return &p, nil
}

type createdItem struct{ ID, Key, Name string }

func (a *App) createWorkItem(ctx context.Context, uid int64, c planeCfg, p *planeProject, title, description string) (*createdItem, error) {
	meID := a.planeMeID(ctx, uid, c)
	body := map[string]any{"name": title, "assignees": []string{}}
	if meID != "" {
		body["assignees"] = []string{meID}
	}
	if d := strings.TrimSpace(description); d != "" {
		body["description_html"] = toHTML(d)
	}
	r, err := a.planeDo(ctx, uid, c, "POST", c.wsPath()+"/projects/"+url.PathEscape(p.PlaneProject)+"/work-items/", body, planeOpts{})
	if err != nil {
		return nil, err
	}
	wi := jm(r)
	if asStr(wi["id"]) == "" {
		return nil, fmt.Errorf("Plane hat kein Work-Item zurückgegeben")
	}
	key := ""
	if seq := asStr(wi["sequence_id"]); seq != "" {
		key = strings.ToUpper(firstNonEmpty(p.Ident, p.JiraKey)) + "-" + seq
	}
	name := firstNonEmpty(asStr(wi["name"]), title)
	a.sysLog(ctx, uid, "success", "plane", fmt.Sprintf("Work-Item angelegt: %s „%s“", firstNonEmpty(key, asStr(wi["id"])), title))
	return &createdItem{ID: asStr(wi["id"]), Key: key, Name: name}, nil
}

// linkTaskToPlane: lokale Aufgabe ohne Ticket in einem Plane-Projekt → Work-Item in Plane anlegen und die Aufgabe verknüpfen.
// Gibt false zurück, wenn nichts zu tun ist (Aufgabe hat schon ein Ticket, Projekt nicht in Plane oder Regel erlaubt Plane nicht,
// Plane nicht eingerichtet, anderer Aufruf legt es gerade an).
// Doppelte Tickets werden verhindert: Die Aufgabe wird zuerst atomar „beansprucht“ (pending-Markierung); danach läuft alles
// unabhängig vom Request weiter, damit ein abgebrochener Aufruf kein verwaistes Work-Item in Plane hinterlässt.
func (a *App) linkTaskToPlane(ctx context.Context, uid, taskID int64) (bool, error) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
	defer cancel()
	var projectID int64
	err := a.DB.QueryRow(ctx, `SELECT project_id FROM tasks WHERE id=$1 AND user_id=$2 AND plane_work_item_id IS NULL AND (jira_ticket IS NULL OR TRIM(jira_ticket)='')`, taskID, uid).Scan(&projectID)
	if err != nil {
		return false, nil
	}
	p, err := a.planeProjectOf(ctx, uid, projectID)
	if err != nil {
		return false, nil
	}
	st, err := a.userSettings(ctx, uid)
	if err != nil {
		return false, err
	}
	if blocked, err := a.blockedPlane(ctx, uid, st); err != nil || containsID(blocked, projectID) {
		return false, err // Regel des Projekts erlaubt Plane nicht
	}
	c, err := a.planeCfgForProject(ctx, uid, projectID)
	if err != nil || !c.ok() {
		return false, err
	}
	// atomar beanspruchen: nur ein Aufruf bekommt die Aufgabe
	claim := "pending:" + fmt.Sprint(time.Now().UnixNano())
	var title, desc string
	err = a.DB.QueryRow(ctx, `UPDATE tasks SET plane_work_item_id=$3 WHERE id=$1 AND user_id=$2 AND plane_work_item_id IS NULL AND (jira_ticket IS NULL OR TRIM(jira_ticket)='')
		RETURNING title, COALESCE(description,'')`, taskID, uid, claim).Scan(&title, &desc)
	if err != nil {
		return false, nil // ein anderer Aufruf war schneller
	}
	release := func() {
		_, _ = a.DB.Exec(ctx, `UPDATE tasks SET plane_work_item_id=NULL WHERE id=$1 AND user_id=$2 AND plane_work_item_id=$3`, taskID, uid, claim)
	}
	it, err := a.createWorkItem(ctx, uid, c, p, title, desc)
	if err != nil {
		release()
		return false, err
	}
	var wsID any
	if c.ID > 0 {
		wsID = c.ID
	}
	tag, err := a.DB.Exec(ctx, `UPDATE tasks SET title=$3, jira_ticket=$4, plane_work_item_id=$5, plane_project_id=$6, plane_workspace_id=$8, local_only=false WHERE id=$1 AND user_id=$2 AND plane_work_item_id=$7`,
		taskID, uid, it.Name, nz(it.Key), it.ID, p.PlaneProject, claim, wsID)
	if err != nil || tag.RowsAffected() == 0 {
		// Work-Item existiert in Plane, die Verknüpfung konnte nicht gespeichert werden: nicht verlieren
		a.sysLog(ctx, uid, "error", "plane", fmt.Sprintf("Work-Item %s (%s) in Plane angelegt, Verknüpfung mit Aufgabe #%d nicht gespeichert – bitte manuell zuordnen", it.Key, it.ID, taskID))
		if err == nil {
			err = fmt.Errorf("Verknüpfung nicht gespeichert")
		}
		return false, err
	}
	return true, nil
}

func containsID(ids []int64, id int64) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

func (a *App) planeWorkItemCreate(w http.ResponseWriter, r *http.Request, u User) {
	var in body
	if !readJSON(w, r, &in) {
		return
	}
	title := strings.TrimSpace(asStr(in["name"]))
	if title == "" {
		badRequest(w, "Titel fehlt")
		return
	}
	if len([]rune(title)) > 255 {
		badRequest(w, "Titel ist zu lang (max. 255 Zeichen)")
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 2*time.Minute) // nach dem Anlegen in Plane muss die Aufgabe noch gespeichert werden
	defer cancel()
	lid := asID(in["local_project_id"])
	if lid == nil {
		badRequest(w, "Projekt ist nicht mit Plane verknüpft (Projekte › Quellen)")
		return
	}
	p, err := a.planeProjectOf(ctx, u.ID, lid.(int64))
	if err != nil {
		badRequest(w, "Projekt ist nicht mit Plane verknüpft (Projekte › Quellen)")
		return
	}
	c, err := a.planeCfgForProject(ctx, u.ID, p.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	it, err := a.createWorkItem(ctx, u.ID, c, p, title, asStr(in["description"]))
	if err != nil {
		a.planeFail(w, err)
		return
	}
	var ws any
	if c.ID > 0 {
		ws = c.ID
	}
	var tid int64
	if err := a.DB.QueryRow(ctx, `INSERT INTO tasks(user_id,project_id,title,jira_ticket,status,plane_work_item_id,plane_project_id,plane_workspace_id) VALUES($1,$2,$3,$4,'open',$5,$6,$7) RETURNING id`,
		u.ID, p.ID, it.Name, nz(it.Key), it.ID, p.PlaneProject, ws).Scan(&tid); err != nil {
		a.sysLog(ctx, u.ID, "error", "plane", fmt.Sprintf("Work-Item %s (%s) in Plane angelegt, Aufgabe nicht gespeichert – bitte manuell zuordnen", it.Key, it.ID))
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"key": nzAny(it.Key), "name": it.Name, "id": it.ID, "task_id": tid, "local_project_id": p.ID})
}

// planeLocalOnly: lokale Aufgaben ohne Ticket in Plane-Projekten (zum Nachholen)
func (a *App) planeLocalOnly(w http.ResponseWriter, r *http.Request, u User) {
	rs, err := a.maps(r.Context(), `SELECT t.id, t.title, t.project_id, p.name AS project_name, p.plane_identifier
		FROM tasks t JOIN projects p ON p.id=t.project_id
		WHERE t.user_id=$1 AND p.plane_project_id IS NOT NULL AND (t.jira_ticket IS NULL OR TRIM(t.jira_ticket)='') AND t.plane_work_item_id IS NULL
		  AND t.status<>'done' ORDER BY t.id DESC LIMIT 50`, u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rs)
}

func (a *App) planeLocalOnlyPush(w http.ResponseWriter, r *http.Request, u User) {
	id, ok := pathID(r)
	if !ok {
		notFound(w)
		return
	}
	ctx := r.Context()
	done, err := a.linkTaskToPlane(ctx, u.ID, id)
	if err != nil {
		a.planeFail(w, err)
		return
	}
	if !done {
		badRequest(w, "Aufgabe hat schon ein Ticket oder ihr Projekt ist nicht mit Plane verknüpft")
		return
	}
	row, err := a.one(ctx, `SELECT * FROM tasks WHERE id=$1 AND user_id=$2`, id, u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, row)
}

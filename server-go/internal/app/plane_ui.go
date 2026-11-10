package app

import (
	"context"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

/* Plane-Routen für die bestehende Oberfläche (wie backend/plane.js):
   GET /api/integrations/plane/my-items – meine offenen Work-Items (5 min zwischengespeichert, je Person; ?refresh=1),
   GET /api/integrations/plane/map     – Zuordnung Jira-Altticket (Tempo-Importe) → Plane-Work-Item (nur lesen). */

func (a *App) routesPlaneUI(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/integrations/plane/my-items", a.auth(a.planeMyItems))
	mux.HandleFunc("GET /api/integrations/plane/map", a.auth(a.planeMap))
}

type myItemsCached struct {
	at    time.Time
	key   string // Adresse|Workspace|Fingerabdruck des Schlüssels: neuer Schlüssel → neue Liste
	items []map[string]any
}

type myItemsState struct {
	mu       sync.Mutex
	cache    *myItemsCached
	inflight chan struct{} // nur EINE Abfrage gleichzeitig je Person
}

var (
	myItemsByUser sync.Map // Person → *myItemsState
	planeStates   sync.Map // „Person|Plane-Projekt“ → statesCached (1 h)
)

type statesCached struct {
	at time.Time
	m  map[string][2]string // Status-Id → {group, name}
}

func purgeIntegrationCaches(now time.Time) {
	for _, m := range []*sync.Map{&jiraSearchCache, &remoteSuggestCache} {
		m.Range(func(k, v any) bool {
			var at time.Time
			switch x := v.(type) {
			case searchCached:
				at = x.at
			case remoteCached:
				at = x.at
			}
			if now.Sub(at) > 10*time.Minute {
				m.Delete(k)
			}
			return true
		})
	}
	planeStates.Range(func(k, v any) bool {
		if now.Sub(v.(statesCached).at) > time.Hour {
			planeStates.Delete(k)
		}
		return true
	})
}

func (a *App) planeStatesOf(ctx context.Context, uid int64, c planeCfg, pid string) (map[string][2]string, error) {
	ck := strings.Join([]string{itoa(uid), c.Base, c.WS, pid}, "|")
	if v, ok := planeStates.Load(ck); ok && time.Since(v.(statesCached).at) < time.Hour {
		return v.(statesCached).m, nil
	}
	r, err := a.planeDo(ctx, uid, c, "GET", c.wsPath()+"/projects/"+url.PathEscape(pid)+"/states/", nil, planeOpts{})
	if err != nil {
		return nil, err
	}
	m := map[string][2]string{}
	for _, s := range listOf(r) {
		m[asStr(s["id"])] = [2]string{asStr(s["group"]), asStr(s["name"])}
	}
	planeStates.Store(ck, statesCached{time.Now(), m})
	return m, nil
}

func itoa(n int64) string { return asStr(n) }

// myItemsFilter (ohne Netz): mir zugewiesen, nicht erledigt/abgebrochen; Form wie Node
func myItemsFilter(list []map[string]any, states map[string][2]string, meID string, p localProj) []map[string]any {
	var out []map[string]any
	for _, i := range list {
		mine := false
		if l, ok := i["assignees"].([]any); ok {
			for _, x := range l {
				id := asStr(x)
				if m := jm(x); m != nil {
					id = asStr(m["id"])
				}
				if id != "" && id == meID {
					mine = true
				}
			}
		}
		sid := asStr(i["state"])
		if m := jm(i["state"]); m != nil {
			sid = asStr(m["id"])
		}
		st := states[sid]
		if !mine || st[0] == "completed" || st[0] == "cancelled" {
			continue
		}
		out = append(out, map[string]any{"id": i["id"], "key": strings.ToUpper(firstNonEmpty(p.Ident, p.JiraKey)) + "-" + asStr(i["sequence_id"]), "name": i["name"],
			"state": nz(st[1]), "updated_at": i["updated_at"], "plane_project_id": p.PlaneProject, "local_project_id": p.ID, "mine": true})
	}
	return out
}

func (a *App) planeMyItems(w http.ResponseWriter, r *http.Request, u User) {
	ctx := r.Context()
	c, err := a.planeCfg(ctx, u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	if !c.ok() {
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	ck := c.Base + "|" + c.WS + "|" + descHash(c.Key)[:8] // Fingerabdruck, nie der Schlüssel selbst
	v, _ := myItemsByUser.LoadOrStore(u.ID, &myItemsState{})
	ms := v.(*myItemsState)
	ms.mu.Lock()
	cached := ms.cache
	have := cached != nil && cached.key == ck
	if have && r.URL.Query().Get("refresh") == "" && time.Since(cached.at) < 5*time.Minute {
		ms.mu.Unlock()
		writeJSON(w, http.StatusOK, cached.items)
		return
	}
	wait := ms.inflight
	if wait == nil {
		done := make(chan struct{})
		ms.inflight = done
		go a.safely("plane-meine", func() { // läuft unabhängig vom Request zu Ende (Ergebnis für den nächsten Aufruf)
			defer func() {
				ms.mu.Lock()
				ms.inflight = nil
				ms.mu.Unlock()
				close(done)
			}()
			lctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			items, err := a.loadMyItems(lctx, u.ID, c)
			if err != nil {
				a.sysLog(lctx, u.ID, "warn", "plane", "Meine Work-Items: "+err.Error())
				return
			}
			ms.mu.Lock()
			ms.cache = &myItemsCached{at: time.Now(), key: ck, items: items}
			ms.mu.Unlock()
		})
		wait = done
	}
	ms.mu.Unlock()
	if have && time.Since(cached.at) < time.Hour { // veraltet, aber sofort
		writeJSON(w, http.StatusOK, cached.items)
		return
	}
	select {
	case <-wait:
	case <-ctx.Done():
		return
	}
	ms.mu.Lock()
	cur := ms.cache
	ms.mu.Unlock()
	if cur != nil && cur.key == ck {
		writeJSON(w, http.StatusOK, cur.items)
		return
	}
	// lief noch mit dem alten Schlüssel: jetzt einmal mit dem neuen laden
	items, err := a.loadMyItems(ctx, u.ID, c)
	if err != nil {
		a.planeFail(w, err)
		return
	}
	ms.mu.Lock()
	ms.cache = &myItemsCached{at: time.Now(), key: ck, items: items}
	ms.mu.Unlock()
	writeJSON(w, http.StatusOK, items)
}

func (a *App) loadMyItems(ctx context.Context, uid int64, c planeCfg) ([]map[string]any, error) {
	meID := a.planeMeID(ctx, uid, c)
	if meID == "" {
		return nil, &planeErr{Status: 400, Msg: "Plane-Benutzer nicht ermittelbar – API-Schlüssel prüfen"}
	}
	projects, err := a.planeProjectsLocal(ctx, uid)
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for _, p := range projects {
		states, err := a.planeStatesOf(ctx, uid, c, p.PlaneProject)
		if err != nil {
			a.sysLog(ctx, uid, "warn", "plane", "Status-Liste "+p.Name+": "+err.Error())
			continue
		}
		cursor := ""
		for page := 0; page < 6; page++ {
			path := c.wsPath() + "/projects/" + url.PathEscape(p.PlaneProject) + "/work-items/?per_page=100&order_by=-updated_at"
			if cursor != "" {
				path += "&cursor=" + url.QueryEscape(cursor)
			}
			r, err := a.planeDo(ctx, uid, c, "GET", path, nil, planeOpts{})
			if err != nil {
				return nil, err
			}
			out = append(out, myItemsFilter(listOf(r), states, meID, p)...)
			m := jm(r)
			if m == nil || m["next_page_results"] != true || asStr(m["next_cursor"]) == "" {
				break
			}
			cursor = asStr(m["next_cursor"])
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return asStr(out[i]["updated_at"]) > asStr(out[j]["updated_at"]) })
	if len(out) > 40 {
		out = out[:40]
	}
	return out, nil
}

// planeMap = Node GET /api/integrations/plane/map: {configured, open, skipped, mapped, projects}
func (a *App) planeMap(w http.ResponseWriter, r *http.Request, u User) {
	ctx := r.Context()
	c, err := a.planeCfg(ctx, u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	rows, err := a.maps(ctx, `SELECT UPPER(TRIM(t.jira_ticket)) AS jira_key, MAX(t.title) AS title, COUNT(e.id) AS entries, COALESCE(SUM(e.duration_seconds),0)::bigint AS secs,
		MIN(e.date) AS first, MAX(e.date) AS last, STRING_AGG(DISTINCT p.name, ',') AS projects
		FROM time_entries e JOIN tasks t ON t.id=e.task_id AND t.user_id=e.user_id LEFT JOIN projects p ON p.id=e.project_id AND p.user_id=e.user_id
		WHERE e.user_id=$1 AND e.source IN ('tempo','tempo_csv') AND COALESCE(e.plane_include,0)=0 AND e.end_time IS NOT NULL AND e.duration_seconds>0
		  AND t.jira_ticket IS NOT NULL AND TRIM(t.jira_ticket)<>'' GROUP BY UPPER(TRIM(t.jira_ticket))`, u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	status := map[string]string{}
	if ms, err := a.maps(ctx, `SELECT jira_key, status FROM ticket_map WHERE user_id=$1`, u.ID); err == nil {
		for _, m := range ms {
			status[asStr(m["jira_key"])] = asStr(m["status"])
		}
	}
	open, skipped := []map[string]any{}, []map[string]any{}
	for _, x := range mapRowsShape(rows, status) {
		switch x["status"] {
		case "open":
			open = append(open, x)
		case "skipped":
			skipped = append(skipped, x)
		}
	}
	mapped, err := a.maps(ctx, `SELECT m.jira_key, m.plane_key, m.title,
		(SELECT COUNT(*) FROM time_entries e JOIN tasks t ON t.id=e.task_id AND t.user_id=e.user_id WHERE e.user_id=m.user_id AND t.jira_origin=m.jira_key AND e.plane_include=1) AS entries,
		(SELECT ROUND(COALESCE(SUM(e.duration_seconds),0)/60.0)::bigint FROM time_entries e JOIN tasks t ON t.id=e.task_id AND t.user_id=e.user_id WHERE e.user_id=m.user_id AND t.jira_origin=m.jira_key AND e.plane_include=1) AS minutes,
		m.mapped_at FROM ticket_map m WHERE m.user_id=$1 AND m.status='mapped' ORDER BY m.mapped_at DESC`, u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	projects, err := a.maps(ctx, `SELECT id, name, plane_identifier FROM projects WHERE user_id=$1 AND plane_project_id IS NOT NULL AND COALESCE(status,'') <> 'archived' ORDER BY name`, u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"configured": c.ok(), "open": open, "skipped": skipped, "mapped": mapped, "projects": projects})
}

// mapRowsShape (ohne DB) = Node mapRows: Titel nur, wenn ≠ Key; Minuten gerundet; Status aus ticket_map (sonst open); nach Minuten absteigend
func mapRowsShape(rows []map[string]any, status map[string]string) []map[string]any {
	out := []map[string]any{}
	for _, r := range rows {
		k := asStr(r["jira_key"])
		if !jiraKeyRE.MatchString(k) {
			continue
		}
		var title any
		if t := asStr(r["title"]); t != "" && strings.ToUpper(t) != k {
			title = t
		}
		st := status[k]
		if st == "" {
			st = "open"
		}
		out = append(out, map[string]any{"jira_key": k, "title": title, "entries": r["entries"], "minutes": int64(asFloat(r["secs"])/60 + 0.5),
			"first": r["first"], "last": r["last"], "projects": r["projects"], "status": st})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i]["minutes"].(int64) > out[j]["minutes"].(int64) })
	return out
}

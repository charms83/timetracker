package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

/* Jira-/Tempo-Routen für die bestehende Oberfläche (Pfade und Antwortformen wie backend/jira.js und server.js):
   GET /api/jira/status, /api/jira/search, /api/jira/projects, /api/integrations/jira/my-tickets/status,
   /api/integrations/tempo/status. Mehrere Jira-Instanzen: ?instance=<Id>, sonst die einzige nutzbare; die Suche fragt
   ohne Angabe alle nutzbaren Instanzen ab und kennzeichnet jeden Treffer mit seiner Instanz. Nur eigene Daten. */

const jiraFields = "summary,status,priority,project,assignee,reporter,updated,created,issuetype,duedate"

func (a *App) routesJiraUI(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/jira/status", a.auth(a.jiraStatusUI)) // auch bei abgeschaltetem Modul: {enabled:false} (Oberfläche blendet aus)
	mux.HandleFunc("GET /api/jira/search", a.tempoGuard(a.jiraSearchUI))
	mux.HandleFunc("GET /api/jira/projects", a.tempoGuard(a.jiraProjectsUI))
	mux.HandleFunc("GET /api/integrations/jira/my-tickets/status", a.tempoGuard(a.jiraMineStatus))
	mux.HandleFunc("GET /api/integrations/tempo/status", a.tempoGuard(a.tempoConnStatus))
}

// jiraOn wie Node on(): Person hat Jira nicht ausgeschaltet (jira_enabled ≠ "0")
func jiraOn(st map[string]string) bool { return st["jira_enabled"] != "0" }

// pickInstances: ?instance=<Id> (muss nutzbar sein) oder alle nutzbaren; Fehlertext für die Oberfläche
func pickInstances(s *tempoSetup, raw string) ([]*tempoInst, string) {
	if raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		ti := s.byID[id]
		if err != nil || ti == nil || !ti.Enabled {
			return nil, "Jira-Instanz unbekannt oder ausgeschaltet"
		}
		if ti.Problem != "" {
			return nil, ti.Problem
		}
		return []*tempoInst{ti}, ""
	}
	u := s.usable()
	if len(u) == 0 {
		msg := "Jira-Anbindung ist ausgeschaltet oder nicht eingerichtet (Einstellungen › Jira)"
		for _, ti := range s.Insts {
			if ti.Enabled && ti.Problem != "" {
				msg = ti.Problem
				break
			}
		}
		return nil, msg
	}
	return u, ""
}

func instList(s *tempoSetup) []map[string]any {
	out := []map[string]any{}
	for _, ti := range s.Insts {
		if ti.Enabled {
			out = append(out, map[string]any{"id": ti.ID, "name": ti.Name, "usable": ti.Problem == "", "problem": nz(ti.Problem)})
		}
	}
	return out
}

// jiraStatusUI = Node GET /api/jira/status: {enabled, configured, base, user} (+ instances)
func (a *App) jiraStatusUI(w http.ResponseWriter, r *http.Request, u User) {
	ctx := r.Context()
	if !a.tempoModuleOn(ctx) {
		writeJSON(w, http.StatusOK, map[string]any{"enabled": false, "configured": false, "base": nil, "user": nil, "module": false})
		return
	}
	s, st, err := a.tempoSetup(ctx, u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	insts, _ := pickInstances(s, r.URL.Query().Get("instance"))
	var base, user any
	if len(insts) == 1 {
		base, user = insts[0].C.Base, nz(insts[0].C.Worker)
	}
	writeJSON(w, http.StatusOK, map[string]any{"enabled": jiraOn(st), "configured": len(insts) > 0, "base": base, "user": user, "instances": instList(s)})
}

/* ── Suche ──────────────────────────────────────────────────────── */

type searchCached struct {
	at  time.Time
	res map[string]any
}

var jiraSearchCache sync.Map // „Person|Instanz|Start|JQL“ → searchCached (30 s); Schlüssel immer mit Person

func searchCacheKey(uid, inst int64, start int, jql string) string {
	return fmt.Sprintf("%d|%d|%d|%s", uid, inst, start, jql)
}

// jiraSearchJQL = Node: Key → key = "…"; Text → text ~ "…"; mine=1 oder ohne q → currentUser(); open≠0 → unresolved
func jiraSearchJQL(q, jql, mine, open string) string {
	if jql = strings.TrimSpace(jql); jql != "" {
		return jql
	}
	q = strings.TrimSpace(q)
	var parts []string
	if q != "" && jiraKeyRE.MatchString(strings.ToUpper(q)) {
		parts = append(parts, `key = "`+strings.ToUpper(q)+`"`)
	} else if q != "" {
		parts = append(parts, `text ~ "`+jqlQuote(q)+`"`)
	}
	if mine == "1" || q == "" {
		parts = append(parts, "assignee = currentUser()")
	}
	if open != "0" {
		parts = append(parts, "resolution = Unresolved")
	}
	return strings.Join(parts, " AND ") + " ORDER BY updated DESC"
}

// shapeIssue = Node shape(): Felder für die Ticket-Liste
func shapeIssue(i map[string]any, sums map[string]*bookedSum) map[string]any {
	f := jm(i["fields"])
	st := jm(f["status"])
	key := asStr(i["key"])
	var secs int64
	var task any
	if b := sums[strings.ToUpper(key)]; b != nil {
		secs, task = b.Secs, b.TaskID
	}
	str := func(m map[string]any, k string) any { return nz(asStr(m[k])) }
	return map[string]any{"key": key, "summary": f["summary"], "status": st["name"], "status_cat": jm(st["statusCategory"])["key"],
		"priority": jm(f["priority"])["name"], "type": jm(f["issuetype"])["name"], "project": jm(f["project"])["key"], "project_name": jm(f["project"])["name"],
		"assignee": str(jm(f["assignee"]), "displayName"), "updated": f["updated"], "due": nz(asStr(f["duedate"])),
		"booked_seconds": secs, "task_id": task}
}

func (a *App) jiraSearchUI(w http.ResponseWriter, r *http.Request, u User) {
	ctx := r.Context()
	s, st, err := a.tempoSetup(ctx, u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	insts, why := pickInstances(s, r.URL.Query().Get("instance"))
	if !jiraOn(st) || len(insts) == 0 {
		badRequest(w, jsOr(map[bool]string{true: "", false: "Jira-Anbindung ist ausgeschaltet oder nicht eingerichtet (Einstellungen › Jira)"}[jiraOn(st)], why))
		return
	}
	q := r.URL.Query()
	start, _ := strconv.Atoi(q.Get("startAt"))
	start = max(0, min(start, 10000))
	jql := jiraSearchJQL(q.Get("q"), q.Get("jql"), q.Get("mine"), q.Get("open"))
	type hit struct {
		i  map[string]any
		ti *tempoInst
	}
	var issues []hit // zwischengespeicherte Antworten nie verändern (mehrere Anfragen teilen sie)
	total := 0
	for _, ti := range insts {
		ck := searchCacheKey(u.ID, ti.ID, start, jql)
		var d map[string]any
		if v, ok := jiraSearchCache.Load(ck); ok && time.Since(v.(searchCached).at) < 30*time.Second {
			d = v.(searchCached).res
		} else {
			res, err := a.jiraDo(ctx, u.ID, ti.C.jira(), "GET", "/rest/api/2/search?startAt="+strconv.Itoa(start)+"&maxResults=50&fields="+jiraFields+"&jql="+url.QueryEscape(jql), nil)
			if err != nil {
				if len(insts) == 1 {
					jiraUIFail(w, err)
					return
				}
				continue // andere Instanzen trotzdem zeigen
			}
			d = jm(res)
			dropExpired(&jiraSearchCache, 30*time.Second) // Speicher klein halten: Abgelaufenes sofort weg
			jiraSearchCache.Store(ck, searchCached{time.Now(), d})
		}
		total += int(asFloat(d["total"]))
		for _, i := range listOf(d) {
			issues = append(issues, hit{i, ti})
		}
	}
	keys := make([]string, 0, len(issues))
	for _, h := range issues {
		keys = append(keys, strings.ToUpper(asStr(h.i["key"])))
	}
	sums := a.jiraLocalSums(ctx, u.ID, keys) // gebuchte Zeit je Instanz|Key bzw. Key (lokal, nur eigene)
	out := make([]map[string]any, 0, len(issues))
	for _, h := range issues {
		sh := shapeIssue(h.i, instSums(sums, h.ti.ID))
		sh["instance_id"], sh["instance"] = h.ti.ID, h.ti.Name
		out = append(out, sh)
	}
	if len(insts) > 1 {
		sort.SliceStable(out, func(x, y int) bool { return asStr(out[x]["updated"]) > asStr(out[y]["updated"]) })
	}
	var base any
	if len(insts) == 1 {
		base = insts[0].C.Base
	}
	writeJSON(w, http.StatusOK, map[string]any{"total": total, "startAt": start, "jql": jql, "issues": out, "base": base})
}

// jiraUIFail wie Node wrap: Status < 500 → 400, sonst 502; Text aus jiraErr (ohne Token)
func jiraUIFail(w http.ResponseWriter, err error) {
	code := http.StatusBadGateway
	if s := jiraStatus(err); s > 0 && s < 500 {
		code = http.StatusBadRequest
	}
	writeJSON(w, code, map[string]any{"error": err.Error()})
}

/* ── Projekte ───────────────────────────────────────────────────── */

func (a *App) jiraProjectsUI(w http.ResponseWriter, r *http.Request, u User) {
	ctx := r.Context()
	s, st, err := a.tempoSetup(ctx, u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	insts, why := pickInstances(s, r.URL.Query().Get("instance"))
	if !jiraOn(st) || len(insts) == 0 {
		badRequest(w, jsOr(map[bool]string{true: "", false: "Jira-Anbindung ist ausgeschaltet oder nicht eingerichtet (Einstellungen › Jira)"}[jiraOn(st)], why))
		return
	}
	locals, err := a.maps(ctx, `SELECT id, name, jira_key, jira_project, plane_identifier, status FROM projects WHERE user_id=$1 AND COALESCE(status,'')<>'archived' ORDER BY name`, u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	linked := func(key string) any {
		for _, l := range locals {
			if strings.EqualFold(asStr(l["jira_project"]), key) {
				return l["id"]
			}
		}
		return nil
	}
	jira := []map[string]any{}
	for _, ti := range insts {
		if err := a.refreshProjectKeys(ctx, u.ID, ti); err != nil && len(insts) == 1 {
			jiraUIFail(w, err)
			return
		}
		ps, err := a.jiraDo(ctx, u.ID, ti.C.jira(), "GET", "/rest/api/2/project", nil)
		if err != nil {
			if len(insts) == 1 {
				jiraUIFail(w, err)
				return
			}
			continue
		}
		for _, p := range listOf(ps) {
			k := asStr(p["key"])
			jira = append(jira, map[string]any{"key": k, "name": p["name"], "linked": linked(k), "instance_id": ti.ID, "instance": ti.Name})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"jira": jira, "locals": locals})
}

/* ── Meine Jira-Tickets (Status) und Tempo-Verbindung ───────────── */

const defaultMyJQL = "assignee = currentUser() AND resolution = Unresolved ORDER BY updated DESC"

// jiraMineStatus = Node GET …/my-tickets/status: {last, count, jql}; last = Ergebnis des letzten Abrufs (…/my-tickets/sync)
func (a *App) jiraMineStatus(w http.ResponseWriter, r *http.Request, u User) {
	ctx := r.Context()
	st, err := a.userSettings(ctx, u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	var n int64
	if err := a.DB.QueryRow(ctx, `SELECT COUNT(*) FROM tasks WHERE user_id=$1 AND jira_mine=1`, u.ID).Scan(&n); err != nil {
		a.fail(w, err)
		return
	}
	var last any
	if v := st["jira_my_last"]; v != "" {
		var m map[string]any
		if json.Unmarshal([]byte(v), &m) == nil {
			last = m
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"last": last, "count": n, "jql": jsOr(strings.TrimSpace(st["jira_my_jql"]), defaultMyJQL)})
}

// tempoConnStatus = Node GET /api/integrations/tempo/status: {configured, connected, message, hint?, jiraUser?, autoFilterActive?, detail?}
func (a *App) tempoConnStatus(w http.ResponseWriter, r *http.Request, u User) {
	ctx := r.Context()
	s, _, err := a.tempoSetup(ctx, u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	insts, why := pickInstances(s, r.URL.Query().Get("instance"))
	switch {
	case len(insts) == 0:
		writeJSON(w, http.StatusOK, map[string]any{"configured": false, "connected": false, "message": jsOr(why, "Jira-URL und PAT fehlen – bitte in Einstellungen eintragen"), "instances": instList(s)})
		return
	case len(insts) > 1:
		writeJSON(w, http.StatusOK, map[string]any{"configured": true, "connected": false, "message": "Mehrere Jira-Instanzen – bitte eine wählen (?instance=<Id>)", "instances": instList(s)})
		return
	}
	writeJSON(w, http.StatusOK, a.tempoConnCheck(ctx, u.ID, insts[0]))
}

func (a *App) tempoConnCheck(ctx context.Context, uid int64, ti *tempoInst) map[string]any {
	me, err := a.jiraMyself(ctx, uid, ti.C.jira())
	if err != nil {
		return map[string]any{"configured": true, "connected": false, "message": "Jira-Authentifizierung fehlgeschlagen: " + err.Error(),
			"hint": "PAT prüfen – muss ein gültiger Jira Personal Access Token sein"}
	}
	a.saveJiraMe(ctx, uid, ti, me)
	user := map[string]any{"name": me.Name, "key": me.Key, "displayName": me.Display}
	today := time.Now().In(a.Loc).Format("2006-01-02")
	_, err = a.tempoSearch(ctx, uid, ti.C.jira(), today, today, jsOr(me.Key, me.Name))
	switch {
	case err == nil:
		return map[string]any{"configured": true, "connected": true, "message": fmt.Sprintf("Verbindung OK ✓ – eingeloggt als %s (%s)", me.Display, me.Name),
			"jiraUser": user, "autoFilterActive": true, "instance_id": ti.ID}
	case jiraStatus(err) == 401:
		return map[string]any{"configured": true, "connected": false, "message": fmt.Sprintf("Jira OK (%s), aber Tempo-Zugriff verweigert (401) – Tempo-Lizenz prüfen", me.Name)}
	case jiraStatus(err) > 0:
		return map[string]any{"configured": true, "connected": false, "message": fmt.Sprintf("Jira OK (%s), Tempo: HTTP %d", me.Name, jiraStatus(err)), "detail": truncRunes(jiraDetail(err), 300)}
	}
	return map[string]any{"configured": true, "connected": false, "message": "Verbindungsfehler: " + err.Error()}
}

// jiraLocalSums = Node localSums: gebuchte Zeit je Jira-Key (Aufgabe über jira_link bzw. jira_ticket), nur eigene Daten
func (a *App) jiraLocalSums(ctx context.Context, uid int64, keys []string) map[string]*bookedSum {
	out := map[string]*bookedSum{}
	if len(keys) == 0 {
		return out
	}
	rows, err := a.DB.Query(ctx, `SELECT UPPER(COALESCE(t.jira_link, t.jira_ticket)), t.id, COALESCE(SUM(COALESCE(e.duration_seconds,0)),0)::bigint, COALESCE(t.jira_instance,0) FROM tasks t
		LEFT JOIN time_entries e ON e.task_id=t.id AND e.user_id=t.user_id
		WHERE t.user_id=$1 AND UPPER(COALESCE(t.jira_link, t.jira_ticket)) = ANY($2) GROUP BY t.id ORDER BY t.id`, uid, keys)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var k string
		var id, secs, inst int64
		if rows.Scan(&k, &id, &secs, &inst) != nil {
			continue
		}
		if inst != 0 { // Aufgabe gehört fest zu einer Instanz
			k = strconv.FormatInt(inst, 10) + "|" + k
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

// instSums: Summen für die Treffer einer Instanz – fest zugeordnete Aufgaben dieser Instanz plus solche ohne Instanz
func instSums(all map[string]*bookedSum, inst int64) map[string]*bookedSum {
	out := map[string]*bookedSum{}
	pre := strconv.FormatInt(inst, 10) + "|"
	for k, v := range all {
		if !strings.Contains(k, "|") {
			out[k] = &bookedSum{Secs: v.Secs, TaskID: v.TaskID}
		}
	}
	for k, v := range all {
		if strings.HasPrefix(k, pre) {
			key := strings.TrimPrefix(k, pre)
			if o := out[key]; o != nil {
				o.Secs += v.Secs
			} else {
				out[key] = &bookedSum{Secs: v.Secs, TaskID: v.TaskID}
			}
		}
	}
	return out
}

// dropExpired: abgelaufene Einträge eines Zwischenspeichers entfernen
func dropExpired(m *sync.Map, ttl time.Duration) {
	now := time.Now()
	m.Range(func(k, v any) bool {
		var at time.Time
		switch x := v.(type) {
		case searchCached:
			at = x.at
		case remoteCached:
			at = x.at
		}
		if now.Sub(at) > ttl {
			m.Delete(k)
		}
		return true
	})
}

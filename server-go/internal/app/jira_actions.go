package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

/* Jira-Aktionen der Oberfläche (wie backend/jira.js und tempo-push.js): Ticket ansehen, kommentieren, Status ändern,
   als Aufgabe übernehmen, anlegen (nur mit Bestätigung, idempotent über createTicketOnce), Projekte verknüpfen,
   „Meine Jira-Tickets“ holen. Instanz: ?instance=<Id> bzw. instance_id, sonst über das Projektkürzel eindeutig. */

var regexpProject = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,29}$`)

func (a *App) routesJiraActions(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/jira/issue/{key}", a.tempoGuard(a.jiraIssueUI))
	mux.HandleFunc("POST /api/jira/issue/{key}/comment", a.tempoGuard(a.jiraCommentUI))
	mux.HandleFunc("POST /api/jira/issue/{key}/transition", a.tempoGuard(a.jiraTransitionUI))
	mux.HandleFunc("POST /api/jira/issue/{key}/task", a.tempoGuard(a.jiraTaskUI))
	mux.HandleFunc("POST /api/jira/issues", a.tempoGuard(a.jiraIssueCreateUI))
	mux.HandleFunc("PUT /api/jira/projects", a.tempoGuard(a.jiraProjectsLink))
	mux.HandleFunc("POST /api/integrations/jira/my-tickets/sync", a.tempoGuard(a.jiraMineSync))
}

// issueInstance: Instanz für einen Ticket-Key – gewählt (?instance=) oder eindeutig über das Kürzel
func (a *App) issueInstance(ctx context.Context, uid int64, key, raw string) (*tempoSetup, *tempoInst, map[string]string, error) {
	s, st, err := a.tempoSetup(ctx, uid)
	if err != nil {
		return nil, nil, nil, err
	}
	if !jiraOn(st) {
		return nil, nil, nil, errUser("Jira-Anbindung ist ausgeschaltet oder nicht eingerichtet (Einstellungen › Jira)")
	}
	if raw != "" {
		insts, why := pickInstances(s, raw)
		if len(insts) == 0 {
			return nil, nil, nil, errUser(why)
		}
		return s, insts[0], st, nil
	}
	ti, err := pickInstance(s, nil, key)
	return s, ti, st, err
}

func (a *App) jiraFail(w http.ResponseWriter, err error) {
	var ue errUser
	var ce errConflict
	switch {
	case errors.As(err, &ue):
		badRequest(w, string(ue))
	case errors.As(err, &ce):
		writeJSON(w, http.StatusConflict, map[string]any{"error": string(ce)})
	case jiraStatus(err) != 0 || errors.As(err, new(*jiraErr)):
		jiraUIFail(w, err)
	default:
		a.fail(w, err)
	}
}

func pathKey(r *http.Request) (string, bool) {
	k := strings.ToUpper(strings.TrimSpace(r.PathValue("key")))
	return k, jiraKeyRE.MatchString(k)
}

// jiraIssueUI = Node GET /api/jira/issue/:key: Ticket mit Beschreibung, Kommentaren, Übergängen und eigenen Buchungen
func (a *App) jiraIssueUI(w http.ResponseWriter, r *http.Request, u User) {
	key, ok := pathKey(r)
	if !ok {
		badRequest(w, "Ungültiger Key")
		return
	}
	ctx := r.Context()
	_, ti, _, err := a.issueInstance(ctx, u.ID, key, r.URL.Query().Get("instance"))
	if err != nil {
		a.jiraFail(w, err)
		return
	}
	i, err := a.jiraDo(ctx, u.ID, ti.C.jira(), "GET", "/rest/api/2/issue/"+key+"?fields="+jiraFields+",description,comment", nil)
	if err != nil {
		jiraUIFail(w, err)
		return
	}
	transitions := []map[string]any{}
	if l, err := a.jiraTransitions(ctx, u.ID, ti.C.jira(), key); err == nil {
		for _, t := range l {
			transitions = append(transitions, map[string]any{"id": t["id"], "name": t["name"], "to": jm(t["to"])["name"]})
		}
	}
	out := shapeIssue(jm(i), instSums(a.jiraLocalSums(ctx, u.ID, []string{key}), ti.ID))
	f := jm(jm(i)["fields"])
	comments := []map[string]any{}
	if cl, ok := jm(f["comment"])["comments"].([]any); ok {
		if len(cl) > 20 {
			cl = cl[len(cl)-20:]
		}
		for _, x := range cl {
			c := jm(x)
			comments = append(comments, map[string]any{"id": c["id"], "author": jm(c["author"])["displayName"], "created": c["created"], "body": c["body"]})
		}
	}
	entries, err := a.maps(ctx, `SELECT e.id, e.date, e.start_time, e.end_time, e.duration_seconds, e.comment FROM time_entries e JOIN tasks t ON t.id=e.task_id AND t.user_id=e.user_id
		WHERE e.user_id=$1 AND UPPER(COALESCE(t.jira_link,t.jira_ticket))=$2 AND (t.jira_instance IS NULL OR t.jira_instance=$3) ORDER BY e.start_time DESC LIMIT 30`, u.ID, key, ti.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	out["description"] = jsOr(asStr(f["description"]), "")
	out["reporter"] = nz(asStr(jm(f["reporter"])["displayName"]))
	out["created"] = f["created"]
	out["comments"], out["transitions"], out["entries"] = comments, transitions, entries
	out["url"] = ti.C.Base + "/browse/" + key
	out["instance_id"], out["instance"] = ti.ID, ti.Name
	writeJSON(w, http.StatusOK, out)
}

func (a *App) jiraCommentUI(w http.ResponseWriter, r *http.Request, u User) {
	key, ok := pathKey(r)
	var in body
	if !readJSON(w, r, &in) {
		return
	}
	text := strings.TrimSpace(asStr(in["body"]))
	if !ok || text == "" {
		badRequest(w, "Kommentar fehlt")
		return
	}
	ctx := r.Context()
	_, ti, _, err := a.issueInstance(ctx, u.ID, key, jsOr(r.URL.Query().Get("instance"), asStr(in["instance_id"])))
	if err != nil {
		a.jiraFail(w, err)
		return
	}
	if _, err := a.jiraDo(ctx, u.ID, ti.C.jira(), "POST", "/rest/api/2/issue/"+key+"/comment", map[string]any{"body": truncRunes(text, 30000)}); err != nil {
		jiraUIFail(w, err)
		return
	}
	a.sysLog(ctx, u.ID, "info", "jira", "Kommentar zu "+key+" geschrieben")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *App) jiraTransitionUI(w http.ResponseWriter, r *http.Request, u User) {
	key, ok := pathKey(r)
	var in body
	if !readJSON(w, r, &in) {
		return
	}
	id := strings.TrimSpace(asStr(in["id"]))
	if !ok || id == "" {
		badRequest(w, "Übergang fehlt")
		return
	}
	ctx := r.Context()
	_, ti, _, err := a.issueInstance(ctx, u.ID, key, jsOr(r.URL.Query().Get("instance"), asStr(in["instance_id"])))
	if err != nil {
		a.jiraFail(w, err)
		return
	}
	if err := a.jiraDoTransition(ctx, u.ID, ti.C.jira(), key, id); err != nil {
		jiraUIFail(w, err)
		return
	}
	a.sysLog(ctx, u.ID, "info", "jira", fmt.Sprintf("Status von %s geändert (%s)", key, jsOr(asStr(in["name"]), id)))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// jiraTaskUI = Node POST …/task: Ticket für die Zeiterfassung auswählen → lokale Aufgabe sicherstellen
func (a *App) jiraTaskUI(w http.ResponseWriter, r *http.Request, u User) {
	key, ok := pathKey(r)
	if !ok {
		badRequest(w, "Ungültiger Key")
		return
	}
	ctx := r.Context()
	s, ti, _, err := a.issueInstance(ctx, u.ID, key, r.URL.Query().Get("instance"))
	if err != nil {
		a.jiraFail(w, err)
		return
	}
	i, err := a.jiraDo(ctx, u.ID, ti.C.jira(), "GET", "/rest/api/2/issue/"+key+"?fields=summary,project", nil)
	if err != nil {
		jiraUIFail(w, err)
		return
	}
	res, err := a.ensureTaskForIssue(ctx, u.ID, ti, jm(i), false, keyUnique(s, ti, key) || r.URL.Query().Get("instance") != "")
	if err != nil {
		a.jiraFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// ensureTaskForIssue = Node ensureTaskForIssue: Aufgabe zum Jira-Ticket finden oder anlegen (Projekt über Verknüpfung/Kürzel).
// unique: Key gehört eindeutig zu dieser Instanz (sonst werden Aufgaben ohne Instanz nicht übernommen).
func (a *App) ensureTaskForIssue(ctx context.Context, uid int64, ti *tempoInst, i map[string]any, mine bool, unique bool) (map[string]any, error) {
	key := strings.ToUpper(asStr(i["key"]))
	if !jiraKeyRE.MatchString(key) {
		return nil, errors.New("Jira hat keinen gültigen Key geliefert")
	}
	f := jm(i["fields"])
	pkey := strings.ToUpper(jsOr(asStr(jm(f["project"])["key"]), jiraProject(key)))
	summary := truncRunes(jsOr(asStr(f["summary"]), key), 240)
	pname := truncRunes(jsOr(asStr(jm(f["project"])["name"]), pkey), 120)
	mineInt := 0
	if mine {
		mineInt = 1
	}
	var out map[string]any
	err := a.inTx(ctx, func(tx pgx.Tx) error {
		// gleiche Aufgabe nicht doppelt anlegen (paralleler Abruf derselben Person)
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, int64(7270060000000000)+uid); err != nil {
			return err
		}
		var tid, pid int64
		var link string
		var inst *int64
		// wie Node: erst Verknüpfung (jira_link/jira_origin), dann Ticket-Feld (ohne Plane-Work-Item)
		err := tx.QueryRow(ctx, `SELECT id, project_id, COALESCE(jira_link,''), jira_instance FROM tasks WHERE user_id=$1 AND (jira_instance IS NULL OR jira_instance=$3)
			AND (UPPER(jira_link)=$2 OR UPPER(jira_origin)=$2) ORDER BY id LIMIT 1`, uid, key, ti.ID).Scan(&tid, &pid, &link, &inst)
		if errors.Is(err, pgx.ErrNoRows) {
			err = tx.QueryRow(ctx, `SELECT id, project_id, COALESCE(jira_link,''), jira_instance FROM tasks WHERE user_id=$1 AND (jira_instance IS NULL OR jira_instance=$3)
				AND UPPER(TRIM(jira_ticket))=$2 AND plane_work_item_id IS NULL ORDER BY id LIMIT 1`, uid, key, ti.ID).Scan(&tid, &pid, &link, &inst)
		}
		if err == nil {
			if inst == nil && !unique {
				return errUser("Aufgabe zu " + key + " ist keiner Jira-Instanz zugeordnet und der Key ist nicht eindeutig – Instanz wählen")
			}
			if link == "" || inst == nil { // Ziel ändert sich (Ticket statt Sammelticket/ohne Ticket): Monatssperre beachten
				if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock_shared($1)`, int64(7270030000000000)+uid); err != nil {
					return err
				}
				if err := a.lockedConflict(ctx, tx, uid, "task", []ticketEntry{{TaskID: tid}}, key); err != nil {
					return err
				}
			}
			_, err = tx.Exec(ctx, `UPDATE tasks SET jira_link=COALESCE(jira_link,$3), jira_instance=COALESCE(jira_instance,$4), jira_mine=CASE WHEN $5 THEN 1 ELSE jira_mine END,
				status=CASE WHEN status='done' AND ticket_system='jira' THEN 'open' ELSE status END WHERE id=$1 AND user_id=$2`, tid, uid, key, ti.ID, mine)
			out = map[string]any{"key": key, "task_id": tid, "project_id": pid, "created": false}
			return err
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		projectCreated := false
		err = tx.QueryRow(ctx, `SELECT id FROM projects WHERE user_id=$1 AND (UPPER(jira_project)=$2 OR UPPER(jira_key)=$2) ORDER BY (UPPER(jira_project)=$2) DESC, id LIMIT 1`, uid, pkey).Scan(&pid)
		if errors.Is(err, pgx.ErrNoRows) {
			if err = tx.QueryRow(ctx, `INSERT INTO projects(user_id,name,jira_key,jira_project,status) VALUES($1,$2,$3,$3,'active') RETURNING id`, uid, pname, pkey).Scan(&pid); err != nil {
				return err
			}
			projectCreated = true
			// gleich als Jira-Quelle verknüpfen (nicht zusätzlich unter „Neue Projekte“)
			if _, err = tx.Exec(ctx, `INSERT INTO project_sources(user_id,system,external_id,key,name,project_id,status) VALUES($1,'jira',$2,$2,$3,$4,'linked')
				ON CONFLICT (user_id,system,external_id) DO UPDATE SET project_id=EXCLUDED.project_id, status='linked'`, uid, pkey, pname, pid); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		var clash bool
		_ = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tasks WHERE user_id=$1 AND project_id=$2 AND UPPER(TRIM(jira_ticket))=$3)`, uid, pid, key).Scan(&clash)
		title := summary
		if clash { // gleicher Key existiert als Plane-Ticket
			title = "Jira: " + summary
		}
		if err = tx.QueryRow(ctx, `INSERT INTO tasks(user_id,project_id,title,jira_ticket,status,jira_link,ticket_system,jira_mine,jira_instance) VALUES($1,$2,$3,$4,'open',$4,'jira',$5,$6) RETURNING id`,
			uid, pid, title, key, mineInt, ti.ID).Scan(&tid); err != nil {
			return err
		}
		out = map[string]any{"key": key, "task_id": tid, "project_id": pid, "created": true, "projectCreated": projectCreated}
		return nil
	})
	return out, err
}

// keyUnique: Key gehört eindeutig zu ti (nur eine nutzbare Instanz oder Kürzel eindeutig)
func keyUnique(s *tempoSetup, ti *tempoInst, key string) bool {
	if len(s.usable()) == 1 {
		return true
	}
	id, problem := resolveTempoInstance(key, "ticket", 0, s.keys())
	return problem == "" && id == ti.ID
}

// jiraIssueCreateUI = Node POST /api/jira/issues – nur mit Bestätigung (confirm: true), idempotent je Auftrag (createTicketOnce)
func (a *App) jiraIssueCreateUI(w http.ResponseWriter, r *http.Request, u User) {
	var in body
	if !readJSON(w, r, &in) {
		return
	}
	if in["confirm"] != true {
		badRequest(w, "Anlegen nur mit Bestätigung (confirm: true)")
		return
	}
	project := strings.ToUpper(strings.TrimSpace(asStr(in["project"])))
	summary := jsSlice(jsCollapse(jsTrim(asStr(in["summary"]))), 250)
	if project == "" || summary == "" {
		badRequest(w, "Projekt und Titel angeben")
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 2*time.Minute) // angelegtes Ticket noch speichern
	defer cancel()
	s, st, err := a.tempoSetup(ctx, u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	if !jiraOn(st) {
		badRequest(w, "Jira-Anbindung ist ausgeschaltet oder nicht eingerichtet (Einstellungen › Jira)")
		return
	}
	ti, err := pickInstance(s, in["instance_id"], project+"-1") // Instanz wählt die Person; sonst über das Projektkürzel
	if err != nil {
		a.jiraFail(w, err)
		return
	}
	rk := asStr(in["request_key"])
	if strings.TrimSpace(rk) == "" {
		rk = requestKeyOf("", ti.ID, nil, "issue|"+project+"|"+summary)
	}
	key, created, err := a.createTicketOnce(ctx, u.ID, ti, truncRunes(rk, 100), summary,
		ticketSpec{Project: project, IssueType: asStr(in["issuetype"]), Description: truncRunes(asStr(in["description"]), 30000)})
	if err != nil {
		a.ticketFail(w, err)
		return
	}
	t, err := a.ensureTaskForIssue(ctx, u.ID, ti, map[string]any{"key": key, "fields": map[string]any{"summary": summary, "project": map[string]any{"key": project}}}, true, true)
	if err != nil {
		a.jiraFail(w, err)
		return
	}
	t["created_issue"], t["instance_id"] = created, ti.ID
	writeJSON(w, http.StatusOK, t)
}

// jiraProjectsLink = Node PUT /api/jira/projects {links: {JIRAKEY: projektId|""}}: Jira-Projekt ↔ lokales Projekt
func (a *App) jiraProjectsLink(w http.ResponseWriter, r *http.Request, u User) {
	var in struct {
		Links map[string]any `json:"links"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || len(in.Links) > 500 {
		badRequest(w, "ungültige Eingabe")
		return
	}
	ctx := r.Context()
	err := a.inTx(ctx, func(tx pgx.Tx) error {
		for jk, v := range in.Links {
			jk = strings.ToUpper(strings.TrimSpace(jk))
			if !regexpProject.MatchString(jk) {
				return errUser("Jira-Projektkürzel ungültig: " + jk)
			}
			pid := asID(v)
			if _, err := tx.Exec(ctx, `UPDATE projects SET jira_project=NULL WHERE user_id=$1 AND UPPER(jira_project)=$2`, u.ID, jk); err != nil {
				return err
			}
			if pid == nil {
				if _, err := tx.Exec(ctx, `UPDATE project_sources SET project_id=NULL, status='new' WHERE user_id=$1 AND system='jira' AND external_id=$2`, u.ID, jk); err != nil {
					return err
				}
				continue
			}
			tag, err := tx.Exec(ctx, `UPDATE projects SET jira_project=$3 WHERE id=$1 AND user_id=$2`, pid, u.ID, jk)
			if err != nil {
				return err
			}
			if tag.RowsAffected() == 0 {
				return errUser("Projekt nicht gefunden")
			}
			if _, err := tx.Exec(ctx, `INSERT INTO project_sources(user_id,system,external_id,key,name,project_id,status) VALUES($1,'jira',$2,$2,$2,$3,'linked')
				ON CONFLICT (user_id,system,external_id) DO UPDATE SET project_id=EXCLUDED.project_id, status='linked'`, u.ID, jk, pid); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		a.jiraFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// jiraMineSync = Node syncMyTickets: eigene Jira-Tickets (jira_my_jql) in die Auswahl holen, je nutzbarer Instanz
func (a *App) jiraMineSync(w http.ResponseWriter, r *http.Request, u User) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Minute)
	defer cancel()
	s, st, err := a.tempoSetup(ctx, u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	insts, why := pickInstances(s, r.URL.Query().Get("instance"))
	if !jiraOn(st) || len(insts) == 0 {
		badRequest(w, jsOr(why, "Jira-Adresse und Token fehlen"))
		return
	}
	jql := jsOr(strings.TrimSpace(st["jira_my_jql"]), defaultMyJQL)
	count, created, projectsNew, gone, muted := 0, 0, 0, 0, 0
	var errs []string
	for _, ti := range insts {
		_ = a.refreshProjectKeys(ctx, u.ID, ti)
		var issues []map[string]any
		ok := true
		for at := 0; at < 1000; at += 100 {
			res, err := a.jiraDo(ctx, u.ID, ti.C.jira(), "GET", "/rest/api/2/search?startAt="+strconv.Itoa(at)+"&maxResults=100&fields=summary,project,status&jql="+url.QueryEscape(jql), nil)
			if err != nil {
				errs = append(errs, ti.Name+": "+err.Error())
				ok = false
				break
			}
			issues = append(issues, listOf(res)...)
			total := int(asFloat(jm(res)["total"]))
			if at+100 >= total {
				break
			}
			if at+100 >= 1000 { // mehr als 1000: Liste unvollständig → nichts als „nicht mehr zugewiesen“ werten
				ok = false
			}
		}
		complete := ok // ohne vollständige Liste nichts als „nicht mehr zugewiesen“ markieren
		if len(issues) == 0 && !ok {
			continue
		}
		seen := []int64{}
		for _, i := range issues {
			pk := strings.ToUpper(jsOr(asStr(jm(jm(i["fields"])["project"])["key"]), jiraProject(asStr(i["key"]))))
			var skip bool
			_ = a.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM project_sources s LEFT JOIN projects p ON p.id=s.project_id AND p.user_id=s.user_id
				WHERE s.user_id=$1 AND s.system='jira' AND s.external_id=$2 AND (s.status='ignored' OR COALESCE(p.muted,0)=1))`, u.ID, pk).Scan(&skip)
			if skip { // stumm geschaltete Jira-Projekte nicht in die Auswahl
				muted++
				continue
			}
			res, err := a.ensureTaskForIssue(ctx, u.ID, ti, i, true, keyUnique(s, ti, asStr(i["key"])))
			if err != nil {
				errs = append(errs, ti.Name+": "+err.Error())
				complete = false
				continue
			}
			count++
			if id, ok := res["task_id"].(int64); ok {
				seen = append(seen, id)
			}
			if res["created"] == true {
				created++
			}
			if res["projectCreated"] == true {
				projectsNew++
			}
		}
		if !complete {
			continue
		}
		// nicht mehr mir zugewiesen / erledigt → aus der Auswahl (Buchungen bleiben); nur Aufgaben dieser Instanz
		tag, err := a.DB.Exec(ctx, `UPDATE tasks SET jira_mine=0, status=CASE WHEN ticket_system='jira' THEN 'done' ELSE status END
			WHERE user_id=$1 AND jira_mine=1 AND (jira_instance=$2 OR (jira_instance IS NULL AND $3)) AND NOT (id = ANY($4))`,
			u.ID, ti.ID, len(insts) == 1 && len(s.usable()) == 1, seen)
		if err == nil {
			gone += int(tag.RowsAffected())
		}
	}
	last := map[string]any{"at": time.Now().UTC().Format(isoMillis), "count": count, "created": created, "projects_new": projectsNew, "gone": gone, "muted": muted}
	if b, err := json.Marshal(last); err == nil {
		a.saveUserSetting(ctx, u.ID, "jira_my_last", string(b))
	}
	a.sysLog(ctx, u.ID, "info", "jira", fmt.Sprintf("Meine Jira-Tickets: %d gefunden, %d neu%s", count, created, map[bool]string{true: fmt.Sprintf(", %d nicht mehr zugewiesen", gone), false: ""}[gone > 0]))
	if len(errs) > 0 {
		last["errors"] = errs
	}
	writeJSON(w, http.StatusOK, last)
}

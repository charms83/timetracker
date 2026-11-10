package app

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

/* Aufräumen: lokale Projekte → richtige Projekte (wie backend/cleanup.js), je Person.
   Lokal = Projekt ohne verknüpfte Plane-/Jira-Quelle. Je Projekt: ist ein Ticket (ticket), gehört zu Projekt (move),
   ist ein Projekt (link), leer (remove), lokal behalten (keep, nur mit allow_local_projects=1).
   Bewusst anders als Node: keine Datei-Sicherung vorher (Sicherung ist Betrieb, /admin); alles in einer Transaktion,
   verschobene Buchungen laufen durch Monatssperre (409 month_locked) und Änderungsprotokoll (entry_audit). */

const maxLocalProjects = 200

func (a *App) routesCleanup(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/cleanup/local-projects", a.auth(a.cleanupLocalProjects))
	mux.HandleFunc("GET /api/cleanup/search", a.auth(a.cleanupSearch))
	mux.HandleFunc("POST /api/cleanup/apply", a.auth(a.cleanupApply))
	mux.HandleFunc("PUT /api/cleanup/allow-local", a.auth(a.cleanupAllowLocal))
}

func (a *App) hasSource(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, uid, pid int64) bool {
	var ok bool
	_ = q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM project_sources WHERE user_id=$1 AND project_id=$2 AND status='linked')`, uid, pid).Scan(&ok)
	return ok
}

// cleanupTargets: Projekte mit Quelle (Ziele für „move“/„ticket“)
func (a *App) cleanupTargets(ctx context.Context, uid int64) ([]map[string]any, error) {
	return a.maps(ctx, `SELECT p.id, p.name,
		json_agg(json_build_object('system', s.system, 'key', COALESCE(s.key,'')) ORDER BY s.system) AS sources
		FROM projects p JOIN project_sources s ON s.project_id=p.id AND s.user_id=p.user_id AND s.status='linked'
		WHERE p.user_id=$1 AND COALESCE(p.status,'')<>'archived' AND p.merged_into IS NULL GROUP BY p.id, p.name ORDER BY p.name`, uid)
}

// projectForKey: welches Projekt passt zu einem Ticket-Key? (Quelle mit gleichem Kürzel, Plane zuerst; sonst Jira-Zuordnung)
func (a *App) projectForKey(ctx context.Context, uid int64, key string) any {
	pre := strings.SplitN(strings.ToUpper(strings.TrimSpace(key)), "-", 2)[0]
	if pre == "" {
		return nil
	}
	var id int64
	if a.DB.QueryRow(ctx, `SELECT project_id FROM project_sources WHERE user_id=$1 AND status='linked' AND UPPER(key)=$2 AND project_id IS NOT NULL
		ORDER BY CASE system WHEN 'plane' THEN 0 ELSE 1 END, id LIMIT 1`, uid, pre).Scan(&id) == nil {
		return id
	}
	return a.projectByJiraKey(ctx, uid, pre)
}

func (a *App) localProjects(ctx context.Context, uid int64) ([]map[string]any, error) {
	ps, err := a.maps(ctx, `SELECT p.id, p.name, p.jira_key FROM projects p WHERE p.user_id=$1 AND COALESCE(p.status,'')<>'archived' AND p.merged_into IS NULL
		AND NOT EXISTS (SELECT 1 FROM project_sources s WHERE s.user_id=p.user_id AND s.project_id=p.id AND s.status='linked') ORDER BY p.name LIMIT $2`, uid, maxLocalProjects)
	if err != nil || len(ps) == 0 {
		return ps, err
	}
	// Tickets mit Quelle (Kandidaten „ist ein Ticket“) und offene Quellen (Kandidaten „ist ein Projekt“): je Person einmal lesen
	tks, err := a.maps(ctx, `SELECT DISTINCT ON (UPPER(t.jira_ticket)) UPPER(t.jira_ticket) AS key, t.title, t.project_id FROM tasks t
		JOIN project_sources s ON s.project_id=t.project_id AND s.user_id=t.user_id AND s.status='linked'
		WHERE t.user_id=$1 AND TRIM(COALESCE(t.jira_ticket,''))<>'' ORDER BY UPPER(t.jira_ticket), t.id LIMIT 5000`, uid)
	if err != nil {
		return nil, err
	}
	srcs, err := a.maps(ctx, `SELECT id, system, key, name, status FROM project_sources WHERE user_id=$1 AND status IN ('new','ignored') AND gone=0 ORDER BY system, name LIMIT 2000`, uid)
	if err != nil {
		return nil, err
	}
	ids := make([]int64, 0, len(ps))
	for _, p := range ps {
		ids = append(ids, p["id"].(int64))
	}
	// Zählungen und Aufgaben aller lokalen Projekte gebündelt (keine Abfragen je Projekt)
	type use struct {
		n, s, ntN, ntS int64
		first, last    *string
	}
	uses := map[int64]*use{}
	ur, err := a.DB.Query(ctx, `SELECT e.project_id, COUNT(*), COALESCE(SUM(e.duration_seconds),0)::bigint, MIN(e.date), MAX(e.date),
		COUNT(*) FILTER (WHERE TRIM(COALESCE(t.jira_ticket,''))=''), COALESCE(SUM(e.duration_seconds) FILTER (WHERE TRIM(COALESCE(t.jira_ticket,''))=''),0)::bigint
		FROM time_entries e LEFT JOIN tasks t ON t.id=e.task_id AND t.user_id=e.user_id WHERE e.user_id=$1 AND e.project_id = ANY($2) GROUP BY e.project_id`, uid, ids)
	if err != nil {
		return nil, err
	}
	for ur.Next() {
		var pid int64
		x := &use{}
		if err := ur.Scan(&pid, &x.n, &x.s, &x.first, &x.last, &x.ntN, &x.ntS); err != nil {
			ur.Close()
			return nil, err
		}
		uses[pid] = x
	}
	ur.Close()
	if err := ur.Err(); err != nil {
		return nil, err
	}
	allTasks, err := a.maps(ctx, `SELECT t.project_id AS pid, t.id, t.title, UPPER(TRIM(COALESCE(t.jira_ticket,''))) AS key, COUNT(e.id) AS n, COALESCE(SUM(e.duration_seconds),0)::bigint AS s
		FROM tasks t LEFT JOIN time_entries e ON e.task_id=t.id AND e.user_id=t.user_id WHERE t.user_id=$1 AND t.project_id = ANY($2) GROUP BY t.id ORDER BY s DESC LIMIT 20000`, uid, ids)
	if err != nil {
		return nil, err
	}
	tasksOf := map[int64][]map[string]any{}
	for _, t := range allTasks {
		pid := t["pid"].(int64)
		delete(t, "pid")
		tasksOf[pid] = append(tasksOf[pid], t)
	}
	keyProject := map[string]any{} // Präfix → Projekt (je Aufruf zwischengespeichert)
	forKey := func(k string) any {
		pre := strings.SplitN(k, "-", 2)[0]
		if v, ok := keyProject[pre]; ok {
			return v
		}
		v := a.projectForKey(ctx, uid, k)
		keyProject[pre] = v
		return v
	}
	out := make([]map[string]any, 0, len(ps))
	for _, p := range ps {
		pid := p["id"].(int64)
		name := asStr(p["name"])
		us := uses[pid]
		if us == nil {
			us = &use{}
		}
		n, secs, first, last, ntN, ntS := us.n, us.s, us.first, us.last, us.ntN, us.ntS
		tasks := tasksOf[pid]
		if tasks == nil {
			tasks = []map[string]any{}
		}
		// Vorschlag 1: Tickets der Aufgaben zeigen schon auf ein Projekt mit Quelle
		votes := map[int64]int64{}
		for _, t := range tasks {
			if k := asStr(t["key"]); ticketKeyRE.MatchString(k) {
				if tp, ok := forKey(k).(int64); ok {
					votes[tp] += max(int64(asFloat(t["s"])), 1)
				}
			}
		}
		var byTickets int64
		var best int64 = -1
		for id, v := range votes {
			if v > best || (v == best && id < byTickets) {
				byTickets, best = id, v
			}
		}
		// Vorschlag 2: ein Ticket heißt wie das Projekt; Vorschlag 3: Name ähnlich einer offenen Quelle
		tk := []map[string]any{}
		for _, t := range tks {
			sc := int(math.Round(math.Max(textSim(name, asStr(t["title"])), nameSim(name, asStr(t["title"]), nil)) * 100))
			if sc >= 60 {
				tk = append(tk, map[string]any{"key": t["key"], "title": t["title"], "project_id": t["project_id"], "score": sc})
			}
		}
		sort.SliceStable(tk, func(i, j int) bool { return tk[i]["score"].(int) > tk[j]["score"].(int) })
		tk = tk[:min(3, len(tk))]
		jk := strings.ToUpper(asStr(p["jira_key"]))
		sm := []map[string]any{}
		for _, s := range srcs {
			v := math.Max(textSim(name, asStr(s["name"])), nameSim(name, asStr(s["name"]), nil))
			if jk != "" && jk == strings.ToUpper(asStr(s["key"])) {
				v = math.Max(v, 0.9)
			}
			if sc := int(math.Round(v * 100)); sc >= 60 {
				sm = append(sm, map[string]any{"id": s["id"], "system": s["system"], "key": s["key"], "name": s["name"], "status": s["status"], "score": sc})
			}
		}
		sort.SliceStable(sm, func(i, j int) bool { return sm[i]["score"].(int) > sm[j]["score"].(int) })
		sm = sm[:min(3, len(sm))]
		var suggest any
		switch {
		case n == 0 && len(tasks) == 0:
			suggest = map[string]any{"action": "remove", "reason": "keine Buchungen, keine Aufgaben"}
		case byTickets != 0 && len(votes) == 1 && ntN == 0:
			suggest = map[string]any{"action": "move", "target": byTickets, "reason": "alle Tickets gehören dorthin"}
		case len(tk) > 0:
			t := tk[0]
			suggest = map[string]any{"action": "ticket", "target": t["project_id"], "ticket": t["key"], "title": t["title"], "reason": fmt.Sprintf("Ticket %v „%v“ (%v %%)", t["key"], t["title"], t["score"])}
		case len(sm) > 0:
			s := sm[0]
			sys := map[bool]string{true: "Plane", false: "Jira"}[asStr(s["system"]) == "plane"]
			suggest = map[string]any{"action": "link", "source_id": s["id"], "reason": fmt.Sprintf("%s-Projekt %v „%v“ (%v %%)", sys, s["key"], s["name"], s["score"])}
		case byTickets != 0:
			suggest = map[string]any{"action": "move", "target": byTickets, "reason": "die meisten Tickets gehören dorthin"}
		}
		out = append(out, map[string]any{"id": pid, "name": name, "entries": n, "seconds": secs, "first": first, "last": last,
			"no_ticket": map[string]any{"n": ntN, "s": ntS}, "tasks": tasks[:min(12, len(tasks))], "ticket_suggestions": tk, "source_suggestions": sm, "suggest": suggest})
	}
	return out, nil
}

func (a *App) cleanupLocalProjects(w http.ResponseWriter, r *http.Request, u User) {
	ctx := r.Context()
	st, err := a.userSettings(ctx, u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	lp, err := a.localProjects(ctx, u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	tg, err := a.cleanupTargets(ctx, u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	srcs, err := a.maps(ctx, `SELECT id, system, key, name, status FROM project_sources WHERE user_id=$1 AND status IN ('new','ignored') AND gone=0 ORDER BY system, name LIMIT 2000`, u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"allow_local": st["allow_local_projects"] == "1", "projects": lp, "targets": tg, "sources": srcs})
}

// cleanupSearch: Ticket zum Projektnamen suchen – eigene Aufgaben, dann Jira (nur wenn Modul an und Jira eingerichtet)
func (a *App) cleanupSearch(w http.ResponseWriter, r *http.Request, u User) {
	q := strings.TrimSpace(strings.NewReplacer(`"`, " ", `\`, " ").Replace(r.URL.Query().Get("q")))
	if q == "" {
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	q = truncRunes(q, 100)
	ctx := r.Context()
	like := "%" + likeArg(q) + "%"
	rows, err := a.maps(ctx, `SELECT UPPER(t.jira_ticket) AS key, t.title, t.project_id, p.name AS project FROM tasks t JOIN projects p ON p.id=t.project_id AND p.user_id=t.user_id
		WHERE t.user_id=$1 AND TRIM(COALESCE(t.jira_ticket,''))<>'' AND (t.title ILIKE $2 ESCAPE '\' OR t.jira_ticket ILIKE $2 ESCAPE '\') LIMIT 8`, u.ID, like)
	if err != nil {
		a.fail(w, err)
		return
	}
	out := []map[string]any{}
	have := map[string]bool{}
	for _, t := range rows {
		var pid any = t["project_id"]
		if p, ok := pid.(int64); !ok || !a.hasSource(ctx, a.DB, u.ID, p) {
			pid = a.projectForKey(ctx, u.ID, asStr(t["key"]))
		}
		have[asStr(t["key"])] = true
		out = append(out, map[string]any{"key": t["key"], "title": t["title"], "project_id": pid, "project": t["project"], "from": "lokal"})
	}
	if a.tempoModuleOn(ctx) {
		if s, st, err := a.tempoSetup(ctx, u.ID); err == nil && jiraOn(st) {
			if insts, _ := pickInstances(s, ""); len(insts) > 0 {
				jql := `text ~ "` + q + `" ORDER BY updated DESC`
				if k := strings.ToUpper(q); ticketKeyRE.MatchString(k) {
					jql = `key = "` + k + `"`
				}
				for _, ti := range insts {
					list, err := a.jiraSearch(ctx, u.ID, ti.C.jira(), jql, 8)
					if err != nil {
						out = append(out, map[string]any{"error": "Jira-Suche (" + ti.Name + "): Abfrage fehlgeschlagen"})
						continue
					}
					for _, i := range list {
						k := strings.ToUpper(asStr(i["key"]))
						if k == "" || have[k] {
							continue
						}
						have[k] = true
						f := jm(i["fields"])
						out = append(out, map[string]any{"key": k, "title": asStr(f["summary"]), "project_id": a.projectForKey(ctx, u.ID, k),
							"project": asStr(jm(f["project"])["name"]), "status": asStr(jm(f["status"])["name"]), "from": "Jira"})
					}
				}
			}
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// cleanupApply: {project_id, action: ticket|move|link|remove|keep, target_project_id?, ticket?, title?, source_id?}
func (a *App) cleanupApply(w http.ResponseWriter, r *http.Request, u User) {
	var in body
	if !readJSON(w, r, &in) {
		return
	}
	ctx := r.Context()
	pid, _ := asID(in["project_id"]).(int64)
	var pname string
	if pid == 0 || a.DB.QueryRow(ctx, `SELECT name FROM projects WHERE id=$1 AND user_id=$2`, pid, u.ID).Scan(&pname) != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "Projekt nicht gefunden"})
		return
	}
	action := asStr(in["action"])
	key := strings.ToUpper(strings.TrimSpace(asStr(in["ticket"])))
	var target int64
	var tname string
	if v, ok := asID(in["target_project_id"]).(int64); ok && a.DB.QueryRow(ctx, `SELECT name FROM projects WHERE id=$1 AND user_id=$2`, v, u.ID).Scan(&tname) == nil {
		target = v
	}
	if action == "ticket" || action == "move" {
		if target == 0 || target == pid {
			badRequest(w, "Zielprojekt wählen")
			return
		}
		if !a.hasSource(ctx, a.DB, u.ID, target) {
			badRequest(w, "Zielprojekt hat keine Plane-/Jira-Quelle")
			return
		}
	}
	if action == "ticket" && !ticketKeyRE.MatchString(key) {
		badRequest(w, "Ticket-Key wie IU-123 angeben")
		return
	}
	if action == "keep" {
		st, err := a.userSettings(ctx, u.ID)
		if err != nil {
			a.fail(w, err)
			return
		}
		if st["allow_local_projects"] != "1" {
			badRequest(w, "Lokale Projekte sind ausgeschaltet (Einstellungen › Arbeitszeit)")
			return
		}
	}
	moved := map[string]int64{"entries": 0, "tasks": 0}
	now := time.Now().UTC().Format(isoMillis)
	err := a.inTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, ticketTaskLock+u.ID); err != nil { // keine doppelten Aufgaben
			return err
		}
		switch action {
		case "ticket", "move":
			if action == "ticket" {
				var tt int64
				err := tx.QueryRow(ctx, `SELECT id FROM tasks WHERE user_id=$1 AND project_id=$2 AND UPPER(TRIM(jira_ticket))=$3 ORDER BY id LIMIT 1`, u.ID, target, key).Scan(&tt)
				if err == pgx.ErrNoRows {
					var isPlane bool
					if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM project_sources WHERE user_id=$1 AND project_id=$2 AND system='plane' AND UPPER(key)=$3)`,
						u.ID, target, strings.SplitN(key, "-", 2)[0]).Scan(&isPlane); err != nil {
						return err
					}
					var link, sys any
					if !isPlane {
						link, sys = key, "jira"
					}
					err = tx.QueryRow(ctx, `INSERT INTO tasks(user_id,project_id,title,jira_ticket,status,jira_link,ticket_system) VALUES($1,$2,$3,$4,'open',$5,$6) RETURNING id`,
						u.ID, target, truncRunes(jsOr(strings.TrimSpace(asStr(in["title"])), pname), 200), key, link, sys).Scan(&tt)
				}
				if err != nil {
					return err
				}
				// Buchungen ohne eigenes Ticket → auf das Ticket
				tag, err := tx.Exec(ctx, `UPDATE time_entries SET task_id=$3, project_id=$4 WHERE user_id=$1 AND project_id=$2 AND (task_id IS NULL OR task_id IN
					(SELECT id FROM tasks WHERE user_id=$1 AND project_id=$2 AND TRIM(COALESCE(jira_ticket,''))=''))`, u.ID, pid, tt, target)
				if err != nil {
					return err
				}
				moved["entries"] += tag.RowsAffected()
				if _, err := tx.Exec(ctx, `DELETE FROM tasks t WHERE t.user_id=$1 AND t.project_id=$2 AND TRIM(COALESCE(t.jira_ticket,''))=''
					AND NOT EXISTS (SELECT 1 FROM time_entries e WHERE e.task_id=t.id)`, u.ID, pid); err != nil {
					return err
				}
			}
			// Aufgaben mit eigenem Ticket wandern ins Ziel (gibt es das Ticket dort schon, werden die Buchungen umgehängt)
			ts, err := mapsQ(ctx, tx, `SELECT id, UPPER(TRIM(COALESCE(jira_ticket,''))) AS k FROM tasks WHERE user_id=$1 AND project_id=$2 ORDER BY id`, u.ID, pid)
			if err != nil {
				return err
			}
			for _, t := range ts {
				tid := t["id"].(int64)
				var twin int64
				if k := asStr(t["k"]); k != "" {
					if err := tx.QueryRow(ctx, `SELECT id FROM tasks WHERE user_id=$1 AND project_id=$2 AND UPPER(TRIM(jira_ticket))=$3 ORDER BY id LIMIT 1`, u.ID, target, k).Scan(&twin); err != nil && err != pgx.ErrNoRows {
						return err
					}
				}
				if twin != 0 {
					tag, err := tx.Exec(ctx, `UPDATE time_entries SET task_id=$3, project_id=$4 WHERE user_id=$1 AND task_id=$2`, u.ID, tid, twin, target)
					if err != nil {
						return err
					}
					moved["entries"] += tag.RowsAffected()
					for _, tbl := range []string{"plan_tasks", "meeting_rules", "timer_state"} {
						if _, err := tx.Exec(ctx, "UPDATE "+tbl+" SET task_id=$3 WHERE user_id=$1 AND task_id=$2", u.ID, tid, twin); err != nil {
							return err
						}
					}
					if _, err := tx.Exec(ctx, `DELETE FROM tasks WHERE id=$1 AND user_id=$2`, tid, u.ID); err != nil {
						return err
					}
				} else {
					if _, err := tx.Exec(ctx, `UPDATE tasks SET project_id=$3 WHERE id=$1 AND user_id=$2`, tid, u.ID, target); err != nil {
						return err
					}
					moved["tasks"]++
				}
			}
			tag, err := tx.Exec(ctx, `UPDATE time_entries SET project_id=$3 WHERE user_id=$1 AND project_id=$2`, u.ID, pid, target)
			if err != nil {
				return err
			}
			moved["entries"] += tag.RowsAffected()
			for _, tbl := range []string{"plan_tasks", "meeting_rules", "timer_state"} {
				if _, err := tx.Exec(ctx, "UPDATE "+tbl+" SET project_id=$3 WHERE user_id=$1 AND project_id=$2", u.ID, pid, target); err != nil {
					return err
				}
			}
			_, err = tx.Exec(ctx, `UPDATE projects SET status='archived', closed_at=$3, successor_id=$4, merged_into=$4, muted=1 WHERE id=$1 AND user_id=$2`, pid, u.ID, now, target)
			return err
		case "link":
			sid, _ := asID(in["source_id"]).(int64)
			s, err := a.loadSource(ctx, tx, u.ID, sid)
			if err == pgx.ErrNoRows {
				return errUser("Quelle nicht gefunden")
			}
			if err != nil {
				return err
			}
			return a.linkSource(ctx, tx, u.ID, s, pid)
		case "remove":
			var n int64
			if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM time_entries WHERE user_id=$1 AND project_id=$2`, u.ID, pid).Scan(&n); err != nil {
				return err
			}
			if n > 0 {
				return errUser("Projekt hat Buchungen – erst verschieben")
			}
			_, err := tx.Exec(ctx, `UPDATE projects SET status='archived', closed_at=$3, muted=1 WHERE id=$1 AND user_id=$2`, pid, u.ID, now)
			return err
		case "keep":
			_, err := tx.Exec(ctx, `UPDATE projects SET output_rule='none' WHERE id=$1 AND user_id=$2`, pid, u.ID)
			return err
		}
		return errUser("Unbekannte Aktion")
	})
	if ue, ok := err.(errUser); ok {
		badRequest(w, string(ue))
		return
	}
	if err != nil {
		a.fail(w, err) // Monatssperre → 409 month_locked
		return
	}
	msg := fmt.Sprintf("„%s“: %s", pname, action)
	if target != 0 {
		msg += " → „" + tname + "“"
	}
	if key != "" {
		msg += " auf " + key
	}
	a.sysLog(ctx, u.ID, "info", "aufräumen", fmt.Sprintf("%s · %d Buchungen, %d Aufgaben", msg, moved["entries"], moved["tasks"]))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "moved": moved, "backup": nil})
}

func (a *App) cleanupAllowLocal(w http.ResponseWriter, r *http.Request, u User) {
	var in body
	if !readJSON(w, r, &in) {
		return
	}
	v := "0"
	if truthy(in["on"]) {
		v = "1"
	}
	if err := a.setUserSetting(r.Context(), a.DB, u.ID, "allow_local_projects", v); err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

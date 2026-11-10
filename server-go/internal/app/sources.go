package app

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
)

/* Projekte und ihre Quellen (wie backend/sources.js).
   Ein Projekt ist das echte Projekt (z. B. „Beispiel Marketing“); Plane-/Jira-Projekte sind QUELLEN für Tickets, die genau
   einem Projekt zugeordnet sind. Der Server legt in Plane/Jira keine Projekte an: Die Projekte werden gelesen, neue landen
   unter „Neue Projekte gefunden“ und werden übernommen (Vorschlag: gleiches Kürzel / ähnlicher Name).
   Hier: Einlesen aus Plane, Verknüpfen, Ignorieren, Regel je Projekt, Nachfolger. Jira-Quelle, Zusammenführen,
   Namen angleichen und „Lokale Zeiten“ folgen. */

var outputRules = []string{"default", "bridge", "plane", "tempo", "none"}

func (a *App) routesSources(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/sources", a.auth(a.sourcesList))
	mux.HandleFunc("POST /api/sources/scan", a.auth(a.sourcesScanRoute))
	mux.HandleFunc("POST /api/sources/{id}/link", a.auth(a.sourceLink))
	mux.HandleFunc("POST /api/sources/{id}/unlink", a.auth(a.sourceUnlink))
	mux.HandleFunc("POST /api/sources/{id}/ignore", a.auth(a.sourceIgnore))
	mux.HandleFunc("POST /api/sources/ignore-all", a.auth(a.sourcesIgnoreAll))
	mux.HandleFunc("PUT /api/sources/default-rule", a.auth(a.sourcesDefaultRule))
	mux.HandleFunc("PUT /api/projects/{id}/setup", a.auth(a.projectSetup))
	mux.HandleFunc("POST /api/integrations/plane/projects/link", a.auth(a.planeProjectsLink))
}

/* ── Regeln ─────────────────────────────────────────────────────── */

func (a *App) defaultRule(st map[string]string) string {
	if r := st["output_rule_default"]; r != "" {
		return r
	}
	return "bridge"
}

// inPlane: nur wenn das Projekt WIRKLICH in Plane ist (verknüpft, Quelle nicht archiviert)
func (a *App) inPlane(ctx context.Context, uid, pid int64) bool {
	var ok bool
	err := a.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM projects WHERE id=$1 AND user_id=$2 AND plane_project_id IS NOT NULL)
		AND NOT EXISTS(SELECT 1 FROM project_sources WHERE project_id=$1 AND user_id=$2 AND system='plane' AND status='linked' AND gone=1)`, pid, uid).Scan(&ok)
	return err == nil && ok
}

func ruleAllows(rule string, systems ...string) bool {
	for _, s := range systems {
		if rule == s {
			return true
		}
	}
	return false
}

/* ── Verknüpfen ─────────────────────────────────────────────────── */

type srcRow struct {
	ID            int64
	System, ExtID string
	Key, Name     string
	ProjectID     *int64
}

func (a *App) loadSource(ctx context.Context, tx pgx.Tx, uid, id int64) (*srcRow, error) {
	var s srcRow
	err := tx.QueryRow(ctx, `SELECT id, system, external_id, COALESCE(key,''), COALESCE(name,''), project_id FROM project_sources WHERE id=$1 AND user_id=$2`, id, uid).
		Scan(&s.ID, &s.System, &s.ExtID, &s.Key, &s.Name, &s.ProjectID)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// mirror spiegelt die Verknüpfung in die Felder am Projekt (plane_project_id, plane_identifier, jira_project)
func (a *App) mirror(ctx context.Context, tx pgx.Tx, uid int64, s *srcRow, pid *int64) error {
	if s.System == "plane" {
		if _, err := tx.Exec(ctx, `UPDATE projects SET plane_project_id=NULL, plane_identifier=NULL WHERE user_id=$1 AND plane_project_id=$2`, uid, s.ExtID); err != nil {
			return err
		}
		if pid != nil {
			_, err := tx.Exec(ctx, `UPDATE projects SET plane_project_id=$3, plane_identifier=$4 WHERE id=$1 AND user_id=$2`, *pid, uid, s.ExtID, nz(s.Key))
			return err
		}
		return nil
	}
	if _, err := tx.Exec(ctx, `UPDATE projects SET jira_project=NULL WHERE user_id=$1 AND UPPER(jira_project)=$2`, uid, strings.ToUpper(s.ExtID)); err != nil {
		return err
	}
	if pid != nil {
		_, err := tx.Exec(ctx, `UPDATE projects SET jira_project=$3 WHERE id=$1 AND user_id=$2`, *pid, uid, s.ExtID)
		return err
	}
	return nil
}

// linkSource ist der EINZIGE Weg, eine Quelle mit einem Projekt zu verbinden; je System höchstens eine Quelle je Projekt.
func (a *App) linkSource(ctx context.Context, tx pgx.Tx, uid int64, s *srcRow, pid int64) error {
	rows, err := tx.Query(ctx, `SELECT id FROM project_sources WHERE user_id=$1 AND project_id=$2 AND system=$3 AND status='linked' AND id<>$4`, uid, pid, s.System, s.ID)
	if err != nil {
		return err
	}
	var others []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		others = append(others, id)
	}
	rows.Close()
	for _, id := range others {
		o, err := a.loadSource(ctx, tx, uid, id)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE project_sources SET project_id=NULL, status='new' WHERE id=$1 AND user_id=$2`, id, uid); err != nil {
			return err
		}
		if err := a.mirror(ctx, tx, uid, o, nil); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE project_sources SET project_id=$3, status='linked' WHERE id=$1 AND user_id=$2`, s.ID, uid, pid); err != nil {
		return err
	}
	return a.mirror(ctx, tx, uid, s, &pid)
}

func (a *App) unlinkSource(ctx context.Context, tx pgx.Tx, uid int64, s *srcRow) error {
	if _, err := tx.Exec(ctx, `UPDATE project_sources SET project_id=NULL, status='new' WHERE id=$1 AND user_id=$2`, s.ID, uid); err != nil {
		return err
	}
	return a.mirror(ctx, tx, uid, s, nil)
}

// sourceOf: Quelle nach System + Kennung finden oder anlegen
func (a *App) sourceOf(ctx context.Context, tx pgx.Tx, uid int64, system, extID, key, name string) (*srcRow, error) {
	if system == "jira" {
		extID = strings.ToUpper(extID)
	}
	k := strings.ToUpper(key)
	if k == "" && system == "jira" {
		k = extID
	}
	if name == "" {
		name = key
	}
	if name == "" {
		name = extID
	}
	var id int64
	err := tx.QueryRow(ctx, `INSERT INTO project_sources(user_id,system,external_id,key,name,status) VALUES($1,$2,$3,$4,$5,'new')
		ON CONFLICT (user_id,system,external_id) DO UPDATE SET system=EXCLUDED.system RETURNING id`, uid, system, extID, nz(k), name).Scan(&id)
	if err != nil {
		return nil, err
	}
	return a.loadSource(ctx, tx, uid, id)
}

func (a *App) sourceLink(w http.ResponseWriter, r *http.Request, u User) {
	id, ok := pathID(r)
	var in body
	if !ok {
		notFound(w)
		return
	}
	if !readJSON(w, r, &in) {
		return
	}
	ctx := r.Context()
	if invalidID(in["project_id"]) {
		badRequest(w, "project_id ungültig")
		return
	}
	var pid int64
	var notFoundSrc, noProject bool
	err := a.inTx(ctx, func(tx pgx.Tx) error {
		s, err := a.loadSource(ctx, tx, u.ID, id)
		if err == pgx.ErrNoRows {
			notFoundSrc = true
			return nil
		}
		if err != nil {
			return err
		}
		if v := asID(in["project_id"]); v != nil {
			pid = v.(int64)
			var ok bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM projects WHERE id=$1 AND user_id=$2)`, pid, u.ID).Scan(&ok); err != nil {
				return err
			}
			if !ok {
				noProject = true
				return nil
			}
		} else if c := jm(in["create"]); c != nil {
			name := strings.TrimSpace(asStr(c["name"]))
			if name == "" {
				name = s.Name
			}
			if name == "" {
				name = s.Key
			}
			if r := []rune(name); len(r) > 120 {
				name = string(r[:120])
			}
			var jk any
			if s.System == "jira" {
				jk = nz(s.Key)
			}
			if err := tx.QueryRow(ctx, `INSERT INTO projects(user_id,name,jira_key,status,output_rule) VALUES($1,$2,$3,'active','default') RETURNING id`, u.ID, name, jk).Scan(&pid); err != nil {
				return err
			}
		} else {
			noProject = true
			return nil
		}
		return a.linkSource(ctx, tx, u.ID, s, pid)
	})
	if err != nil {
		a.fail(w, err)
		return
	}
	if notFoundSrc {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "Quelle nicht gefunden"})
		return
	}
	if noProject {
		badRequest(w, "Projekt wählen oder neu anlegen")
		return
	}
	a.sysLog(ctx, u.ID, "info", "projekte", fmt.Sprintf("Quelle #%d → Projekt #%d", id, pid))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "project_id": pid})
}

func (a *App) sourceUnlink(w http.ResponseWriter, r *http.Request, u User) {
	id, ok := pathID(r)
	if !ok {
		notFound(w)
		return
	}
	ctx := r.Context()
	missing := false
	err := a.inTx(ctx, func(tx pgx.Tx) error {
		s, err := a.loadSource(ctx, tx, u.ID, id)
		if err == pgx.ErrNoRows {
			missing = true
			return nil
		}
		if err != nil {
			return err
		}
		return a.unlinkSource(ctx, tx, u.ID, s)
	})
	if err != nil {
		a.fail(w, err)
		return
	}
	if missing {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "Quelle nicht gefunden"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *App) sourceIgnore(w http.ResponseWriter, r *http.Request, u User) {
	id, ok := pathID(r)
	var in body
	if !ok {
		notFound(w)
		return
	}
	if !readJSON(w, r, &in) {
		return
	}
	status := "ignored"
	if undo, _ := in["undo"].(bool); undo {
		status = "new"
	}
	ctx := r.Context()
	err := a.inTx(ctx, func(tx pgx.Tx) error {
		if s, err := a.loadSource(ctx, tx, u.ID, id); err == nil && s.ProjectID != nil {
			if err := a.mirror(ctx, tx, u.ID, s, nil); err != nil { // Verknüpfung lösen, auch in den Projektfeldern
				return err
			}
		}
		_, err := tx.Exec(ctx, `UPDATE project_sources SET status=$3, project_id=NULL WHERE id=$1 AND user_id=$2`, id, u.ID, status)
		return err
	})
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// sourcesIgnoreAll: alle offenen „Neue Projekte“ auf einmal stumm schalten (außer den genannten)
func (a *App) sourcesIgnoreAll(w http.ResponseWriter, r *http.Request, u User) {
	var in body
	if r.ContentLength != 0 && !readJSON(w, r, &in) {
		return
	}
	except := []int64{} // nicht nil: ein nil-Slice ginge als NULL an die Datenbank (id <> ALL(NULL) trifft nie)
	if l, ok := in["except"].([]any); ok {
		for _, x := range l {
			if n := int64(asFloat(x)); n > 0 {
				except = append(except, n)
			}
		}
	}
	tag, err := a.DB.Exec(r.Context(), `UPDATE project_sources SET status='ignored' WHERE user_id=$1 AND status='new' AND gone=0 AND id <> ALL($2::bigint[])`, u.ID, except)
	if err != nil {
		a.fail(w, err)
		return
	}
	a.sysLog(r.Context(), u.ID, "info", "projekte", fmt.Sprintf("%d neue Projekte stumm geschaltet", tag.RowsAffected()))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "n": tag.RowsAffected()})
}

func (a *App) sourcesDefaultRule(w http.ResponseWriter, r *http.Request, u User) {
	var in body
	if !readJSON(w, r, &in) {
		return
	}
	rule := asStr(in["rule"])
	if !contains(outputRules[1:], rule) {
		badRequest(w, "Unbekannte Regel")
		return
	}
	if _, err := a.DB.Exec(r.Context(), `INSERT INTO user_settings(user_id,key,value) VALUES($1,'output_rule_default',$2) ON CONFLICT (user_id,key) DO UPDATE SET value=EXCLUDED.value`, u.ID, rule); err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

/* ── Einlesen aus Plane ─────────────────────────────────────────── */

var scanning sync.Map // Benutzer-Id → true, solange ein Einlesen läuft

func (a *App) upsertSource(ctx context.Context, uid int64, system, extID, key, name string, archived bool) error {
	gone := 0
	if archived {
		gone = 1
	}
	tag, err := a.DB.Exec(ctx, `UPDATE project_sources SET key=$4, name=$5, gone=$6, last_seen=now() WHERE user_id=$1 AND system=$2 AND external_id=$3`, uid, system, extID, nz(key), name, gone)
	if err != nil || tag.RowsAffected() > 0 {
		return err
	}
	// schon über die Projektfelder verknüpft?
	var linked *int64
	q := `SELECT id FROM projects WHERE user_id=$1 AND plane_project_id=$2 LIMIT 1`
	if system == "jira" {
		q = `SELECT id FROM projects WHERE user_id=$1 AND UPPER(jira_project)=$2 LIMIT 1`
	}
	_ = a.DB.QueryRow(ctx, q, uid, extID).Scan(&linked)
	status := "new"
	if linked != nil {
		status = "linked"
	}
	_, err = a.DB.Exec(ctx, `INSERT INTO project_sources(user_id,system,external_id,key,name,project_id,status,gone) VALUES($1,$2,$3,$4,$5,$6,$7,$8)
		ON CONFLICT (user_id,system,external_id) DO NOTHING`, uid, system, extID, nz(key), name, linked, status, gone)
	return err
}

func (a *App) scanSources(ctx context.Context, uid int64, trigger string) (map[string]any, error) {
	if _, busy := scanning.LoadOrStore(uid, true); busy {
		return map[string]any{"busy": true}, nil
	}
	defer scanning.Delete(uid)
	out := map[string]any{"plane": nil, "jira": nil, "new": 0, "gone": 0, "errors": []string{}}
	var errs []string
	c, err := a.planeCfg(ctx, uid)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	planeOK := false
	if c.ok() {
		r, err := a.planeDo(ctx, uid, c, "GET", c.wsPath()+"/projects/", nil, planeOpts{})
		if err != nil {
			errs = append(errs, "Plane: "+err.Error())
		} else {
			list := listOf(r)
			out["plane"] = len(list)
			planeOK = true
			for _, p := range list {
				id := asStr(p["id"])
				if id == "" {
					continue
				}
				seen[id] = true
				if err := a.upsertSource(ctx, uid, "plane", id, strings.ToUpper(asStr(p["identifier"])), asStr(p["name"]), asStr(p["archived_at"]) != ""); err != nil {
					errs = append(errs, fmt.Sprintf("Plane-Projekt %s: %v", asStr(p["identifier"]), err))
				}
			}
		}
	}
	gone := 0
	if planeOK { // verschwundene Quellen markieren (nur Systeme, die gerade erfolgreich gelesen wurden)
		rows, err := a.DB.Query(ctx, `SELECT id, external_id FROM project_sources WHERE user_id=$1 AND system='plane' AND gone=0`, uid)
		if err != nil {
			return nil, err
		}
		var drop []int64
		for rows.Next() {
			var id int64
			var ext string
			if err := rows.Scan(&id, &ext); err != nil {
				rows.Close()
				return nil, err
			}
			if !seen[ext] {
				drop = append(drop, id)
			}
		}
		rows.Close()
		if len(drop) > 0 {
			if _, err := a.DB.Exec(ctx, `UPDATE project_sources SET gone=1 WHERE user_id=$1 AND id = ANY($2)`, uid, drop); err != nil {
				return nil, err
			}
			gone = len(drop)
		}
	}
	var n int
	if err := a.DB.QueryRow(ctx, `SELECT COUNT(*) FROM project_sources WHERE user_id=$1 AND status='new' AND gone=0`, uid).Scan(&n); err != nil {
		return nil, err
	}
	out["new"], out["gone"], out["errors"] = n, gone, errs
	now := time.Now().UTC().Format(isoMillis)
	_, _ = a.DB.Exec(ctx, `INSERT INTO user_settings(user_id,key,value) VALUES($1,'sources_last_scan',$2) ON CONFLICT (user_id,key) DO UPDATE SET value=EXCLUDED.value`, uid, now)
	out["at"], out["trigger"] = now, trigger
	lvl := "info"
	if len(errs) > 0 {
		lvl = "warn"
	}
	pl := "–"
	if v, ok := out["plane"].(int); ok {
		pl = fmt.Sprint(v)
	}
	a.sysLog(ctx, uid, lvl, "projekte", fmt.Sprintf("Projekte eingelesen (%s): Plane %s, neu zu prüfen %d%s", trigger, pl, n, strings.Join(append([]string{""}, errs...), " – ")))
	return out, nil
}

func (a *App) sourcesScanRoute(w http.ResponseWriter, r *http.Request, u User) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Minute)
	defer cancel()
	out, err := a.scanSources(ctx, u.ID, "von Hand")
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

/* ── Liste ──────────────────────────────────────────────────────── */

func (a *App) commonWords(projects []map[string]any) map[string]bool {
	cnt := map[string]int{}
	for _, p := range projects {
		if asStr(p["status"]) == "archived" { // Firmenwörter zählen nur über aktive Projekte
			continue
		}
		for w := range setOf(toks(asStr(p["name"]))) {
			cnt[w]++
		}
	}
	out := map[string]bool{}
	for w, n := range cnt {
		if n >= 3 {
			out[w] = true
		}
	}
	return out
}

// suggestFor: Vorschlag für eine neue Quelle (gleiches Kürzel in der anderen Quelle, ähnlicher Name/Kürzel)
func (a *App) suggestFor(ctx context.Context, uid int64, src map[string]any, projects []map[string]any, common map[string]bool) []map[string]any {
	out := []map[string]any{}
	key := strings.ToUpper(asStr(src["key"]))
	sys := asStr(src["system"])
	if key != "" {
		var pid int64
		var name string
		if err := a.DB.QueryRow(ctx, `SELECT s.project_id, p.name FROM project_sources s JOIN projects p ON p.id=s.project_id
			WHERE s.user_id=$1 AND s.system<>$2 AND UPPER(s.key)=$3 AND s.status='linked' LIMIT 1`, uid, sys, key).Scan(&pid, &name); err == nil {
			other := map[bool]string{true: "Jira", false: "Plane"}[sys == "plane"]
			out = append(out, map[string]any{"project_id": pid, "name": name, "score": 95, "reason": fmt.Sprintf("gleiches Kürzel %s in %s", asStr(src["key"]), other)})
		}
	}
	for _, p := range projects {
		if asStr(p["status"]) == "archived" {
			continue
		}
		pid, _ := p["id"].(int64)
		dup := false
		for _, o := range out {
			if o["project_id"] == pid {
				dup = true
			}
		}
		if dup {
			continue
		}
		keyHit := key != "" && (strings.ToUpper(asStr(p["jira_key"])) == key || strings.ToUpper(asStr(p["plane_identifier"])) == key)
		sim := math.Max(textSim(asStr(src["name"]), asStr(p["name"])), nameSim(asStr(src["name"]), asStr(p["name"]), common)*0.95)
		base := 0.0
		if keyHit {
			base = 85
		}
		score := int(math.Round(math.Max(base, sim*90)))
		if score >= 45 {
			reason := fmt.Sprintf("Name ähnlich (%d %%)", int(math.Round(sim*100)))
			if keyHit {
				reason = "Kürzel " + asStr(src["key"])
			}
			out = append(out, map[string]any{"project_id": pid, "name": p["name"], "score": score, "reason": reason})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i]["score"].(int) > out[j]["score"].(int) })
	if len(out) > 3 {
		out = out[:3]
	}
	return out
}

func (a *App) projectRow(ctx context.Context, uid int64, p map[string]any, def string) (map[string]any, error) {
	pid, _ := p["id"].(int64)
	srcs, err := a.maps(ctx, `SELECT id, system, key, name, gone FROM project_sources WHERE user_id=$1 AND project_id=$2 AND status='linked' ORDER BY system`, uid, pid)
	if err != nil {
		return nil, err
	}
	var n, secs int64
	var last *string
	if err := a.DB.QueryRow(ctx, `SELECT COUNT(*), COALESCE(SUM(duration_seconds),0)::bigint, MAX(date) FROM time_entries WHERE user_id=$1 AND project_id=$2`, uid, pid).Scan(&n, &secs, &last); err != nil {
		return nil, err
	}
	rule := asStr(p["output_rule"])
	if rule == "" || rule == "default" {
		rule = def
	}
	inP := a.inPlane(ctx, uid, pid)
	var successorName any
	if sid, ok := p["successor_id"].(int64); ok {
		var nm string
		if a.DB.QueryRow(ctx, `SELECT name FROM projects WHERE id=$1 AND user_id=$2`, sid, uid).Scan(&nm) == nil {
			successorName = nm
		}
	}
	allGone := len(srcs) > 0
	for _, s := range srcs {
		if asFloat(s["gone"]) == 0 {
			allGone = false
		}
	}
	// Projektname soll später genau wie in Plane heißen (sonst wie in Jira)
	var nameSource any
	var pick map[string]any
	for _, sys := range []string{"plane", "jira"} {
		for _, s := range srcs {
			if asStr(s["system"]) == sys && asFloat(s["gone"]) == 0 {
				pick = s
				break
			}
		}
		if pick != nil {
			break
		}
	}
	if pick != nil {
		if nm := strings.TrimSpace(asStr(pick["name"])); nm != "" && nm != strings.TrimSpace(asStr(p["name"])) {
			nameSource = map[string]any{"system": pick["system"], "name": nm}
		}
	}
	outRule := asStr(p["output_rule"])
	if outRule == "" {
		outRule = "default"
	}
	return map[string]any{
		"id": pid, "name": p["name"], "status": p["status"], "closed": asStr(p["status"]) == "archived", "muted": asFloat(p["muted"]) != 0,
		"merged_into": p["merged_into"], "output_rule": outRule, "successor_id": p["successor_id"],
		"effective": map[string]any{"plane": ruleAllows(rule, "bridge", "plane") && inP, "tempo": ruleAllows(rule, "bridge", "tempo")},
		"in_plane":  inP, "successor_name": successorName, "sources": srcs, "entries": n, "seconds": secs, "last": last,
		"all_gone": allGone, "gone_ack": asFloat(p["gone_ack"]) != 0, "name_source": nameSource,
	}, nil
}

func (a *App) sourcesList(w http.ResponseWriter, r *http.Request, u User) {
	ctx := r.Context()
	st, err := a.userSettings(ctx, u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	def := a.defaultRule(st)
	projects, err := a.maps(ctx, `SELECT * FROM projects WHERE user_id=$1 ORDER BY CASE WHEN status='archived' THEN 1 ELSE 0 END, name`, u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	pending, err := a.maps(ctx, `SELECT * FROM project_sources WHERE user_id=$1 AND status='new' AND gone=0 ORDER BY system, name`, u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	common := a.commonWords(projects)
	for _, s := range pending {
		s["suggestions"] = a.suggestFor(ctx, u.ID, s, projects, common)
	}
	rows := make([]map[string]any, 0, len(projects))
	for _, p := range projects {
		row, err := a.projectRow(ctx, u.ID, p, def)
		if err != nil {
			a.fail(w, err)
			return
		}
		rows = append(rows, row)
	}
	ignored, err := a.maps(ctx, `SELECT * FROM project_sources WHERE user_id=$1 AND status='ignored' ORDER BY system, name`, u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	var lastScan any
	if v := st["sources_last_scan"]; v != "" {
		lastScan = v
	}
	writeJSON(w, http.StatusOK, map[string]any{"pending": pending, "projects": rows, "ignored": ignored, "duplicates": []any{},
		"last_scan": lastScan, "last": nil, "default_rule": def})
}

/* ── Projekt-Einstellungen: Regel, Nachfolger, Abschluss ────────── */

func (a *App) projectSetup(w http.ResponseWriter, r *http.Request, u User) {
	id, ok := pathID(r)
	var in body
	if !ok {
		notFound(w)
		return
	}
	if !readJSON(w, r, &in) {
		return
	}
	ctx := r.Context()
	if !a.owned(ctx, "projects", id, u.ID) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "Projekt nicht gefunden"})
		return
	}
	err := a.inTx(ctx, func(tx pgx.Tx) error {
		if v, has := in["output_rule"]; has {
			if !contains(outputRules, asStr(v)) {
				return errUser("Unbekannte Regel")
			}
			if _, err := tx.Exec(ctx, `UPDATE projects SET output_rule=$3 WHERE id=$1 AND user_id=$2`, id, u.ID, asStr(v)); err != nil {
				return err
			}
		}
		if v, has := in["successor_id"]; has {
			if invalidID(v) {
				return errUser("successor_id ungültig")
			}
			var sid any = asID(v)
			if sid != nil {
				s := sid.(int64)
				if s == id {
					return errUser("Ein Projekt kann nicht sein eigener Nachfolger sein")
				}
				var okp bool
				if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM projects WHERE id=$1 AND user_id=$2)`, s, u.ID).Scan(&okp); err != nil {
					return err
				}
				if !okp {
					return errUser("Nachfolger nicht gefunden")
				}
				cur := s
				for n := 0; cur != 0 && n < 20; n++ {
					if cur == id {
						return errUser("Nachfolger würde einen Kreis bilden")
					}
					var next *int64
					if err := tx.QueryRow(ctx, `SELECT successor_id FROM projects WHERE id=$1 AND user_id=$2`, cur, u.ID).Scan(&next); err != nil || next == nil {
						break
					}
					cur = *next
				}
			}
			if _, err := tx.Exec(ctx, `UPDATE projects SET successor_id=$3 WHERE id=$1 AND user_id=$2`, id, u.ID, sid); err != nil {
				return err
			}
		}
		flag := func(col string, v any) error {
			n := 0
			if truthy(v) {
				n = 1
			}
			_, err := tx.Exec(ctx, "UPDATE projects SET "+col+"=$3 WHERE id=$1 AND user_id=$2", id, u.ID, n)
			return err
		}
		if v, has := in["gone_ack"]; has {
			if err := flag("gone_ack", v); err != nil {
				return err
			}
		}
		if v, has := in["muted"]; has {
			if err := flag("muted", v); err != nil {
				return err
			}
		}
		if v, has := in["closed"]; has {
			if b, _ := v.(bool); b {
				if _, err := tx.Exec(ctx, `UPDATE projects SET status='archived', closed_at=$3 WHERE id=$1 AND user_id=$2`, id, u.ID, time.Now().UTC().Format(isoMillis)); err != nil {
					return err
				}
			} else if _, err := tx.Exec(ctx, `UPDATE projects SET status='active', closed_at=NULL WHERE id=$1 AND user_id=$2`, id, u.ID); err != nil {
				return err
			}
		}
		return nil
	})
	if ue, ok := err.(errUser); ok {
		badRequest(w, string(ue))
		return
	}
	if err != nil {
		a.fail(w, err)
		return
	}
	st, err := a.userSettings(ctx, u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	p, err := a.one(ctx, `SELECT * FROM projects WHERE id=$1 AND user_id=$2`, id, u.ID)
	if err != nil || p == nil {
		a.fail(w, err)
		return
	}
	row, err := a.projectRow(ctx, u.ID, p, a.defaultRule(st))
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, row)
}

type errUser string

func (e errUser) Error() string { return string(e) }

/* ── Nachfolger: Buchung auf geschlossenes Projekt → Nachfolger ─── */

// redirect: Aufgabe nur behalten, wenn sie zum Nachfolger gehört. Rückgabe: Projekt, Aufgabe, Name des ursprünglichen Projekts (oder "")
func (a *App) redirect(ctx context.Context, uid int64, project, task any) (any, any, string) {
	pid, _ := project.(int64)
	if pid == 0 {
		return project, task, ""
	}
	orig := pid
	tid, _ := task.(int64)
	from := ""
	for n := 0; pid != 0 && n < 10; n++ {
		var name, status string
		var succ *int64
		if err := a.DB.QueryRow(ctx, `SELECT name, COALESCE(status,''), successor_id FROM projects WHERE id=$1 AND user_id=$2`, pid, uid).Scan(&name, &status, &succ); err != nil {
			break
		}
		if status != "archived" || succ == nil {
			break
		}
		var ok bool
		if a.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM projects WHERE id=$1 AND user_id=$2)`, *succ, uid).Scan(&ok) != nil || !ok {
			break // Nachfolger fehlt oder gehört nicht dieser Person: nicht dorthin umleiten
		}
		if from == "" {
			from = name
		}
		pid = *succ
		if tid != 0 {
			var tp int64
			if a.DB.QueryRow(ctx, `SELECT project_id FROM tasks WHERE id=$1 AND user_id=$2`, tid, uid).Scan(&tp) != nil || tp != pid {
				tid = 0
			}
		}
	}
	var outTask any
	if tid != 0 {
		outTask = tid
	}
	if pid == orig {
		return project, outTask, ""
	}
	return pid, outTask, from
}

/* ── Plane-Projekte verknüpfen (alte Route der Oberfläche) ──────── */

// planeProjectsLink: {links:[{plane_project_id, identifier, local_id|null, create:true, name}]} – läuft über dieselbe Verknüpfung wie „Quellen“
func (a *App) planeProjectsLink(w http.ResponseWriter, r *http.Request, u User) {
	var in body
	if !readJSON(w, r, &in) {
		return
	}
	links, _ := in["links"].([]any)
	ctx := r.Context()
	linked, created := 0, 0
	err := a.inTx(ctx, func(tx pgx.Tx) error {
		for _, x := range links {
			l := jm(x)
			ext := asStr(l["plane_project_id"])
			if ext == "" {
				continue
			}
			s, err := a.sourceOf(ctx, tx, u.ID, "plane", ext, asStr(l["identifier"]), asStr(l["name"]))
			if err != nil {
				return err
			}
			var pid int64
			if v := asID(l["local_id"]); v != nil {
				pid = v.(int64)
				var ok bool
				if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM projects WHERE id=$1 AND user_id=$2)`, pid, u.ID).Scan(&ok); err != nil {
					return err
				}
				if !ok {
					return errUser("Projekt nicht gefunden")
				}
			} else if c, _ := l["create"].(bool); c && strings.TrimSpace(asStr(l["name"])) != "" {
				if err := tx.QueryRow(ctx, `INSERT INTO projects(user_id,name,status) VALUES($1,$2,'active') RETURNING id`, u.ID, strings.TrimSpace(asStr(l["name"]))).Scan(&pid); err != nil {
					return err
				}
				created++
			}
			if pid != 0 {
				if err := a.linkSource(ctx, tx, u.ID, s, pid); err != nil {
					return err
				}
				linked++
			} else if err := a.unlinkSource(ctx, tx, u.ID, s); err != nil {
				return err
			}
		}
		return nil
	})
	if ue, ok := err.(errUser); ok {
		badRequest(w, string(ue))
		return
	}
	if err != nil {
		a.fail(w, err)
		return
	}
	a.sysLog(ctx, u.ID, "info", "plane", fmt.Sprintf("Projekte verknüpft: %d (neu angelegt: %d)", linked, created))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "linked": linked, "created": created})
}

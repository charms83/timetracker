package app

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

/* Projekt-Werkzeuge der Projekte-Seite und des Assistenten (wie backend/sources.js): Zusammenführen, Namen angleichen,
   doppelte Projekte, „Lokale Zeiten“. Immer nur Daten der angemeldeten Person (user_id in jeder Abfrage).
   Verschobene Buchungen laufen durch die Monatssperre der Datenbank (ZP001 → 409 month_locked über a.fail). */

var ticketKeyRE = regexp.MustCompile(`^[A-Z][A-Z0-9_]*-\d+$`)

const ticketTaskLock = int64(7270060000000000) // gleiche Sperre wie ensureTaskForIssue: Aufgaben je Person nicht doppelt anlegen

func (a *App) routesProjectTools(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/projects/{id}/merge", a.auth(a.projectMerge))
	mux.HandleFunc("POST /api/projects/adopt-names", a.auth(a.projectsAdoptNames))
	mux.HandleFunc("GET /api/sources/duplicates", a.auth(a.sourcesDuplicates))
	mux.HandleFunc("POST /api/sources/duplicates/ignore", a.auth(a.sourcesDuplicatesIgnore))
	mux.HandleFunc("GET /api/sources/local-times", a.auth(a.sourcesLocalTimes))
	mux.HandleFunc("POST /api/sources/local-times/assign", a.auth(a.sourcesLocalTimesAssign))
}

// ruleOfProject: Regel des Projekts, „default“/leer → Standardregel der Person
func (a *App) ruleOfProject(st map[string]string, rule string) string {
	if rule == "" || rule == "default" {
		return a.defaultRule(st)
	}
	return rule
}

/* ── Zusammenführen ─────────────────────────────────────────────── */

func (a *App) projectMerge(w http.ResponseWriter, r *http.Request, u User) {
	id, ok := pathID(r)
	var in body
	if !ok {
		notFound(w)
		return
	}
	if !readJSON(w, r, &in) {
		return
	}
	into, _ := asID(in["into"]).(int64)
	ctx := r.Context()
	if into == 0 || !a.owned(ctx, "projects", id, u.ID) || !a.owned(ctx, "projects", into, u.ID) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "Projekt nicht gefunden"})
		return
	}
	if id == into {
		badRequest(w, "Ein Projekt kann nicht in sich selbst zusammengeführt werden")
		return
	}
	keepFrom := map[string]bool{}
	if l, ok := in["keep_from"].([]any); ok {
		for _, x := range l {
			keepFrom[asStr(x)] = true
		}
	}
	moved := map[string]int64{}
	var fromName, intoName string
	err := a.inTx(ctx, func(tx pgx.Tx) error {
		// beide Zeilen in id-Reihenfolge sperren (gleichzeitiges A→B und B→A: kein Deadlock)
		if _, err := tx.Exec(ctx, `SELECT 1 FROM projects WHERE id = ANY($1) AND user_id=$2 ORDER BY id FOR UPDATE`, []int64{id, into}, u.ID); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT name FROM projects WHERE id=$1 AND user_id=$2`, id, u.ID).Scan(&fromName); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT name FROM projects WHERE id=$1 AND user_id=$2`, into, u.ID).Scan(&intoName); err != nil {
			return err
		}
		for _, tbl := range []string{"time_entries", "tasks", "plan_tasks", "meeting_rules", "timer_state"} {
			tag, err := tx.Exec(ctx, "UPDATE "+tbl+" SET project_id=$3 WHERE user_id=$1 AND project_id=$2", u.ID, id, into)
			if err != nil {
				return err
			}
			moved[tbl] = tag.RowsAffected()
		}
		// Quellen: je System höchstens eine am Ziel; Konflikt: keep_from = Quelle von A behalten
		srcs, err := mapsQ(ctx, tx, `SELECT id FROM project_sources WHERE user_id=$1 AND project_id=$2 AND status='linked' ORDER BY id`, u.ID, id)
		if err != nil {
			return err
		}
		for _, sm := range srcs {
			s, err := a.loadSource(ctx, tx, u.ID, sm["id"].(int64))
			if err != nil {
				return err
			}
			var hasID int64
			err = tx.QueryRow(ctx, `SELECT id FROM project_sources WHERE user_id=$1 AND project_id=$2 AND system=$3 AND status='linked' LIMIT 1`, u.ID, into, s.System).Scan(&hasID)
			switch {
			case err == pgx.ErrNoRows:
				if err := a.linkSource(ctx, tx, u.ID, s, into); err != nil {
					return err
				}
			case err != nil:
				return err
			case keepFrom[s.System]:
				if err := a.linkSource(ctx, tx, u.ID, s, into); err != nil { // löst die Quelle des Ziels (wird „neu“)
					return err
				}
			default:
				if err := a.unlinkSource(ctx, tx, u.ID, s); err != nil {
					return err
				}
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE projects b SET tempo_issue=COALESCE(b.tempo_issue, x.tempo_issue), tempo_account=COALESCE(b.tempo_account, x.tempo_account)
			FROM projects x WHERE b.id=$2 AND b.user_id=$1 AND x.id=$3 AND x.user_id=$1`, u.ID, into, id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE projects SET successor_id=$3 WHERE user_id=$1 AND successor_id=$2`, u.ID, id, into); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE projects SET status='archived', closed_at=$4, successor_id=$3, merged_into=$3, muted=1, plane_project_id=NULL, plane_identifier=NULL, jira_project=NULL
			WHERE id=$2 AND user_id=$1`, u.ID, id, into, time.Now().UTC().Format(isoMillis))
		return err
	})
	if err != nil {
		a.fail(w, err)
		return
	}
	b, _ := json.Marshal(moved)
	a.sysLog(ctx, u.ID, "info", "projekte", fmt.Sprintf("Projekt „%s“ in „%s“ zusammengeführt: %s", fromName, intoName, b))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "moved": moved, "into": into})
}

/* ── Namen angleichen ───────────────────────────────────────────── */

// nameSourceSQL: Name der verknüpften, nicht verschwundenen Quelle (Plane vor Jira) – wie name_source in projectRow
const nameSourceSQL = `(SELECT s.name FROM project_sources s WHERE s.user_id=p.user_id AND s.project_id=p.id AND s.status='linked' AND COALESCE(s.gone,0)=0
	ORDER BY CASE s.system WHEN 'plane' THEN 0 ELSE 1 END, s.system LIMIT 1)`

func (a *App) projectsAdoptNames(w http.ResponseWriter, r *http.Request, u User) {
	var in body
	if r.ContentLength != 0 && !readJSON(w, r, &in) {
		return
	}
	var ids []int64
	l, filter := in["ids"].([]any)
	for _, x := range l {
		if n, ok := asID(x).(int64); ok {
			ids = append(ids, n)
		}
	}
	ctx := r.Context()
	done := []map[string]any{}
	err := a.inTx(ctx, func(tx pgx.Tx) error {
		rows, err := mapsQ(ctx, tx, `SELECT p.id, p.name, `+nameSourceSQL+` AS src FROM projects p WHERE p.user_id=$1 AND COALESCE(p.status,'')<>'archived' ORDER BY p.id`, u.ID)
		if err != nil {
			return err
		}
		for _, p := range rows {
			pid := p["id"].(int64)
			if filter && !containsID(ids, pid) {
				continue
			}
			to := strings.TrimSpace(asStr(p["src"]))
			if to == "" || to == strings.TrimSpace(asStr(p["name"])) {
				continue
			}
			if _, err := tx.Exec(ctx, `UPDATE projects SET name=$3 WHERE id=$1 AND user_id=$2`, pid, u.ID, to); err != nil {
				return err
			}
			done = append(done, map[string]any{"id": pid, "from": p["name"], "to": to})
		}
		return nil
	})
	if err != nil {
		a.fail(w, err)
		return
	}
	if len(done) > 0 {
		parts := make([]string, 0, len(done))
		for _, d := range done {
			parts = append(parts, fmt.Sprintf("„%v“ → „%v“", d["from"], d["to"]))
		}
		a.sysLog(ctx, u.ID, "info", "projekte", "Projektnamen angeglichen: "+strings.Join(parts, ", "))
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "renamed": done})
}

/* ── Doppelte Projekte ──────────────────────────────────────────── */

func (a *App) dupIgnore(ctx context.Context, uid int64) []string {
	var raw *string
	if err := a.DB.QueryRow(ctx, `SELECT value FROM user_settings WHERE user_id=$1 AND key='dup_ignore'`, uid).Scan(&raw); err != nil || raw == nil {
		return nil
	}
	var ign []string
	_ = json.Unmarshal([]byte(*raw), &ign)
	return ign
}

type dupProj struct {
	id         int64
	name       string
	n, secs    int64
	srcs       []map[string]any
	keys       map[string]bool
	hasPlane   bool
	srcSystems map[string]bool
}

// duplicates = duplicates() in sources.js: vermutlich doppelte Projekte (höchstens 10)
func (a *App) duplicates(ctx context.Context, uid int64) ([]map[string]any, error) {
	ign := map[string]bool{}
	for _, k := range a.dupIgnore(ctx, uid) {
		ign[k] = true
	}
	all, err := a.maps(ctx, `SELECT p.id, p.name, p.status, p.jira_key, p.plane_identifier, p.jira_project,
		(SELECT COUNT(*) FROM time_entries e WHERE e.user_id=p.user_id AND e.project_id=p.id) AS n,
		(SELECT COALESCE(SUM(duration_seconds),0)::bigint FROM time_entries e WHERE e.user_id=p.user_id AND e.project_id=p.id) AS secs,
		COALESCE((SELECT json_agg(json_build_object('system', s.system, 'key', s.key)) FROM project_sources s WHERE s.user_id=p.user_id AND s.project_id=p.id AND s.status='linked'), '[]'::json)::text AS srcs
		FROM projects p WHERE p.user_id=$1 AND COALESCE(p.status,'')<>'archived' AND COALESCE(p.muted,0)=0 AND p.merged_into IS NULL ORDER BY p.id LIMIT 500`, uid)
	if err != nil {
		return nil, err
	}
	common := a.commonWords(all)
	ps := make([]dupProj, 0, len(all))
	for _, p := range all {
		d := dupProj{id: p["id"].(int64), name: asStr(p["name"]), n: int64(asFloat(p["n"])), secs: int64(asFloat(p["secs"])), keys: map[string]bool{}, srcSystems: map[string]bool{}}
		_ = json.Unmarshal([]byte(asStr(p["srcs"])), &d.srcs)
		if d.srcs == nil {
			d.srcs = []map[string]any{}
		}
		for _, k := range []any{p["jira_key"], p["plane_identifier"], p["jira_project"]} {
			if s := strings.ToUpper(asStr(k)); s != "" {
				d.keys[s] = true
			}
		}
		for _, s := range d.srcs {
			if k := strings.ToUpper(asStr(s["key"])); k != "" {
				d.keys[k] = true
			}
			d.srcSystems[asStr(s["system"])] = true
			if asStr(s["system"]) == "plane" {
				d.hasPlane = true
			}
		}
		ps = append(ps, d)
	}
	out := []map[string]any{}
	for i := 0; i < len(ps); i++ {
		for j := i + 1; j < len(ps); j++ {
			x, y := ps[i], ps[j]
			if ign[pairKey(x.id, y.id)] {
				continue
			}
			sameName := strings.ToLower(strings.TrimSpace(x.name)) == strings.ToLower(strings.TrimSpace(y.name))
			conflict := []string{}
			for _, s := range x.srcs {
				if y.srcSystems[asStr(s["system"])] {
					conflict = append(conflict, asStr(s["system"]))
				}
			}
			if len(conflict) > 0 && !sameName {
				continue // beide haben eine Quelle desselben Systems und heißen verschieden → verschiedene Projekte
			}
			keyHit := false
			for k := range x.keys {
				if y.keys[k] {
					keyHit = true
				}
			}
			score := 100
			if !sameName {
				base := 0.0
				if keyHit {
					base = 90
				}
				score = int(math.Round(math.Max(base, nameSim(x.name, y.name, common)*85)))
			}
			if score < 70 {
				continue
			}
			into, from := x, y
			if y.secs > x.secs || (y.secs == x.secs && y.hasPlane) {
				into, from = y, x
			}
			reason := "ähnlicher Name"
			if sameName {
				reason = "gleicher Name"
			} else if keyHit {
				reason = "gleiches Kürzel"
			}
			d := func(p dupProj) map[string]any {
				return map[string]any{"id": p.id, "name": p.name, "entries": p.n, "seconds": p.secs, "sources": p.srcs}
			}
			out = append(out, map[string]any{"score": score, "reason": reason, "from": d(from), "into": d(into), "conflict": conflict})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i]["score"].(int) > out[j]["score"].(int) })
	if len(out) > 10 {
		out = out[:10]
	}
	return out, nil
}

func (a *App) sourcesDuplicates(w http.ResponseWriter, r *http.Request, u User) {
	out, err := a.duplicates(r.Context(), u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *App) sourcesDuplicatesIgnore(w http.ResponseWriter, r *http.Request, u User) {
	var in body
	if !readJSON(w, r, &in) {
		return
	}
	x, ok1 := asID(in["a"]).(int64)
	y, ok2 := asID(in["b"]).(int64)
	if !ok1 || !ok2 {
		badRequest(w, "a + b erforderlich")
		return
	}
	ctx := r.Context()
	ign := a.dupIgnore(ctx, u.ID)
	if k := pairKey(x, y); !contains(ign, k) {
		ign = append(ign, k)
	}
	if len(ign) > 1000 {
		ign = ign[len(ign)-1000:]
	}
	b, _ := json.Marshal(ign)
	if err := a.setUserSetting(ctx, a.DB, u.ID, "dup_ignore", string(b)); err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

/* ── Lokale Zeiten: was kommt wo an? ────────────────────────────── */

type localTimeRow struct {
	ProjectID int64  `json:"project_id"`
	Name      string `json:"name"`
	InPlane   bool   `json:"in_plane"`
	Plane     int64  `json:"plane"`
	Jira      int64  `json:"jira_ticket"`
	NoTicket  int64  `json:"no_ticket"`
	NotPlane  int64  `json:"not_plane"`
	NNoTicket int64  `json:"n_no_ticket"`
}

// classifyLocal: Zuordnung wie localTimes() in sources.js
func classifyLocal(x *localTimeRow, pref, key string, workItem bool, secs, n int64) {
	switch {
	case !x.InPlane:
		x.NotPlane += secs
	case workItem || (pref != "" && strings.HasPrefix(key, pref+"-")):
		x.Plane += secs
	case key != "":
		x.Jira += secs // alte Buchung auf Jira-Ticket – bleibt in Tempo, wie sie ist
	default:
		x.NoTicket += secs
		x.NNoTicket += n
	}
}

func (a *App) sourcesLocalTimes(w http.ResponseWriter, r *http.Request, u User) {
	ctx := r.Context()
	st, err := a.userSettings(ctx, u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	from := r.URL.Query().Get("from")
	if _, ok := parseDay(from); !ok {
		from = jsOr(st["tempo_push_since"], "2000-01-01")
	}
	ps, err := a.maps(ctx, `SELECT p.id, p.name, COALESCE(p.output_rule,'') AS rule, UPPER(COALESCE(p.plane_identifier,'')) AS pref,
		(p.plane_project_id IS NOT NULL AND NOT EXISTS (SELECT 1 FROM project_sources s WHERE s.user_id=p.user_id AND s.project_id=p.id AND s.system='plane' AND s.status='linked' AND s.gone=1)) AS in_plane
		FROM projects p WHERE p.user_id=$1 AND COALESCE(p.status,'')<>'archived' AND COALESCE(p.muted,0)=0 AND p.merged_into IS NULL ORDER BY p.name`, u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	byID := map[int64]*localTimeRow{}
	prefs := map[int64]string{}
	order := []*localTimeRow{}
	for _, p := range ps {
		id := p["id"].(int64)
		x := &localTimeRow{ProjectID: id, Name: asStr(p["name"]), InPlane: p["in_plane"] == true && ruleAllows(a.ruleOfProject(st, asStr(p["rule"])), "bridge", "plane")}
		byID[id], prefs[id] = x, asStr(p["pref"])
		order = append(order, x)
	}
	agg, err := a.DB.Query(ctx, `SELECT e.project_id, UPPER(TRIM(COALESCE(t.jira_ticket,''))), (t.plane_work_item_id IS NOT NULL), SUM(e.duration_seconds)::bigint, COUNT(*)
		FROM time_entries e LEFT JOIN tasks t ON t.id=e.task_id AND t.user_id=e.user_id
		WHERE e.user_id=$1 AND e.project_id IS NOT NULL AND e.end_time IS NOT NULL AND COALESCE(e.duration_seconds,0)>0 AND e.date>=$2 AND COALESCE(e.source,'manual') NOT IN ('tempo','tempo_csv')
		GROUP BY 1, 2, 3`, u.ID, from)
	if err != nil {
		a.fail(w, err)
		return
	}
	seen := map[int64]bool{}
	for agg.Next() {
		var pid, secs, n int64
		var key string
		var wi bool
		if err := agg.Scan(&pid, &key, &wi, &secs, &n); err != nil {
			agg.Close()
			a.fail(w, err)
			return
		}
		if x := byID[pid]; x != nil {
			classifyLocal(x, prefs[pid], key, wi, secs, n)
			seen[pid] = true
		}
	}
	agg.Close()
	if err := agg.Err(); err != nil {
		a.fail(w, err)
		return
	}
	out := []*localTimeRow{}
	for _, x := range order {
		if seen[x.ProjectID] {
			out = append(out, x)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"from": from, "projects": out})
}

// sourcesLocalTimesAssign: Buchungen ganz ohne Ticket eines Plane-Projekts auf ein Plane-Ticket legen (danach übernimmt der Plane-Abgleich)
func (a *App) sourcesLocalTimesAssign(w http.ResponseWriter, r *http.Request, u User) {
	var in body
	if !readJSON(w, r, &in) {
		return
	}
	pid, _ := asID(in["project_id"]).(int64)
	key := strings.ToUpper(strings.TrimSpace(asStr(in["ticket"])))
	from := asStr(in["from"])
	if _, ok := parseDay(from); !ok {
		from = "2000-01-01"
	}
	ctx := r.Context()
	st, err := a.userSettings(ctx, u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	var rule, pref string
	if pid == 0 || a.DB.QueryRow(ctx, `SELECT COALESCE(output_rule,''), COALESCE(plane_identifier,'') FROM projects WHERE id=$1 AND user_id=$2`, pid, u.ID).Scan(&rule, &pref) != nil ||
		!ruleAllows(a.ruleOfProject(st, rule), "bridge", "plane") || !a.inPlane(ctx, u.ID, pid) {
		badRequest(w, "Projekt ist nicht in Plane")
		return
	}
	pref = strings.ToUpper(pref)
	if pref == "" || !strings.HasPrefix(key, pref+"-") || !ticketKeyRE.MatchString(key) {
		badRequest(w, fmt.Sprintf("Ticket muss zu %s gehören (z. B. %s-12)", jsOr(pref, "diesem Plane-Projekt"), jsOr(pref, "PRJB")))
		return
	}
	title := truncRunes(jsOr(strings.TrimSpace(asStr(in["title"])), key), 200)
	var tid, n, skipped int64
	err = a.inTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, ticketTaskLock+u.ID); err != nil {
			return err
		}
		err := tx.QueryRow(ctx, `SELECT id FROM tasks WHERE user_id=$1 AND project_id=$2 AND UPPER(TRIM(jira_ticket))=$3 ORDER BY id LIMIT 1`, u.ID, pid, key).Scan(&tid)
		if err == pgx.ErrNoRows {
			err = tx.QueryRow(ctx, `INSERT INTO tasks(user_id,project_id,title,jira_ticket,status) VALUES($1,$2,$3,$4,'open') RETURNING id`, u.ID, pid, title, key).Scan(&tid)
		}
		if err != nil {
			return err
		}
		cand := `e.user_id=$1 AND e.project_id=$2 AND e.end_time IS NOT NULL AND e.date>=$3 AND COALESCE(e.source,'manual') NOT IN ('tempo','tempo_csv')
			AND (e.task_id IS NULL OR EXISTS (SELECT 1 FROM tasks t WHERE t.id=e.task_id AND t.user_id=$1 AND TRIM(COALESCE(t.jira_ticket,''))='' AND t.plane_work_item_id IS NULL))`
		// gesperrte Monate (eingereicht/freigegeben/geschlossen) nicht anfassen, sondern als übersprungen melden – die Sperre selbst bleibt in der Datenbank
		locked := `EXISTS (SELECT 1 FROM month_approvals m WHERE m.user_id=$1 AND m.month=substr(e.date,1,7) AND m.status IN ('submitted','approved','closed'))`
		if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM time_entries e WHERE `+cand+` AND `+locked, u.ID, pid, from).Scan(&skipped); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `UPDATE time_entries e SET task_id=$4 WHERE `+cand+` AND NOT `+locked, u.ID, pid, from, tid)
		n = tag.RowsAffected()
		return err
	})
	if err != nil {
		a.fail(w, err) // Monatssperre → 409 month_locked
		return
	}
	a.sysLog(ctx, u.ID, "info", "projekte", fmt.Sprintf("%d Buchungen von Projekt #%d ohne Plane-Ticket auf %s gelegt", n, pid, key))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "n": n, "task_id": tid, "skipped_locked": skipped})
}

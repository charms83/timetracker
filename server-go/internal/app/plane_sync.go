package app

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

/* Übertragung nach Plane (wie backend/plane.js): Je Ticket wird EINE Summen-Buchung („Worklog“) in Plane gepflegt –
   Gesamtdauer plus Liste der Einträge in der Beschreibung. Direkt in Plane gebuchte Einträge (source plane) und das
   Zurückholen (pull) folgen mit dem Quellen-Modul. Gebremst wird in plane_client.go. */

const sumTag = "[zp-sum]"

var keyRE = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9_]*)-(\d+)$`)

type planeSync struct {
	WorkItemID, ProjectID, WorklogID string
	SyncedMinutes                    int
	SyncedAt, Err, Title, DescHash   string
}

type tsum struct {
	Ticket, First, Last, Title, WI, PP, Projects, IDs string
	Secs                                              int64
	N, Minutes, Legacy                                int
	Sync                                              *planeSync
	Desc, DHash                                       string
}

type planeState struct {
	mu      sync.Mutex
	pushing bool
	again   bool
	timer   *time.Timer
}

func (a *App) planeStateFor(uid int64) *planeState {
	v, _ := a.planeRun.LoadOrStore(uid, &planeState{})
	return v.(*planeState)
}

func (a *App) routesPlane(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/integrations/plane/status", a.auth(a.planeStatus))
	mux.HandleFunc("GET /api/integrations/plane/projects", a.auth(a.planeProjects))
	mux.HandleFunc("GET /api/integrations/plane/outbox", a.auth(a.planeOutbox))
	mux.HandleFunc("GET /api/integrations/plane/outbox/{ticket}", a.auth(a.planeOutboxTicket))
	mux.HandleFunc("POST /api/integrations/plane/push", a.auth(a.planePushRoute))
	mux.HandleFunc("POST /api/integrations/plane/skip", a.auth(a.planeSkip))
	mux.HandleFunc("DELETE /api/integrations/plane/deletions/{id}", a.auth(a.planeDeletionDrop))
}

func (a *App) planeFail(w http.ResponseWriter, err error) {
	var pe *planeErr
	if errors.As(err, &pe) {
		code := http.StatusBadGateway
		switch {
		case pe.Status == 429:
			code = http.StatusTooManyRequests
		case pe.Status > 0 && pe.Status < 500:
			code = http.StatusBadRequest
		}
		writeJSON(w, code, map[string]any{"error": pe.Msg})
		return
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		writeJSON(w, http.StatusGatewayTimeout, map[string]any{"error": "Zeitüberschreitung"})
		return
	}
	a.fail(w, err)
}

/* ── Hilfen ─────────────────────────────────────────────────────── */

func deDate(d string) string {
	if len(d) < 10 {
		return ""
	}
	return d[8:10] + "." + d[5:7] + "." + d[:4]
}

func fmtMin(m int) string { return fmt.Sprintf("%d:%02d h", m/60, m%60) }

func descHash(d string) string {
	h := sha1.Sum([]byte(d))
	return hex.EncodeToString(h[:])[:16]
}

func (a *App) sumDescription(ctx context.Context, uid int64, t *tsum) string {
	var ids []int64
	for _, s := range strings.Split(t.IDs, ",") {
		if n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64); err == nil && n > 0 {
			ids = append(ids, n)
		}
	}
	head := fmt.Sprintf("Zeitprotokoll: %d %s, %s (%s", t.N, map[bool]string{true: "Eintrag", false: "Einträge"}[t.N == 1], fmtMin(t.Minutes), deDate(t.First))
	if t.Last != t.First {
		head += "–" + deDate(t.Last)
	}
	head += ")"
	var lines []string
	size, more := len(head), 0
	if len(ids) > 0 {
		rows, err := a.DB.Query(ctx, `SELECT date, start_time, COALESCE(duration_seconds,0), COALESCE(comment,''), COALESCE(activity,'')
			FROM time_entries WHERE user_id=$1 AND id = ANY($2) ORDER BY start_time`, uid, ids)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var date, st, comment, act string
				var secs int64
				if rows.Scan(&date, &st, &secs, &comment, &act) != nil {
					continue
				}
				txt := strings.TrimSpace(strings.Join(strings.Fields(comment), " "))
				if act != "" && txt != "" {
					txt = act + ": " + txt
				} else if act != "" {
					txt = act
				}
				if r := []rune(txt); len(r) > 200 {
					txt = string(r[:200])
				}
				l := fmt.Sprintf("• %s %s · %s", deDate(date), a.hm(st), fmtMin(int(math.Round(float64(secs)/60))))
				if txt != "" {
					l += " · " + txt
				}
				if size+len(l) > 1800 {
					more++
					continue
				}
				lines = append(lines, l)
				size += len(l) + 1
			}
		}
	}
	if more > 0 {
		lines = append(lines, fmt.Sprintf("… und %d weitere", more))
	}
	return strings.Join(append(append([]string{head}, lines...), "Summe wird automatisch gepflegt "+sumTag), "\n")
}

// blockedPlane: Projekte, deren Zeit nicht nach Plane soll (Ausgabe-Regel) oder die nicht wirklich in Plane sind
func (a *App) blockedPlane(ctx context.Context, uid int64, st map[string]string) ([]int64, error) {
	def := st["output_rule_default"]
	if def == "" {
		def = "bridge"
	}
	rows, err := a.DB.Query(ctx, `SELECT p.id, COALESCE(NULLIF(p.output_rule,'default'), $2), (p.plane_project_id IS NOT NULL),
		EXISTS(SELECT 1 FROM project_sources s WHERE s.project_id=p.id AND s.system='plane' AND s.status='linked' AND s.gone=1)
		FROM projects p WHERE p.user_id=$1`, uid, def)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	blocked := []int64{}
	for rows.Next() {
		var id int64
		var rule string
		var hasPP, gone bool
		if err := rows.Scan(&id, &rule, &hasPP, &gone); err != nil {
			return nil, err
		}
		if !((rule == "bridge" || rule == "plane") && hasPP && !gone) {
			blocked = append(blocked, id)
		}
	}
	return blocked, rows.Err()
}

func (a *App) planeSyncs(ctx context.Context, uid int64) (map[string]*planeSync, error) {
	rows, err := a.DB.Query(ctx, `SELECT ticket, COALESCE(work_item_id,''), COALESCE(project_id,''), COALESCE(worklog_id,''), COALESCE(synced_minutes,0),
		COALESCE(synced_at,''), COALESCE(error,''), COALESCE(title,''), COALESCE(desc_hash,'') FROM plane_ticket_sync WHERE user_id=$1`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]*planeSync{}
	for rows.Next() {
		var k string
		s := &planeSync{}
		if err := rows.Scan(&k, &s.WorkItemID, &s.ProjectID, &s.WorklogID, &s.SyncedMinutes, &s.SyncedAt, &s.Err, &s.Title, &s.DescHash); err != nil {
			return nil, err
		}
		out[k] = s
	}
	return out, rows.Err()
}

// localSums: lokale Summen je Ticket (nur eigene Buchungen; Tempo-/Plane-Importe zählen nicht)
func (a *App) localSums(ctx context.Context, uid int64, st map[string]string) ([]*tsum, error) {
	blocked, err := a.blockedPlane(ctx, uid, st)
	if err != nil {
		return nil, err
	}
	rows, err := a.DB.Query(ctx, `SELECT UPPER(TRIM(t.jira_ticket)), COALESCE(SUM(e.duration_seconds),0)::bigint, COUNT(*), MIN(e.date), MAX(e.date),
		COALESCE(MAX(t.title),''), COALESCE(MAX(t.plane_work_item_id),''), COALESCE(MAX(t.plane_project_id),''),
		COALESCE(STRING_AGG(DISTINCT p.name, ','),''), STRING_AGG(e.id::text, ',')
		FROM time_entries e JOIN tasks t ON t.id=e.task_id LEFT JOIN projects p ON p.id=e.project_id
		WHERE e.user_id=$1 AND t.user_id=$1 AND e.end_time IS NOT NULL AND e.duration_seconds > 0
		  AND (COALESCE(e.source,'manual') NOT IN ('tempo','tempo_csv','plane','plane_import') OR (COALESCE(e.plane_include,0)=1 AND COALESCE(e.source,'manual') <> 'plane_import'))
		  AND COALESCE(e.plane_skip,0)=0 AND t.jira_ticket IS NOT NULL AND TRIM(t.jira_ticket) <> ''
		  AND COALESCE(e.project_id,0) <> ALL($2::bigint[])
		  AND (t.plane_work_item_id IS NOT NULL OR EXISTS (SELECT 1 FROM projects tp WHERE tp.id=t.project_id AND tp.plane_project_id IS NOT NULL
		       AND tp.plane_identifier IS NOT NULL AND UPPER(TRIM(t.jira_ticket)) LIKE UPPER(tp.plane_identifier) || '-%')
		  )
		GROUP BY UPPER(TRIM(t.jira_ticket)) ORDER BY 1`, uid, blocked)
	if err != nil {
		return nil, err
	}
	var out []*tsum
	byKey := map[string]*tsum{}
	for rows.Next() {
		t := &tsum{}
		if err := rows.Scan(&t.Ticket, &t.Secs, &t.N, &t.First, &t.Last, &t.Title, &t.WI, &t.PP, &t.Projects, &t.IDs); err != nil {
			rows.Close()
			return nil, err
		}
		if keyRE.MatchString(t.Ticket) {
			out = append(out, t)
			byKey[t.Ticket] = t
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Altbestand: je Eintrag ein eigener Worklog (v5.0.0)
	leg := map[string]int{}
	lrows, err := a.DB.Query(ctx, `SELECT UPPER(TRIM(t.jira_ticket)), COUNT(*) FROM time_entries e JOIN tasks t ON t.id=e.task_id
		WHERE e.user_id=$1 AND e.plane_worklog_id IS NOT NULL AND COALESCE(e.source,'manual') <> 'plane' GROUP BY 1`, uid)
	if err != nil {
		return nil, err
	}
	for lrows.Next() {
		var k string
		var n int
		if err := lrows.Scan(&k, &n); err != nil {
			lrows.Close()
			return nil, err
		}
		if k != "" && keyRE.MatchString(k) {
			leg[k] = n
		}
	}
	lrows.Close()
	syncs, err := a.planeSyncs(ctx, uid)
	if err != nil {
		return nil, err
	}
	for _, t := range out {
		t.Minutes = int(math.Round(float64(t.Secs) / 60))
		t.Legacy = leg[t.Ticket]
		t.Sync = syncs[t.Ticket]
		t.Desc = a.sumDescription(ctx, uid, t)
		t.DHash = descHash(t.Desc)
	}
	// Tickets, die in Plane eine Summe haben, lokal aber nichts mehr → Summe muss weg
	var extra []string
	for k := range syncs {
		if byKey[k] == nil {
			extra = append(extra, k)
		}
	}
	sort.Strings(extra)
	for _, k := range extra {
		t := &tsum{Ticket: k, Title: syncs[k].Title, Legacy: leg[k], Sync: syncs[k]}
		out = append(out, t)
		byKey[k] = t
	}
	var legKeys []string
	for k := range leg {
		if byKey[k] == nil {
			legKeys = append(legKeys, k)
		}
	}
	sort.Strings(legKeys)
	for _, k := range legKeys {
		out = append(out, &tsum{Ticket: k, Legacy: leg[k]})
	}
	return out, nil
}

func ticketStatus(t *tsum) string {
	sy := t.Sync
	if sy != nil && sy.Err != "" {
		return "error"
	}
	if sy == nil || sy.WorklogID == "" {
		if t.Minutes > 0 {
			return "new"
		}
		if t.Legacy > 0 {
			return "changed"
		}
		return "synced"
	}
	if t.Minutes != sy.SyncedMinutes || t.Legacy > 0 || (t.DHash != "" && sy.DescHash != t.DHash) { // auch wenn sich nur ein Text geändert hat
		return "changed"
	}
	return "synced"
}

/* ── Übertragen ─────────────────────────────────────────────────── */

type wiRef struct{ ID, Project, Name string }

func (a *App) resolveKey(ctx context.Context, uid int64, c planeCfg, key, hintWI, hintPP string) (wiRef, error) {
	if hintWI != "" && hintPP != "" {
		return wiRef{ID: hintWI, Project: hintPP}, nil
	}
	r, err := a.planeDo(ctx, uid, c, "GET", c.wsPath()+"/work-items/"+urlPart(key)+"/", nil, planeOpts{})
	if err != nil {
		return wiRef{}, err
	}
	m := jm(r)
	project := asStr(m["project"])
	if project == "" {
		project = asStr(m["project_id"])
	}
	id := asStr(m["id"])
	if id == "" || project == "" {
		return wiRef{}, fmt.Errorf("Work-Item %s nicht gefunden", key)
	}
	if _, err := a.DB.Exec(ctx, `UPDATE tasks SET plane_work_item_id=$3, plane_project_id=$4 WHERE user_id=$1 AND UPPER(TRIM(jira_ticket))=$2`, uid, key, id, project); err != nil {
		return wiRef{}, err
	}
	name := asStr(m["name"])
	if name != "" {
		_, _ = a.DB.Exec(ctx, `UPDATE tasks SET title=$3 WHERE user_id=$1 AND UPPER(TRIM(jira_ticket))=$2 AND (title IS NULL OR UPPER(TRIM(title))=$2)`, uid, key, name)
	}
	return wiRef{ID: id, Project: project, Name: name}, nil
}

func urlPart(s string) string {
	return strings.NewReplacer("/", "%2F", "?", "%3F", "#", "%23", " ", "%20").Replace(s)
}

func (a *App) planeDel404(ctx context.Context, uid int64, c planeCfg, path string) error {
	if strings.Contains(path, "//") || strings.HasSuffix(strings.TrimSuffix(path, "/"), "/worklogs") {
		return errors.New("Löschung unvollständig (Projekt, Work-Item oder Worklog-Id fehlt)")
	}
	_, err := a.planeDo(ctx, uid, c, "DELETE", path, nil, planeOpts{})
	var pe *planeErr
	if errors.As(err, &pe) && pe.Status == 404 {
		return nil
	}
	return err
}

func shortErr(err error) string {
	s := err.Error()
	if r := []rune(s); len(r) > 300 {
		return string(r[:300])
	}
	return s
}

func (a *App) pushTicket(ctx context.Context, uid int64, c planeCfg, t *tsum) map[string]any {
	key := t.Ticket
	res, err := a.pushTicketInner(ctx, uid, c, t)
	if err == nil {
		return res
	}
	_, _ = a.DB.Exec(ctx, `INSERT INTO plane_ticket_sync(user_id,ticket,error,title) VALUES($1,$2,$3,$4)
		ON CONFLICT (user_id,ticket) DO UPDATE SET error=EXCLUDED.error`, uid, key, shortErr(err), nz(t.Title))
	a.sysLog(ctx, uid, "error", "plane", key+": "+err.Error())
	return map[string]any{"ticket": key, "status": "error", "error": err.Error()}
}

func (a *App) pushTicketInner(ctx context.Context, uid int64, c planeCfg, t *tsum) (map[string]any, error) {
	key := t.Ticket
	sy := t.Sync
	hintWI, hintPP := t.WI, t.PP
	if sy != nil {
		if hintWI == "" {
			hintWI = sy.WorkItemID
		}
		if hintPP == "" {
			hintPP = sy.ProjectID
		}
	}
	wi, err := a.resolveKey(ctx, uid, c, key, hintWI, hintPP)
	if err != nil {
		return nil, err
	}
	// 1) Altbestand (ein Worklog je Eintrag) aufräumen
	lrows, err := a.maps(ctx, `SELECT e.id, e.plane_worklog_id, e.plane_work_item_id, e.plane_project_id FROM time_entries e JOIN tasks t ON t.id=e.task_id
		WHERE e.user_id=$1 AND e.plane_worklog_id IS NOT NULL AND COALESCE(e.source,'manual') <> 'plane' AND UPPER(TRIM(t.jira_ticket))=$2`, uid, key)
	if err != nil {
		return nil, err
	}
	for _, e := range lrows {
		pp, wid := asStr(e["plane_project_id"]), asStr(e["plane_work_item_id"])
		if pp == "" {
			pp = wi.Project
		}
		if wid == "" {
			wid = wi.ID
		}
		if err := a.planeDel404(ctx, uid, c, c.wlPath(pp, wid, asStr(e["plane_worklog_id"]))); err != nil {
			return nil, err
		}
		if _, err := a.DB.Exec(ctx, `UPDATE time_entries SET plane_worklog_id=NULL, plane_work_item_id=NULL, plane_project_id=NULL, plane_hash=NULL WHERE id=$1 AND user_id=$2`, e["id"], uid); err != nil {
			return nil, err
		}
	}
	wlID := ""
	if sy != nil {
		wlID = sy.WorklogID
		if wlID != "" && sy.WorkItemID != "" && sy.WorkItemID != wi.ID {
			if err := a.planeDel404(ctx, uid, c, c.wlPath(sy.ProjectID, sy.WorkItemID, wlID)); err != nil {
				return nil, err
			}
			wlID = ""
		}
	}
	// 2) Summe ist 0 → Summen-Worklog entfernen
	if t.Minutes == 0 {
		if wlID != "" {
			if err := a.planeDel404(ctx, uid, c, c.wlPath(wi.Project, wi.ID, wlID)); err != nil {
				return nil, err
			}
		}
		_, _ = a.DB.Exec(ctx, `DELETE FROM plane_ticket_sync WHERE user_id=$1 AND ticket=$2`, uid, key)
		_, _ = a.DB.Exec(ctx, `UPDATE time_entries SET plane_sum_key=NULL WHERE user_id=$1 AND plane_sum_key=$2`, uid, key)
		return map[string]any{"ticket": key, "status": "removed"}, nil
	}
	desc := t.Desc
	if desc == "" {
		desc = a.sumDescription(ctx, uid, t)
	}
	body := map[string]any{"duration": t.Minutes, "description": desc}
	if wlID != "" {
		if _, err := a.planeDo(ctx, uid, c, "PATCH", c.wlPath(wi.Project, wi.ID, wlID), body, planeOpts{}); err != nil {
			var pe *planeErr
			if errors.As(err, &pe) && pe.Status == 404 {
				wlID = ""
			} else {
				return nil, err
			}
		}
	}
	if wlID == "" {
		// vorhandene eigene Summe wiederverwenden (z. B. nach Neuinstallation) statt doppelt anzulegen
		meID := c.MeID
		if meID == "" {
			me, err := a.planeMe(ctx, uid, c, planeOpts{})
			if err != nil {
				return nil, fmt.Errorf("Plane-Benutzer nicht ermittelbar: %w", err)
			}
			if meID = asStr(me["id"]); meID == "" {
				return nil, errors.New("Plane-Benutzer nicht ermittelbar (keine Id) – API-Schlüssel prüfen")
			}
		}
		all, err := a.planeListAll(ctx, uid, c, c.wlPath(wi.Project, wi.ID, ""), planeOpts{})
		if err != nil {
			return nil, err
		}
		for _, w := range all {
			if !strings.Contains(asStr(w["description"]), sumTag) {
				continue
			}
			mine := false
			for _, f := range []string{"logged_by", "logged_by_id", "created_by", "created_by_id"} {
				if asStr(w[f]) == meID {
					mine = true
				}
			}
			if mine {
				wlID = asStr(w["id"])
				break
			}
		}
		if wlID != "" {
			if _, err := a.planeDo(ctx, uid, c, "PATCH", c.wlPath(wi.Project, wi.ID, wlID), body, planeOpts{}); err != nil {
				return nil, err
			}
		} else {
			r, err := a.planeDo(ctx, uid, c, "POST", c.wlPath(wi.Project, wi.ID, ""), body, planeOpts{})
			if err != nil {
				return nil, err
			}
			if wlID = asStr(jm(r)["id"]); wlID == "" {
				return nil, errors.New("Plane hat keine Worklog-ID zurückgegeben")
			}
		}
	}
	title := t.Title
	if title == "" {
		title = wi.Name
	}
	dh := t.DHash
	if dh == "" {
		dh = descHash(desc)
	}
	var ids []int64
	for _, s := range strings.Split(t.IDs, ",") {
		if n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64); err == nil && n > 0 {
			ids = append(ids, n)
		}
	}
	err = a.inTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO plane_ticket_sync(user_id,ticket,work_item_id,project_id,worklog_id,synced_minutes,synced_at,error,title,desc_hash)
			VALUES($1,$2,$3,$4,$5,$6,$7,NULL,$8,$9) ON CONFLICT (user_id,ticket) DO UPDATE SET work_item_id=EXCLUDED.work_item_id,
			project_id=EXCLUDED.project_id, worklog_id=EXCLUDED.worklog_id, synced_minutes=EXCLUDED.synced_minutes, synced_at=EXCLUDED.synced_at,
			error=NULL, title=EXCLUDED.title, desc_hash=EXCLUDED.desc_hash`,
			uid, key, wi.ID, wi.Project, wlID, t.Minutes, time.Now().UTC().Format(isoMillis), nz(title), dh); err != nil {
			return err
		}
		// nur die Einträge markieren, die tatsächlich in dieser Summe stecken (für „Plane ✓“ in der Tagesliste)
		if _, err := tx.Exec(ctx, `UPDATE time_entries SET plane_sum_key=NULL WHERE user_id=$1 AND plane_sum_key=$2`, uid, key); err != nil {
			return err
		}
		if len(ids) > 0 {
			if _, err := tx.Exec(ctx, `UPDATE time_entries SET plane_sum_key=$3, plane_sum_secs=duration_seconds WHERE user_id=$1 AND id = ANY($2)`, uid, ids, key); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"ticket": key, "status": "synced", "minutes": t.Minutes}, nil
}

func (a *App) processPlaneDeletions(ctx context.Context, uid int64, c planeCfg) []map[string]any {
	rows, err := a.maps(ctx, `SELECT * FROM plane_deletions WHERE user_id=$1 ORDER BY id`, uid)
	if err != nil {
		return nil
	}
	res := []map[string]any{}
	for _, d := range rows {
		id := d["id"]
		if err := a.planeDel404(ctx, uid, c, c.wlPath(asStr(d["project_id"]), asStr(d["work_item_id"]), asStr(d["worklog_id"]))); err != nil {
			_, _ = a.DB.Exec(ctx, `UPDATE plane_deletions SET error=$3 WHERE id=$1 AND user_id=$2`, id, uid, shortErr(err))
			res = append(res, map[string]any{"id": id, "status": "error", "error": err.Error()})
			continue
		}
		_, _ = a.DB.Exec(ctx, `DELETE FROM plane_deletions WHERE id=$1 AND user_id=$2`, id, uid)
		res = append(res, map[string]any{"id": id, "status": "deleted"})
	}
	return res
}

// planePush überträgt offene Summen (alle oder nur die genannten Tickets) und führt vorgemerkte Löschungen aus.
func (a *App) planePush(ctx context.Context, uid int64, tickets []string, createDrafts bool) (map[string]any, error) {
	ps := a.planeStateFor(uid)
	ps.mu.Lock()
	if ps.pushing {
		ps.again = true
		ps.mu.Unlock()
		return map[string]any{"busy": true, "results": []any{}, "deletions": []any{}, "ok": 0, "errors": 0}, nil
	}
	ps.pushing = true
	ps.mu.Unlock()
	defer func() {
		ps.mu.Lock()
		ps.pushing = false
		again := ps.again
		ps.again = false
		ps.mu.Unlock()
		if again {
			a.syncAuto(uid)
		}
	}()

	c, err := a.planeCfg(ctx, uid)
	if err != nil {
		return nil, err
	}
	if !c.ok() {
		return nil, &planeErr{Status: 400, Msg: "Plane ist nicht eingerichtet (Adresse/Workspace in der Servereinrichtung, API-Schlüssel im Profil)"}
	}
	st, err := a.userSettings(ctx, uid)
	if err != nil {
		return nil, err
	}
	// Altbestand ohne gültiges Ticket (Ticket entfernt/Aufgabe gelöscht): alte Einzel-Worklogs zur Löschung vormerken
	err = a.inTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO plane_deletions(user_id,worklog_id,work_item_id,project_id,label)
			SELECT e.user_id, e.plane_worklog_id, e.plane_work_item_id, e.plane_project_id, e.date || ' ' || LEFT(COALESCE(e.comment,''),60)
			FROM time_entries e LEFT JOIN tasks t ON t.id=e.task_id
			WHERE e.user_id=$1 AND e.plane_worklog_id IS NOT NULL AND COALESCE(e.source,'manual') <> 'plane'
			  AND (t.id IS NULL OR t.jira_ticket IS NULL OR TRIM(t.jira_ticket)='')`, uid); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE time_entries e SET plane_worklog_id=NULL, plane_work_item_id=NULL, plane_project_id=NULL
			WHERE e.user_id=$1 AND e.plane_worklog_id IS NOT NULL AND COALESCE(e.source,'manual') <> 'plane'
			  AND NOT EXISTS (SELECT 1 FROM tasks t WHERE t.id=e.task_id AND t.jira_ticket IS NOT NULL AND TRIM(t.jira_ticket)<>'')`, uid)
		return err
	})
	if err != nil {
		return nil, err
	}
	// Entwurfs-Tickets (Aufgaben ohne Ticket in Plane-Projekten, auf die schon Zeit gebucht ist): erst jetzt in Plane anlegen
	results := []map[string]any{}
	created := 0
	// nur bei ausdrücklicher Übertragung (nicht bei der Automatik, nicht bei Auswahl einzelner Tickets)
	if blocked, err := a.blockedPlane(ctx, uid, st); err == nil && createDrafts && len(tickets) == 0 {
		drows, err := a.DB.Query(ctx, `SELECT DISTINCT t.id, t.title FROM time_entries e JOIN tasks t ON t.id=e.task_id
			WHERE e.user_id=$1 AND t.user_id=$1 AND e.end_time IS NOT NULL AND e.duration_seconds > 0
			  AND COALESCE(e.source,'manual') NOT IN ('tempo','tempo_csv','plane','plane_import') AND COALESCE(e.plane_skip,0)=0
			  AND (t.jira_ticket IS NULL OR TRIM(t.jira_ticket)='') AND t.plane_work_item_id IS NULL
			  AND COALESCE(e.project_id,0) <> ALL($2::bigint[]) AND t.project_id <> ALL($2::bigint[])`, uid, blocked)
		if err != nil {
			return nil, err
		}
		type draft struct {
			id    int64
			title string
		}
		var drafts []draft
		for drows.Next() {
			var d draft
			if err := drows.Scan(&d.id, &d.title); err != nil {
				drows.Close()
				return nil, err
			}
			drafts = append(drafts, d)
		}
		drows.Close()
		for _, d := range drafts {
			done, err := a.linkTaskToPlane(ctx, uid, d.id)
			switch {
			case err != nil:
				results = append(results, map[string]any{"ticket": "Entwurf „" + d.title + "“", "status": "error", "error": "Ticket in Plane nicht angelegt: " + err.Error()})
				a.sysLog(ctx, uid, "error", "plane", fmt.Sprintf("Entwurfs-Ticket „%s“ nicht angelegt: %v", d.title, err))
			case done:
				created++
			}
			if ctx.Err() != nil {
				break
			}
		}
	}
	sums, err := a.localSums(ctx, uid, st)
	if err != nil {
		return nil, err
	}
	want := map[string]bool{}
	for _, k := range tickets {
		want[strings.ToUpper(strings.TrimSpace(k))] = true
	}
	for _, t := range sums {
		if len(want) > 0 && !want[t.Ticket] {
			continue
		}
		if t.Sync != nil && t.Sync.Err != "" {
			_, _ = a.DB.Exec(ctx, `UPDATE plane_ticket_sync SET error=NULL WHERE user_id=$1 AND ticket=$2`, uid, t.Ticket)
			t.Sync.Err = ""
		}
		if ticketStatus(t) != "synced" {
			results = append(results, a.pushTicket(ctx, uid, c, t))
			if ctx.Err() != nil {
				break
			}
		}
	}
	deletions := []map[string]any{}
	if len(want) == 0 {
		deletions = a.processPlaneDeletions(ctx, uid, c)
	}
	ok, bad, del := 0, 0, 0
	for _, r := range results {
		switch r["status"] {
		case "synced", "removed":
			ok++
		case "error":
			bad++
		}
	}
	for _, d := range deletions {
		if d["status"] == "deleted" {
			del++
		}
	}
	if len(results) > 0 || len(deletions) > 0 {
		lvl := "success"
		if bad > 0 {
			lvl = "warn"
		}
		a.sysLog(ctx, uid, lvl, "plane", fmt.Sprintf("Übertragen: %d Tickets ok, %d Fehler, %d gelöscht", ok, bad, del))
	}
	return map[string]any{"results": results, "deletions": deletions, "ok": ok, "errors": bad, "created_tickets": created}, nil
}

// syncAuto stößt (entprellt, 8 s) die Übertragung an, wenn die Person „automatisch“ eingestellt hat.
func (a *App) syncAuto(uid int64) {
	ps := a.planeStateFor(uid)
	ps.mu.Lock()
	defer ps.mu.Unlock()
	if ps.timer != nil {
		ps.timer.Stop()
	}
	db := a.DB
	ps.timer = time.AfterFunc(8*time.Second, func() { a.safely("plane-automatik", func() { a.autoPush(db, uid) }) })
	a.tempoAuto(uid) // Tempo-Ausgang (Modul/Modus prüft der Lauf selbst)
}

func (a *App) autoPush(_ *pgxpool.Pool, uid int64) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	c, err := a.planeCfg(ctx, uid)
	if err != nil || c.Mode != "auto" || !c.ok() {
		return
	}
	if _, err := a.planePush(ctx, uid, nil, false); err != nil {
		a.sysLog(ctx, uid, "warn", "plane", "Auto-Sync: "+err.Error())
	}
}

/* ── Routen ─────────────────────────────────────────────────────── */

func (a *App) planeStatus(w http.ResponseWriter, r *http.Request, u User) {
	ctx := r.Context()
	c, err := a.planeCfg(ctx, u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	if !c.ok() {
		msg := "Noch nicht eingerichtet"
		switch {
		case c.Base == "" || c.WS == "":
			msg = "Plane-Adresse und Workspace fehlen in der Servereinrichtung"
		case c.Key == "":
			msg = "Plane-API-Schlüssel fehlt – bitte im Profil eintragen"
		}
		writeJSON(w, http.StatusOK, map[string]any{"configured": false, "mode": c.Mode, "message": msg})
		return
	}
	me, err := a.planeMe(ctx, u.ID, c, planeOpts{Interactive: true})
	if err != nil {
		a.planeFail(w, err)
		return
	}
	pr, err := a.planeDo(ctx, u.ID, c, "GET", c.wsPath()+"/projects/", nil, planeOpts{Interactive: true})
	if err != nil {
		a.planeFail(w, err)
		return
	}
	n := len(listOf(pr))
	name := asStr(me["display_name"])
	if name == "" {
		name = asStr(me["first_name"])
	}
	if name == "" {
		name = asStr(me["email"])
	}
	writeJSON(w, http.StatusOK, map[string]any{"configured": true, "connected": true, "mode": c.Mode,
		"user":     map[string]any{"id": me["id"], "name": name, "email": me["email"]},
		"projects": n, "message": fmt.Sprintf("Verbunden als %s – %d Projekte im Workspace „%s“", name, n, c.WS)})
}

func (a *App) planeProjects(w http.ResponseWriter, r *http.Request, u User) {
	ctx := r.Context()
	c, err := a.planeCfg(ctx, u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	pr, err := a.planeDo(ctx, u.ID, c, "GET", c.wsPath()+"/projects/", nil, planeOpts{Interactive: true})
	if err != nil {
		a.planeFail(w, err)
		return
	}
	locals, err := a.maps(ctx, `SELECT id,name,jira_key,plane_project_id,plane_identifier,status FROM projects WHERE user_id=$1`, u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	out := []map[string]any{}
	for _, p := range listOf(pr) {
		var local map[string]any
		for _, l := range locals {
			if asStr(l["plane_project_id"]) == asStr(p["id"]) {
				local = l
				break
			}
		}
		if local == nil {
			for _, l := range locals {
				ident := asStr(l["plane_identifier"])
				if ident == "" {
					ident = asStr(l["jira_key"])
				}
				if asStr(l["plane_project_id"]) == "" && strings.EqualFold(ident, asStr(p["identifier"])) {
					local = l
					break
				}
			}
		}
		e := map[string]any{"id": p["id"], "name": p["name"], "identifier": p["identifier"], "local_id": nil, "local_name": nil, "linked": false}
		if local != nil {
			e["local_id"], e["local_name"] = local["id"], local["name"]
			e["linked"] = asStr(local["plane_project_id"]) == asStr(p["id"])
		}
		out = append(out, e)
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *App) planeOutbox(w http.ResponseWriter, r *http.Request, u User) {
	ctx := r.Context()
	c, err := a.planeCfg(ctx, u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	st, err := a.userSettings(ctx, u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	sums, err := a.localSums(ctx, u.ID, st)
	if err != nil {
		a.fail(w, err)
		return
	}
	tickets := []map[string]any{}
	counts := map[string]int{}
	for _, t := range sums {
		status := ticketStatus(t)
		counts[status]++
		title := t.Title
		var syncedAt, errMsg any
		planeMin := 0
		if t.Sync != nil {
			if title == "" {
				title = t.Sync.Title
			}
			if t.Sync.WorklogID != "" {
				planeMin = t.Sync.SyncedMinutes
			}
			syncedAt, errMsg = nz(t.Sync.SyncedAt), nz(t.Sync.Err)
		}
		tickets = append(tickets, map[string]any{"ticket": t.Ticket, "title": nz(title), "projects": nz(t.Projects), "entries": t.N,
			"local_minutes": t.Minutes, "plane_minutes": planeMin, "first": nz(t.First), "last": nz(t.Last), "legacy": t.Legacy,
			"status": status, "error": errMsg, "synced_at": syncedAt})
	}
	sort.SliceStable(tickets, func(i, j int) bool {
		li, lj := asStr(tickets[i]["last"]), asStr(tickets[j]["last"])
		if li != lj {
			return li > lj
		}
		return asStr(tickets[i]["ticket"]) < asStr(tickets[j]["ticket"])
	})
	since := time.Now().Add(-31 * 24 * time.Hour).In(a.Loc).Format("2006-01-02")
	var noN, noS int64
	if err := a.DB.QueryRow(ctx, `SELECT COUNT(*), COALESCE(SUM(e.duration_seconds),0)::bigint FROM time_entries e LEFT JOIN tasks t ON t.id=e.task_id
		WHERE e.user_id=$1 AND e.date>=$2 AND e.end_time IS NOT NULL AND COALESCE(e.source,'manual') NOT IN ('tempo','tempo_csv','plane','plane_import')
		  AND (t.jira_ticket IS NULL OR TRIM(t.jira_ticket)='')`, u.ID, since).Scan(&noN, &noS); err != nil {
		a.fail(w, err)
		return
	}
	skipped, err := a.maps(ctx, `SELECT e.id, e.date, e.start_time, e.end_time, ROUND(COALESCE(e.duration_seconds,0)/60.0)::int AS minutes,
		t.jira_ticket AS ticket, e.comment, p.name AS project_name FROM time_entries e LEFT JOIN tasks t ON t.id=e.task_id LEFT JOIN projects p ON p.id=e.project_id
		WHERE e.user_id=$1 AND COALESCE(e.plane_skip,0)=1 ORDER BY e.start_time DESC LIMIT 100`, u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	deletions, err := a.maps(ctx, `SELECT * FROM plane_deletions WHERE user_id=$1 ORDER BY id`, u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"mode": c.Mode, "configured": c.ok(), "counts": counts, "tickets": tickets, "deletions": deletions,
		"skipped": skipped, "without_ticket": map[string]any{"count": noN, "minutes": int(math.Round(float64(noS) / 60)), "since": since}})
}

func (a *App) planeOutboxTicket(w http.ResponseWriter, r *http.Request, u User) {
	key := strings.ToUpper(r.PathValue("ticket"))
	rows, err := a.maps(r.Context(), `SELECT e.id, e.date, e.start_time, e.end_time, ROUND(COALESCE(e.duration_seconds,0)/60.0)::int AS minutes, e.comment,
		p.name AS project_name, COALESCE(e.source,'manual') AS source, COALESCE(e.plane_skip,0)=1 AS skipped,
		(e.end_time IS NOT NULL AND COALESCE(e.plane_skip,0)=0 AND (COALESCE(e.source,'manual') NOT IN ('tempo','tempo_csv','plane','plane_import') OR (COALESCE(e.source,'manual') NOT IN ('plane','plane_import') AND COALESCE(e.plane_include,0)=1))) AS counted,
		(e.plane_sum_key=$2 AND e.plane_sum_secs=e.duration_seconds) AS in_plane
		FROM time_entries e JOIN tasks t ON t.id=e.task_id LEFT JOIN projects p ON p.id=e.project_id
		WHERE e.user_id=$1 AND UPPER(TRIM(t.jira_ticket))=$2 ORDER BY e.start_time DESC`, u.ID, key)
	if err != nil {
		a.fail(w, err)
		return
	}
	for _, e := range rows {
		if e["in_plane"] == nil {
			e["in_plane"] = false
		}
	}
	writeJSON(w, http.StatusOK, rows)
}

func (a *App) planePushRoute(w http.ResponseWriter, r *http.Request, u User) {
	var in body
	if r.ContentLength != 0 && !readJSON(w, r, &in) {
		return
	}
	var tickets []string
	if l, ok := in["tickets"].([]any); ok {
		for _, x := range l {
			tickets = append(tickets, asStr(x))
		}
	}
	// nicht mit dem Request abbrechen: nach einem erfolgreichen POST muss die Worklog-Id noch gespeichert werden
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 10*time.Minute)
	defer cancel()
	res, err := a.planePush(ctx, u.ID, tickets, true)
	if err != nil {
		a.planeFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// planeSkip nimmt Einträge (ids) oder ganze Tickets (tickets) von der Übertragung aus bzw. wieder hinein
func (a *App) planeSkip(w http.ResponseWriter, r *http.Request, u User) {
	var in body
	if !readJSON(w, r, &in) {
		return
	}
	skip := 1
	if b, ok := in["skip"].(bool); ok && !b {
		skip = 0
	}
	ctx := r.Context()
	if l, ok := in["ids"].([]any); ok {
		var ids []int64
		for _, x := range l {
			if n := int64(asFloat(x)); n > 0 {
				ids = append(ids, n)
			}
		}
		if len(ids) > 0 {
			if _, err := a.DB.Exec(ctx, `UPDATE time_entries SET plane_skip=$3, plane_error=NULL WHERE user_id=$1 AND id = ANY($2)`, u.ID, ids, skip); err != nil {
				a.fail(w, err)
				return
			}
		}
	}
	if l, ok := in["tickets"].([]any); ok {
		for _, x := range l {
			if _, err := a.DB.Exec(ctx, `UPDATE time_entries SET plane_skip=$3 WHERE user_id=$1 AND task_id IN (SELECT id FROM tasks WHERE user_id=$1 AND UPPER(TRIM(jira_ticket))=$2)`,
				u.ID, strings.ToUpper(asStr(x)), skip); err != nil {
				a.fail(w, err)
				return
			}
		}
	}
	a.syncAuto(u.ID)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *App) planeDeletionDrop(w http.ResponseWriter, r *http.Request, u User) {
	id, ok := pathID(r)
	if !ok {
		notFound(w)
		return
	}
	if _, err := a.DB.Exec(r.Context(), `DELETE FROM plane_deletions WHERE id=$1 AND user_id=$2`, id, u.ID); err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

/* Plane-Import: Zeiten der Person AUS Plane holen – nur auf Anforderung (kein Hintergrundlauf), nur aus Projekten, die als
   Plane-Quelle verknüpft sind, Zeitraum höchstens 12 Monate je Lauf (Bremse von planeDo).
   Plane kennt nur die Dauer: Einträge bekommen Datum + Dauer, Uhrzeit als Platzhalter ab 08:00 des Tages gestapelt
   (wie backend/tempo-sync.js) und time_precise=false. Grundsätze:
   • nie überschreiben oder verdoppeln: eigene Summen ([zp-sum]) und frühere Einzel-Übertragungen ([zp#…]) nie zurück;
     gibt es für Ticket und Tag schon eigene Buchungen, wird nicht importiert (deckt die Summe nicht → Hinweis „prüfen“);
   • idempotent über plane_import_id (eindeutig je Person), Wiederholung ändert nichts;
   • eingereichte/freigegebene Monate werden übersprungen und gemeldet (Datenbank-Sperre wird nie umgangen);
   • importierte Einträge (source plane_import) schließt die Plane-Übertragung und der Tempo-Ausgang aus. */

const planeImportSource = "plane_import"

var errCovered = errors.New("inzwischen lokal erfasst")

var planeDescDateRE = regexp.MustCompile(`·\s*(\d{4}-\d{2}-\d{2})`) // nur „· JJJJ-MM-TT“ (Muster unserer Beschreibungen), nicht jedes Datum im Text
var planeDescTailRE = regexp.MustCompile(`\s*·\s*\d{4}-\d{2}-\d{2}.*$`)

// planeOwnPush: Worklog stammt von uns (Summe je Ticket oder alter Einzel-Worklog je Eintrag) → nie importieren
func planeOwnPush(desc string) bool {
	return strings.Contains(desc, sumTag) || strings.Contains(desc, "[zp#")
}

type planeWL struct {
	ID, WI, Key, Date, Desc string
	Mins                    int64
	ProjectID               int64
	Created                 time.Time
}

type importDecision struct {
	WL         planeWL
	Action     string // import | skip
	Reason     string
	Start, End time.Time
}

// planeWLDate: Datum aus der Beschreibung („… · JJJJ-MM-TT …“), sonst Erstellungstag in der Zeitzone des Servers
func planeWLDate(desc string, created time.Time, loc *time.Location) string {
	if m := planeDescDateRE.FindStringSubmatch(desc); m != nil {
		if _, err := time.Parse("2006-01-02", m[1]); err == nil {
			return m[1]
		}
	}
	if created.IsZero() {
		return ""
	}
	return created.In(loc).Format("2006-01-02")
}

func groupKey(w planeWL) string {
	if w.Key != "" {
		return w.Key + "|" + w.Date
	}
	return "wi:" + w.WI + "|" + w.Date
}

// planImport (ohne Datenbank): was wird importiert, was übersprungen und warum.
// localSecs: eigene (nicht importierte) Buchungen je „KEY|Datum“ bzw. „wi:<Id>|Datum“; dayUsed: schon gestapelte Minuten je Tag.
func planImport(wls []planeWL, known map[string]string, localSecs map[string]int64, locked map[string]bool, dayUsed map[string]int64, from, to string, loc *time.Location) []importDecision {
	sort.SliceStable(wls, func(i, j int) bool {
		if wls[i].Date != wls[j].Date {
			return wls[i].Date < wls[j].Date
		}
		return wls[i].Created.Before(wls[j].Created)
	})
	planeSum := map[string]int64{}
	for _, w := range wls {
		planeSum[groupKey(w)] += w.Mins * 60
	}
	used := map[string]int64{}
	for k, v := range dayUsed {
		used[k] = v
	}
	out := make([]importDecision, 0, len(wls))
	for _, w := range wls {
		d := importDecision{WL: w, Action: "skip"}
		local := localSecs[groupKey(w)]
		if w.Key != "" {
			local = max(local, localSecs["wi:"+w.WI+"|"+w.Date]) // dieselben Buchungen unter beiden Schlüsseln: Maximum, nicht Summe
		}
		switch {
		case known[w.ID] != "":
			d.Reason = known[w.ID]
		case w.Date == "" || w.Mins <= 0:
			d.Reason = "ohne Datum oder Dauer"
		case w.Date < from || w.Date > to:
			d.Reason = "außerhalb des Zeitraums"
		case locked[monthOfDate(w.Date)]:
			d.Reason = "Monat geschlossen, eingereicht oder freigegeben"
		case local > 0 && local >= planeSum[groupKey(w)]:
			d.Reason = "schon lokal erfasst"
		case local > 0:
			d.Reason = "teilweise lokal erfasst – bitte prüfen (nicht importiert)"
		default:
			day, err := time.ParseInLocation("2006-01-02", w.Date, loc)
			if err != nil {
				d.Reason = "Datum ungültig"
				break
			}
			d.Action, d.Reason = "import", ""
			d.Start = day.Add(8*time.Hour + time.Duration(used[w.Date])*time.Minute)
			d.End = d.Start.Add(time.Duration(w.Mins) * time.Minute)
			used[w.Date] += w.Mins
		}
		out = append(out, d)
	}
	return out
}

func planeImportComment(desc string) string {
	return truncRunes(strings.TrimSpace(planeDescTailRE.ReplaceAllString(desc, "")), 2000)
}

// planeImportRange: Standard ab tracking_start_date (sonst Monatsanfang) bis heute; höchstens 366 Tage
func planeImportRange(from, to, tracking string, now time.Time) (string, string, error) {
	if to == "" {
		to = now.Format("2006-01-02")
	}
	if from == "" {
		from = tracking
		if !isoDateRE.MatchString(from) {
			from = now.Format("2006-01") + "-01"
		}
	}
	f, err1 := time.Parse("2006-01-02", from)
	t, err2 := time.Parse("2006-01-02", to)
	switch {
	case err1 != nil || err2 != nil:
		return "", "", errUser("from/to: Datum JJJJ-MM-TT")
	case t.Before(f):
		return "", "", errUser("to liegt vor from")
	case t.Sub(f) > 366*24*time.Hour:
		if f2 := t.AddDate(-1, 0, 1); from == tracking { // Standard zu lang: auf die letzten 12 Monate kürzen
			return f2.Format("2006-01-02"), to, nil
		}
		return "", "", errUser("höchstens 12 Monate je Lauf")
	}
	return from, to, nil
}

/* ── Ausführung ─────────────────────────────────────────────────── */

var planeImportRunning sync.Map // Person → true (ein Import je Person)

func (a *App) routesPlaneImport(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/integrations/plane/import", a.auth(a.planeImportRoute))
	mux.HandleFunc("POST /api/integrations/plane/pull", a.auth(a.planeImportRoute)) // Alias wie Node „Aus Plane holen“
}

func (a *App) planeImportRoute(w http.ResponseWriter, r *http.Request, u User) {
	var in body
	if r.ContentLength != 0 && !readJSON(w, r, &in) {
		return
	}
	dry := truthy(in["dry_run"])
	wsRaw := strings.TrimSpace(r.URL.Query().Get("workspace"))
	if wsRaw == "" {
		wsRaw = asStr(in["workspace"])
	}
	if _, busy := planeImportRunning.LoadOrStore(u.ID, true); busy {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "Import läuft bereits"})
		return
	}
	defer planeImportRunning.Delete(u.ID)
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 15*time.Minute)
	defer cancel()
	res, err := a.planeImport(ctx, u.ID, asStr(in["from"]), asStr(in["to"]), dry, wsRaw)
	if err != nil {
		var ue errUser
		if errors.As(err, &ue) {
			badRequest(w, string(ue))
			return
		}
		a.planeFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

type importProject struct {
	ID, WorkspaceID int64
	Plane, Name     string
	Ident           string
}

func (a *App) planeImport(ctx context.Context, uid int64, from, to string, dry bool, workspaceRaw string) (map[string]any, error) {
	cfgs, err := a.planeCfgsForQuery(ctx, uid, workspaceRaw)
	if err != nil {
		return nil, err
	}
	if len(cfgs) == 0 {
		c, err := a.planeCfg(ctx, uid)
		if err != nil {
			return nil, err
		}
		if !c.ok() {
			return nil, &planeErr{Status: 400, Msg: "Plane ist nicht eingerichtet (Adresse/Workspace in der Servereinrichtung, API-Schlüssel im Profil)"}
		}
		cfgs = []planeCfg{c}
	}
	st, err := a.userSettings(ctx, uid)
	if err != nil {
		return nil, err
	}
	if from, to, err = planeImportRange(from, to, st["tracking_start_date"], time.Now().In(a.Loc)); err != nil {
		return nil, err
	}
	projects, err := a.importProjects(ctx, uid)
	if err != nil {
		return nil, err
	}
	localKey := func(wi string) string {
		var k string
		_ = a.DB.QueryRow(ctx, `SELECT UPPER(TRIM(jira_ticket)) FROM tasks WHERE user_id=$1 AND plane_work_item_id=$2 AND jira_ticket IS NOT NULL LIMIT 1`, uid, wi).Scan(&k)
		return k
	}
	var wls []planeWL
	stats := map[string]int{}
	var errs []string
	anyMe := false
	for _, c := range cfgs {
		if !c.ok() {
			continue
		}
		meID := a.planeMeID(ctx, uid, c)
		if meID == "" {
			errs = append(errs, "Workspace „"+c.WS+"“: Plane-Benutzer nicht ermittelbar")
			continue
		}
		anyMe = true
		var subset []importProject
		for _, p := range projects {
			if c.ID == 0 || p.WorkspaceID == 0 || p.WorkspaceID == c.ID {
				subset = append(subset, p)
			}
		}
		part, stPart, ePart := a.fetchPlaneWorklogs(ctx, uid, c, subset, meID, localKey)
		wls = append(wls, part...)
		for k, v := range stPart {
			stats[k] += v
		}
		errs = append(errs, ePart...)
	}
	if !anyMe {
		return nil, &planeErr{Status: 400, Msg: "Plane-Benutzer nicht ermittelbar – API-Schlüssel prüfen"}
	}
	known, local, locked, used, err := a.importState(ctx, uid, from, to)
	if err != nil {
		return nil, err
	}
	decisions := planImport(wls, known, local, locked, used, from, to, a.Loc)
	out := []map[string]any{}
	imported, skipped := 0, map[string]int{}
	for _, d := range decisions {
		row := map[string]any{"worklog_id": d.WL.ID, "ticket": nz(d.WL.Key), "date": d.WL.Date, "minutes": d.WL.Mins, "action": d.Action, "reason": nz(d.Reason)}
		if d.Action == "import" {
			row["start"], row["end"] = d.Start.UTC().Format(isoMillis), d.End.UTC().Format(isoMillis)
			if !dry {
				ok, err := a.importEntry(ctx, uid, d, projects)
				switch {
				case errors.Is(err, errCovered):
					row["action"], row["reason"] = "skip", "schon lokal erfasst"
				case err != nil:
					var pe *pgconn.PgError
					if errors.As(err, &pe) && pe.Code == "ZP001" {
						row["action"], row["reason"] = "skip", "Monat geschlossen, eingereicht oder freigegeben"
					} else {
						row["action"], row["reason"] = "skip", "Fehler: "+shortErr(err)
					}
				case !ok:
					row["action"], row["reason"] = "skip", "schon importiert"
				}
			}
		}
		if row["action"] == "import" {
			imported++
		} else {
			skipped[asStr(row["reason"])]++
		}
		out = append(out, row)
	}
	if !dry && imported > 0 {
		a.sysLog(ctx, uid, "success", "plane", fmt.Sprintf("Aus Plane importiert: %d Einträge (%s bis %s, Uhrzeiten ungenau)", imported, from, to))
	}
	return map[string]any{"dry_run": dry, "from": from, "to": to, "imported": imported, "skipped": skipped, "stats": stats, "errors": errs, "items": out,
		"note": "Importierte Zeiten sind ungenau (nur Dauer, Uhrzeit ab 08:00 gestapelt) und gehen weder zurück nach Plane noch nach Tempo."}, nil
}

// importProjects: nur Projekte, die als Plane-Quelle verknüpft sind
func (a *App) importProjects(ctx context.Context, uid int64) ([]importProject, error) {
	rows, err := a.DB.Query(ctx, `SELECT DISTINCT p.id, p.plane_project_id, p.name, COALESCE(NULLIF(p.plane_identifier,''), p.jira_key, ''),
		COALESCE(p.plane_workspace_id, s.workspace_id, 0)
		FROM projects p JOIN project_sources s ON s.project_id=p.id AND s.user_id=p.user_id AND s.system='plane' AND s.status='linked' AND s.gone=0
		WHERE p.user_id=$1 AND p.plane_project_id IS NOT NULL ORDER BY p.id`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []importProject
	for rows.Next() {
		var p importProject
		if err := rows.Scan(&p.ID, &p.Plane, &p.Name, &p.Ident, &p.WorkspaceID); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// fetchPlaneWorklogs: eigene Plane-Worklogs der Projekte (ohne unsere Summen/Einzel-Übertragungen), mit Ticket-Key
func (a *App) fetchPlaneWorklogs(ctx context.Context, uid int64, c planeCfg, projects []importProject, meID string, localKey func(string) string) ([]planeWL, map[string]int, []string) {
	stats := map[string]int{"projects": len(projects), "checked": 0, "other_user": 0, "own_push": 0}
	var errs []string
	var wls []planeWL
	keyOf := map[string]string{} // Work-Item → Ticket-Key
	for _, p := range projects {
		totals, err := a.planeDo(ctx, uid, c, "GET", c.wsPath()+"/projects/"+url.PathEscape(p.Plane)+"/total-worklogs/", nil, planeOpts{})
		if err != nil {
			errs = append(errs, p.Name+": "+err.Error())
			continue
		}
		for _, t := range listOf(totals) {
			if asFloat(t["duration"]) <= 0 {
				continue
			}
			wi := jsOr(asStr(t["issue_id"]), asStr(t["work_item_id"]))
			if wi == "" {
				continue
			}
			list, err := a.planeListAll(ctx, uid, c, c.wlPath(p.Plane, wi, ""), planeOpts{})
			if err != nil {
				errs = append(errs, p.Name+"/"+wi+": "+err.Error())
				continue
			}
			mine := false
			for _, x := range list {
				stats["checked"]++
				owner := ""
				for _, f := range []string{"logged_by", "logged_by_id", "created_by", "created_by_id"} {
					if owner = asStr(x[f]); owner != "" {
						break
					}
				}
				if owner == "" || owner != meID {
					stats["other_user"]++
					continue
				}
				desc := asStr(x["description"])
				if planeOwnPush(desc) {
					stats["own_push"]++
					continue
				}
				created, _ := time.Parse(time.RFC3339Nano, asStr(x["created_at"]))
				wls = append(wls, planeWL{ID: asStr(x["id"]), WI: wi, Desc: desc, Mins: int64(asFloat(x["duration"])), ProjectID: p.ID,
					Created: created, Date: planeWLDate(desc, created, a.Loc)})
				mine = true
			}
			if _, ok := keyOf[wi]; mine && !ok {
				keyOf[wi] = a.importKeyOf(ctx, uid, c, p, wi, localKey)
			}
		}
		if ctx.Err() != nil {
			break
		}
	}
	for i := range wls {
		wls[i].Key = keyOf[wls[i].WI]
	}
	return wls, stats, errs
}

// importKeyOf: Ticket-Key des Work-Items (lokal bekannt, sonst aus Plane)
func (a *App) importKeyOf(ctx context.Context, uid int64, c planeCfg, p importProject, wi string, localKey func(string) string) string {
	if localKey != nil {
		if k := localKey(wi); k != "" {
			return k
		}
	}
	r, err := a.planeDo(ctx, uid, c, "GET", c.wsPath()+"/projects/"+url.PathEscape(p.Plane)+"/work-items/"+url.PathEscape(wi)+"/", nil, planeOpts{})
	if err != nil || p.Ident == "" || asStr(jm(r)["sequence_id"]) == "" {
		return ""
	}
	return strings.ToUpper(p.Ident) + "-" + asStr(jm(r)["sequence_id"])
}

// importState: bekannte Importe, eigene Buchungen je Ticket/Tag, gesperrte Monate, schon gestapelte Minuten je Tag
func (a *App) importState(ctx context.Context, uid int64, from, to string) (map[string]string, map[string]int64, map[string]bool, map[string]int64, error) {
	known, local, locked, used := map[string]string{}, map[string]int64{}, map[string]bool{}, map[string]int64{}
	// bekannte Worklog-Ids: importiert, gelöscht (Grabstein) und alles, was wir selbst übertragen haben (unabhängig vom Text)
	rows, err := a.DB.Query(ctx, `SELECT plane_import_id, 'schon importiert' FROM time_entries WHERE user_id=$1 AND plane_import_id IS NOT NULL
		UNION ALL SELECT plane_import_id, 'gelöscht – nicht erneut importiert' FROM plane_import_ignored WHERE user_id=$1
		UNION ALL SELECT worklog_id, 'eigene Übertragung' FROM plane_ticket_sync WHERE user_id=$1 AND worklog_id IS NOT NULL
		UNION ALL SELECT plane_worklog_id, 'eigene Übertragung' FROM time_entries WHERE user_id=$1 AND plane_worklog_id IS NOT NULL
		UNION ALL SELECT worklog_id, 'eigene Übertragung (zum Löschen vorgemerkt)' FROM plane_deletions WHERE user_id=$1`, uid)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	for rows.Next() {
		var id, why string
		if rows.Scan(&id, &why) == nil && known[id] == "" {
			known[id] = why
		}
	}
	rows.Close()
	rows, err = a.DB.Query(ctx, `SELECT COALESCE(UPPER(TRIM(t.jira_ticket)),''), COALESCE(t.plane_work_item_id,''), e.date, COALESCE(SUM(e.duration_seconds),0)::bigint
		FROM time_entries e JOIN tasks t ON t.id=e.task_id AND t.user_id=e.user_id
		WHERE e.user_id=$1 AND e.date BETWEEN $2 AND $3 AND COALESCE(e.source,'manual') <> 'plane_import' AND e.end_time IS NOT NULL
		GROUP BY 1, 2, 3`, uid, from, to)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	for rows.Next() {
		var k, wi, d string
		var s int64
		if rows.Scan(&k, &wi, &d, &s) != nil {
			continue
		}
		if k != "" {
			local[k+"|"+d] += s
		}
		if wi != "" {
			local["wi:"+wi+"|"+d] += s
		}
	}
	rows.Close()
	rows, err = a.DB.Query(ctx, `SELECT month FROM month_approvals WHERE user_id=$1 AND status IN `+lockedStatusSQL, uid)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	for rows.Next() {
		var m string
		if rows.Scan(&m) == nil {
			locked[m] = true
		}
	}
	rows.Close()
	rows, err = a.DB.Query(ctx, `SELECT date, COALESCE(SUM(duration_seconds),0)::bigint/60 FROM time_entries WHERE user_id=$1 AND source='plane_import' AND date BETWEEN $2 AND $3 GROUP BY 1`, uid, from, to)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	for rows.Next() {
		var d string
		var m int64
		if rows.Scan(&d, &m) == nil {
			used[d] = m
		}
	}
	rows.Close()
	return known, local, locked, used, nil
}

// importEntry: Aufgabe sicherstellen, Eintrag anlegen; false = schon vorhanden (eindeutig je Person und Plane-Worklog)
func (a *App) importEntry(ctx context.Context, uid int64, d importDecision, projects []importProject) (bool, error) {
	var p importProject
	for _, x := range projects {
		if x.ID == d.WL.ProjectID {
			p = x
		}
	}
	var inserted bool
	err := a.inTx(ctx, func(tx pgx.Tx) error {
		// ein Import je Person zur Zeit (auch über mehrere Server-Instanzen); Abdeckung hier erneut prüfen
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, int64(7270050000000000)+uid); err != nil {
			return err
		}
		var covered bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM time_entries e JOIN tasks t ON t.id=e.task_id AND t.user_id=e.user_id
			WHERE e.user_id=$1 AND e.date=$2 AND COALESCE(e.source,'manual') <> 'plane_import' AND e.end_time IS NOT NULL
			  AND (t.plane_work_item_id=$3 OR ($4 <> '' AND UPPER(TRIM(t.jira_ticket))=$4)))`, uid, d.WL.Date, d.WL.WI, d.WL.Key).Scan(&covered); err != nil {
			return err
		}
		if covered {
			return errCovered
		}
		var task int64
		err := tx.QueryRow(ctx, `SELECT id FROM tasks WHERE user_id=$1 AND project_id=$4 AND (plane_work_item_id=$2 OR ($3 <> '' AND UPPER(TRIM(jira_ticket))=$3)) ORDER BY id LIMIT 1`,
			uid, d.WL.WI, d.WL.Key, p.ID).Scan(&task)
		if errors.Is(err, pgx.ErrNoRows) {
			err = tx.QueryRow(ctx, `INSERT INTO tasks(user_id,project_id,title,jira_ticket,status,plane_work_item_id,plane_project_id) VALUES($1,$2,$3,NULLIF($4,''),'open',$5,$6) RETURNING id`,
				uid, p.ID, jsOr(d.WL.Key, "Plane"), d.WL.Key, d.WL.WI, p.Plane).Scan(&task)
		}
		if err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `INSERT INTO time_entries(user_id,project_id,task_id,start_time,end_time,duration_seconds,comment,billable,date,source,time_precise,plane_import_id)
			VALUES($1,$2,$3,$4,$5,$6,NULLIF($7,''),1,$8,'plane_import',false,$9) ON CONFLICT (user_id, plane_import_id) WHERE plane_import_id IS NOT NULL DO NOTHING`,
			uid, p.ID, task, d.Start.UTC().Format(isoMillis), d.End.UTC().Format(isoMillis), d.WL.Mins*60, planeImportComment(d.WL.Desc), d.WL.Date, d.WL.ID)
		inserted = err == nil && tag.RowsAffected() == 1
		return err
	})
	return inserted, err
}

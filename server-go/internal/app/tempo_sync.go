package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
)

/* Tempo → lokal (nur lesen), wie backend/tempo-sync.js.
   Routen: GET/POST /api/tempo-sync. Spiegel-Buchungen source='tempo' + tempo_worklog_id.
   Eigene Übertragungen (tempo_push_id) werden nicht doppelt angelegt. Automatik: alle 12 h, letzte 2 Monate. */

type tempoSyncRun struct {
	phase string
	done  int
	total int
}

var (
	tempoSyncMu   sync.Mutex
	tempoSyncRuns = map[int64]*tempoSyncRun{}
)

func (a *App) routesTempoSync(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/tempo-sync", a.tempoGuard(a.tempoSyncStatus))
	mux.HandleFunc("POST /api/tempo-sync", a.tempoGuard(a.tempoSyncPost))
}

func (a *App) startTempoSyncCron(ctx context.Context) {
	go func() {
		t := time.NewTicker(15 * time.Minute)
		defer t.Stop()
		for {
			a.safely("tempo-abgleich", func() { a.tempoSyncTick(ctx) })
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
}

func (a *App) tempoSyncTick(ctx context.Context) {
	if a.DB == nil || !a.tempoModuleOn(ctx) {
		return
	}
	rows, err := a.DB.Query(ctx, `SELECT u.id FROM users u
		WHERE NOT u.disabled AND u.role<>'breakglass' AND NOT (u.kind='local' AND NOT u.productive)
		  AND EXISTS (SELECT 1 FROM user_settings s WHERE s.user_id=u.id AND s.key='tempo_sync_from' AND s.value<>'')`)
	if err != nil {
		return
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	today := a.today()
	for _, uid := range ids {
		st, _ := a.userSettings(ctx, uid)
		from := st["tempo_sync_from"]
		if !isoDateRE.MatchString(from) {
			continue
		}
		if last := st["tempo_sync_last"]; last != "" {
			if t, err := time.Parse(time.RFC3339, last); err == nil && time.Since(t) < 12*time.Hour {
				continue
			}
		}
		// Fenster: max(tempo_sync_from, 1. des Vormonats) … heute
		win := firstOfPrevMonth(today)
		if from > win {
			win = from
		}
		_, err := a.tempoSyncDo(ctx, uid, win, today, false, false, true)
		if err != nil && !errors.Is(err, errTempoSyncBusy) {
			a.sysLog(ctx, uid, "warn", "tempo-abgleich", "Automatischer Abgleich: "+err.Error())
		}
	}
}

func firstOfPrevMonth(today string) string {
	t, ok := parseDay(today)
	if !ok {
		return today
	}
	d := time.Date(t.Year(), t.Month()-1, 1, 12, 0, 0, 0, time.UTC)
	return dayStr(d)
}

var errTempoSyncBusy = errors.New("Abgleich läuft bereits")

func (a *App) tempoSyncStatus(w http.ResponseWriter, r *http.Request, u User) {
	ctx := r.Context()
	st, err := a.userSettings(ctx, u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	var n int
	var hours float64
	var first, latest *string
	_ = a.DB.QueryRow(ctx, `SELECT COUNT(*), COALESCE(SUM(duration_seconds),0)/3600.0, MIN(date), MAX(date)
		FROM time_entries WHERE user_id=$1 AND source='tempo'`, u.ID).Scan(&n, &hours, &first, &latest)
	setup, _, _ := a.tempoSetup(ctx, u.ID)
	connected := false
	if setup != nil {
		for _, i := range setup.usable() {
			if i.Tempo == "dc" || i.Tempo == "cloud" {
				connected = true
				break
			}
		}
	}
	tempoSyncMu.Lock()
	run := tempoSyncRuns[u.ID]
	tempoSyncMu.Unlock()
	var running any
	if run != nil {
		running = map[string]any{"phase": run.phase, "done": run.done, "total": run.total}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"from": st["tempo_sync_from"], "last": st["tempo_sync_last"], "running": running,
		"entries": n, "first": first, "latest": latest, "hours": round1(hours),
		"since": st["tempo_push_since"], "tracking_start": st["tracking_start_date"], "connected": connected,
	})
}

func (a *App) tempoSyncPost(w http.ResponseWriter, r *http.Request, u User) {
	var in body
	if !readJSON(w, r, &in) {
		return
	}
	ctx := r.Context()
	st, err := a.userSettings(ctx, u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	from := asStr(in["from"])
	if !isoDateRE.MatchString(from) {
		from = st["tempo_sync_from"]
	}
	if !isoDateRE.MatchString(from) {
		badRequest(w, "Bitte angeben, ab wann (z. B. Eintrittsdatum)")
		return
	}
	to := a.today()
	dry := asBool(in["dry"])
	replace := asBool(in["replace_old"])
	res, err := a.tempoSyncDo(ctx, u.ID, from, to, dry, replace, false)
	if err != nil {
		if errors.Is(err, errTempoSyncBusy) {
			writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error()})
			return
		}
		var je *jiraErr
		if errors.As(err, &je) {
			a.jiraFail(w, err)
			return
		}
		badRequest(w, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func asBool(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case string:
		s := strings.ToLower(strings.TrimSpace(x))
		return s == "1" || s == "true" || s == "yes" || s == "on"
	case float64:
		return x != 0
	case int:
		return x != 0
	case int64:
		return x != 0
	}
	return false
}

func round1(f float64) float64 { return float64(int(f*10+0.5)) / 10 }

func (a *App) tempoSyncDo(ctx context.Context, uid int64, from, to string, dry, replaceOld, quiet bool) (map[string]any, error) {
	tempoSyncMu.Lock()
	if tempoSyncRuns[uid] != nil {
		tempoSyncMu.Unlock()
		return nil, errTempoSyncBusy
	}
	tempoSyncRuns[uid] = &tempoSyncRun{phase: "lesen"}
	tempoSyncMu.Unlock()
	defer func() {
		tempoSyncMu.Lock()
		delete(tempoSyncRuns, uid)
		tempoSyncMu.Unlock()
	}()

	setup, _, err := a.tempoSetup(ctx, uid)
	if err != nil {
		return nil, err
	}
	var ti *tempoInst
	for _, i := range setup.usable() {
		if i.Tempo == "dc" || i.Tempo == "cloud" {
			ti = i
			break
		}
	}
	if ti == nil {
		return nil, errors.New("Jira/Tempo ist nicht verbunden")
	}
	worker := strings.TrimSpace(ti.C.WorkerKey)
	if worker == "" {
		worker = strings.TrimSpace(ti.C.Worker)
	}
	if worker == "" {
		me, err := a.jiraDo(ctx, uid, ti.C.jira(), "GET", "/rest/api/2/myself", nil)
		if err != nil {
			return nil, err
		}
		m := jm(me)
		worker = asStr(m["key"])
		if worker == "" {
			worker = asStr(m["name"])
		}
		if worker != "" {
			_ = a.setUserSetting(ctx, a.DB, uid, "jira_userkey", worker)
		}
	}
	if worker == "" {
		return nil, errors.New("Eigener Jira-Benutzer nicht ermittelbar")
	}

	stUsr, _ := a.userSettings(ctx, uid)
	since := stUsr["tempo_push_since"]
	if !isoDateRE.MatchString(since) {
		since = ""
	}

	out := map[string]any{
		"from": from, "to": to, "worklogs": 0, "hours": 0.0, "new": 0, "changed": 0, "removed": 0,
		"own_skipped": 0, "foreign_skipped": 0, "projects_new": []string{}, "years": map[string]float64{},
		"old_local": 0, "old_local_h": 0.0, "holidays": 0, "errors": []string{}, "since": since,
	}
	ms := monthChunks(from, to)
	tempoSyncMu.Lock()
	if r := tempoSyncRuns[uid]; r != nil {
		r.total = len(ms)
	}
	tempoSyncMu.Unlock()

	own := map[string]bool{}
	rows, err := a.DB.Query(ctx, `SELECT tempo_push_id FROM time_entries WHERE user_id=$1 AND tempo_push_id IS NOT NULL`, uid)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			own[id] = true
		}
	}
	rows.Close()

	seen := map[int64]bool{}
	var complete [][2]string
	newProjects := map[string]bool{}
	years := map[string]float64{}
	var hours float64
	var nNew, nChanged, nRemoved, nOwn, nForeign int
	var errs []string

	cursor := map[string]int{} // Datum → nächste Startminute (wenn Tempo 00:00 liefert)

	for _, ab := range ms {
		list, err := a.jiraDo(ctx, uid, ti.C.jira(), "POST", "/rest/tempo-timesheets/4/worklogs/search",
			map[string]any{"from": ab[0], "to": ab[1], "worker": []string{worker}})
		tempoSyncMu.Lock()
		if r := tempoSyncRuns[uid]; r != nil {
			r.done++
		}
		tempoSyncMu.Unlock()
		if err != nil {
			errs = append(errs, ab[0][:7]+": "+err.Error())
			continue
		}
		complete = append(complete, ab)
		arr, _ := list.([]any)
		type wl struct {
			id, secs                                                             int64
			date, time, key, summary, comment, account, worker                   string
		}
		var wls []wl
		for _, raw := range arr {
			m := jm(raw)
			id := int64(asFloat(m["tempoWorklogId"]))
			if id == 0 {
				id = int64(asFloat(m["id"]))
			}
			st := strings.ReplaceAll(asStr(coalesceAny(m["started"], m["dateStarted"])), " ", "T")
			date, tim := st, "00:00:00"
			if len(st) >= 10 {
				date = st[:10]
			}
			if len(st) >= 19 {
				tim = st[11:19]
			} else if len(st) >= 16 {
				tim = st[11:16] + ":00"
			}
			iss := jm(m["issue"])
			key := strings.ToUpper(asStr(coalesceAny(iss["key"], m["originTaskId"])))
			acc := ""
			if attrs := jm(m["attributes"]); attrs != nil {
				if a := jm(attrs["_Account_"]); a != nil {
					acc = asStr(a["value"])
				}
			}
			wk := asStr(coalesceAny(m["worker"], jm(m["author"])["key"], jm(m["author"])["name"]))
			wls = append(wls, wl{id: id, secs: int64(asFloat(m["timeSpentSeconds"])), date: date, time: tim, key: key,
				summary: asStr(iss["summary"]), comment: asStr(m["comment"]), account: acc, worker: wk})
		}
		sort.Slice(wls, func(i, j int) bool { return wls[i].date+wls[i].time < wls[j].date+wls[j].time })

		for _, w := range wls {
			if w.id == 0 || w.date == "" || w.secs <= 0 {
				continue
			}
			if w.worker != "" && w.worker != worker && !strings.EqualFold(w.worker, ti.C.Worker) {
				nForeign++
				continue
			}
			if seen[w.id] {
				continue
			}
			seen[w.id] = true
			if own[strconv.FormatInt(w.id, 10)] {
				nOwn++
				continue
			}
			hours += float64(w.secs) / 3600
			years[w.date[:4]] += float64(w.secs) / 3600
			out["worklogs"] = asInt(out["worklogs"]) + 1

			tim := w.time
			if tim == "00:00:00" {
				m := cursor[w.date]
				if m == 0 {
					m = 8 * 60
				}
				tim = fmt.Sprintf("%02d:%02d:00", m/60, m%60)
				cursor[w.date] = m + int((w.secs+30)/60)
			}
			startLocal := w.date + "T" + tim
			startT, ok := parseTimeIn(startLocal, a.Loc)
			if !ok {
				continue
			}
			endT := startT.Add(time.Duration(w.secs) * time.Second)
			startISO := startT.UTC().Format(time.RFC3339Nano)
			endISO := endT.UTC().Format(time.RFC3339Nano)

			if dry {
				var ex int
				_ = a.DB.QueryRow(ctx, `SELECT 1 FROM time_entries WHERE user_id=$1 AND tempo_worklog_id=$2`, uid, w.id).Scan(&ex)
				if ex == 0 {
					nNew++
				}
				if pkey := jiraProject(w.key); pkey != "" && a.projectByJiraKey(ctx, uid, pkey) == nil {
					newProjects[pkey] = true
				}
				continue
			}

			pkey := jiraProject(w.key)
			pid, tid, pname, err := a.tempoSyncResolve(ctx, uid, ti, pkey, w.key, w.summary)
			if err != nil {
				errs = append(errs, w.key+": "+err.Error())
				continue
			}
			if pname != "" {
				newProjects[pname] = true
			}
			var exID int64
			var exDate string
			var exDur int64
			var exComment *string
			var exTask, exAcc any
			err = a.DB.QueryRow(ctx, `SELECT id, date, duration_seconds, comment, task_id, tempo_account FROM time_entries
				WHERE user_id=$1 AND tempo_worklog_id=$2`, uid, w.id).Scan(&exID, &exDate, &exDur, &exComment, &exTask, &exAcc)
			if err == nil {
				com := ""
				if exComment != nil {
					com = *exComment
				}
				sameTask := (tid == nil && exTask == nil) || (tid != nil && exTask != nil && fmt.Sprint(exTask) == fmt.Sprint(tid))
				sameAcc := asStr(exAcc) == w.account
				if exDate != w.date || exDur != w.secs || com != w.comment || !sameTask || !sameAcc {
					_, err = a.DB.Exec(ctx, `UPDATE time_entries SET project_id=$3, task_id=$4, date=$5, start_time=$6, end_time=$7,
						duration_seconds=$8, comment=$9, tempo_account=$10 WHERE id=$1 AND user_id=$2`,
						exID, uid, pid, tid, w.date, startISO, endISO, w.secs, nz(w.comment), nz(w.account))
					if err != nil {
						errs = append(errs, fmt.Sprintf("#%d: %v", w.id, err))
					} else {
						nChanged++
					}
				}
			} else if errors.Is(err, pgx.ErrNoRows) {
				_, err = a.DB.Exec(ctx, `INSERT INTO time_entries(user_id,project_id,task_id,start_time,end_time,duration_seconds,comment,billable,date,source,tempo_worklog_id,tempo_account)
					VALUES($1,$2,$3,$4,$5,$6,$7,1,$8,'tempo',$9,$10)`,
					uid, pid, tid, startISO, endISO, w.secs, nz(w.comment), w.date, w.id, nz(w.account))
				if err != nil {
					errs = append(errs, fmt.Sprintf("insert %d: %v", w.id, err))
				} else {
					nNew++
				}
			} else {
				return nil, err
			}
		}
	}

	// Gelöschte Tempo-Worklogs entfernen (nur vollständige Monate)
	if !dry {
		for _, ab := range complete {
			rs, err := a.maps(ctx, `SELECT id, tempo_worklog_id FROM time_entries WHERE user_id=$1 AND source='tempo' AND date BETWEEN $2 AND $3`, uid, ab[0], ab[1])
			if err != nil {
				return nil, err
			}
			for _, row := range rs {
				wid := int64(asFloat(row["tempo_worklog_id"]))
				if wid > 0 && !seen[wid] {
					if _, err := a.DB.Exec(ctx, `DELETE FROM time_entries WHERE id=$1 AND user_id=$2 AND source='tempo'`, asID(row["id"]), uid); err == nil {
						nRemoved++
					}
				}
			}
		}
	}

	// Grundimport: alte lokale vor since
	if since != "" {
		var n int
		var s float64
		_ = a.DB.QueryRow(ctx, `SELECT COUNT(*), COALESCE(SUM(duration_seconds),0)/3600.0 FROM time_entries
			WHERE user_id=$1 AND date>=$2 AND date<$3 AND COALESCE(source,'manual') NOT IN ('tempo') AND tempo_push_id IS NULL`,
			uid, from, since).Scan(&n, &s)
		out["old_local"] = n
		out["old_local_h"] = round1(s)
		if replaceOld && !dry && n > 0 {
			_, _ = a.DB.Exec(ctx, `DELETE FROM time_entries WHERE user_id=$1 AND date>=$2 AND date<$3
				AND COALESCE(source,'manual') NOT IN ('tempo') AND tempo_push_id IS NULL`, uid, from, since)
			a.sysLog(ctx, uid, "warn", "tempo-abgleich", fmt.Sprintf("Grundimport: %d alte lokale Buchungen vor %s durch Tempo ersetzt", n, since))
		}
	}

	holidays := 0
	if !dry {
		holidays = a.ensureNIHolidays(ctx, uid, from, to)
		ts := stUsr["tracking_start_date"]
		if ts == "" || ts > from {
			_ = a.setUserSetting(ctx, a.DB, uid, "tracking_start_date", from)
		}
		prevFrom := stUsr["tempo_sync_from"]
		if prevFrom == "" || from < prevFrom {
			_ = a.setUserSetting(ctx, a.DB, uid, "tempo_sync_from", from)
		}
		_ = a.setUserSetting(ctx, a.DB, uid, "tempo_sync_last", time.Now().UTC().Format(time.RFC3339))
		if !quiet || nNew > 0 || nChanged > 0 || nRemoved > 0 {
			a.sysLog(ctx, uid, "info", "tempo-abgleich",
				fmt.Sprintf("Abgleich %s – %s: %d neu, %d geändert, %d entfernt, %d eigene Übertragungen übersprungen",
					from, to, nNew, nChanged, nRemoved, nOwn))
		}
	}

	yp := map[string]float64{}
	for y, h := range years {
		yp[y] = round1(h)
	}
	var pn []string
	for k := range newProjects {
		pn = append(pn, k)
	}
	sort.Strings(pn)
	out["hours"] = round1(hours)
	out["new"] = nNew
	out["changed"] = nChanged
	out["removed"] = nRemoved
	out["own_skipped"] = nOwn
	out["foreign_skipped"] = nForeign
	out["years"] = yp
	out["projects_new"] = pn
	out["holidays"] = holidays
	out["errors"] = errs
	return out, nil
}

func coalesceAny(vals ...any) any {
	for _, v := range vals {
		if asStr(v) != "" {
			return v
		}
	}
	return ""
}

func monthChunks(from, to string) [][2]string {
	f, ok1 := parseDay(from)
	t, ok2 := parseDay(to)
	if !ok1 || !ok2 || t.Before(f) {
		return nil
	}
	var out [][2]string
	for d := time.Date(f.Year(), f.Month(), 1, 12, 0, 0, 0, time.UTC); !d.After(t); d = d.AddDate(0, 1, 0) {
		start := dayStr(d)
		if start < from {
			start = from
		}
		end := dayStr(d.AddDate(0, 1, -1))
		if end > to {
			end = to
		}
		out = append(out, [2]string{start, end})
	}
	return out
}

// tempoSyncResolve: Projekt/Aufgabe für Jira-Key; legt archiviertes Projekt an falls nötig.
func (a *App) tempoSyncResolve(ctx context.Context, uid int64, ti *tempoInst, pkey, ticket, summary string) (pid, tid any, newName string, err error) {
	if pkey == "" {
		return nil, nil, "", nil
	}
	if v := a.projectByJiraKey(ctx, uid, pkey); v != nil {
		pid = v
	} else {
		name := pkey
		if pr, e := a.jiraDo(ctx, uid, ti.C.jira(), "GET", "/rest/api/2/project/"+url.PathEscape(pkey), nil); e == nil {
			if n := asStr(jm(pr)["name"]); n != "" {
				name = n
			}
		}
		var existing int64
		e := a.DB.QueryRow(ctx, `SELECT id FROM projects WHERE user_id=$1 AND name=$2`, uid, name).Scan(&existing)
		if e == nil {
			pid = existing
		} else {
			e = a.DB.QueryRow(ctx, `INSERT INTO projects(user_id,name,status,jira_key) VALUES($1,$2,'archived',$3) RETURNING id`, uid, name, pkey).Scan(&existing)
			if e != nil {
				return nil, nil, "", e
			}
			pid = existing
			newName = fmt.Sprintf("%s (%s)", name, pkey)
		}
		_, _ = a.DB.Exec(ctx, `INSERT INTO project_sources(user_id,system,external_id,key,name,project_id,status)
			VALUES($1,'jira',$2,$2,$3,$4,'linked') ON CONFLICT (user_id,system,external_id) DO NOTHING`, uid, pkey, name, pid)
	}
	ticket = strings.ToUpper(strings.TrimSpace(ticket))
	if !jiraKeyRE.MatchString(ticket) {
		return pid, nil, newName, nil
	}
	title := summary
	if title == "" {
		title = ticket
	}
	var taskID int64
	e := a.DB.QueryRow(ctx, `SELECT id FROM tasks WHERE user_id=$1 AND project_id=$2 AND UPPER(TRIM(jira_ticket))=$3`, uid, pid, ticket).Scan(&taskID)
	if e == nil {
		_, _ = a.DB.Exec(ctx, `UPDATE tasks SET title=$3 WHERE id=$1 AND user_id=$2 AND (title IS NULL OR title='' OR UPPER(title)=$4)`,
			taskID, uid, truncRunes(title, 255), ticket)
		return pid, taskID, newName, nil
	}
	e = a.DB.QueryRow(ctx, `INSERT INTO tasks(user_id,project_id,title,jira_ticket,status) VALUES($1,$2,$3,$4,'done') RETURNING id`,
		uid, pid, truncRunes(title, 255), ticket).Scan(&taskID)
	if e != nil {
		return pid, nil, newName, e
	}
	return pid, taskID, newName, nil
}

func (a *App) ensureNIHolidays(ctx context.Context, uid int64, from, to string) int {
	n := 0
	for _, h := range niHolidays {
		if h.date < from || h.date > to {
			continue
		}
		var exists int
		_ = a.DB.QueryRow(ctx, `SELECT 1 FROM absences WHERE user_id=$1 AND date_from=$2 AND type='feiertag'`, uid, h.date).Scan(&exists)
		if exists == 1 {
			continue
		}
		if _, err := a.DB.Exec(ctx, `INSERT INTO absences(user_id,date_from,date_to,type,note) VALUES($1,$2,$2,'feiertag',$3)`, uid, h.date, h.name); err == nil {
			n++
		}
	}
	return n
}

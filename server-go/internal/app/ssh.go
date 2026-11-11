package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

/* SSH-Sitzungen – Parität zu backend/server.js (ssh_sessions) + ssh-watch.
   Erkennung bleibt am Mac (zsh-Log); der Client synct Sitzungen an den Server. */

const sshMaxOpenS = 24 * 3600

func (a *App) routesSSH(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/ssh-sessions", a.auth(a.sshList))
	mux.HandleFunc("POST /api/ssh-sessions/sync", a.auth(a.sshSync))
	mux.HandleFunc("PUT /api/ssh-sessions/{id}", a.auth(a.sshUpdate))
	mux.HandleFunc("DELETE /api/ssh-sessions/{id}", a.auth(a.sshDelete))
	mux.HandleFunc("POST /api/ssh-sessions/to-entries", a.auth(a.sshToEntries))
	mux.HandleFunc("GET /api/ssh-tracking", a.auth(a.sshTrackingGet))
	mux.HandleFunc("POST /api/ssh-tracking", a.auth(a.sshTrackingPost))
}

func sshEffective(r map[string]any, nowMs int64) int {
	open := asStr(r["end_time"]) == ""
	startMs := DateParseMs(asStr(r["start_time"]))
	age := 0.0
	if startMs > 0 {
		age = float64(nowMs-startMs) / 1000
	}
	samples := int(asFloat(r["samples"]))
	if samples > 0 {
		act := asFloat(r["active_seconds"])
		cap := age
		if !open {
			cap = asFloat(r["duration_seconds"])
			if cap <= 0 {
				cap = act
			}
		}
		if act > cap {
			act = cap
		}
		if act < 0 {
			act = 0
		}
		return int(act)
	}
	if open {
		if age > sshMaxOpenS {
			return 0
		}
		return int(age + 0.5)
	}
	return int(asFloat(r["duration_seconds"]))
}

func (a *App) sshTrackingGet(w http.ResponseWriter, r *http.Request, u User) {
	st, err := a.userSettings(r.Context(), u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled":    st["ssh_watch_enabled"] != "0",
		"log_exists": st["ssh_log_exists"] == "1",
	})
}

func (a *App) sshTrackingPost(w http.ResponseWriter, r *http.Request, u User) {
	var in body
	if !readJSON(w, r, &in) {
		return
	}
	en := "1"
	if _, ok := in["enabled"]; ok && !truthy(in["enabled"]) {
		en = "0"
	}
	if _, err := a.DB.Exec(r.Context(), `INSERT INTO user_settings(user_id,key,value) VALUES($1,'ssh_watch_enabled',$2)
		ON CONFLICT (user_id,key) DO UPDATE SET value=EXCLUDED.value`, u.ID, en); err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"enabled": en == "1"})
}

func (a *App) sshSync(w http.ResponseWriter, r *http.Request, u User) {
	var in struct {
		Sessions  []body `json:"sessions"`
		LogExists bool   `json:"log_exists"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if len(in.Sessions) > 500 {
		badRequest(w, "höchstens 500 Sitzungen je Sync")
		return
	}
	ctx := r.Context()
	logFlag := "0"
	if in.LogExists {
		logFlag = "1"
	}
	_, _ = a.DB.Exec(ctx, `INSERT INTO user_settings(user_id,key,value) VALUES($1,'ssh_log_exists',$2)
		ON CONFLICT (user_id,key) DO UPDATE SET value=EXCLUDED.value`, u.ID, logFlag)

	upserted := 0
	err := a.inTx(ctx, func(tx pgx.Tx) error {
		for _, s := range in.Sessions {
			uid := asStr(s["session_uid"])
			start := asStr(s["start_time"])
			if uid == "" || start == "" {
				continue
			}
			date := asStr(s["date"])
			if date == "" {
				date = a.dateOf(start)
			}
			var port any
			if p := asFloat(s["port"]); p > 0 {
				port = int(p)
			}
			tag, err := tx.Exec(ctx, `INSERT INTO ssh_sessions(
				user_id,session_uid,date,start_time,end_time,duration_seconds,active_seconds,samples,
				last_seen,last_active,end_reason,target,hostname,username,port,remote_cmd,exit_code)
				VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)
				ON CONFLICT (user_id,session_uid) DO UPDATE SET
					date=EXCLUDED.date, start_time=EXCLUDED.start_time,
					end_time=COALESCE(EXCLUDED.end_time, ssh_sessions.end_time),
					duration_seconds=COALESCE(EXCLUDED.duration_seconds, ssh_sessions.duration_seconds),
					active_seconds=COALESCE(EXCLUDED.active_seconds, ssh_sessions.active_seconds),
					samples=GREATEST(EXCLUDED.samples, ssh_sessions.samples),
					last_seen=COALESCE(EXCLUDED.last_seen, ssh_sessions.last_seen),
					last_active=COALESCE(EXCLUDED.last_active, ssh_sessions.last_active),
					end_reason=COALESCE(EXCLUDED.end_reason, ssh_sessions.end_reason),
					target=COALESCE(NULLIF(EXCLUDED.target,''), ssh_sessions.target),
					hostname=COALESCE(NULLIF(EXCLUDED.hostname,''), ssh_sessions.hostname),
					username=COALESCE(NULLIF(EXCLUDED.username,''), ssh_sessions.username),
					port=COALESCE(EXCLUDED.port, ssh_sessions.port),
					remote_cmd=COALESCE(NULLIF(EXCLUDED.remote_cmd,''), ssh_sessions.remote_cmd),
					exit_code=COALESCE(EXCLUDED.exit_code, ssh_sessions.exit_code)`,
				u.ID, uid, date, start, nz(s["end_time"]), nullInt(s["duration_seconds"]), nullInt(s["active_seconds"]),
				int(asFloat(s["samples"])), nz(s["last_seen"]), nz(s["last_active"]), nz(s["end_reason"]),
				asStr(s["target"]), asStr(s["hostname"]), asStr(s["username"]), port, asStr(s["remote_cmd"]), nullInt(s["exit_code"]))
			if err != nil {
				return err
			}
			if tag.RowsAffected() > 0 {
				upserted++
			}
		}
		return nil
	})
	if err != nil {
		a.fail(w, err)
		return
	}
	n, _ := a.autoAssignSSH(ctx, u.ID)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "upserted": upserted, "assigned": n})
}

func nullInt(v any) any {
	if v == nil {
		return nil
	}
	if s := asStr(v); s == "" {
		return nil
	}
	return int64(asFloat(v))
}

func (a *App) autoAssignSSH(ctx context.Context, uid int64) (int, error) {
	rows, err := a.maps(ctx, `SELECT id, start_time FROM ssh_sessions WHERE user_id=$1 AND project_id IS NULL AND assign_source IS NULL`, uid)
	if err != nil {
		return 0, err
	}
	n := 0
	timer, _ := a.one(ctx, `SELECT * FROM timer_state WHERE user_id=$1`, uid)
	for _, s := range rows {
		hit, _ := a.one(ctx, `SELECT project_id, task_id FROM time_entries
			WHERE user_id=$1 AND project_id IS NOT NULL AND start_time<=$2 AND end_time>$2 ORDER BY start_time DESC LIMIT 1`, uid, s["start_time"])
		if hit == nil && timer != nil && asStr(timer["start_time"]) != "" && timer["project_id"] != nil && asStr(timer["start_time"]) <= asStr(s["start_time"]) {
			hit = map[string]any{"project_id": timer["project_id"], "task_id": timer["task_id"]}
		}
		if hit == nil {
			continue
		}
		if _, err := a.DB.Exec(ctx, `UPDATE ssh_sessions SET project_id=$1, task_id=$2, assign_source='timer' WHERE id=$3 AND user_id=$4`,
			hit["project_id"], hit["task_id"], s["id"], uid); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

func (a *App) sshList(w http.ResponseWriter, r *http.Request, u User) {
	q := r.URL.Query()
	today := a.today()
	from, to := q.Get("from"), q.Get("to")
	if !isoDateRE.MatchString(from) {
		from = today[:8] + "01"
	}
	if !isoDateRE.MatchString(to) {
		to = today
	}
	ctx := r.Context()
	rows, err := a.maps(ctx, `SELECT s.*, p.name AS project_name, p.jira_key AS project_key,
		t.title AS task_title, t.jira_ticket AS task_ticket
		FROM ssh_sessions s
		LEFT JOIN projects p ON p.id=s.project_id
		LEFT JOIN tasks t ON t.id=s.task_id
		WHERE s.user_id=$1 AND s.date BETWEEN $2 AND $3 ORDER BY s.start_time`, u.ID, from, to)
	if err != nil {
		a.fail(w, err)
		return
	}
	nowMs := time.Now().UnixMilli()
	sessions := make([]map[string]any, 0, len(rows))
	total := 0
	for _, row := range rows {
		open := asStr(row["end_time"]) == ""
		age := 0.0
		if s := DateParseMs(asStr(row["start_time"])); s > 0 {
			age = float64(nowMs-s) / 1000
		}
		status := "closed"
		window := int(asFloat(row["duration_seconds"]))
		if open {
			if age > sshMaxOpenS {
				status, window = "unknown", 0
			} else {
				status, window = "open", int(age+0.5)
			}
		}
		eff := sshEffective(row, nowMs)
		total += eff
		server := asStr(row["hostname"])
		if server == "" {
			server = asStr(row["target"])
		}
		row["status"] = status
		row["effective_seconds"] = eff
		row["window_seconds"] = window
		row["measured"] = asFloat(row["samples"]) > 0
		row["server"] = server
		sessions = append(sessions, row)
	}
	st, _ := a.userSettings(ctx, u.ID)
	writeJSON(w, http.StatusOK, map[string]any{
		"from": from, "to": to, "log_exists": st["ssh_log_exists"] == "1",
		"sessions": sessions, "byHost": sshAgg(sessions, func(s map[string]any) string { return asStr(s["server"]) }),
		"byDay": sshAgg(sessions, func(s map[string]any) string { return asStr(s["date"]) }),
		"byProject": sshAgg(sessions, func(s map[string]any) string {
			if n := asStr(s["project_name"]); n != "" {
				return n
			}
			return "(ohne Projekt)"
		}),
		"total_seconds": total,
	})
}

func sshAgg(rows []map[string]any, keyFn func(map[string]any) string) []map[string]any {
	type agg struct {
		key, first, last string
		count, seconds   int
		servers          map[string]bool
	}
	m := map[string]*agg{}
	var order []string
	for _, r := range rows {
		k := keyFn(r)
		a := m[k]
		if a == nil {
			a = &agg{key: k, first: asStr(r["start_time"]), last: firstNonEmptyStr(asStr(r["end_time"]), asStr(r["start_time"])), servers: map[string]bool{}}
			m[k] = a
			order = append(order, k)
		}
		a.count++
		a.seconds += int(asFloat(r["effective_seconds"]))
		if asStr(r["start_time"]) < a.first {
			a.first = asStr(r["start_time"])
		}
		e := firstNonEmptyStr(asStr(r["end_time"]), asStr(r["start_time"]))
		if e > a.last {
			a.last = e
		}
		if s := asStr(r["server"]); s != "" {
			a.servers[s] = true
		}
	}
	out := make([]map[string]any, 0, len(order))
	for _, k := range order {
		a := m[k]
		servers := make([]string, 0, len(a.servers))
		for s := range a.servers {
			servers = append(servers, s)
		}
		sort.Strings(servers)
		out = append(out, map[string]any{"key": a.key, "count": a.count, "seconds": a.seconds, "first": a.first, "last": a.last, "servers": servers})
	}
	sort.SliceStable(out, func(i, j int) bool { return asFloat(out[i]["seconds"]) > asFloat(out[j]["seconds"]) })
	return out
}

func firstNonEmptyStr(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func (a *App) sshUpdate(w http.ResponseWriter, r *http.Request, u User) {
	id, ok := pathID(r)
	if !ok {
		notFound(w)
		return
	}
	var in body
	if !readJSON(w, r, &in) {
		return
	}
	ctx := r.Context()
	sets, args := []string{}, []any{}
	add := func(col string, v any) {
		args = append(args, v)
		sets = append(sets, fmt.Sprintf("%s=$%d", col, len(args)))
	}
	if _, has := in["note"]; has {
		add("note", nz(in["note"]))
	}
	if _, has := in["project_id"]; has {
		if invalidID(in["project_id"]) {
			badRequest(w, "project_id ungültig")
			return
		}
		pid := asID(in["project_id"])
		if pid != nil && !a.owned(ctx, "projects", pid, u.ID) {
			badRequest(w, "Projekt nicht gefunden")
			return
		}
		add("project_id", pid)
		if pid != nil {
			add("assign_source", "manual")
		} else {
			add("assign_source", nil)
		}
	}
	if _, has := in["task_id"]; has {
		if invalidID(in["task_id"]) {
			badRequest(w, "task_id ungültig")
			return
		}
		tid := asID(in["task_id"])
		if tid != nil && !a.owned(ctx, "tasks", tid, u.ID) {
			badRequest(w, "Ticket nicht gefunden")
			return
		}
		add("task_id", tid)
	}
	if len(sets) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	args = append(args, id, u.ID)
	tag, err := a.DB.Exec(ctx, `UPDATE ssh_sessions SET `+strings.Join(sets, ",")+fmt.Sprintf(` WHERE id=$%d AND user_id=$%d`, len(args)-1, len(args)), args...)
	if err != nil {
		a.fail(w, err)
		return
	}
	if tag.RowsAffected() == 0 {
		notFound(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *App) sshDelete(w http.ResponseWriter, r *http.Request, u User) {
	id, ok := pathID(r)
	if !ok {
		notFound(w)
		return
	}
	tag, err := a.DB.Exec(r.Context(), `DELETE FROM ssh_sessions WHERE id=$1 AND user_id=$2`, id, u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	if tag.RowsAffected() == 0 {
		notFound(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *App) sshToEntries(w http.ResponseWriter, r *http.Request, u User) {
	var in body
	if !readJSON(w, r, &in) {
		return
	}
	rawIDs, _ := in["ids"].([]any)
	ids := make([]int64, 0, len(rawIDs))
	for _, x := range rawIDs {
		if id := int64(asFloat(x)); id > 0 {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		badRequest(w, "ids erforderlich")
		return
	}
	ctx := r.Context()
	merge := truthy(in["merge"])
	created, skipped := []int64{}, []int64{}
	wr := selfWriter(u)
	nowMs := time.Now().UnixMilli()

	bookOne := func(tx pgx.Tx, s map[string]any, pid, tid any, comment string) (int64, error) {
		eff := sshEffective(s, nowMs)
		if eff < 1 {
			eff = 1
		}
		start := asStr(s["start_time"])
		end := asStr(s["end_time"])
		if asFloat(s["samples"]) > 0 {
			if sm := DateParseMs(start); sm > 0 {
				end = time.UnixMilli(sm + int64(eff)*1000).UTC().Format(time.RFC3339Nano)
			}
		}
		date := asStr(s["date"])
		if date == "" {
			date = a.dateOf(start)
		}
		if err := a.requireLocked(ctx, tx, wr, date); err != nil {
			return 0, err
		}
		var newID int64
		if err := tx.QueryRow(ctx, `INSERT INTO time_entries(user_id,project_id,task_id,start_time,end_time,duration_seconds,comment,billable,date,source,entered_by,time_precise)
			VALUES($1,$2,$3,$4,$5,$6,$7,1,$8,'ssh',$9,true) RETURNING id`,
			u.ID, pid, tid, start, end, eff, comment, date, wr.enteredBy()).Scan(&newID); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(ctx, `UPDATE ssh_sessions SET time_entry_id=$1, project_id=$2, task_id=$3 WHERE id=$4 AND user_id=$5`,
			newID, pid, tid, s["id"], u.ID); err != nil {
			return 0, err
		}
		return newID, a.nachtragLog(ctx, tx, wr, "SSH gebucht", fmt.Sprintf("#%d", newID), date)
	}

	err := a.writeTx(ctx, wr, func(tx pgx.Tx) error {
		rows, err := a.maps(ctx, `SELECT * FROM ssh_sessions WHERE user_id=$1 AND id = ANY($2)`, u.ID, ids)
		if err != nil {
			return err
		}
		byID := map[int64]map[string]any{}
		for _, row := range rows {
			byID[int64(asFloat(row["id"]))] = row
		}
		if merge {
			list := []map[string]any{}
			for _, id := range ids {
				s := byID[id]
				if s == nil || asStr(s["end_time"]) == "" || s["time_entry_id"] != nil || asStr(s["assign_source"]) == "timer" {
					if s != nil {
						skipped = append(skipped, id)
					}
					continue
				}
				list = append(list, s)
			}
			byDay := map[string][]map[string]any{}
			for _, s := range list {
				d := asStr(s["date"])
				byDay[d] = append(byDay[d], s)
			}
			var pid0, tid0 any
			if p := asID(in["project_id"]); p != nil {
				pid0 = p
			}
			if t := asID(in["task_id"]); t != nil {
				tid0 = t
			}
			for _, ss := range byDay {
				if pid0 == nil {
					for _, s := range ss {
						if s["project_id"] != nil {
							pid0 = s["project_id"]
							break
						}
					}
				}
				if tid0 == nil && pid0 != nil {
					for _, s := range ss {
						if s["task_id"] != nil && fmt.Sprint(s["project_id"]) == fmt.Sprint(pid0) {
							tid0 = s["task_id"]
							break
						}
					}
				}
			}
			if pid0 == nil {
				return errUser("Bitte erst einer der Sitzungen ein Projekt zuordnen (oder Projekt wählen)")
			}
			if !a.owned(ctx, "projects", pid0, u.ID) || !a.owned(ctx, "tasks", tid0, u.ID) {
				return errUser("Projekt oder Ticket nicht gefunden")
			}
			for day, ss := range byDay {
				sort.Slice(ss, func(i, j int) bool { return asStr(ss[i]["start_time"]) < asStr(ss[j]["start_time"]) })
				eff := 0
				hosts := []string{}
				seen := map[string]bool{}
				for _, s := range ss {
					eff += max(1, sshEffective(s, nowMs))
					h := asStr(s["hostname"])
					if h == "" {
						h = asStr(s["target"])
					}
					if h != "" && !seen[h] {
						seen[h], hosts = true, append(hosts, h)
					}
				}
				hostTxt := strings.Join(hosts[:min(4, len(hosts))], ", ")
				if len(hosts) > 4 {
					hostTxt += fmt.Sprintf(" +%d", len(hosts)-4)
				}
				text := asStr(in["comment"])
				if text == "" {
					text = fmt.Sprintf("SSH %s (%d Sitzungen)", hostTxt, len(ss))
				}
				start := asStr(ss[0]["start_time"])
				end := time.UnixMilli(DateParseMs(start) + int64(eff)*1000).UTC().Format(time.RFC3339Nano)
				if err := a.requireLocked(ctx, tx, wr, day); err != nil {
					return err
				}
				var newID int64
				if err := tx.QueryRow(ctx, `INSERT INTO time_entries(user_id,project_id,task_id,start_time,end_time,duration_seconds,comment,billable,date,source,entered_by,time_precise)
					VALUES($1,$2,$3,$4,$5,$6,$7,1,$8,'ssh',$9,true) RETURNING id`,
					u.ID, pid0, tid0, start, end, eff, text, day, wr.enteredBy()).Scan(&newID); err != nil {
					return err
				}
				for _, s := range ss {
					if _, err := tx.Exec(ctx, `UPDATE ssh_sessions SET time_entry_id=$1, project_id=$2, task_id=$3 WHERE id=$4 AND user_id=$5`,
						newID, pid0, tid0, s["id"], u.ID); err != nil {
						return err
					}
				}
				if err := a.nachtragLog(ctx, tx, wr, "SSH gebucht", fmt.Sprintf("#%d", newID), day); err != nil {
					return err
				}
				created = append(created, newID)
			}
			return nil
		}
		for _, id := range ids {
			s := byID[id]
			if s == nil || asStr(s["end_time"]) == "" || s["time_entry_id"] != nil || asStr(s["assign_source"]) == "timer" {
				skipped = append(skipped, id)
				continue
			}
			pid := asID(in["project_id"])
			if pid == nil {
				pid = s["project_id"]
			}
			tid := asID(in["task_id"])
			if tid == nil {
				tid = s["task_id"]
			}
			if pid == nil || !a.owned(ctx, "projects", pid, u.ID) || !a.owned(ctx, "tasks", tid, u.ID) {
				skipped = append(skipped, id)
				continue
			}
			text := asStr(in["comment"])
			if text == "" {
				host := asStr(s["hostname"])
				if host == "" {
					host = asStr(s["target"])
				}
				text = "SSH " + host
				if cmd := asStr(s["remote_cmd"]); cmd != "" {
					if len(cmd) > 60 {
						cmd = cmd[:60]
					}
					text += " (" + cmd + ")"
				}
				if note := asStr(s["note"]); note != "" {
					text += " – " + note
				}
			}
			newID, err := bookOne(tx, s, pid, tid, text)
			if err != nil {
				return err
			}
			created = append(created, newID)
		}
		return nil
	})
	if err != nil {
		var ue errUser
		if errors.As(err, &ue) {
			badRequest(w, string(ue))
			return
		}
		a.writeFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "created": created, "skipped": skipped, "merged": merge})
}

// sshDaySpans: SSH-Fenster eines Tages für dayOverview / gap-suggest
func (a *App) sshDaySpans(ctx context.Context, uid int64, date string) ([]map[string]any, error) {
	rows, err := a.maps(ctx, `SELECT id, hostname, target, start_time, end_time, duration_seconds, active_seconds, samples, time_entry_id, assign_source
		FROM ssh_sessions WHERE user_id=$1 AND date=$2 AND COALESCE(assign_source,'')<>'timer' AND time_entry_id IS NULL
		ORDER BY start_time`, uid, date)
	if err != nil {
		return nil, err
	}
	nowMs := time.Now().UnixMilli()
	out := []map[string]any{}
	for _, r := range rows {
		s, okS := a.minOfDay(asStr(r["start_time"]))
		if !okS || asStr(r["end_time"]) == "" {
			continue // offene Sitzungen nicht als Buchungsvorschlag
		}
		en := s
		if v, ok := a.minOfDay(asStr(r["end_time"])); ok {
			en = v
		}
		if en-s < 10 {
			continue
		}
		server := asStr(r["hostname"])
		if server == "" {
			server = asStr(r["target"])
		}
		out = append(out, map[string]any{"id": r["id"], "server": server, "s": s, "e": en, "effective_seconds": sshEffective(r, nowMs)})
	}
	return out, nil
}

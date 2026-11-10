package app

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
)

/* Meetings & Kalender – Parität zu backend/meetings.js.
   Die Mac-App meldet Signale und Termine; der Server schlägt Buchungen vor (nie ungefragt).
   Maßstab: lokales Mac-System. */

const meetingCloseAfter = 100 * time.Second
const meetingGeneric = "Meeting"

type meetingRT struct {
	mu          sync.Mutex
	lastSignal  time.Time
	lastDiag    map[string]any
	lastCalSync map[string]any
	resync      bool
}

var meetingRuntime sync.Map // user_id → *meetingRT

func meetingStateOf(uid int64) *meetingRT {
	v, _ := meetingRuntime.LoadOrStore(uid, &meetingRT{})
	return v.(*meetingRT)
}

var (
	meetingDateRE = regexp.MustCompile(`\b\d{1,4}[./-]\d{1,2}([./-]\d{2,4})?\b`)
	meetingTimeRE = regexp.MustCompile(`\b\d{1,2}:\d{2}\b`)
	meetingWordRE = regexp.MustCompile(`[^a-zäöüß ]+`)
)

func normMeetingTitle(t string) string {
	s := strings.ToLower(t)
	s = meetingDateRE.ReplaceAllString(s, " ")
	s = meetingTimeRE.ReplaceAllString(s, " ")
	s = meetingWordRE.ReplaceAllString(s, " ")
	return strings.Join(strings.Fields(s), " ")
}

func meetingOverlapMs(aS, aE, bS, bE int64) int64 {
	lo := max(aS, bS)
	hi := min(aE, bE)
	if hi <= lo {
		return 0
	}
	return hi - lo
}

func meetingSpecificRank(app string) int {
	if app != "" && app != meetingGeneric && !strings.Contains(strings.ToLower(app), "mikrofon") {
		return 2
	}
	return 1
}

func (a *App) routesMeetings(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/meetings/config", a.auth(a.meetingsConfig))
	mux.HandleFunc("POST /api/meetings/signal", a.auth(a.meetingsSignal))
	mux.HandleFunc("POST /api/meetings/calendar", a.auth(a.meetingsCalendar))
	mux.HandleFunc("GET /api/meetings/suggestions", a.auth(a.meetingsSuggestions))
	mux.HandleFunc("POST /api/meetings/book", a.auth(a.meetingsBook))
	mux.HandleFunc("POST /api/meetings/dismiss", a.auth(a.meetingsDismiss))
	mux.HandleFunc("DELETE /api/meetings/rules/{pattern}", a.auth(a.meetingsRuleDelete))
	mux.HandleFunc("GET /api/meetings/suggest-task", a.auth(a.meetingsSuggestTask))
	mux.HandleFunc("GET /api/meetings/now", a.auth(a.meetingsNow))
	mux.HandleFunc("GET /api/meetings/events", a.auth(a.meetingsEvents))
	mux.HandleFunc("GET /api/meetings/status", a.auth(a.meetingsStatus))
	mux.HandleFunc("POST /api/meetings/resync", a.auth(a.meetingsResync))
}

type meetingCfg struct {
	detect, calendar, browser, ask bool
	exclude                        []string
	minMinutes                     int
}

func (a *App) meetingCfg(ctx context.Context, uid int64) (meetingCfg, error) {
	st, err := a.userSettings(ctx, uid)
	if err != nil {
		return meetingCfg{}, err
	}
	excl := strings.Split(strOr(st["meeting_calendar_exclude"], "Geburtstage,Birthdays,Feiertage,Holidays,Deutsche Feiertage,Siri Suggestions"), ",")
	var clean []string
	for _, x := range excl {
		x = strings.TrimSpace(x)
		if x != "" {
			clean = append(clean, strings.ToLower(x))
		}
	}
	minM, _ := strconv.Atoi(st["meeting_min_minutes"])
	if minM < 1 {
		minM = 3
	}
	if minM > 60 {
		minM = 60
	}
	return meetingCfg{
		detect: st["meeting_detect"] != "0", calendar: st["meeting_calendar"] != "0",
		browser: st["meeting_browser"] != "0", ask: st["meeting_ask"] != "0",
		exclude: clean, minMinutes: minM,
	}, nil
}

func strOr(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func (a *App) meetingsConfig(w http.ResponseWriter, r *http.Request, u User) {
	c, err := a.meetingCfg(r.Context(), u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	rt := meetingStateOf(u.ID)
	rt.mu.Lock()
	resync := rt.resync
	rt.resync = false
	rt.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"detect": c.detect, "calendar": c.calendar, "browser": c.browser, "ask": c.ask,
		"exclude": c.exclude, "resync": resync,
	})
}

func (a *App) meetingsSignal(w http.ResponseWriter, r *http.Request, u User) {
	var in body
	if !readJSON(w, r, &in) {
		return
	}
	now := time.Now()
	rt := meetingStateOf(u.ID)
	rt.mu.Lock()
	rt.lastSignal = now
	diag := map[string]any{}
	if d, ok := in["diag"].(map[string]any); ok {
		for k, v := range d {
			diag[k] = v
		}
	}
	diag["at"] = now.UTC().Format(time.RFC3339Nano)
	diag["signals"] = in["signals"]
	rt.lastDiag = diag
	rt.mu.Unlock()

	sigs, _ := in["signals"].([]any)
	if err := a.applyMeetingSignals(r.Context(), u.ID, sigs, now); err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *App) applyMeetingSignals(ctx context.Context, uid int64, signals []any, now time.Time) error {
	var bestApp string
	bestRank := 0
	for _, s := range signals {
		m, _ := s.(map[string]any)
		appName := strings.TrimSpace(asStr(m["app"]))
		if appName == "" {
			continue
		}
		if r := meetingSpecificRank(appName); r > bestRank {
			bestRank, bestApp = r, appName
		}
	}
	nowIso := now.UTC().Format(time.RFC3339Nano)
	open, err := a.one(ctx, `SELECT * FROM meeting_events WHERE user_id=$1 AND end_time IS NULL ORDER BY id DESC LIMIT 1`, uid)
	if err != nil {
		return err
	}
	if bestApp != "" {
		if open != nil {
			cur := asStr(open["app"])
			app := cur
			if meetingSpecificRank(bestApp) > meetingSpecificRank(cur) {
				app = bestApp
			}
			_, err = a.DB.Exec(ctx, `UPDATE meeting_events SET last_seen=$1, app=$2 WHERE id=$3 AND user_id=$4`, nowIso, app, open["id"], uid)
			return err
		}
		_, err = a.DB.Exec(ctx, `INSERT INTO meeting_events(user_id,app,start_time,last_seen) VALUES($1,$2,$3,$4)`, uid, bestApp, nowIso, nowIso)
		return err
	}
	if open == nil {
		return nil
	}
	last, ok := a.parseTime(asStr(open["last_seen"]))
	if !ok || now.Sub(last) <= meetingCloseAfter {
		return nil
	}
	_, err = a.DB.Exec(ctx, `UPDATE meeting_events SET end_time=$1 WHERE id=$2 AND user_id=$3`, asStr(open["last_seen"]), open["id"], uid)
	return err
}

func (a *App) meetingsCalendar(w http.ResponseWriter, r *http.Request, u User) {
	var in body
	if !readJSON(w, r, &in) {
		return
	}
	from, to := asStr(in["from"]), asStr(in["to"])
	events, _ := in["events"].([]any)
	if !isoDateRE.MatchString(from) || !isoDateRE.MatchString(to) || events == nil {
		badRequest(w, "from, to, events erwartet")
		return
	}
	c, err := a.meetingCfg(r.Context(), u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	excl := map[string]bool{}
	for _, x := range c.exclude {
		excl[x] = true
	}
	n := 0
	err = a.inTx(r.Context(), func(tx pgx.Tx) error {
		if _, err := tx.Exec(r.Context(), `DELETE FROM calendar_events WHERE user_id=$1 AND date BETWEEN $2 AND $3`, u.ID, from, to); err != nil {
			return err
		}
		for _, raw := range events {
			e, _ := raw.(map[string]any)
			sMs, eMs := DateParseMs(asStr(e["start"])), DateParseMs(asStr(e["end"]))
			if sMs == 0 || eMs == 0 || eMs <= sMs {
				continue
			}
			if truthy(e["allDay"]) || eMs-sMs > 12*3600*1000 {
				continue
			}
			cal := asStr(e["calendar"])
			if excl[strings.ToLower(cal)] || strings.HasPrefix(strings.ToLower(cal), "zeitprotokoll") {
				continue
			}
			startISO := time.UnixMilli(sMs).UTC().Format(time.RFC3339Nano)
			endISO := time.UnixMilli(eMs).UTC().Format(time.RFC3339Nano)
			uid := meetingUID(asStr(e["title"]), startISO, cal)
			date := a.dateOf(startISO)
			title := asStr(e["title"])
			if title == "" {
				title = "Termin"
			}
			if len(title) > 200 {
				title = title[:200]
			}
			if _, err := tx.Exec(r.Context(), `INSERT INTO calendar_events(user_id,uid,date,start_time,end_time,title,calendar,location)
				VALUES($1,$2,$3,$4,$5,$6,$7,$8)
				ON CONFLICT (user_id,uid) DO UPDATE SET date=EXCLUDED.date,start_time=EXCLUDED.start_time,end_time=EXCLUDED.end_time,
				title=EXCLUDED.title,calendar=EXCLUDED.calendar,location=EXCLUDED.location,updated_at=now()`,
				u.ID, uid, date, startISO, endISO, title, nz(cal), nz(e["location"])); err != nil {
				return err
			}
			n++
		}
		return nil
	})
	if err != nil {
		a.fail(w, err)
		return
	}
	rt := meetingStateOf(u.ID)
	rt.mu.Lock()
	rt.lastCalSync = map[string]any{"at": time.Now().UTC().Format(time.RFC3339Nano), "count": n, "from": from, "to": to, "diag": in["diag"]}
	rt.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "count": n})
}

func meetingUID(title, startISO, calendar string) string {
	h := sha1.Sum([]byte(title + "|" + startISO + "|" + calendar))
	return hex.EncodeToString(h[:])[:16]
}

// DateParseMs: ISO/ms-Zeit → Unix-Millisekunden (0 = ungültig)
func DateParseMs(s string) int64 {
	if s == "" {
		return 0
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t.UnixMilli()
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UnixMilli()
	}
	for _, l := range timeLayouts {
		if t, err := time.Parse(l, s); err == nil {
			return t.UnixMilli()
		}
	}
	return 0
}

func (a *App) meetingsSuggestions(w http.ResponseWriter, r *http.Request, u User) {
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	if days < 1 {
		days = 3
	}
	if days > 14 {
		days = 14
	}
	out, err := a.meetingSuggestions(r.Context(), u.ID, days)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *App) meetingSuggestions(ctx context.Context, uid int64, days int) ([]map[string]any, error) {
	c, err := a.meetingCfg(ctx, uid)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	sinceDate := now.AddDate(0, 0, -(days - 1)).In(a.Loc).Format("2006-01-02")

	stateRows, err := a.maps(ctx, `SELECT key, status FROM meeting_state WHERE user_id=$1`, uid)
	if err != nil {
		return nil, err
	}
	state := map[string]string{}
	for _, r := range stateRows {
		state[asStr(r["key"])] = asStr(r["status"])
	}

	type spanMs struct{ s, e int64 }
	var entries []spanMs
	erows, err := a.maps(ctx, `SELECT start_time, end_time FROM time_entries WHERE user_id=$1 AND date>=$2 AND end_time IS NOT NULL`, uid, sinceDate)
	if err != nil {
		return nil, err
	}
	for _, e := range erows {
		s, e2 := DateParseMs(asStr(e["start_time"])), DateParseMs(asStr(e["end_time"]))
		if s > 0 && e2 > s {
			entries = append(entries, spanMs{s, e2})
		}
	}
	timer, _ := a.one(ctx, `SELECT * FROM timer_state WHERE user_id=$1`, uid)
	if timer != nil && asStr(timer["start_time"]) != "" && timer["project_id"] != nil {
		if s := DateParseMs(asStr(timer["start_time"])); s > 0 {
			entries = append(entries, spanMs{s, now.UnixMilli()})
		}
	}
	covered := func(s, e int64) float64 {
		var sum int64
		for _, x := range entries {
			sum += meetingOverlapMs(s, e, x.s, x.e)
		}
		den := e - s
		if den < 1 {
			den = 1
		}
		return float64(sum) / float64(den)
	}

	var cals []map[string]any
	if c.calendar {
		cals, err = a.maps(ctx, `SELECT * FROM calendar_events WHERE user_id=$1 AND date>=$2 ORDER BY start_time`, uid, sinceDate)
		if err != nil {
			return nil, err
		}
	}
	var dets []map[string]any
	if c.detect {
		sinceISO := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, a.Loc).AddDate(0, 0, -(days - 1)).UTC().Format(time.RFC3339Nano)
		dets, err = a.maps(ctx, `SELECT * FROM meeting_events WHERE user_id=$1 AND start_time>=$2 ORDER BY start_time`, uid, sinceISO)
		if err != nil {
			return nil, err
		}
	}
	type detDone struct {
		row      map[string]any
		s, e     int64
		open     bool
		id       int64
		used     bool
	}
	var detsDone []detDone
	minMs := int64(c.minMinutes) * 60 * 1000
	for _, d := range dets {
		s := DateParseMs(asStr(d["start_time"]))
		endRaw := asStr(d["end_time"])
		open := endRaw == ""
		e := DateParseMs(asStr(d["last_seen"]))
		if !open {
			e = DateParseMs(endRaw)
		}
		if e-s < minMs {
			continue
		}
		id, _ := d["id"].(int64)
		if id == 0 {
			id = int64(asFloat(d["id"]))
		}
		detsDone = append(detsDone, detDone{row: d, s: s, e: e, open: open, id: id})
	}

	var out []map[string]any
	nowMs := now.UnixMilli()
	for _, ev := range cals {
		s, e := DateParseMs(asStr(ev["start_time"])), DateParseMs(asStr(ev["end_time"]))
		if s > nowMs+5*60*1000 {
			continue
		}
		var det *detDone
		span := e - s
		for i := range detsDone {
			d := &detsDone[i]
			if d.used {
				continue
			}
			den := min(span, d.e-d.s)
			if den < 1 {
				den = 1
			}
			if float64(meetingOverlapMs(s, e, d.s, d.e))/float64(den) >= 0.3 || (d.s >= s-10*60*1000 && d.s <= e) {
				det = d
				d.used = true
				break
			}
		}
		bs, be := s, min(e, nowMs)
		if det != nil {
			bs = max(det.s, s-15*60*1000)
			be = min(max(det.e, min(e, det.e)), e+30*60*1000)
		}
		sources := []string{"Kalender"}
		var detected any
		if det != nil {
			sources = append(sources, asStr(det.row["app"]))
			detected = map[string]any{"app": det.row["app"], "start_time": det.row["start_time"], "end_time": pickStr(det.row["end_time"], det.row["last_seen"])}
		}
		out = append(out, map[string]any{
			"key": "cal:" + asStr(ev["uid"]), "title": ev["title"],
			"start_time": time.UnixMilli(bs).UTC().Format(time.RFC3339Nano),
			"end_time":   time.UnixMilli(be).UTC().Format(time.RFC3339Nano),
			"minutes":    int(math.Round(float64(be-bs) / 60000)),
			"sources": sources, "cal": map[string]any{"start_time": ev["start_time"], "end_time": ev["end_time"], "calendar": ev["calendar"], "location": ev["location"]},
			"detected": detected, "running": false,
		})
	}
	for _, d := range detsDone {
		if d.used {
			continue
		}
		app := asStr(d.row["app"])
		title := app + "-Meeting (erkannt)"
		if app == meetingGeneric {
			title = "Meeting (erkannt)"
		}
		out = append(out, map[string]any{
			"key": fmt.Sprintf("det:%d", d.id), "title": title,
			"start_time": time.UnixMilli(d.s).UTC().Format(time.RFC3339Nano),
			"end_time":   time.UnixMilli(d.e).UTC().Format(time.RFC3339Nano),
			"minutes":    int(math.Round(float64(d.e-d.s) / 60000)),
			"sources": []string{app}, "cal": nil,
			"detected": map[string]any{"app": app, "start_time": d.row["start_time"], "end_time": pickStr(d.row["end_time"], d.row["last_seen"])},
			"running":  d.open,
		})
	}

	rules, err := a.maps(ctx, `SELECT r.*, p.name AS project_name FROM meeting_rules r LEFT JOIN projects p ON p.id=r.project_id WHERE r.user_id=$1`, uid)
	if err != nil {
		return nil, err
	}
	ruleBy := map[string]map[string]any{}
	for _, r := range rules {
		ruleBy[asStr(r["pattern"])] = r
	}

	var filtered []map[string]any
	for _, x := range out {
		key := asStr(x["key"])
		if state[key] != "" {
			continue
		}
		s, e := DateParseMs(asStr(x["start_time"])), DateParseMs(asStr(x["end_time"]))
		if covered(s, e) >= 0.6 {
			continue
		}
		mins, _ := x["minutes"].(int)
		if mins < 1 {
			continue
		}
		pat := normMeetingTitle(asStr(x["title"]))
		if r := ruleBy[pat]; r != nil {
			x["rule"] = map[string]any{
				"project_id": r["project_id"], "project_name": r["project_name"],
				"task_id": r["task_id"], "activity": r["activity"], "hits": r["hits"],
			}
			x["best"] = nil
		} else {
			best, _ := a.suggestMeetingTasks(ctx, uid, asStr(x["title"]), 1)
			if len(best) > 0 && asFloat(best[0]["score"]) >= 60 {
				x["best"] = best[0]
			} else {
				x["best"] = nil
			}
			x["rule"] = nil
		}
		x["date"] = a.dateOf(asStr(x["start_time"]))
		filtered = append(filtered, x)
	}
	// neueste zuerst
	for i := 0; i < len(filtered); i++ {
		for j := i + 1; j < len(filtered); j++ {
			if asStr(filtered[j]["start_time"]) > asStr(filtered[i]["start_time"]) {
				filtered[i], filtered[j] = filtered[j], filtered[i]
			}
		}
	}
	if filtered == nil {
		filtered = []map[string]any{}
	}
	return filtered, nil
}

func pickStr(vals ...any) any {
	for _, v := range vals {
		if s := asStr(v); s != "" {
			return s
		}
	}
	return nil
}

func (a *App) meetingsBook(w http.ResponseWriter, r *http.Request, u User) {
	var in body
	if !readJSON(w, r, &in) {
		return
	}
	key := asStr(in["key"])
	sMs, eMs := DateParseMs(asStr(in["start_time"])), DateParseMs(asStr(in["end_time"]))
	if key == "" || sMs == 0 || eMs == 0 || eMs <= sMs {
		badRequest(w, "Zeit ungültig")
		return
	}
	if eMs-sMs > 12*3600*1000 {
		badRequest(w, "Länger als 12 Stunden – bitte prüfen")
		return
	}
	if invalidID(in["project_id"]) || asID(in["project_id"]) == nil {
		badRequest(w, "Bitte ein Projekt wählen")
		return
	}
	ctx := r.Context()
	if ex, _ := a.one(ctx, `SELECT 1 AS x FROM meeting_state WHERE user_id=$1 AND key=$2`, u.ID, key); ex != nil {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "Schon erledigt"})
		return
	}
	startISO := time.UnixMilli(sMs).UTC().Format(time.RFC3339Nano)
	endISO := time.UnixMilli(eMs).UTC().Format(time.RFC3339Nano)
	date := a.dateOf(startISO)
	pid, tid := asID(in["project_id"]), asID(in["task_id"])
	if !a.owned(ctx, "projects", pid, u.ID) || !a.owned(ctx, "tasks", tid, u.ID) {
		badRequest(w, "Projekt oder Ticket nicht gefunden")
		return
	}
	pid, tid, _ = a.redirect(ctx, u.ID, pid, tid)

	if !truthy(in["force"]) {
		rows, err := a.maps(ctx, `SELECT id, start_time, end_time FROM time_entries WHERE user_id=$1 AND date=$2 AND end_time IS NOT NULL`, u.ID, date)
		if err != nil {
			a.fail(w, err)
			return
		}
		var bestID int64
		var bestOv int64
		span := eMs - sMs
		for _, x := range rows {
			xs, xe := DateParseMs(asStr(x["start_time"])), DateParseMs(asStr(x["end_time"]))
			ov := meetingOverlapMs(sMs, eMs, xs, xe)
			lenX := xe - xs
			if ov >= int64(0.5*float64(min(span, lenX))) && ov > bestOv {
				bestOv = ov
				bestID, _ = x["id"].(int64)
				if bestID == 0 {
					bestID = int64(asFloat(x["id"]))
				}
			}
		}
		if bestID > 0 {
			_, err := a.DB.Exec(ctx, `INSERT INTO meeting_state(user_id,key,status,entry_id) VALUES($1,$2,'booked',$3)
				ON CONFLICT (user_id,key) DO UPDATE SET status='booked', entry_id=EXCLUDED.entry_id`, u.ID, key, bestID)
			if err != nil {
				a.fail(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "entry_id": bestID, "merged": true, "message": "War schon gebucht – Termin der vorhandenen Buchung zugeordnet"})
			return
		}
	}

	title := asStr(in["comment"])
	if title == "" {
		title = asStr(in["title"])
	}
	if title == "" {
		title = "Meeting"
	}
	if len(title) > 300 {
		title = title[:300]
	}
	dur := int64(math.Round(float64(eMs-sMs) / 1000))
	wr := selfWriter(u)
	cr, stop := a.guard(w, r, u.ID, in, candidate{Date: date, Start: startISO, End: endISO})
	if stop {
		return
	}
	var newID int64
	err := a.writeTx(ctx, wr, func(tx pgx.Tx) error {
		if err := a.requireLocked(ctx, tx, wr, date); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `INSERT INTO time_entries(user_id,project_id,task_id,start_time,end_time,duration_seconds,comment,billable,date,source,activity,entered_by,time_precise)
			VALUES($1,$2,$3,$4,$5,$6,$7,1,$8,'meeting',$9,$10,true) RETURNING id`,
			u.ID, pid, tid, startISO, endISO, dur, title, date, nz(in["activity"]), wr.enteredBy()).Scan(&newID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO meeting_state(user_id,key,status,entry_id) VALUES($1,$2,'booked',$3)
			ON CONFLICT (user_id,key) DO UPDATE SET status='booked', entry_id=EXCLUDED.entry_id`, u.ID, key, newID); err != nil {
			return err
		}
		remember := true
		if _, ok := in["remember"]; ok {
			remember = truthy(in["remember"])
		}
		if remember {
			pat := normMeetingTitle(strOr(asStr(in["rule_title"]), strOr(asStr(in["title"]), title)))
			if pat != "" {
				if _, err := tx.Exec(ctx, `INSERT INTO meeting_rules(user_id,pattern,project_id,task_id,activity,hits)
					VALUES($1,$2,$3,$4,$5,1)
					ON CONFLICT (user_id,pattern) DO UPDATE SET project_id=EXCLUDED.project_id, task_id=EXCLUDED.task_id,
					activity=EXCLUDED.activity, hits=meeting_rules.hits+1, updated_at=now()`,
					u.ID, pat, pid, tid, nz(in["activity"])); err != nil {
					return err
				}
			}
		}
		return a.nachtragLog(ctx, tx, wr, "Meeting gebucht", fmt.Sprintf("#%d", newID), date)
	})
	if err != nil {
		a.writeFail(w, err)
		return
	}
	a.stamp(ctx, u.ID, newID, cr)
	a.afterWrite(ctx, wr, date)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "entry_id": newID})
}

func (a *App) meetingsDismiss(w http.ResponseWriter, r *http.Request, u User) {
	var in body
	if !readJSON(w, r, &in) {
		return
	}
	var keys []string
	if arr, ok := in["keys"].([]any); ok {
		for _, k := range arr {
			if s := asStr(k); s != "" {
				keys = append(keys, s)
			}
		}
	} else if k := asStr(in["key"]); k != "" {
		keys = []string{k}
	}
	for _, k := range keys {
		if _, err := a.DB.Exec(r.Context(), `INSERT INTO meeting_state(user_id,key,status) VALUES($1,$2,'dismissed')
			ON CONFLICT (user_id,key) DO UPDATE SET status='dismissed', entry_id=NULL`, u.ID, k); err != nil {
			a.fail(w, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *App) meetingsRuleDelete(w http.ResponseWriter, r *http.Request, u User) {
	pat := r.PathValue("pattern")
	if _, err := a.DB.Exec(r.Context(), `DELETE FROM meeting_rules WHERE user_id=$1 AND pattern=$2`, u.ID, pat); err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *App) meetingsSuggestTask(w http.ResponseWriter, r *http.Request, u User) {
	text := strings.TrimSpace(r.URL.Query().Get("text"))
	if text == "" {
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	out, err := a.suggestMeetingTasks(r.Context(), u.ID, text, 6)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *App) suggestMeetingTasks(ctx context.Context, uid int64, text string, limit int) ([]map[string]any, error) {
	var out []map[string]any
	rule, err := a.one(ctx, `SELECT r.*, p.name AS project_name, t.title AS task_title, t.jira_ticket
		FROM meeting_rules r LEFT JOIN projects p ON p.id=r.project_id LEFT JOIN tasks t ON t.id=r.task_id
		WHERE r.user_id=$1 AND r.pattern=$2`, uid, normMeetingTitle(text))
	if err != nil {
		return nil, err
	}
	if rule != nil {
		out = append(out, map[string]any{
			"task_id": rule["task_id"], "project_id": rule["project_id"], "key": rule["jira_ticket"],
			"title": rule["task_title"], "project_name": rule["project_name"], "score": 100,
			"reasons": []string{fmt.Sprintf("so %v× gebucht", rule["hits"])},
		})
	}
	since := time.Now().AddDate(0, 0, -180).In(a.Loc).Format("2006-01-02")
	type hist struct {
		comments []string
		n        int
	}
	hmap := map[int64]*hist{}
	hrows, err := a.maps(ctx, `SELECT task_id, comment, date FROM time_entries
		WHERE user_id=$1 AND task_id IS NOT NULL AND date>=$2 AND comment IS NOT NULL AND TRIM(comment)<>''
		ORDER BY date DESC LIMIT 3000`, uid, since)
	if err != nil {
		return nil, err
	}
	for _, r := range hrows {
		tid := int64(asFloat(r["task_id"]))
		h := hmap[tid]
		if h == nil {
			h = &hist{}
			hmap[tid] = h
		}
		h.n++
		if len(h.comments) < 25 {
			h.comments = append(h.comments, asStr(r["comment"]))
		}
	}
	tasks, err := a.maps(ctx, `SELECT t.id, t.title, t.jira_ticket, t.project_id, p.name AS project_name
		FROM tasks t JOIN projects p ON p.id=t.project_id
		WHERE t.user_id=$1 AND COALESCE(t.status,'open')<>'done' AND COALESCE(p.status,'')<>'archived'`, uid)
	if err != nil {
		return nil, err
	}
	ruleTID := int64(0)
	if rule != nil {
		ruleTID = int64(asFloat(rule["task_id"]))
	}
	for _, t := range tasks {
		tid := int64(asFloat(t["id"]))
		if rule != nil && tid == ruleTID {
			continue
		}
		simT := textSim(text, asStr(t["title"])+" "+asStr(t["jira_ticket"]))
		simH := 0.0
		if h := hmap[tid]; h != nil {
			for _, c := range h.comments {
				if v := textSim(text, c); v > simH {
					simH = v
				}
			}
		}
		sim := simT
		if simH*0.9 > sim {
			sim = simH * 0.9
		}
		if sim < 0.3 {
			continue
		}
		freq := 0.0
		if h := hmap[tid]; h != nil {
			freq = math.Min(1, math.Log(1+float64(h.n))/math.Log(25))
		}
		score := int(math.Round(math.Min(0.99, 0.8*sim+0.2*freq) * 100))
		reasons := []string{}
		if simT >= simH*0.9 {
			reasons = append(reasons, fmt.Sprintf("Titel ähnlich (%d %%)", int(math.Round(simT*100))))
		} else {
			reasons = append(reasons, "ähnliche frühere Buchung")
		}
		if h := hmap[tid]; h != nil {
			reasons = append(reasons, fmt.Sprintf("%d× gebucht", h.n))
		}
		out = append(out, map[string]any{
			"task_id": tid, "project_id": t["project_id"], "key": t["jira_ticket"],
			"title": t["title"], "project_name": t["project_name"], "score": score, "reasons": reasons,
		})
	}
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if asFloat(out[j]["score"]) > asFloat(out[i]["score"]) {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	if len(out) > limit {
		out = out[:limit]
	}
	if out == nil {
		out = []map[string]any{}
	}
	return out, nil
}

func (a *App) meetingsNow(w http.ResponseWriter, r *http.Request, u User) {
	ctx := r.Context()
	c, err := a.meetingCfg(ctx, u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	now := time.Now()
	ev, err := a.one(ctx, `SELECT * FROM calendar_events WHERE user_id=$1 AND start_time <= $2 AND end_time >= $3 ORDER BY start_time DESC LIMIT 1`,
		u.ID, now.Add(10*time.Minute).UTC().Format(time.RFC3339Nano), now.UTC().Format(time.RFC3339Nano))
	if err != nil {
		a.fail(w, err)
		return
	}
	var rule map[string]any
	var best map[string]any
	var state any
	if ev != nil {
		rule, _ = a.one(ctx, `SELECT r.*, p.name AS project_name FROM meeting_rules r LEFT JOIN projects p ON p.id=r.project_id
			WHERE r.user_id=$1 AND r.pattern=$2`, u.ID, normMeetingTitle(asStr(ev["title"])))
		if rule == nil {
			list, _ := a.suggestMeetingTasks(ctx, u.ID, asStr(ev["title"]), 1)
			if len(list) > 0 && asFloat(list[0]["score"]) >= 60 {
				best = list[0]
			}
		}
		st, _ := a.one(ctx, `SELECT status FROM meeting_state WHERE user_id=$1 AND key=$2`, u.ID, "cal:"+asStr(ev["uid"]))
		if st != nil {
			state = st["status"]
		}
	}
	var next map[string]any
	if ev == nil {
		next, _ = a.one(ctx, `SELECT * FROM calendar_events WHERE user_id=$1 AND start_time > $2 AND start_time <= $3 ORDER BY start_time LIMIT 1`,
			u.ID, now.UTC().Format(time.RFC3339Nano), now.Add(30*time.Minute).UTC().Format(time.RFC3339Nano))
	}
	writeJSON(w, http.StatusOK, map[string]any{"ask": c.ask, "event": ev, "rule": rule, "best": best, "state": state, "next": next})
}

func (a *App) meetingsEvents(w http.ResponseWriter, r *http.Request, u User) {
	q := r.URL.Query()
	days, _ := strconv.Atoi(q.Get("days"))
	if days < 1 {
		days = 2
	}
	if days > 14 {
		days = 14
	}
	from := q.Get("from")
	if !isoDateRE.MatchString(from) {
		from = time.Now().AddDate(0, 0, -(days - 1)).In(a.Loc).Format("2006-01-02")
	}
	to := q.Get("to")
	if !isoDateRE.MatchString(to) {
		to = "9999-12-31"
	}
	ctx := r.Context()
	stateRows, err := a.maps(ctx, `SELECT key, status, entry_id FROM meeting_state WHERE user_id=$1`, u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	state := map[string]map[string]any{}
	for _, r := range stateRows {
		state[asStr(r["key"])] = r
	}
	rules, err := a.maps(ctx, `SELECT r.*, p.name AS project_name FROM meeting_rules r LEFT JOIN projects p ON p.id=r.project_id WHERE r.user_id=$1`, u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	ruleBy := map[string]map[string]any{}
	for _, r := range rules {
		ruleBy[asStr(r["pattern"])] = r
	}
	evs, err := a.maps(ctx, `SELECT * FROM calendar_events WHERE user_id=$1 AND date>=$2 AND date<=$3 ORDER BY start_time`, u.ID, from, to)
	if err != nil {
		a.fail(w, err)
		return
	}
	out := make([]map[string]any, 0, len(evs))
	for _, e := range evs {
		key := "cal:" + asStr(e["uid"])
		row := map[string]any{}
		for k, v := range e {
			row[k] = v
		}
		row["key"] = key
		row["state"] = nil
		row["entry_id"] = nil
		if st := state[key]; st != nil {
			row["state"] = st["status"]
			row["entry_id"] = st["entry_id"]
		}
		row["rule"] = ruleBy[normMeetingTitle(asStr(e["title"]))]
		out = append(out, row)
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *App) meetingsStatus(w http.ResponseWriter, r *http.Request, u User) {
	ctx := r.Context()
	c, err := a.meetingCfg(ctx, u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	rt := meetingStateOf(u.ID)
	rt.mu.Lock()
	last := rt.lastSignal
	diag := rt.lastDiag
	cal := rt.lastCalSync
	rt.mu.Unlock()
	var age any
	var lastISO any
	if !last.IsZero() {
		lastISO = last.UTC().Format(time.RFC3339Nano)
		age = int(math.Round(time.Since(last).Seconds()))
	}
	open, _ := a.one(ctx, `SELECT * FROM meeting_events WHERE user_id=$1 AND end_time IS NULL ORDER BY id DESC LIMIT 1`, u.ID)
	var nCal, nMeet, nRules int
	_ = a.DB.QueryRow(ctx, `SELECT COUNT(*) FROM calendar_events WHERE user_id=$1`, u.ID).Scan(&nCal)
	_ = a.DB.QueryRow(ctx, `SELECT COUNT(*) FROM meeting_events WHERE user_id=$1`, u.ID).Scan(&nMeet)
	_ = a.DB.QueryRow(ctx, `SELECT COUNT(*) FROM meeting_rules WHERE user_id=$1`, u.ID).Scan(&nRules)
	rules, _ := a.maps(ctx, `SELECT r.pattern, r.hits, r.activity, p.name AS project_name FROM meeting_rules r
		LEFT JOIN projects p ON p.id=r.project_id WHERE r.user_id=$1 ORDER BY r.hits DESC LIMIT 50`, u.ID)
	if rules == nil {
		rules = []map[string]any{}
	}
	if diag == nil {
		diag = map[string]any{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"detect": c.detect, "calendar": c.calendar, "browser": c.browser, "ask": c.ask,
		"last_signal": lastISO, "signal_age_s": age, "diag": diag, "calendar_sync": cal,
		"open_meeting": open,
		"counts":       map[string]any{"calendar_events": nCal, "meetings": nMeet, "rules": nRules},
		"rules":        rules,
	})
}

func (a *App) meetingsResync(w http.ResponseWriter, r *http.Request, u User) {
	rt := meetingStateOf(u.ID)
	rt.mu.Lock()
	rt.resync = true
	rt.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

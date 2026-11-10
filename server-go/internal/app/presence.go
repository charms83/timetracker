package app

import (
	"context"
	"math"
	"math/rand/v2"
	"net/http"
	"sort"
	"strconv"
	"time"
)

/* Anwesenheit am Mac + Tagesüberblick (wie backend/presence.js).
   Die Mac-App meldet jede Minute, ob jemand am Rechner ist. Gespeichert wird nur die Minute – kein Inhalt.
   Daraus entstehen „von wann bis wann“ und Lücken ohne Buchung. SSH-Sitzungen bleiben lokal am Mac (leer). */

type span struct {
	S int `json:"s"`
	E int `json:"e"`
}

func (a *App) routesPresence(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/presence", a.auth(a.presencePost))
	mux.HandleFunc("GET /api/day-overview", a.auth(a.dayOverviewRoute))
}

func (a *App) minOfDay(iso string) (int, bool) {
	t, ok := a.parseTime(iso)
	if !ok {
		return 0, false
	}
	t = t.In(a.Loc)
	return t.Hour()*60 + t.Minute(), true
}

func (a *App) presenceOn(ctx context.Context, uid int64) bool {
	var v *string
	if err := a.DB.QueryRow(ctx, `SELECT value FROM user_settings WHERE user_id=$1 AND key='presence_track'`, uid).Scan(&v); err != nil || v == nil {
		return true // Standard an
	}
	return *v != "0"
}

func (a *App) presencePost(w http.ResponseWriter, r *http.Request, u User) {
	var in body
	if !readJSON(w, r, &in) {
		return
	}
	ctx := r.Context()
	if !a.presenceOn(ctx, u.ID) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "off": true})
		return
	}
	if truthy(in["active"]) {
		now := time.Now().In(a.Loc)
		if _, err := a.DB.Exec(ctx, `INSERT INTO presence_min(user_id,ts,date) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`,
			u.ID, now.Format("2006-01-02T15:04"), now.Format("2006-01-02")); err != nil {
			a.fail(w, err)
			return
		}
	}
	if rand.Float64() < 0.01 { // älter als 120 Tage weg
		_, _ = a.DB.Exec(ctx, `DELETE FROM presence_min WHERE user_id=$1 AND date < $2`, u.ID, time.Now().Add(-120*24*time.Hour).In(a.Loc).Format("2006-01-02"))
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// presenceSpans: Minuten → Spannen (Lücken bis 10 min gelten als durchgehend)
func (a *App) presenceSpans(ctx context.Context, uid int64, date string) ([]span, error) {
	rows, err := a.DB.Query(ctx, `SELECT ts FROM presence_min WHERE user_id=$1 AND date=$2 ORDER BY ts`, uid, date)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []span{}
	for rows.Next() {
		var ts string
		if err := rows.Scan(&ts); err != nil {
			return nil, err
		}
		if len(ts) < 16 {
			continue
		}
		h, e1 := strconv.Atoi(ts[11:13])
		m, e2 := strconv.Atoi(ts[14:16])
		if e1 != nil || e2 != nil {
			continue
		}
		mins := h*60 + m
		if n := len(out); n > 0 && mins-out[n-1].E <= 10 {
			out[n-1].E = mins + 1
		} else {
			out = append(out, span{mins, mins + 1})
		}
	}
	return out, rows.Err()
}

func (a *App) dayOverviewRoute(w http.ResponseWriter, r *http.Request, u User) {
	date := r.URL.Query().Get("date")
	if _, ok := parseDay(date); !ok {
		date = a.today()
	}
	out, err := a.dayOverview(r.Context(), u.ID, date)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func overlapMin(as, ae, bs, be int) int {
	return int(math.Min(float64(ae), float64(be)) - math.Max(float64(as), float64(bs)))
}

func (a *App) dayOverview(ctx context.Context, uid int64, date string) (map[string]any, error) {
	isToday := date == a.today()
	nowT := time.Now().In(a.Loc)
	nowMin := nowT.Hour()*60 + nowT.Minute()
	tracking := a.presenceOn(ctx, uid)
	pres := []span{}
	if tracking {
		var err error
		if pres, err = a.presenceSpans(ctx, uid, date); err != nil {
			return nil, err
		}
	}

	entries, err := a.maps(ctx, `SELECT e.id, e.start_time, e.end_time, e.duration_seconds, e.comment, e.project_id, e.task_id, e.source, e.tempo_push_id,
		(t.plane_work_item_id IS NOT NULL) AS plane, p.name AS project_name, t.jira_ticket, t.title AS task_title
		FROM time_entries e LEFT JOIN projects p ON p.id=e.project_id LEFT JOIN tasks t ON t.id=e.task_id
		WHERE e.user_id=$1 AND e.date=$2 AND COALESCE(e.is_absence,0)=0 ORDER BY e.start_time`, uid, date)
	if err != nil {
		return nil, err
	}
	type bk struct{ s, e int }
	var busy []bk
	for _, e := range entries {
		s, _ := a.minOfDay(asStr(e["start_time"]))
		en := s + 1
		if asStr(e["end_time"]) != "" {
			if v, ok := a.minOfDay(asStr(e["end_time"])); ok {
				en = v
			}
		} else if isToday {
			en = nowMin
		}
		e["s"], e["e"] = s, en
		busy = append(busy, bk{s, en})
	}

	prow, err := a.maps(ctx, `SELECT start_time, end_time FROM pauses WHERE user_id=$1 AND date=$2`, uid, date)
	if err != nil {
		return nil, err
	}
	pauses := []map[string]any{}
	for _, p := range prow {
		s, _ := a.minOfDay(asStr(p["start_time"]))
		en := s
		if asStr(p["end_time"]) != "" {
			if v, ok := a.minOfDay(asStr(p["end_time"])); ok {
				en = v
			}
		} else if isToday {
			en = nowMin
		}
		pauses = append(pauses, map[string]any{"start_time": p["start_time"], "end_time": p["end_time"], "s": s, "e": en})
		busy = append(busy, bk{s, en})
	}

	// offene Termine: Kalender-Einträge ohne passende Buchung und noch nicht gebucht/verworfen
	evRows, err := a.maps(ctx, `SELECT uid, title, start_time, end_time FROM calendar_events WHERE user_id=$1 AND date=$2 ORDER BY start_time`, uid, date)
	if err != nil {
		return nil, err
	}
	handled := map[string]bool{}
	hr, err := a.DB.Query(ctx, `SELECT key FROM meeting_state WHERE user_id=$1 AND status IN ('booked','dismissed')`, uid)
	if err != nil {
		return nil, err
	}
	for hr.Next() {
		var k string
		if err := hr.Scan(&k); err != nil {
			hr.Close()
			return nil, err
		}
		handled[k] = true
	}
	hr.Close()
	events := []map[string]any{}
	for _, c := range evRows {
		s, ok1 := a.minOfDay(asStr(c["start_time"]))
		en, ok2 := a.minOfDay(asStr(c["end_time"]))
		if !ok1 || !ok2 {
			continue
		}
		covered := false
		for _, e := range entries {
			if float64(max(0, overlapMin(e["s"].(int), e["e"].(int), s, en))) >= 0.5*float64(en-s) {
				covered = true
				break
			}
		}
		if covered || handled["cal:"+asStr(c["uid"])] || handled[asStr(c["uid"])] {
			continue
		}
		c["s"], c["e"] = s, en
		events = append(events, c)
	}

	// Arbeitstag: Anwesenheit, sonst erste/letzte Buchung
	var work *span
	first, last, have := math.MaxInt, math.MinInt, false
	upd := func(s, e int) {
		have = true
		first, last = min(first, s), max(last, e)
	}
	if len(pres) > 0 {
		upd(pres[0].S, pres[len(pres)-1].E)
	}
	for _, e := range entries {
		upd(e["s"].(int), e["e"].(int))
	}
	if have {
		work = &span{first, last}
	}

	// Lücken: innerhalb der Anwesenheit (oder des Arbeitstags), ohne Buchung und ohne Pause, ab 10 Minuten
	base := pres
	if len(base) == 0 && work != nil {
		base = []span{*work}
	}
	sort.Slice(busy, func(i, j int) bool { return busy[i].s < busy[j].s })
	gaps := []span{}
	for _, sp := range base {
		cur := sp.S
		for _, b := range busy {
			if b.e <= cur || b.s >= sp.E {
				continue
			}
			if b.s-cur >= 10 {
				gaps = append(gaps, span{cur, min(b.s, sp.E)})
			}
			cur = max(cur, b.e)
		}
		if sp.E-cur >= 10 {
			gaps = append(gaps, span{cur, sp.E})
		}
	}
	presMin, gapMin := 0, 0
	for _, p := range pres {
		presMin += p.E - p.S
	}
	for _, g := range gaps {
		gapMin += g.E - g.S
	}
	bookedSecs := 0.0
	for _, e := range entries {
		if d := asFloat(e["duration_seconds"]); d != 0 {
			bookedSecs += d
		} else {
			bookedSecs += float64(e["e"].(int)-e["s"].(int)) * 60
		}
	}
	// Überschneidungen (vermutlich doppelt gebucht), ab 5 Minuten
	overlaps := []map[string]any{}
	for i := 0; i < len(entries); i++ {
		for j := i + 1; j < len(entries); j++ {
			if ov := overlapMin(entries[i]["s"].(int), entries[i]["e"].(int), entries[j]["s"].(int), entries[j]["e"].(int)); ov >= 5 {
				overlaps = append(overlaps, map[string]any{"a": entries[i], "b": entries[j], "minutes": ov})
			}
		}
	}
	var nowVal any
	if isToday {
		nowVal = nowMin
	}
	return map[string]any{
		"date": date, "tracking": tracking, "presence": pres, "overlaps": overlaps, "presence_minutes": presMin, "work": work,
		"pauses": pauses, "entries": entries, "events": events, "ssh": []any{}, "gaps": gaps, "gap_minutes": gapMin,
		"booked_minutes": int(math.Round(bookedSecs / 60)), "now": nowVal,
	}, nil
}

// truthy: wie JS – true, Zahl ≠ 0 oder nicht leerer Text (außer "false"/"0")
func truthy(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case float64:
		return x != 0
	case string:
		return x != "" && x != "false" && x != "0"
	}
	return false
}

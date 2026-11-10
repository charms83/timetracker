package app

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"
)

/* Vorschläge zum Nachtragen und „Jetzt vermutlich“ – „Light“-Variante von backend/monthend.js (suggest, suggestNow, learn).
   Nur Regeln aus den eigenen Buchungen, nichts wird automatisch gebucht. Dreifach abschaltbar (suggestAllowedFor) plus
   „Konto erfasst Zeiten“; abgeschaltet = keine Abfragen, leere Antwort mit Hinweis.
   Bewusst anders: keine Termin-/SSH-Vorschläge und kein Verknüpfen von Termin/SSH beim Lernen (Kalender/SSH bleiben im Client).
     davor / danach gebucht (≤ 30 min)   +3 / +2
     gleicher Wochentag, ähnliche Zeit    2/w + 0,5 (letzte 5 Wochen)
     viel gebucht in den letzten 14 Tagen bis +2
     früher hier gewählt (≥ 2×, 90 Tage)  bis +1
     typischer Tag (keine Buchungen)      +4 */

type candEntry struct {
	TaskID, ProjectID                        int64
	Comment, JiraTicket, TaskTitle, ProjName string
	S, E, Week                               int // Minuten, Wochenabstand (Historie)
	Absence                                  bool
	TaskDone                                 bool
}

func (e candEntry) key() string {
	if e.TaskID != 0 {
		return fmt.Sprintf("t%d", e.TaskID)
	}
	return fmt.Sprintf("p%d", e.ProjectID)
}

type cand struct {
	score float64
	why   []string
}

// cands: Kandidaten in Einfügereihenfolge (stabile Sortierung wie Map in JS); info teilt den ersten Eintrag je Schlüssel
type cands struct {
	order []string
	m     map[string]*cand
	info  map[string]candEntry
}

func newCands(info map[string]candEntry) *cands {
	return &cands{m: map[string]*cand{}, info: info}
}

func (c *cands) bump(e *candEntry, pts float64, why string) {
	if e == nil || e.ProjectID == 0 {
		return
	}
	k := e.key()
	if _, ok := c.info[k]; !ok {
		c.info[k] = *e
	}
	x := c.m[k]
	if x == nil {
		x = &cand{}
		c.m[k] = x
		c.order = append(c.order, k)
	}
	x.score += pts
	if why != "" && !contains(x.why, why) {
		x.why = append(x.why, why)
	}
}

func (c *cands) clone() *cands {
	n := newCands(c.info)
	for _, k := range c.order {
		x := c.m[k]
		n.order = append(n.order, k)
		n.m[k] = &cand{x.score, append([]string{}, x.why...)}
	}
	return n
}

func (c *cands) sorted() []string {
	ks := append([]string{}, c.order...)
	sort.SliceStable(ks, func(i, j int) bool { return c.m[ks[i]].score > c.m[ks[j]].score })
	return ks
}

func (e candEntry) label() string {
	l := jsOr(e.TaskTitle, e.ProjName)
	if e.JiraTicket != "" {
		l = e.JiraTicket + " " + l
	}
	return l
}

// top: beste n Kandidaten in der Form von monthend.js
func (c *cands) top(n int) []map[string]any {
	out := []map[string]any{}
	for _, k := range c.sorted() {
		if len(out) == n {
			break
		}
		e, x := c.info[k], c.m[k]
		why := x.why
		if len(why) > 2 {
			why = why[:2]
		}
		out = append(out, map[string]any{"task_id": nzID(e.TaskID), "project_id": e.ProjectID, "jira_ticket": nz(e.JiraTicket), "label": e.label(),
			"project_name": nz(e.ProjName), "last_comment": e.Comment, "why": append([]string{}, why...), "score": r1(x.score)})
	}
	return out
}

// cutIntervals: Teile von parts ohne busy, nur Stücke ≥ minLen Minuten (reine Logik, getestet)
func cutIntervals(parts [][2]int, busy [][2]int, minLen int) [][2]int {
	for _, b := range busy {
		var next [][2]int
		for _, p := range parts {
			if b[1] <= p[0] || b[0] >= p[1] {
				next = append(next, p)
				continue
			}
			if x := [2]int{p[0], min(p[1], b[0])}; x[1]-x[0] > 0 {
				next = append(next, x)
			}
			if x := [2]int{max(p[0], b[1]), p[1]}; x[1]-x[0] > 0 {
				next = append(next, x)
			}
		}
		parts = next
	}
	out := [][2]int{}
	for _, p := range parts {
		if p[1]-p[0] >= minLen {
			out = append(out, p)
		}
	}
	return out
}

type dayBlock struct {
	S, E  int
	Entry candEntry
}

// typicalDay: je 15-min-Raster das häufigste Ticket der letzten 6 gleichen Wochentage (mind. 3×) → Blöcke ≥ 30 min, auf need Minuten begrenzt
func typicalDay(hist []candEntry, need int) []dayBlock {
	type cnt struct {
		n int
		e candEntry
	}
	type slot struct {
		order []string
		m     map[string]*cnt
	}
	slots := map[int]*slot{}
	for _, x := range hist {
		if x.Week < 1 || x.Week > 6 || x.ProjectID == 0 || x.Absence || x.TaskDone || x.E <= x.S {
			continue
		}
		for q := x.S / 15; q < (x.E+14)/15; q++ {
			s := slots[q]
			if s == nil {
				s = &slot{m: map[string]*cnt{}}
				slots[q] = s
			}
			k := x.key()
			if s.m[k] == nil {
				s.m[k] = &cnt{e: x}
				s.order = append(s.order, k)
			}
			s.m[k].n++
		}
	}
	type best struct {
		q, n int
		k    string
		e    candEntry
	}
	var bs []best
	for q, s := range slots {
		b := best{q: q, n: -1}
		for _, k := range s.order {
			if c := s.m[k]; c.n > b.n {
				b = best{q, c.n, k, c.e}
			}
		}
		if b.n >= 3 {
			bs = append(bs, b)
		}
	}
	sort.Slice(bs, func(i, j int) bool { return bs[i].q < bs[j].q })
	type blk struct {
		k      string
		qs, qe int
		e      candEntry
	}
	var blocks []blk
	for _, b := range bs {
		if n := len(blocks); n > 0 && blocks[n-1].k == b.k && blocks[n-1].qe == b.q {
			blocks[n-1].qe = b.q + 1
		} else {
			blocks = append(blocks, blk{b.k, b.q, b.q + 1, b.e})
		}
	}
	left, out := need, []dayBlock{}
	for _, b := range blocks {
		if left < 15 {
			break
		}
		if (b.qe-b.qs)*15 < 30 {
			continue
		}
		l := min((b.qe-b.qs)*15, left)
		if l < 15 {
			continue
		}
		out = append(out, dayBlock{b.qs * 15, b.qs*15 + l, b.e})
		left -= l
	}
	return out
}

func slotKey(date string, m int) string {
	d, _ := parseDay(date)
	return fmt.Sprintf("slot:%d:%d", int(d.Weekday()), m/60)
}

/* ── Daten ─────────────────────────────────────────────────────── */

const candCols = `e.task_id, e.project_id, COALESCE(e.comment,''), COALESCE(t.jira_ticket,''), COALESCE(t.title,''), COALESCE(p.name,'')`
const candJoin = ` FROM time_entries e LEFT JOIN tasks t ON t.id=e.task_id AND t.user_id=e.user_id LEFT JOIN projects p ON p.id=e.project_id AND p.user_id=e.user_id`

func scanCand(sc interface{ Scan(...any) error }, extra ...any) (candEntry, error) {
	var e candEntry
	var tid, pid *int64
	err := sc.Scan(append([]any{&tid, &pid, &e.Comment, &e.JiraTicket, &e.TaskTitle, &e.ProjName}, extra...)...)
	if tid != nil {
		e.TaskID = *tid
	}
	if pid != nil {
		e.ProjectID = *pid
	}
	return e, err
}

// history: Buchungen der letzten n gleichen Wochentage vor date (Week = Abstand in Wochen)
func (a *App) history(ctx context.Context, uid int64, date string, weeks int) ([]candEntry, error) {
	dates := make([]string, weeks)
	for w := 1; w <= weeks; w++ {
		dates[w-1] = addDays(date, -7*w)
	}
	rows, err := a.DB.Query(ctx, `SELECT `+candCols+`, e.date, e.start_time, e.end_time, COALESCE(e.is_absence,0)<>0, COALESCE(t.status,'open') IN ('done','closed')`+candJoin+`
		WHERE e.user_id=$1 AND e.date = ANY($2) AND e.end_time IS NOT NULL AND e.project_id IS NOT NULL ORDER BY e.date DESC, e.start_time`, uid, dates)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []candEntry
	for rows.Next() {
		var d, st, en string
		var absence, done bool
		e, err := scanCand(rows, &d, &st, &en, &absence, &done)
		if err != nil {
			return nil, err
		}
		e.Absence, e.TaskDone = absence, done
		s, ok1 := a.minOfDay(st)
		x, ok2 := a.minOfDay(en)
		if !ok1 || !ok2 {
			continue
		}
		e.S, e.E = s, x
		for w, dd := range dates {
			if dd == d {
				e.Week = w + 1
			}
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

type recentCand struct {
	e    candEntry
	secs float64
}

// recentTop: viel gebucht in [from, to] (bis 12 Gruppen) mit der jüngsten Buchung je Gruppe
func (a *App) recentTop(ctx context.Context, uid int64, from, to string) ([]recentCand, error) {
	rows, err := a.DB.Query(ctx, `WITH g AS (SELECT task_id, project_id, COALESCE(SUM(duration_seconds),0)::float8 AS secs FROM time_entries
			WHERE user_id=$1 AND date>=$2 AND date<=$3 AND project_id IS NOT NULL GROUP BY task_id, project_id ORDER BY 3 DESC LIMIT 12)
		SELECT x.*, g.secs FROM g CROSS JOIN LATERAL (SELECT `+candCols+candJoin+`
			WHERE e.user_id=$1 AND e.date>=$2 AND e.date<=$3 AND e.project_id=g.project_id AND e.task_id IS NOT DISTINCT FROM g.task_id
			ORDER BY e.date DESC, e.start_time DESC LIMIT 1) x ORDER BY g.secs DESC`, uid, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []recentCand
	for rows.Next() {
		var secs float64
		e, err := scanCand(rows, &secs)
		if err != nil {
			return nil, err
		}
		out = append(out, recentCand{e, secs})
	}
	return out, rows.Err()
}

type learnedCand struct {
	e candEntry
	n int
}

// learned: früher gewählt – zählt erst ab 2× gleicher Wahl, nur aus den letzten 90 Tagen (höchstens 2)
func (a *App) learned(ctx context.Context, uid int64, key string, cache map[string][]learnedCand) []learnedCand {
	key = truncate(key, 200)
	if v, ok := cache[key]; ok {
		return v
	}
	var out []learnedCand
	rows, err := a.DB.Query(ctx, `SELECT task_id, project_id, n FROM suggest_feedback WHERE user_id=$1 AND key=$2 AND n>=2 AND last >= now() - interval '90 days'
		ORDER BY n DESC, last DESC LIMIT 2`, uid, key)
	if err == nil {
		type fb struct {
			tid, pid int64
			n        int32
		}
		var fbs []fb
		for rows.Next() {
			var x fb
			if rows.Scan(&x.tid, &x.pid, &x.n) == nil {
				fbs = append(fbs, x)
			}
		}
		rows.Close()
		for _, x := range fbs {
			cond, args := "e.task_id IS NULL", []any{uid, x.pid}
			if x.tid != 0 {
				cond, args = "e.task_id=$3", append(args, x.tid)
			}
			e, err := scanCand(a.DB.QueryRow(ctx, `SELECT `+candCols+candJoin+` WHERE e.user_id=$1 AND e.project_id=$2 AND `+cond+` ORDER BY e.date DESC LIMIT 1`, args...))
			if err == nil {
				out = append(out, learnedCand{e, int(x.n)})
			}
		}
	}
	cache[key] = out
	return out
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}

/* ── Routen ────────────────────────────────────────────────────── */

func (a *App) routesGapSuggest(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/gap-suggest", a.auth(a.gapSuggest))
	mux.HandleFunc("POST /api/gap-suggest/learn", a.auth(a.gapLearn))
	mux.HandleFunc("GET /api/suggest-now", a.auth(a.suggestNow))
}

func (a *App) gapSuggest(w http.ResponseWriter, r *http.Request, u User) {
	date := r.URL.Query().Get("date")
	if _, ok := parseDay(date); !ok {
		badRequest(w, "Datum fehlt")
		return
	}
	ctx := r.Context()
	if on, why := a.suggestAllowedFor(ctx, u.ID, "Vorschläge zum Nachtragen"); !on {
		writeJSON(w, http.StatusOK, map[string]any{"date": date, "items": []any{}, "suggest": false, "reason": why})
		return
	}
	items, err := a.gapItems(ctx, u.ID, date)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"date": date, "items": items})
}

func (a *App) gapItems(ctx context.Context, uid int64, date string) ([]map[string]any, error) {
	ov, err := a.dayOverview(ctx, uid, date)
	if err != nil {
		return nil, err
	}
	st, err := a.userSettings(ctx, uid)
	if err != nil {
		return nil, err
	}
	hist, err := a.history(ctx, uid, date, 6)
	if err != nil {
		return nil, err
	}
	recent, err := a.recentTop(ctx, uid, addDays(date, -14), date)
	if err != nil {
		return nil, err
	}
	maxSecs := 1.0
	if len(recent) > 0 && recent[0].secs > 0 {
		maxSecs = recent[0].secs
	}
	var dayEntries []candEntry
	taken := [][2]int{}
	for _, e := range ov["entries"].([]map[string]any) {
		c := candEntry{TaskID: asInt64(e["task_id"]), ProjectID: asInt64(e["project_id"]), Comment: asStr(e["comment"]), JiraTicket: asStr(e["jira_ticket"]),
			TaskTitle: asStr(e["task_title"]), ProjName: asStr(e["project_name"]), S: e["s"].(int), E: e["e"].(int)}
		dayEntries = append(dayEntries, c)
		taken = append(taken, [2]int{c.S, c.E})
	}
	for _, p := range ov["pauses"].([]map[string]any) {
		taken = append(taken, [2]int{p["s"].(int), p["e"].(int)})
	}
	d, _ := parseDay(date)
	wd := int(d.Weekday())
	info, lcache := map[string]candEntry{}, map[string][]learnedCand{}
	common := func(m *cands, s, e int) {
		var before, after *candEntry
		for i := range dayEntries {
			x := &dayEntries[i]
			if x.E <= s+2 && s-x.E <= 30 {
				before = x
			}
			if after == nil && x.S >= e-2 && x.S-e <= 30 {
				after = x
			}
		}
		if before != nil {
			m.bump(before, 3, "davor "+hhmm(before.S)+"–"+hhmm(before.E))
		}
		if after != nil {
			m.bump(after, 2, "danach ab "+hhmm(after.S))
		}
		for i := range hist {
			x := &hist[i]
			if x.Week >= 1 && x.Week <= 5 && min(x.E, e)-max(x.S, s) >= 15 {
				m.bump(x, 2/float64(x.Week)+0.5, "sonst "+weekdaysAd[wd]+" um diese Zeit")
			}
		}
		for i := range recent {
			m.bump(&recent[i].e, 2*recent[i].secs/maxSecs, "zuletzt oft gebucht")
		}
		for _, l := range a.learned(ctx, uid, slotKey(date, s), lcache) {
			m.bump(&l.e, float64(min(4, l.n))/4, "früher hier gewählt")
		}
	}
	items := []map[string]any{}
	presence, _ := ov["presence"].([]span)
	// Termine zuerst (wie Node): freie Stücke im Termin, danach Lücken ohne Doppelung
	events, _ := ov["events"].([]map[string]any)
	sort.SliceStable(events, func(i, j int) bool { return events[i]["s"].(int) < events[j]["s"].(int) })
	for _, ev := range events {
		es, ee := ev["s"].(int), ev["e"].(int)
		title := asStr(ev["title"])
		seed := newCands(info)
		if prev := a.prevMeetingEntry(ctx, uid, title, date); prev != nil {
			seed.bump(prev, 6, "Termin früher so gebucht")
		}
		for _, l := range a.learned(ctx, uid, "ev:"+title, lcache) {
			e := l.e
			seed.bump(&e, 3*float64(min(4, l.n))/4, "früher für diesen Termin gewählt")
		}
		if byText := a.entryByComment(ctx, uid, title, date); byText != nil {
			seed.bump(byText, 5, "gleicher Titel früher gebucht")
		}
		for _, pp := range cutIntervals([][2]int{{es, ee}}, taken, 10) {
			taken = append(taken, pp)
			m := seed.clone()
			common(m, pp[0], pp[1])
			items = append(items, map[string]any{
				"kind": "event", "ref": ev["uid"], "key": "ev:" + title,
				"s": pp[0], "e": pp[1], "title": title, "comment": title, "candidates": m.top(4),
			})
		}
	}
	busyEv := make([][2]int, 0, len(items))
	for _, it := range items {
		busyEv = append(busyEv, [2]int{it["s"].(int), it["e"].(int)})
	}
	gapTitle := "Zwischen Buchungen, nichts gebucht"
	if len(presence) > 0 {
		gapTitle = "Am Mac, nichts gebucht"
	}
	for _, g := range ov["gaps"].([]span) {
		for _, pp := range cutIntervals([][2]int{{g.S, g.E}}, busyEv, 10) {
			m := newCands(info)
			common(m, pp[0], pp[1])
			items = append(items, map[string]any{"kind": "gap", "key": slotKey(date, pp[0]), "s": pp[0], "e": pp[1], "title": gapTitle, "comment": "", "candidates": m.top(4)})
		}
	}
	// Rest: fehlt danach noch Zeit → typischer Tag (ohne Buchungen) bzw. Block nach der letzten Buchung
	target := dayTargets(st)[wd]
	abs, _, err := a.monthData(ctx, uid, date, date)
	if err != nil {
		return nil, err
	}
	absH := 0.0
	for _, x := range abs {
		if x.Hours != nil {
			absH += *x.Hours
		} else {
			absH += target
		}
	}
	bm, _ := ov["booked_minutes"].(int)
	covered := float64(bm)
	for _, it := range items {
		covered += float64(it["e"].(int) - it["s"].(int))
	}
	rest := int(math.Round(math.Max(0, target-absH)*60 - covered))
	if rest >= 15 {
		var pat []dayBlock
		if len(presence) == 0 && len(dayEntries) == 0 {
			pat = typicalDay(hist, int(math.Round(float64(rest)/15))*15)
		}
		patLen := 0
		for _, b0 := range pat {
			patLen += b0.E - b0.S
			for _, pp := range cutIntervals([][2]int{{b0.S, b0.E}}, taken, 10) {
				taken = append(taken, pp)
				m := newCands(info)
				e := b0.Entry
				m.bump(&e, 4, "sonst "+weekdaysAd[wd]+" "+hhmm(pp[0])+"–"+hhmm(pp[1]))
				common(m, pp[0], pp[1])
				items = append(items, map[string]any{"kind": "pattern", "key": slotKey(date, pp[0]), "s": pp[0], "e": pp[1], "title": "Typisch für diesen Wochentag", "comment": "", "candidates": m.top(4)})
			}
		}
		if left := rest - patLen; left >= 15 {
			s := -1
			for _, x := range dayEntries {
				s = max(s, x.E)
			}
			for _, b := range pat {
				s = max(s, b.E)
			}
			for _, it := range items {
				s = max(s, it["e"].(int))
			}
			if s < 0 {
				s = 9 * 60
			}
			e := min(24*60-1, s+int(math.Round(float64(left)/15))*15)
			m := newCands(info)
			common(m, s, e)
			items = append(items, map[string]any{"kind": "rest", "key": slotKey(date, s), "s": s, "e": e, "title": "Restzeit bis zum Soll", "comment": "", "candidates": m.top(4)})
		}
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i]["s"].(int) < items[j]["s"].(int) })
	return items, nil
}

func asInt64(v any) int64 {
	switch x := v.(type) {
	case int64:
		return x
	case int32:
		return int64(x)
	case float64:
		return int64(x)
	}
	return 0
}

// prevMeetingEntry: letzte Buchung zu einem gleichnamigen Termin (über meeting_state)
func (a *App) prevMeetingEntry(ctx context.Context, uid int64, title, beforeDate string) *candEntry {
	var entryID int64
	err := a.DB.QueryRow(ctx, `SELECT ms.entry_id FROM calendar_events c
		JOIN meeting_state ms ON ms.user_id=c.user_id AND ms.key IN ('cal:'||c.uid, c.uid) AND ms.status='booked' AND ms.entry_id IS NOT NULL
		WHERE c.user_id=$1 AND c.title=$2 AND c.date<$3 ORDER BY c.date DESC LIMIT 1`, uid, title, beforeDate).Scan(&entryID)
	if err != nil || entryID == 0 {
		return nil
	}
	e, err := scanCand(a.DB.QueryRow(ctx, `SELECT `+candCols+candJoin+` WHERE e.user_id=$1 AND e.id=$2`, uid, entryID))
	if err != nil || e.ProjectID == 0 {
		return nil
	}
	return &e
}

func (a *App) entryByComment(ctx context.Context, uid int64, comment, beforeDate string) *candEntry {
	e, err := scanCand(a.DB.QueryRow(ctx, `SELECT `+candCols+candJoin+`
		WHERE e.user_id=$1 AND e.comment=$2 AND e.date<$3 AND e.project_id IS NOT NULL ORDER BY e.date DESC, e.start_time DESC LIMIT 1`, uid, comment, beforeDate))
	if err != nil || e.ProjectID == 0 {
		return nil
	}
	return &e
}

// gapLearn: aus einem Vorschlag gebucht → Wahl merken (nur eigene Projekte/Tickets). Termin/SSH verknüpft der Client selbst.
func (a *App) gapLearn(w http.ResponseWriter, r *http.Request, u User) {
	var in body
	if !readJSON(w, r, &in) {
		return
	}
	ctx := r.Context()
	if on, why := a.suggestAllowedFor(ctx, u.ID, "Vorschläge"); !on { // aus: nichts lernen
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "learned": false, "reason": why})
		return
	}
	if invalidID(in["task_id"]) || invalidID(in["project_id"]) {
		badRequest(w, "task_id/project_id ungültig")
		return
	}
	pid, tid := asID(in["project_id"]), asID(in["task_id"])
	if pid == nil || !a.owned(ctx, "projects", pid, u.ID) || !a.owned(ctx, "tasks", tid, u.ID) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "learned": false})
		return
	}
	var t int64
	if tid != nil {
		t = tid.(int64)
	}
	learned := false
	for _, k := range []string{asStr(in["key"]), asStr(in["slot"])} {
		if strings.TrimSpace(k) == "" {
			continue
		}
		if _, err := a.DB.Exec(ctx, `INSERT INTO suggest_feedback(user_id,key,task_id,project_id,n,last) VALUES($1,$2,$3,$4,1,now())
			ON CONFLICT (user_id,key,task_id,project_id) DO UPDATE SET n=suggest_feedback.n+1, last=now()`, u.ID, truncate(k, 200), t, pid); err != nil {
			a.fail(w, err)
			return
		}
		learned = true
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "learned": learned})
}

// suggestNow: „Jetzt vermutlich“ – Gewohnheit (gleicher Wochentag/Uhrzeit), früher gewählt, zuletzt heute
func (a *App) suggestNow(w http.ResponseWriter, r *http.Request, u User) {
	ctx := r.Context()
	if on, why := a.suggestAllowedFor(ctx, u.ID, "Vorschläge"); !on {
		writeJSON(w, http.StatusOK, map[string]any{"context": nil, "items": []any{}, "suggest": false, "reason": why})
		return
	}
	now := time.Now().In(a.Loc)
	date, nm := now.Format("2006-01-02"), now.Hour()*60+now.Minute()
	hist, err := a.history(ctx, u.ID, date, 5)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"context": nil, "items": a.nowItems(ctx, u.ID, date, nm, hist)})
}

func (a *App) nowItems(ctx context.Context, uid int64, date string, nm int, hist []candEntry) []map[string]any {
	m := newCands(map[string]candEntry{})
	for _, l := range a.learned(ctx, uid, slotKey(date, nm), map[string][]learnedCand{}) {
		m.bump(&l.e, float64(min(4, l.n))/4, "früher hier gewählt")
	}
	nowBump(m, hist, nm)
	if last, err := scanCand(a.DB.QueryRow(ctx, `SELECT `+candCols+candJoin+` WHERE e.user_id=$1 AND e.date=$2 AND e.project_id IS NOT NULL ORDER BY e.start_time DESC LIMIT 1`, uid, date)); err == nil {
		m.bump(&last, 1.5, "zuletzt heute")
	}
	out := []map[string]any{}
	for _, c := range m.top(3) {
		if c["score"].(float64) < 2 {
			continue
		}
		c["comment"] = c["last_comment"]
		delete(c, "last_comment")
		delete(c, "project_name")
		c["key"] = slotKey(date, nm)
		out = append(out, c)
	}
	return out
}

// nowBump: Buchungen der letzten 5 gleichen Wochentage, die jetzt liefen (reine Logik, getestet)
func nowBump(m *cands, hist []candEntry, nm int) {
	for i := range hist {
		x := &hist[i]
		if x.Week >= 1 && x.Week <= 5 && x.S <= nm && x.E > nm {
			m.bump(x, 2/float64(x.Week)+0.5, "sonst um diese Zeit")
		}
	}
}

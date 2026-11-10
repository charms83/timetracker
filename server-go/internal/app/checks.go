package app

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

/* Plausibilitätsprüfung (wie backend/checks.js) – Schutz vor Fehl- und Doppelbuchungen.
   block   – wird nicht gespeichert (Zukunft, > 16 h)
   confirm – nur mit Bestätigung + Begründung (Einzelbuchung/Tag > 10 h, Überschneidung, Urlaubs-/Krank-/Feiertag, gesperrter Tempo-Monat)
   hint    – nur Hinweis (Ruhezeit < 11 h, Pause nach 6/9 h fehlt)
   Der Timer wird nie blockiert, die Buchung wird nur markiert („Bitte prüfen“). */

type issue struct {
	Level string         `json:"level"`
	Code  string         `json:"code"`
	Text  string         `json:"text"`
	Ref   any            `json:"ref,omitempty"`
	Other map[string]any `json:"other,omitempty"`
}

type candidate struct {
	ID               int64
	Date, Start, End string
}

type checkResult struct {
	Issues []issue
	Note   string
}

const hour = 3600.0

func (a *App) routesChecks(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/checks/entry", a.auth(a.checkEntryRoute))
	mux.HandleFunc("GET /api/checks/open", a.auth(a.checksOpen))
	mux.HandleFunc("POST /api/checks/ack", a.auth(a.checksAck))
	mux.HandleFunc("GET /api/overlaps", a.auth(a.overlapsRoute))
	mux.HandleFunc("POST /api/overlaps/ignore", a.auth(a.overlapsIgnore))
	mux.HandleFunc("POST /api/time-entries/resolve-overlap", a.auth(a.resolveOverlap))
}

func (a *App) hm(iso string) string {
	if t, ok := a.parseTime(iso); ok {
		return t.In(a.Loc).Format("15:04")
	}
	return clock(iso)
}

func fmtH(sec float64) string {
	return strings.Replace(strconv.FormatFloat(math.Round(sec/360)/10, 'f', -1, 64), ".", ",", 1) + " h"
}

func (a *App) overlapIgnore(ctx context.Context, uid int64) []string {
	var raw *string
	if err := a.DB.QueryRow(ctx, `SELECT value FROM user_settings WHERE user_id=$1 AND key='overlap_ignore'`, uid).Scan(&raw); err != nil || raw == nil {
		return nil
	}
	var ign []string
	_ = json.Unmarshal([]byte(*raw), &ign)
	return ign
}

func pairKey(x, y int64) string {
	if x > y {
		x, y = y, x
	}
	return fmt.Sprintf("%d-%d", x, y)
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func (a *App) check(ctx context.Context, uid int64, e candidate) ([]issue, error) {
	out := []issue{}
	if e.Start == "" {
		return out, nil
	}
	s, ok := a.parseTime(e.Start)
	if !ok {
		return out, nil
	}
	date := e.Date
	if date == "" {
		date = s.In(a.Loc).Format("2006-01-02")
	}
	if s.After(time.Now().Add(5 * time.Minute)) {
		out = append(out, issue{Level: "block", Code: "future", Text: "Beginn liegt in der Zukunft"})
	}
	if en, ok := a.parseTime(e.End); ok && e.End != "" {
		dur := en.Sub(s).Seconds()
		if dur <= 0 {
			out = append(out, issue{Level: "block", Code: "order", Text: "Ende liegt vor dem Beginn"})
		}
		if dur > 16*hour {
			out = append(out, issue{Level: "block", Code: "long16", Text: "Einzelbuchung über 16 h (" + fmtH(dur) + ")"})
		} else if dur > 10*hour {
			out = append(out, issue{Level: "confirm", Code: "long10", Text: "Einzelbuchung über 10 h (" + fmtH(dur) + ") – Timer vergessen?"})
		}
		rows, err := a.DB.Query(ctx, `SELECT id, start_time, end_time, COALESCE(duration_seconds,0), COALESCE(comment,''), COALESCE(source,'')
			FROM time_entries WHERE user_id=$1 AND date=$2 AND end_time IS NOT NULL AND id<>$3`, uid, date, e.ID)
		if err != nil {
			return nil, err
		}
		type other struct {
			id           int64
			st, en       string
			dur          int64
			comment, src string
		}
		var others []other
		day := math.Max(0, dur)
		for rows.Next() {
			var o other
			if err := rows.Scan(&o.id, &o.st, &o.en, &o.dur, &o.comment, &o.src); err != nil {
				rows.Close()
				return nil, err
			}
			others = append(others, o)
			day += float64(o.dur)
		}
		rows.Close()
		if day > 16*hour {
			out = append(out, issue{Level: "block", Code: "day16", Text: "Tag hätte " + fmtH(day) + " – mehr als 16 h ist nicht möglich"})
		} else if day > 10*hour {
			out = append(out, issue{Level: "confirm", Code: "day10", Text: "Tag hätte " + fmtH(day) + " – über 10 h (Arbeitszeitgesetz)"})
		}
		ign := a.overlapIgnore(ctx, uid)
		for _, o := range others {
			if o.src == "tempo" || o.src == "tempo_csv" || o.src == "plane_import" { // Spiegel/Import: Uhrzeiten nur Platzhalter
				continue
			}
			if e.ID != 0 && contains(ign, pairKey(e.ID, o.id)) { // „Ist richtig so“
				continue
			}
			os, ok1 := a.parseTime(o.st)
			oe, ok2 := a.parseTime(o.en)
			if !ok1 || !ok2 {
				continue
			}
			end, start := en, s
			if oe.Before(end) {
				end = oe
			}
			if os.After(start) {
				start = os
			}
			if ov := end.Sub(start).Minutes(); ov >= 3 {
				txt := fmt.Sprintf("Überschneidet sich %d min mit %s–%s", int(math.Round(ov)), a.hm(o.st), a.hm(o.en))
				if o.comment != "" {
					c := []rune(o.comment)
					if len(c) > 40 {
						c = c[:40]
					}
					txt += " „" + string(c) + "“"
				}
				out = append(out, issue{Level: "confirm", Code: "overlap", Ref: o.id, Text: txt,
					Other: map[string]any{"id": o.id, "start_time": o.st, "end_time": o.en, "comment": o.comment}})
				break
			}
		}
		// Pause (ArbZG §4): > 6 h → 30 min, > 9 h → 45 min
		if day > 6*hour {
			prows, err := a.DB.Query(ctx, `SELECT start_time, end_time FROM pauses WHERE user_id=$1 AND date=$2 AND end_time IS NOT NULL`, uid, date)
			if err != nil {
				return nil, err
			}
			pauses := 0.0
			for prows.Next() {
				var ps, pe string
				if err := prows.Scan(&ps, &pe); err != nil {
					prows.Close()
					return nil, err
				}
				if secs, ok := a.seconds(ps, pe); ok {
					pauses += float64(secs)
				}
			}
			prows.Close()
			need := 30.0 * 60
			if day > 9*hour {
				need = 45 * 60
			}
			if pauses < need {
				out = append(out, issue{Level: "hint", Code: "pause", Text: fmt.Sprintf("Bei %s Arbeit sind %d min Pause vorgeschrieben – erfasst: %d min", fmtH(day), int(need/60), int(math.Round(pauses/60)))})
			}
		}
	}
	var absType string
	err := a.DB.QueryRow(ctx, `SELECT type FROM absences WHERE user_id=$1 AND date_from<=$2 AND date_to>=$2 AND type IN ('urlaub','krankheit','feiertag') LIMIT 1`, uid, date).Scan(&absType)
	if err == nil {
		what := map[string]string{"urlaub": "Urlaub", "krankheit": "Krankheit"}[absType]
		if what == "" {
			what = "ein Feiertag"
		}
		out = append(out, issue{Level: "confirm", Code: "absence", Text: "An diesem Tag ist " + what + " eingetragen"})
	} else if err != pgx.ErrNoRows {
		return nil, err
	}
	if len(date) >= 7 {
		var locked bool
		if err := a.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tempo_months WHERE user_id=$1 AND month=$2 AND locked=1)`, uid, date[:7]).Scan(&locked); err == nil && locked {
			out = append(out, issue{Level: "confirm", Code: "locked", Text: fmt.Sprintf("%s/%s ist in Tempo gesperrt – die Änderung kommt dort nicht mehr an", date[5:7], date[:4])})
		}
	}
	// Ruhezeit (ArbZG §5): 11 h zwischen Arbeitsende und -beginn
	var prev *string
	since := s.Add(-2 * 24 * time.Hour).In(a.Loc).Format("2006-01-02")
	if err := a.DB.QueryRow(ctx, `SELECT MAX(end_time) FROM time_entries WHERE user_id=$1 AND date<$2 AND end_time IS NOT NULL AND date>=$3`, uid, date, since).Scan(&prev); err == nil && prev != nil {
		if pe, ok := a.parseTime(*prev); ok {
			if gap := s.Sub(pe); gap > 0 && gap < 11*time.Hour {
				out = append(out, issue{Level: "hint", Code: "rest", Text: fmt.Sprintf("Nur %s Ruhezeit seit gestern %s (11 h vorgeschrieben)", fmtH(gap.Seconds()), a.hm(*prev))})
			}
		}
	}
	return out, nil
}

func nonHints(is []issue) []issue {
	out := []issue{}
	for _, i := range is {
		if i.Level != "hint" {
			out = append(out, i)
		}
	}
	return out
}

func codes(is []issue) string {
	var c []string
	for _, i := range nonHints(is) {
		c = append(c, i.Code)
	}
	return strings.Join(c, ",")
}

// guard: true = Antwort schon gesendet (blockiert oder Bestätigung nötig). Sonst Ergebnis für stamp().
func (a *App) guard(w http.ResponseWriter, r *http.Request, uid int64, in body, e candidate) (*checkResult, bool) {
	issues, err := a.check(r.Context(), uid, e)
	if err != nil {
		a.fail(w, err)
		return nil, true
	}
	var blocks, conf []string
	for _, i := range issues {
		switch i.Level {
		case "block":
			blocks = append(blocks, i.Text)
		case "confirm":
			conf = append(conf, i.Text)
		}
	}
	if len(blocks) > 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": strings.Join(blocks, " · "), "issues": issues})
		return nil, true
	}
	reason := strings.TrimSpace(asStr(in["confirm_reason"]))
	if b, _ := in["confirm_checks"].(bool); len(conf) > 0 && !(b && len([]rune(reason)) >= 3) {
		writeJSON(w, http.StatusConflict, map[string]any{"needs_confirm": true, "error": strings.Join(conf, " · "), "issues": issues})
		return nil, true
	}
	res := &checkResult{Issues: issues}
	if len(conf) > 0 {
		if rr := []rune(reason); len(rr) > 300 {
			reason = string(rr[:300])
		}
		res.Note = reason
	}
	return res, false
}

// stamp markiert die gespeicherte Buchung (Codes + Begründung) und schreibt das Protokoll.
func (a *App) stamp(ctx context.Context, uid, id int64, c *checkResult) {
	if c == nil {
		return
	}
	flags := codes(c.Issues)
	if _, err := a.DB.Exec(ctx, `UPDATE time_entries SET check_flags=$3, check_note=$4 WHERE id=$1 AND user_id=$2`, id, uid, nz(flags), nz(c.Note)); err != nil {
		a.Log.Error("Prüfmarkierung", "err", err)
		return
	}
	if c.Note != "" {
		a.sysLog(ctx, uid, "warn", "pruefung", fmt.Sprintf("Buchung #%d trotz Hinweis gespeichert (%s): %s", id, flags, c.Note))
	}
}

// flagAfter: Timer/automatische Buchungen nie blockieren, nur markieren (geht dann nicht ungeprüft weiter)
func (a *App) flagAfter(ctx context.Context, uid, id int64) {
	var date, st string
	var en *string
	if err := a.DB.QueryRow(ctx, `SELECT date, start_time, end_time FROM time_entries WHERE id=$1 AND user_id=$2`, id, uid).Scan(&date, &st, &en); err != nil {
		return
	}
	c := candidate{ID: id, Date: date, Start: st}
	if en != nil {
		c.End = *en
	}
	is, err := a.check(ctx, uid, c)
	if err != nil {
		return
	}
	if flags := codes(is); flags == "" {
		_, _ = a.DB.Exec(ctx, `UPDATE time_entries SET check_flags=NULL WHERE id=$1 AND user_id=$2`, id, uid)
	} else {
		_, _ = a.DB.Exec(ctx, `UPDATE time_entries SET check_flags=$3 WHERE id=$1 AND user_id=$2 AND check_note IS NULL`, id, uid, flags)
	}
}

func (a *App) sysLog(ctx context.Context, uid int64, level, category, msg string) {
	var who any
	if uid != 0 {
		who = uid
	}
	if _, err := a.DB.Exec(ctx, `INSERT INTO system_logs(user_id,timestamp,level,category,message) VALUES($1,$2,$3,$4,$5)`,
		who, time.Now().UTC().Format(isoMillis), level, category, msg); err != nil {
		a.Log.Error("System-Log", "err", err)
	}
}

/* ── Routen ───────────────────────────────────────────────────────── */

func (a *App) checkEntryRoute(w http.ResponseWriter, r *http.Request, u User) {
	var in body
	if !readJSON(w, r, &in) {
		return
	}
	is, err := a.check(r.Context(), u.ID, candidate{ID: int64(asFloat(in["id"])), Date: asStr(in["date"]), Start: asStr(in["start_time"]), End: asStr(in["end_time"])})
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, is)
}

func (a *App) checksOpen(w http.ResponseWriter, r *http.Request, u User) {
	since := time.Now().Add(-31 * 24 * time.Hour).In(a.Loc).Format("2006-01-02")
	rs, err := a.maps(r.Context(), `SELECT e.id, e.date, e.start_time, e.end_time, e.duration_seconds, e.comment, e.check_flags, t.jira_ticket, p.name AS project_name
		FROM time_entries e LEFT JOIN tasks t ON t.id=e.task_id LEFT JOIN projects p ON p.id=e.project_id
		WHERE e.user_id=$1 AND e.check_flags IS NOT NULL AND e.check_note IS NULL AND e.date>=$2 ORDER BY e.date DESC, e.start_time`, u.ID, since)
	if err != nil {
		a.fail(w, err)
		return
	}
	for _, e := range rs {
		c := candidate{ID: e["id"].(int64), Date: asStr(e["date"]), Start: asStr(e["start_time"]), End: asStr(e["end_time"])}
		is, err := a.check(r.Context(), u.ID, c)
		if err != nil {
			a.fail(w, err)
			return
		}
		e["issues"] = nonHints(is)
	}
	writeJSON(w, http.StatusOK, rs)
}

func (a *App) checksAck(w http.ResponseWriter, r *http.Request, u User) {
	var in body
	if !readJSON(w, r, &in) {
		return
	}
	id, note := int64(asFloat(in["id"])), strings.TrimSpace(asStr(in["reason"]))
	if len([]rune(note)) < 3 {
		badRequest(w, "Bitte kurz begründen")
		return
	}
	if rr := []rune(note); len(rr) > 300 {
		note = string(rr[:300])
	}
	tag, err := a.DB.Exec(r.Context(), `UPDATE time_entries SET check_note=$3 WHERE id=$1 AND user_id=$2`, id, u.ID, note)
	if err != nil {
		a.fail(w, err)
		return
	}
	if tag.RowsAffected() == 0 {
		notFound(w)
		return
	}
	a.sysLog(r.Context(), u.ID, "warn", "pruefung", fmt.Sprintf("Buchung #%d als geprüft bestätigt: %s", id, note))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

/* ── Überlappungen der letzten Tage ──────────────────────────────── */

// eqNonNil: wie JS „a && a === b“ – leer/NULL zählt als nicht gesetzt
func eqNonNil(x, y any) bool {
	return x != nil && y != nil && fmt.Sprint(x) != "" && fmt.Sprint(x) == fmt.Sprint(y)
}

func (a *App) overlapPairs(ctx context.Context, uid int64, days int) ([]map[string]any, error) {
	since := time.Now().Add(-time.Duration(days-1) * 24 * time.Hour).In(a.Loc).Format("2006-01-02")
	rows, err := a.maps(ctx, `SELECT e.id, e.date, e.start_time, e.end_time, e.duration_seconds, e.comment, e.task_id, e.project_id, e.source,
		p.name AS project_name, t.jira_ticket, t.title AS task_title
		FROM time_entries e LEFT JOIN projects p ON p.id=e.project_id LEFT JOIN tasks t ON t.id=e.task_id
		WHERE e.user_id=$1 AND e.date>=$2 AND e.end_time IS NOT NULL AND COALESCE(e.source,'') NOT IN ('tempo','tempo_csv','plane_import') ORDER BY e.date, e.start_time`, uid, since)
	if err != nil {
		return nil, err
	}
	ign := a.overlapIgnore(ctx, uid)
	out := []map[string]any{}
	for i := 0; i < len(rows); i++ {
		for j := i + 1; j < len(rows) && rows[j]["date"] == rows[i]["date"]; j++ {
			x, y := rows[i], rows[j]
			xs, ok1 := a.parseTime(asStr(x["start_time"]))
			xe, ok2 := a.parseTime(asStr(x["end_time"]))
			ys, ok3 := a.parseTime(asStr(y["start_time"]))
			ye, ok4 := a.parseTime(asStr(y["end_time"]))
			if !ok1 || !ok2 || !ok3 || !ok4 {
				continue
			}
			end, start := xe, xs
			if ye.Before(end) {
				end = ye
			}
			if ys.After(start) {
				start = ys
			}
			ov := end.Sub(start).Minutes()
			if ov < 3 || contains(ign, pairKey(x["id"].(int64), y["id"].(int64))) {
				continue
			}
			same := eqNonNil(x["task_id"], y["task_id"]) || eqNonNil(x["jira_ticket"], y["jira_ticket"]) ||
				(x["task_id"] == nil && y["task_id"] == nil && fmt.Sprint(x["project_id"]) == fmt.Sprint(y["project_id"]))
			dx, _ := x["duration_seconds"].(int32)
			dy, _ := y["duration_seconds"].(int32)
			shorter := math.Min(float64(dx), float64(dy)) / 60
			suggest := "trim"
			if same || ov >= 0.7*shorter {
				suggest = "merge"
			}
			out = append(out, map[string]any{"a": x, "b": y, "minutes": int(math.Round(ov)), "same": same, "suggest": suggest})
		}
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 { // jüngste zuerst, wie Node
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

func (a *App) overlapsRoute(w http.ResponseWriter, r *http.Request, u User) {
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	if days == 0 {
		days = 14
	}
	if days < 1 {
		days = 1
	}
	if days > 90 {
		days = 90
	}
	out, err := a.overlapPairs(r.Context(), u.ID, days)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *App) overlapsIgnore(w http.ResponseWriter, r *http.Request, u User) {
	var in body
	if !readJSON(w, r, &in) {
		return
	}
	x, y := int64(asFloat(in["a"])), int64(asFloat(in["b"]))
	if x == 0 || y == 0 || x == y {
		badRequest(w, "a und b erforderlich")
		return
	}
	ctx := r.Context()
	ign := append(a.overlapIgnore(ctx, u.ID), pairKey(x, y))
	seen, uniq := map[string]bool{}, []string{}
	for _, k := range ign {
		if !seen[k] {
			seen[k] = true
			uniq = append(uniq, k)
		}
	}
	if len(uniq) > 500 {
		uniq = uniq[len(uniq)-500:]
	}
	raw, _ := json.Marshal(uniq)
	// „Ist richtig so“ → Überschneidungs-Markierung an beiden Buchungen entfernen
	for _, id := range []int64{x, y} {
		var flags *string
		if err := a.DB.QueryRow(ctx, `SELECT check_flags FROM time_entries WHERE id=$1 AND user_id=$2`, id, u.ID).Scan(&flags); err != nil || flags == nil {
			continue
		}
		var keep []string
		for _, f := range strings.Split(*flags, ",") {
			if f != "overlap" {
				keep = append(keep, f)
			}
		}
		if _, err := a.DB.Exec(ctx, `UPDATE time_entries SET check_flags=$3 WHERE id=$1 AND user_id=$2`, id, u.ID, nz(strings.Join(keep, ","))); err != nil {
			a.fail(w, err)
			return
		}
	}
	if _, err := a.DB.Exec(ctx, `INSERT INTO user_settings(user_id,key,value) VALUES($1,'overlap_ignore',$2)
		ON CONFLICT (user_id,key) DO UPDATE SET value=EXCLUDED.value`, u.ID, string(raw)); err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

/* ── Doppelte/überlappende Buchungen auflösen ────────────────────── */

type entryRow struct {
	ID                int64
	Start, End        string
	Dur               int64
	Comment, Activity *string
	TaskID, ProjectID *int64
}

func (a *App) loadEntryRow(ctx context.Context, uid, id int64) (*entryRow, error) {
	var e entryRow
	var st string
	var en *string
	err := a.DB.QueryRow(ctx, `SELECT id, start_time, end_time, COALESCE(duration_seconds,0), comment, activity, task_id, project_id
		FROM time_entries WHERE id=$1 AND user_id=$2`, id, uid).Scan(&e.ID, &st, &en, &e.Dur, &e.Comment, &e.Activity, &e.TaskID, &e.ProjectID)
	if err != nil {
		return nil, err
	}
	e.Start = st
	if en != nil {
		e.End = *en
	}
	return &e, nil
}

func (a *App) resolveOverlap(w http.ResponseWriter, r *http.Request, u User) {
	var in body
	if !readJSON(w, r, &in) {
		return
	}
	ctx := r.Context()
	A, errA := a.loadEntryRow(ctx, u.ID, int64(asFloat(in["a"])))
	B, errB := a.loadEntryRow(ctx, u.ID, int64(asFloat(in["b"])))
	if errA != nil || errB != nil || A.ID == B.ID || A.End == "" || B.End == "" {
		badRequest(w, "Buchungen nicht gefunden")
		return
	}
	if asStr(in["mode"]) != "trim" {
		// behalten: die mit Ticket, sonst die längere
		keep, drop := A, B
		if (B.TaskID != nil && A.TaskID == nil) || ((B.TaskID != nil) == (A.TaskID != nil) && B.Dur > A.Dur) {
			keep, drop = B, A
		}
		st, en := keep.Start, keep.End
		if drop.Start < st {
			st = drop.Start
		}
		if drop.End > en {
			en = drop.End
		}
		secs, ok := a.seconds(st, en)
		if !ok {
			badRequest(w, "Zeit nicht lesbar")
			return
		}
		var texts []string
		seen := map[string]bool{}
		for _, c := range []*string{keep.Comment, drop.Comment} {
			if c != nil {
				if t := strings.TrimSpace(*c); t != "" && !seen[t] {
					seen[t] = true
					texts = append(texts, t)
				}
			}
		}
		err := a.inTx(ctx, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `UPDATE time_entries SET start_time=$3, end_time=$4, duration_seconds=$5, comment=$6, activity=COALESCE(activity,$7),
				task_id=COALESCE(task_id,$8), project_id=COALESCE(project_id,$9), version=version+1, updated_at=now() WHERE id=$1 AND user_id=$2`,
				keep.ID, u.ID, st, en, secs, nz(strings.Join(texts, " · ")), drop.Activity, drop.TaskID, drop.ProjectID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE meeting_state SET entry_id=$3 WHERE entry_id=$2 AND user_id=$1`, u.ID, drop.ID, keep.ID); err != nil {
				return err
			}
			return a.deleteEntryTx(ctx, tx, u.ID, drop.ID)
		})
		if err != nil {
			a.fail(w, err)
			return
		}
		a.flagAfter(ctx, u.ID, keep.ID)
		a.sysLog(ctx, u.ID, "info", "buchungen", fmt.Sprintf("Buchungen #%d und #%d zusammengeführt (%s–%s)", drop.ID, keep.ID, a.hm(st), a.hm(en)))
		a.syncAuto(u.ID)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "kept": keep.ID, "removed": drop.ID})
		return
	}
	// trim: kürzere Buchung um den überlappenden Teil kürzen
	long, short := A, B
	if A.Dur < B.Dur {
		long, short = B, A
	}
	s2, e2 := short.Start, short.End
	switch {
	case short.Start >= long.Start && short.Start < long.End: // fängt in der langen an → später beginnen
		s2 = long.End
	case short.End > long.Start && short.End <= long.End: // endet in der langen → früher enden
		e2 = long.Start
	default:
		badRequest(w, "Die kürzere Buchung liegt ganz in der längeren – bitte zusammenführen oder eine löschen")
		return
	}
	d, ok := a.seconds(s2, e2)
	if !ok {
		badRequest(w, "Zeit nicht lesbar")
		return
	}
	if d < 60 {
		badRequest(w, "Danach bliebe weniger als 1 Minute – bitte zusammenführen")
		return
	}
	if _, err := a.DB.Exec(ctx, `UPDATE time_entries SET start_time=$3, end_time=$4, duration_seconds=$5, version=version+1, updated_at=now() WHERE id=$1 AND user_id=$2`,
		short.ID, u.ID, s2, e2, d); err != nil {
		a.fail(w, err)
		return
	}
	a.flagAfter(ctx, u.ID, short.ID)
	a.flagAfter(ctx, u.ID, long.ID)
	a.syncAuto(u.ID)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "trimmed": short.ID})
}

package app

import (
	"fmt"
	"math"
	"net/http"
	"sort"
	"time"
)

/* Datenkonsistenz der eigenen Buchungen (wie GET /api/check/integrity in backend/server.js), nur eigene Daten.
   Bewusst begrenzt: Überschneidungen und Pausen der letzten 365 Tage, sonst höchstens 500 Treffer je Prüfung.
   Überschneidungen nach derselben Regel wie Assistent/Plausibilitätsprüfung (overlapPairs, checks.go). */

const integrityDays = 365

func (a *App) routesIntegrity(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/check/integrity", a.auth(a.integrityRoute))
}

func integritySummary(issues []map[string]any) map[string]any {
	n := map[string]int{}
	for _, i := range issues {
		n[asStr(i["severity"])]++
	}
	return map[string]any{"total": len(issues), "errors": n["error"], "warnings": n["warn"], "infos": n["info"]}
}

func (a *App) integrityRoute(w http.ResponseWriter, r *http.Request, u User) {
	ctx := r.Context()
	issues := []map[string]any{}
	// 1. Überschneidungen
	pairs, err := a.overlapPairs(ctx, u.ID, integrityDays)
	if err != nil {
		a.fail(w, err)
		return
	}
	for _, p := range pairs {
		x, y := jm(p["a"]), jm(p["b"])
		issues = append(issues, map[string]any{"type": "overlap", "severity": "warn", "date": x["date"],
			"message": fmt.Sprintf("Überschneidung %v min: %s–%s und %s–%s – im Assistenten lösen", p["minutes"],
				a.hm(asStr(x["start_time"])), a.hm(asStr(x["end_time"])), a.hm(asStr(y["start_time"])), a.hm(asStr(y["end_time"]))),
			"ids": []any{x["id"], y["id"]}})
	}
	sliceHM := func(s string) string {
		if len(s) >= 16 {
			return s[11:16]
		}
		return "?"
	}
	// 2. fehlendes Ende (älter als heute), 3. negative Dauer, 5. sehr lange Einträge (> 12 h, ohne Tempo-Spiegel)
	rs, err := a.maps(ctx, `SELECT id, date, start_time, end_time, duration_seconds, comment,
		(end_time IS NULL AND date < $2) AS missing_end,
		(duration_seconds < 0 OR (end_time IS NOT NULL AND end_time < start_time)) AS negative,
		(duration_seconds > 43200 AND COALESCE(source,'') NOT IN ('tempo','tempo_csv')) AS too_long
		FROM time_entries WHERE user_id=$1 AND ((end_time IS NULL AND date < $2) OR duration_seconds < 0 OR (end_time IS NOT NULL AND end_time < start_time)
		  OR (duration_seconds > 43200 AND COALESCE(source,'') NOT IN ('tempo','tempo_csv')))
		ORDER BY date DESC, start_time LIMIT 500`, u.ID, a.today())
	if err != nil {
		a.fail(w, err)
		return
	}
	comment := func(e map[string]any) string { return jsOr(asStr(e["comment"]), "kein Kommentar") }
	for _, e := range rs {
		if e["missing_end"] == true {
			issues = append(issues, map[string]any{"type": "missing_end", "severity": "error", "date": e["date"],
				"message": "Kein Ende: " + sliceHM(asStr(e["start_time"])) + " – " + comment(e), "id": e["id"]})
		}
		if e["negative"] == true {
			end := "?"
			if e["end_time"] != nil {
				end = sliceHM(asStr(e["end_time"]))
			}
			issues = append(issues, map[string]any{"type": "negative_duration", "severity": "error", "date": e["date"],
				"message": "Negative Dauer: " + sliceHM(asStr(e["start_time"])) + "–" + end, "id": e["id"]})
		}
		if e["too_long"] == true {
			h := math.Round(asFloat(e["duration_seconds"])/3600*10) / 10
			issues = append(issues, map[string]any{"type": "too_long", "severity": "warn", "date": e["date"],
				"message": fmt.Sprintf("Sehr langer Eintrag: %vh – %s", h, comment(e)), "id": e["id"]})
		}
	}
	// 6. Pausen: an einen Arbeitstag gebundene Pausen dürfen keine Buchung schneiden; Pausen untereinander nie
	since := time.Now().In(a.loc()).AddDate(0, 0, -integrityDays).Format("2006-01-02")
	pauses, err := a.maps(ctx, `SELECT id, date, start_time, end_time, work_day_id FROM pauses WHERE user_id=$1 AND end_time IS NOT NULL AND date >= $2 ORDER BY date, start_time LIMIT 5000`, u.ID, since)
	if err != nil {
		a.fail(w, err)
		return
	}
	entries, err := a.maps(ctx, `SELECT id, date, start_time, end_time FROM time_entries WHERE user_id=$1 AND end_time IS NOT NULL AND date >= $2
		AND date IN (SELECT date FROM pauses WHERE user_id=$1 AND work_day_id IS NOT NULL AND date >= $2) ORDER BY date, start_time LIMIT 20000`, u.ID, since)
	if err != nil {
		a.fail(w, err)
		return
	}
	byDate := map[string][]map[string]any{}
	for _, e := range entries {
		byDate[asStr(e["date"])] = append(byDate[asStr(e["date"])], e)
	}
	pByDate := map[string][]map[string]any{}
	var dates []string
	for _, p := range pauses {
		d := asStr(p["date"])
		if pByDate[d] == nil {
			dates = append(dates, d)
		}
		pByDate[d] = append(pByDate[d], p)
	}
	sort.Strings(dates)
	t := func(s any) (time.Time, bool) { return a.parseTime(asStr(s)) }
	for _, d := range dates {
		dp := pByDate[d]
		for _, pz := range dp {
			if pz["work_day_id"] == nil { // freie Pause – Überschneidung ist gewollt
				continue
			}
			ps, ok1 := t(pz["start_time"])
			pe, ok2 := t(pz["end_time"])
			if !ok1 || !ok2 {
				continue
			}
			for _, e := range byDate[d] {
				es, ok3 := t(e["start_time"])
				ee, ok4 := t(e["end_time"])
				if ok3 && ok4 && ps.Before(ee) && es.Before(pe) {
					issues = append(issues, map[string]any{"type": "pause_overlap", "severity": "warn", "date": d,
						"message": fmt.Sprintf("Pause überschneidet sich mit Eintrag: Pause %s–%s, Eintrag %s–%s", a.hm(asStr(pz["start_time"])), a.hm(asStr(pz["end_time"])), a.hm(asStr(e["start_time"])), a.hm(asStr(e["end_time"]))),
						"ids":     []any{pz["id"], e["id"]}})
				}
			}
		}
		for i := 0; i+1 < len(dp); i++ {
			x, y := dp[i], dp[i+1]
			xe, ok1 := t(x["end_time"])
			ys, ok2 := t(y["start_time"])
			if ok1 && ok2 && xe.After(ys) {
				issues = append(issues, map[string]any{"type": "pause_overlap", "severity": "warn", "date": d,
					"message": fmt.Sprintf("Zwei Pausen überschneiden sich: %s–%s und %s–%s", a.hm(asStr(x["start_time"])), a.hm(asStr(x["end_time"])), a.hm(asStr(y["start_time"])), a.hm(asStr(y["end_time"]))),
					"ids":     []any{x["id"], y["id"]}})
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"issues": issues, "summary": integritySummary(issues), "checkedAt": time.Now().UTC().Format(isoMillis), "days": integrityDays})
}

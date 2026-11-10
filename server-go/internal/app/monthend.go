package app

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
)

/* Monatsende & Nachtragen – Verhalten wie backend/monthend.js (status, /month-end/ok) und /api/missing-hours (server.js).
   „Monatsende“ in der Oberfläche heißt: Erinnerung vor dem Monatsende und Liste der Tage unter Soll („passt so“ je Tag).
   Das ist NICHT der Monatsabschluss (month_closings, konto.go) und NICHT die Freigabe (approval.go).
   Verbindung zur Monatsfreigabe: Tage in eingereichten/freigegebenen Monaten werden nicht zum Nachtragen angeboten
   (dort ist nichts mehr änderbar), „passt so“ ist dort gesperrt (409 month_locked); die Antwort nennt den Freigabestand.
   Bewusst anders: keine Werte „am Mac von–bis“ (Anwesenheit bleibt im Client). */

type missDay struct {
	Date     string  `json:"date"`
	TargetH  float64 `json:"target_h"`
	BookedH  float64 `json:"booked_h"`
	MissingH float64 `json:"missing_h"`
	OK       bool    `json:"ok"`
}

type monthPrev struct {
	Month     string    `json:"month"`
	MonthName string    `json:"month_name"`
	Days      []missDay `json:"days"`
	MissingH  float64   `json:"missing_h"`
	Approval  string    `json:"approval"`
}

type monthEnd struct {
	Month        string     `json:"month"`
	MonthName    string     `json:"month_name"`
	LastWorkday  any        `json:"last_workday"`
	WorkdaysLeft int        `json:"workdays_left"`
	Remind       bool       `json:"remind"`
	Days         []missDay  `json:"days"`
	OpenDays     []missDay  `json:"open_days"`
	MissingH     float64    `json:"missing_h"`
	Prev         *monthPrev `json:"prev"`
	BalanceH     any        `json:"balance_h"`
	Optional     bool       `json:"optional"`
	Approval     string     `json:"approval"`      // Freigabestand des laufenden Monats: open | submitted | approved | rejected
	LockedMonths []string   `json:"locked_months"` // eingereicht/freigegeben: dort wird nichts nachgetragen
}

type absRange struct {
	From, To, Type string
	Hours          *float64
}

// missingDays: Tage in [from, to] unter Soll (ab 0,25 h), ohne Feiertage und ohne gesperrte Monate (reine Logik, getestet)
func missingDays(from, to string, targets [7]float64, abs []absRange, bookedSecs map[string]float64, ok, locked map[string]bool) []missDay {
	out := []missDay{}
	f, ok1 := parseDay(from)
	t, ok2 := parseDay(to)
	if !ok1 || !ok2 || t.Sub(f) > 800*24*time.Hour {
		return out
	}
	for d := f; !d.After(t); d = d.AddDate(0, 0, 1) {
		date := dayStr(d)
		target := targets[int(d.Weekday())]
		if target == 0 || locked[date[:7]] {
			continue
		}
		absH, holiday := 0.0, false
		for _, a := range abs {
			if date < a.From || date > a.To {
				continue
			}
			if a.Type == "feiertag" {
				holiday = true
			}
			if a.Hours != nil {
				absH += *a.Hours
			} else {
				absH += target
			}
		}
		want := math.Max(0, target-absH)
		if holiday || want <= 0 {
			continue
		}
		booked := bookedSecs[date] / 3600
		if miss := r2(want - booked); miss >= 0.25 {
			out = append(out, missDay{Date: date, TargetH: want, BookedH: r2(booked), MissingH: miss, OK: ok[date]})
		}
	}
	return out
}

func sumMissing(ds []missDay) float64 {
	s := 0.0
	for _, d := range ds {
		s += d.MissingH
	}
	return r2(s)
}

func openOnly(ds []missDay) []missDay {
	out := []missDay{}
	for _, d := range ds {
		if !d.OK {
			out = append(out, d)
		}
	}
	return out
}

// gapOK: Liste der „passt so“-Tage (user_settings gapfill_ok, JSON wie in Node)
func gapOK(st map[string]string) map[string]bool {
	var l []string
	_ = json.Unmarshal([]byte(st["gapfill_ok"]), &l)
	out := map[string]bool{}
	for _, d := range l {
		out[d] = true
	}
	return out
}

// monthData lädt Abwesenheiten und gebuchte Sekunden (abgeschlossene Einträge, ohne Abwesenheitsbuchungen) für [from, to]
func (a *App) monthData(ctx context.Context, uid int64, from, to string) ([]absRange, map[string]float64, error) {
	var abs []absRange
	rows, err := a.DB.Query(ctx, `SELECT date_from, date_to, type, hours FROM absences WHERE user_id=$1 AND date_to>=$2 AND date_from<=$3 ORDER BY id`, uid, from, to)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var x absRange
		if err := rows.Scan(&x.From, &x.To, &x.Type, &x.Hours); err != nil {
			rows.Close()
			return nil, nil, err
		}
		abs = append(abs, x)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	booked := map[string]float64{}
	rows, err = a.DB.Query(ctx, `SELECT date, COALESCE(SUM(duration_seconds),0)::float8 FROM time_entries
		WHERE user_id=$1 AND date>=$2 AND date<=$3 AND end_time IS NOT NULL AND COALESCE(is_absence,0)=0 GROUP BY date`, uid, from, to)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var d string
		var s float64
		if err := rows.Scan(&d, &s); err != nil {
			return nil, nil, err
		}
		booked[d] = s
	}
	return abs, booked, rows.Err()
}

func (a *App) approvalOf(ctx context.Context, uid int64, month string) string {
	var st string
	if err := a.DB.QueryRow(ctx, `SELECT status FROM month_approvals WHERE user_id=$1 AND month=$2`, uid, month).Scan(&st); err != nil {
		return "open"
	}
	return st
}

// monthEndStatus = status() in monthend.js. withBalance=false spart die Kontoberechnung (für das To-do „Zeiten nachtragen“).
func (a *App) monthEndStatus(ctx context.Context, uid int64, withBalance bool) (*monthEnd, error) {
	st, err := a.userSettings(ctx, uid)
	if err != nil {
		return nil, err
	}
	today := a.today()
	t, _ := parseDay(today)
	monthStart := today[:8] + "01"
	prevStart := dayStr(time.Date(t.Year(), t.Month()-1, 1, 0, 0, 0, 0, time.UTC))
	monthEndD := dayStr(time.Date(t.Year(), t.Month()+1, 0, 0, 0, 0, 0, time.UTC))
	since := st["tempo_push_since"]
	if !isoDateRE.MatchString(since) {
		since = "2000-01-01"
	}
	cal, err := a.workCalendar(ctx, uid, prevStart, monthEndD)
	if err != nil {
		return nil, err
	}
	locked, err := a.lockedMonths(ctx, uid)
	if err != nil {
		return nil, err
	}
	abs, booked, err := a.monthData(ctx, uid, minStr(prevStart, addDays(today, -14)), today)
	if err != nil {
		return nil, err
	}
	targets, ok := dayTargets(st), gapOK(st)
	var left []string
	for d := t; d.Month() == t.Month(); d = d.AddDate(0, 0, 1) {
		if cal.workday(dayStr(d)) {
			left = append(left, dayStr(d))
		}
	}
	yesterday := addDays(today, -1)
	// offen: dieser Monat und die letzten 14 Tage (auch über den Monatswechsel)
	openFrom := maxStr(minStr(addDays(today, -14), monthStart), since)
	days := []missDay{}
	if yesterday >= openFrom {
		days = missingDays(openFrom, yesterday, targets, abs, booked, ok, locked)
	}
	res := &monthEnd{Month: today[:7], MonthName: monthsDE[t.Month()-1], WorkdaysLeft: len(left), Remind: len(left) >= 1 && len(left) <= 5,
		Days: days, OpenDays: openOnly(days), Approval: a.approvalOf(ctx, uid, today[:7]), LockedMonths: []string{}}
	if len(left) > 0 {
		res.LastWorkday = left[len(left)-1]
	}
	res.MissingH = sumMissing(res.OpenDays)
	for m := range locked {
		res.LockedMonths = append(res.LockedMonths, m)
	}
	sort.Strings(res.LockedMonths)
	// Konto bis gestern: deckt das Plus das Fehlende, ist Nachtragen nur optional
	if withBalance {
		res.BalanceH = a.balanceAt(ctx, uid, st, yesterday)
		if b, isNum := res.BalanceH.(float64); isNum {
			res.Optional = res.MissingH > 0 && b >= res.MissingH
		}
	}
	// Neuer Monat: in den ersten 3 Arbeitstagen den Vormonat nachfragen
	passed := 0
	for d, _ := parseDay(monthStart); !d.After(t); d = d.AddDate(0, 0, 1) {
		if cal.workday(dayStr(d)) {
			passed++
		}
	}
	if pEnd := addDays(monthStart, -1); passed <= 3 && pEnd >= since {
		pd := openOnly(missingDays(maxStr(prevStart, since), pEnd, targets, abs, booked, ok, locked))
		if len(pd) > 0 {
			pt, _ := parseDay(prevStart)
			res.Prev = &monthPrev{Month: prevStart[:7], MonthName: monthsDE[pt.Month()-1], Days: pd, MissingH: sumMissing(pd), Approval: a.approvalOf(ctx, uid, prevStart[:7])}
		}
	}
	return res, nil
}

func minStr(a, b string) string {
	if a < b {
		return a
	}
	return b
}

func maxStr(a, b string) string {
	if a > b {
		return a
	}
	return b
}

func (a *App) routesMonthEnd(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/month-end", a.auth(a.monthEndRoute))
	mux.HandleFunc("POST /api/month-end/ok", a.auth(a.monthEndOK))
	mux.HandleFunc("GET /api/missing-hours", a.auth(a.missingHours))
}

func (a *App) monthEndRoute(w http.ResponseWriter, r *http.Request, u User) {
	res, err := a.monthEndStatus(r.Context(), u.ID, true)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// monthEndOK: Tag als „passt so“ markieren (oder mit undo zurücknehmen). Nur bei offenem Monat.
func (a *App) monthEndOK(w http.ResponseWriter, r *http.Request, u User) {
	var in body
	if !readJSON(w, r, &in) {
		return
	}
	date := asStr(in["date"])
	if _, ok := parseDay(date); !ok {
		badRequest(w, "Datum fehlt")
		return
	}
	ctx := r.Context()
	var lockedDay bool
	err := a.inTx(ctx, func(tx pgx.Tx) error {
		var err error
		if lockedDay, err = monthLockedTx(ctx, tx, u.ID, date); err != nil || lockedDay {
			return err
		}
		// Zeile sperren, damit gleichzeitige Klicks sich nicht gegenseitig überschreiben
		if _, err := tx.Exec(ctx, `INSERT INTO user_settings(user_id,key,value) VALUES($1,'gapfill_ok','[]') ON CONFLICT DO NOTHING`, u.ID); err != nil {
			return err
		}
		var raw *string
		if err := tx.QueryRow(ctx, `SELECT value FROM user_settings WHERE user_id=$1 AND key='gapfill_ok' FOR UPDATE`, u.ID).Scan(&raw); err != nil {
			return err
		}
		var l []string
		if raw != nil {
			_ = json.Unmarshal([]byte(*raw), &l)
		}
		b, _ := json.Marshal(toggleDay(l, date, truthy(in["undo"])))
		_, err = tx.Exec(ctx, `UPDATE user_settings SET value=$2 WHERE user_id=$1 AND key='gapfill_ok'`, u.ID, string(b))
		return err
	})
	if err != nil {
		a.fail(w, err)
		return
	}
	if lockedDay {
		writeJSON(w, http.StatusConflict, monthLockedBody(date))
		return
	}
	todoSuggest.Delete(u.ID)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// toggleDay: Tag hinzufügen bzw. entfernen, sortiert, höchstens die letzten 400 (wie Node)
func toggleDay(l []string, date string, undo bool) []string {
	set := map[string]bool{}
	for _, d := range l {
		if isoDateRE.MatchString(d) {
			set[d] = true
		}
	}
	if undo {
		delete(set, date)
	} else {
		set[date] = true
	}
	out := make([]string, 0, len(set))
	for d := range set {
		out = append(out, d)
	}
	sort.Strings(out)
	if len(out) > 400 {
		out = out[len(out)-400:]
	}
	return out
}

/* ── Fehlende Stunden (vergangene Arbeitstage unter Soll) ─────────── */

type missingHour struct {
	Date     string  `json:"date"`
	Dow      int     `json:"dow"`
	SollH    float64 `json:"soll_h"`
	IstH     float64 `json:"ist_h"`
	AbsenceH float64 `json:"absence_h"`
	MissingH float64 `json:"missing_h"`
	Empty    bool    `json:"empty"`
}

// missingHourDays: wie /api/missing-hours in server.js; skip = Monat abgeschlossen (month_closings) oder gesperrt (Freigabe)
func missingHourDays(today string, days int, minGap float64, trackFrom string, targets [7]float64, abs []absRange, bookedSecs map[string]float64, skip map[string]bool) []missingHour {
	out := []missingHour{}
	for i := days; i >= 1; i-- {
		date := addDays(today, -i)
		if date < trackFrom || date >= today || skip[date[:7]] {
			continue
		}
		d, _ := parseDay(date)
		soll := targets[int(d.Weekday())]
		if soll == 0 {
			continue
		}
		absH := 0.0
		for _, a := range abs { // erste passende Abwesenheit (wie LIMIT 1)
			if date >= a.From && date <= a.To {
				absH = soll
				if a.Hours != nil {
					absH = *a.Hours
				}
				break
			}
		}
		ist := bookedSecs[date] / 3600
		if miss := r2(soll - absH - ist); miss >= minGap {
			out = append(out, missingHour{Date: date, Dow: int(d.Weekday()), SollH: soll, IstH: r2(ist), AbsenceH: absH, MissingH: miss, Empty: ist == 0})
		}
	}
	return out
}

func (a *App) missingHours(w http.ResponseWriter, r *http.Request, u User) {
	ctx := r.Context()
	st, err := a.userSettings(ctx, u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	if days == 0 {
		days = 14
	}
	days = max(1, min(62, days))
	minGap := 0.25
	if v := r.URL.Query().Get("min_h"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && !math.IsNaN(f) && !math.IsInf(f, 0) {
			minGap = math.Max(0, f)
		}
	}
	today := a.today()
	trackFrom, err := a.trackFrom(ctx, u.ID, st, today)
	if err != nil {
		a.fail(w, err)
		return
	}
	skip, err := a.lockedMonths(ctx, u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	rows, err := a.DB.Query(ctx, `SELECT year, month FROM month_closings WHERE user_id=$1`, u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	for rows.Next() {
		var y, m int32
		if rows.Scan(&y, &m) == nil {
			skip[time.Date(int(y), time.Month(m), 1, 0, 0, 0, 0, time.UTC).Format("2006-01")] = true // Monat abgeschlossen → keine Fehlzeit-Meldung
		}
	}
	rows.Close()
	abs, booked, err := a.monthData(ctx, u.ID, addDays(today, -days), today)
	if err != nil {
		a.fail(w, err)
		return
	}
	out := missingHourDays(today, days, minGap, trackFrom, dayTargets(st), abs, booked, skip)
	total := 0.0
	for _, d := range out {
		total += d.MissingH
	}
	total = r2(total)
	bal := a.balanceAt(ctx, u.ID, st, addDays(today, -1))
	b, isNum := bal.(float64)
	writeJSON(w, http.StatusOK, map[string]any{"days": out, "total_missing_h": total, "balance_h": bal, "optional": isNum && total > 0 && b >= total})
}

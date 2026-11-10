package app

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

/* Überblick (Soll/Ist/Konto) und Monatsabschlüsse – Rechenweg wie overviewHandler in backend/server.js.
   Bewusst anders: Das Arbeitstag-Fenster (work_days, ist_from_workday) entfällt, Ist = gebuchte Zeit
   abzüglich freier Pausen. Die Tageswerte werden gebündelt für den ganzen Zeitraum gelesen. */

const maxOverviewDays = 20000 // ~54 Jahre; gilt auch für die Rekursion ab Tracking-Start

func (a *App) routesKonto(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/overview", a.auth(a.asTarget(a.overview)))
	mux.HandleFunc("GET /api/month-closings", a.auth(a.asTarget(a.closingsList)))
	mux.HandleFunc("POST /api/month-closings", a.auth(a.closingsCreate))
	mux.HandleFunc("DELETE /api/month-closings/{id}", a.auth(a.closingsDelete))
}

type dayAgg struct {
	booked, all, tempoS, manualExtra, freePause float64 // Sekunden
	totalN, tempoN                              int64
}

// confirmedSQL: „bestätigte“ Zeit = aus Tempo importiert oder (nach Tempo/Plane) übertragen.
// Abschaltbar mit konto_nur_freigegeben=0 – dann zählt alles Gebuchte.
func confirmedSQL(st map[string]string) string {
	if st["konto_nur_freigegeben"] == "0" {
		return "TRUE"
	}
	return "(source IN ('tempo','tempo_csv') OR tempo_worklog_id IS NOT NULL OR tempo_push_id IS NOT NULL)"
}

func (a *App) dayAggs(ctx context.Context, uid int64, from, to string, st map[string]string) (map[string]*dayAgg, error) {
	conf := confirmedSQL(st)
	out := map[string]*dayAgg{}
	get := func(d string) *dayAgg {
		if out[d] == nil {
			out[d] = &dayAgg{}
		}
		return out[d]
	}
	rows, err := a.DB.Query(ctx, `SELECT date,
		COALESCE(SUM(duration_seconds) FILTER (WHERE `+conf+`),0)::float8,
		COALESCE(SUM(duration_seconds),0)::float8,
		COUNT(*) FILTER (WHERE `+conf+`),
		COUNT(*) FILTER (WHERE COALESCE(source,'manual') IN ('tempo','tempo_csv')),
		COALESCE(SUM(duration_seconds) FILTER (WHERE COALESCE(source,'manual') IN ('tempo','tempo_csv')),0)::float8,
		COALESCE(SUM(duration_seconds) FILTER (WHERE COALESCE(source,'manual') NOT IN ('tempo','tempo_csv') AND `+conf+`),0)::float8
		FROM time_entries WHERE user_id=$1 AND date>=$2 AND date<=$3 AND COALESCE(is_absence,0)=0 GROUP BY date`, uid, from, to)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var d string
		g := &dayAgg{}
		if err := rows.Scan(&d, &g.booked, &g.all, &g.totalN, &g.tempoN, &g.tempoS, &g.manualExtra); err != nil {
			rows.Close()
			return nil, err
		}
		out[d] = g
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// freie Pausen (ohne Arbeitstag) je Datum; Zeiten in Go lesen, damit gemischte Formate nichts verfälschen
	prows, err := a.DB.Query(ctx, `SELECT date, start_time, end_time FROM pauses
		WHERE user_id=$1 AND work_day_id IS NULL AND end_time IS NOT NULL AND date>=$2 AND date<=$3`, uid, from, to)
	if err != nil {
		return nil, err
	}
	defer prows.Close()
	for prows.Next() {
		var d, st, en string
		if err := prows.Scan(&d, &st, &en); err != nil {
			return nil, err
		}
		if secs, ok := a.seconds(st, en); ok {
			get(d).freePause += float64(secs)
		}
	}
	return out, prows.Err()
}

/* ── Datums-Hilfen (immer UTC-Mittag, zeitzonensicher) ── */

func parseDay(s string) (time.Time, bool) {
	t, err := time.Parse("2006-01-02", s)
	return t, err == nil
}

func dayStr(t time.Time) string { return t.Format("2006-01-02") }

func r2(x float64) float64 { return math.Round(x*100) / 100 }

func (a *App) today() string { return time.Now().In(a.Loc).Format("2006-01-02") }

func dayTargets(st map[string]string) [7]float64 {
	f := func(k string) float64 { v, _ := strconv.ParseFloat(st[k], 64); return v }
	return [7]float64{f("target_sun"), f("target_mon"), f("target_tue"), f("target_wed"), f("target_thu"), f("target_fri"), f("target_sat")}
}

// absenceFor liefert die erste Abwesenheit, die das Datum überdeckt (Reihenfolge wie in der Liste).
func absenceFor(abs []map[string]any, date string) map[string]any {
	for _, m := range abs {
		if date >= asStr(m["date_from"]) && date <= asStr(m["date_to"]) {
			return m
		}
	}
	return nil
}

func (a *App) trackFrom(ctx context.Context, uid int64, st map[string]string, fallback string) (string, error) {
	if v := st["tracking_start_date"]; v != "" {
		return v, nil
	}
	var d *string
	if err := a.DB.QueryRow(ctx, `SELECT MIN(date) FROM time_entries WHERE user_id=$1`, uid).Scan(&d); err != nil {
		return "", err
	}
	if d != nil && *d != "" {
		return *d, nil
	}
	return fallback, nil
}

/* ── Überblick ───────────────────────────────────────────────────── */

type ovDay struct {
	Date             string         `json:"date"`
	Dow              int            `json:"dow"`
	TargetH          float64        `json:"targetH"`
	BookedH          float64        `json:"bookedH"`
	PendingH         float64        `json:"pendingH"`
	WorkH            float64        `json:"workH"`
	AbsenceH         float64        `json:"absenceH"`
	Absence          map[string]any `json:"absence"`
	BalanceH         float64        `json:"balanceH"`
	CumulativeBal    float64        `json:"cumulativeBalance"`
	Workday          any            `json:"workday"`
	IsBeforeTracking bool           `json:"isBeforeTracking"`
	IsToday          bool           `json:"isToday"`
	IsFuture         bool           `json:"isFuture"`
}

type ovResult struct {
	Days          []ovDay           `json:"days"`
	Settings      map[string]string `json:"settings"`
	OpenBal       float64           `json:"openBal"`
	TrackFrom     string            `json:"trackFrom"`
	VacationTotal float64           `json:"vacationTotal"`
	VacationUsed  int               `json:"vacationUsed"`
}

var errBadRange = errors.New("Zeitraum ungültig")

func (a *App) computeOverview(ctx context.Context, uid int64, st map[string]string, from, to string) (*ovResult, error) {
	fromT, ok1 := parseDay(from)
	toT, ok2 := parseDay(to)
	if !ok1 || !ok2 || toT.Before(fromT) || toT.Sub(fromT) > maxOverviewDays*24*time.Hour {
		return nil, errBadRange
	}
	openBal, _ := strconv.ParseFloat(st["opening_balance_hours"], 64)
	targets := dayTargets(st)
	trackFrom, err := a.trackFrom(ctx, uid, st, from)
	if err != nil {
		return nil, err
	}

	// Basis: letzter Monatsabschluss vor `from`, sonst Eröffnungssaldo
	var carry *float64
	err = a.DB.QueryRow(ctx, `SELECT carry_over_hours FROM month_closings WHERE user_id=$1 AND (year*12+month) < $2
		ORDER BY year DESC, month DESC LIMIT 1`, uid, fromT.Year()*12+int(fromT.Month())).Scan(&carry)
	hasClose := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	runningBase := openBal
	if hasClose {
		runningBase = 0
		if carry != nil {
			runningBase = *carry
		}
	}

	// Saldo-Korrekturen: die jüngste vor `from` ist Fixpunkt, spätere setzen das Konto an ihrem Datum
	type corr struct {
		date string
		bal  float64
	}
	var corrs []corr
	crows, err := a.DB.Query(ctx, `SELECT date, balance_hours FROM balance_corrections WHERE user_id=$1 AND date<=$2 ORDER BY date, id`, uid, to)
	if err != nil {
		return nil, err
	}
	for crows.Next() {
		var c corr
		if err := crows.Scan(&c.date, &c.bal); err != nil {
			crows.Close()
			return nil, err
		}
		corrs = append(corrs, c)
	}
	crows.Close()
	if err := crows.Err(); err != nil {
		return nil, err
	}
	var base *corr
	for i := range corrs {
		if corrs[i].date < from {
			base = &corrs[i]
		}
	}
	if base != nil {
		runningBase = base.bal
	} else if !hasClose && trackFrom < from {
		// Kein Abschluss, keine Korrektur: Konto läuft ab Tracking-Start, nicht erst ab `from`
		prev, err := a.computeOverview(ctx, uid, st, trackFrom, dayStr(fromT.AddDate(0, 0, -1)))
		if err != nil {
			return nil, err
		}
		if len(prev.Days) > 0 {
			runningBase = prev.Days[len(prev.Days)-1].CumulativeBal
		}
	}
	internalFrom := from
	if base != nil {
		internalFrom = base.date
	}
	corrByDate := map[string]float64{}
	for _, c := range corrs {
		if c.date >= internalFrom && c.date <= to {
			corrByDate[c.date] = c.bal
		}
	}

	startT, ok := parseDay(internalFrom)
	if !ok {
		return nil, errBadRange
	}
	abs, err := a.maps(ctx, `SELECT * FROM absences WHERE user_id=$1 AND date_to >= $2 AND date_from <= $3 ORDER BY id`, uid, internalFrom, to)
	if err != nil {
		return nil, err
	}
	aggs, err := a.dayAggs(ctx, uid, internalFrom, to, st)
	if err != nil {
		return nil, err
	}
	pendingOn := confirmedSQL(st) != "TRUE"
	todayStr := a.today()
	cum := runningBase

	var all []ovDay
	for d := startT; !d.After(toT); d = d.AddDate(0, 0, 1) {
		date := dayStr(d)
		dow := int(d.Weekday())
		before := date < trackFrom
		targetH := 0.0
		if !before {
			targetH = targets[dow]
		}
		absence := absenceFor(abs, date)
		absenceH := 0.0
		if absence != nil {
			if h, ok := absence["hours"].(float64); ok {
				absenceH = h
			} else {
				absenceH = targets[dow]
			}
		}
		g := aggs[date]
		if g == nil {
			g = &dayAgg{}
		}
		bookedH := g.booked / 3600
		pendingH := 0.0
		if pendingOn {
			pendingH = math.Max(0, (g.all-g.booked)/3600) // gebucht, aber noch nicht freigegeben
		}
		var workH float64
		if g.totalN > 0 && g.tempoN == g.totalN && date < todayStr {
			workH = (g.tempoS + g.manualExtra) / 3600 // Tempo-Tage: Summe der Buchungen
		} else {
			workH = math.Max(0, bookedH-g.freePause/3600)
		}
		effective := workH
		if absence != nil {
			effective = absenceH
		}
		isFuture := date > todayStr // Zukunft zählt nicht ins Minus
		balanceH := 0.0
		if !before && !isFuture {
			balanceH = effective - targetH
		}
		corrVal, hasCorr := corrByDate[date]
		if hasCorr {
			cum = corrVal
		} else {
			cum += balanceH
		}
		isToday := date == todayStr
		shown := cum
		if isToday && !hasCorr {
			shown = cum - balanceH // heute zählt erst ab morgen
		}
		var wd any
		all = append(all, ovDay{Date: date, Dow: dow, TargetH: targetH, BookedH: bookedH, PendingH: r2(pendingH), WorkH: r2(workH),
			AbsenceH: absenceH, Absence: absence, BalanceH: r2(balanceH), CumulativeBal: r2(shown), Workday: wd,
			IsBeforeTracking: before, IsToday: isToday, IsFuture: isFuture})
	}
	days := make([]ovDay, 0, len(all))
	for _, d := range all {
		if d.Date >= from {
			days = append(days, d)
		}
	}

	vacTotal, _ := strconv.ParseFloat(st["vacation_days_per_year"], 64)
	vacUsed, err := a.vacationUsed(ctx, uid, targets)
	if err != nil {
		return nil, err
	}
	return &ovResult{Days: days, Settings: st, OpenBal: openBal, TrackFrom: trackFrom, VacationTotal: vacTotal, VacationUsed: vacUsed}, nil
}

// vacationUsed: Urlaubstage = Arbeitstage (Mo–Fr, kein Feiertag, Soll > 0) in allen Urlaubs-Zeiträumen des laufenden Jahres.
func (a *App) vacationUsed(ctx context.Context, uid int64, targets [7]float64) (int, error) {
	year := time.Now().In(a.Loc).Year()
	y0, y1 := fmt.Sprintf("%d-01-01", year), fmt.Sprintf("%d-12-31", year)
	rs, err := a.maps(ctx, `SELECT type, date_from, date_to FROM absences WHERE user_id=$1 AND type IN ('feiertag','urlaub') AND date_to >= $2 AND date_from <= $3`, uid, y0, y1)
	if err != nil {
		return 0, err
	}
	hol := map[string]bool{}
	for _, m := range rs {
		if asStr(m["type"]) == "feiertag" && strings.HasPrefix(asStr(m["date_from"]), strconv.Itoa(year)+"-") {
			hol[asStr(m["date_from"])] = true
		}
	}
	used := map[string]bool{}
	for _, m := range rs {
		if asStr(m["type"]) != "urlaub" {
			continue
		}
		from, to := asStr(m["date_from"]), asStr(m["date_to"])
		if from < y0 {
			from = y0
		}
		if to > y1 {
			to = y1
		}
		f, ok1 := parseDay(from)
		t, ok2 := parseDay(to)
		if !ok1 || !ok2 {
			continue
		}
		for d := f; !d.After(t); d = d.AddDate(0, 0, 1) {
			w := int(d.Weekday())
			if w > 0 && w < 6 && !hol[dayStr(d)] && targets[w] > 0 {
				used[dayStr(d)] = true
			}
		}
	}
	return len(used), nil
}

func (a *App) overview(w http.ResponseWriter, r *http.Request, u User) {
	from, to := r.URL.Query().Get("from"), r.URL.Query().Get("to")
	if from == "" || to == "" {
		badRequest(w, "from + to erforderlich")
		return
	}
	st, err := a.userSettings(r.Context(), u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	res, err := a.computeOverview(r.Context(), u.ID, st, from, to)
	if errors.Is(err, errBadRange) {
		badRequest(w, "Zeitraum ungültig (JJJJ-MM-TT, höchstens ~54 Jahre)")
		return
	}
	if err != nil {
		a.fail(w, err)
		return
	}
	if u.Kind == "viewed" { // fremde Person: nur die für Soll/Saldo nötigen Einstellungen, keine persönlichen (Jira-Name, Plane-Kennung, freie Schlüssel)
		keep := map[string]string{}
		for k, v := range res.Settings {
			if strings.HasPrefix(k, "target_") || k == "opening_balance_hours" || k == "vacation_days_per_year" || k == "tracking_start_date" {
				keep[k] = v
			}
		}
		res.Settings = keep
	}
	writeJSON(w, http.StatusOK, res)
}

/* ── Monatsabschlüsse ───────────────────────────────────────────── */

func (a *App) closingsList(w http.ResponseWriter, r *http.Request, u User) {
	rs, err := a.maps(r.Context(), `SELECT * FROM month_closings WHERE user_id=$1 ORDER BY year DESC, month DESC`, u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rs)
}

func (a *App) closingsCreate(w http.ResponseWriter, r *http.Request, u User) {
	var in body
	if !readJSON(w, r, &in) {
		return
	}
	year, month := int(asFloat(in["year"])), int(asFloat(in["month"]))
	if year == 0 || month == 0 {
		badRequest(w, "year + month erforderlich")
		return
	}
	if month < 1 || month > 12 || year < 2000 || year > 2200 {
		badRequest(w, "year/month ungültig")
		return
	}
	row, err := a.closeMonth(r.Context(), u.ID, year, month, nz(in["notes"]))
	switch e := err.(type) {
	case nil:
		a.audit(r.Context(), u.ID, "month.close", fmt.Sprintf("%d-%02d", year, month))
		writeJSON(w, http.StatusOK, row)
	case errConflict:
		writeJSON(w, http.StatusConflict, map[string]any{"error": string(e)})
	default:
		a.fail(w, err)
	}
}

type errConflict string

func (e errConflict) Error() string { return string(e) }

// closeMonth schließt einen Monat für eine Person ab (Soll/Ist/Übertrag). Auch für den Notfallzugang nutzbar,
// ohne dass dieser Zeiten sieht: Rückgabe wird dort nicht ausgegeben.
func (a *App) closeMonth(ctx context.Context, uid int64, year, month int, notes any) (map[string]any, error) {
	var exists bool
	if err := a.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM month_closings WHERE user_id=$1 AND year=$2 AND month=$3)`, uid, year, month).Scan(&exists); err != nil {
		return nil, err
	}
	if exists {
		return nil, errConflict("Monat bereits abgeschlossen")
	}
	first := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.UTC)
	from, to := dayStr(first), dayStr(first.AddDate(0, 1, -1))
	st, err := a.userSettings(ctx, uid)
	if err != nil {
		return nil, err
	}
	// Abschluss zählt alles Gebuchte, nicht nur Bestätigtes (wie Node)
	stAll := map[string]string{}
	for k, v := range st {
		stAll[k] = v
	}
	stAll["konto_nur_freigegeben"] = "0"
	trackFrom, err := a.trackFrom(ctx, uid, st, from)
	if err != nil {
		return nil, err
	}
	openBal, _ := strconv.ParseFloat(st["opening_balance_hours"], 64)
	targets := dayTargets(st)
	base := openBal
	var carry *float64
	err = a.DB.QueryRow(ctx, `SELECT carry_over_hours FROM month_closings WHERE user_id=$1 AND (year*12+month) < $2 ORDER BY year DESC, month DESC LIMIT 1`,
		uid, year*12+month).Scan(&carry)
	if err == nil {
		base = 0
		if carry != nil {
			base = *carry
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	abs, err := a.maps(ctx, `SELECT * FROM absences WHERE user_id=$1 AND date_to >= $2 AND date_from <= $3 ORDER BY id`, uid, from, to)
	if err != nil {
		return nil, err
	}
	aggs, err := a.dayAggs(ctx, uid, from, to, stAll)
	if err != nil {
		return nil, err
	}
	start := from
	if trackFrom > start {
		start = trackFrom
	}
	var soll, ist float64
	if s, ok := parseDay(start); ok {
		end, _ := parseDay(to)
		for d := s; !d.After(end); d = d.AddDate(0, 0, 1) {
			date, dow := dayStr(d), int(d.Weekday())
			targetH := targets[dow]
			g := aggs[date]
			if g == nil {
				g = &dayAgg{}
			}
			workH := math.Max(0, g.booked/3600-g.freePause/3600)
			if g.totalN > 0 && g.tempoN == g.totalN {
				workH = g.tempoS / 3600
			}
			soll += targetH
			if ab := absenceFor(abs, date); ab != nil {
				if h, ok := ab["hours"].(float64); ok {
					ist += h
				} else {
					ist += targetH
				}
			} else {
				ist += workH
			}
		}
	}
	delta := ist - soll
	row, err := a.one(ctx, `INSERT INTO month_closings(user_id,year,month,soll_hours,ist_hours,delta_hours,carry_over_hours,notes,closed_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING *`,
		uid, year, month, r2(soll), r2(ist), r2(delta), r2(base+delta), notes, time.Now().UTC().Format(isoMillis))
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return nil, errConflict("Monat bereits abgeschlossen")
	}
	if err != nil {
		return nil, err
	}
	return row, nil
}

func (a *App) closingsDelete(w http.ResponseWriter, r *http.Request, u User) {
	id, ok := pathID(r)
	if !ok {
		notFound(w)
		return
	}
	if _, err := a.DB.Exec(r.Context(), `DELETE FROM month_closings WHERE id=$1 AND user_id=$2`, id, u.ID); err != nil {
		a.fail(w, err)
		return
	}
	a.audit(r.Context(), u.ID, "month.reopen", strconv.FormatInt(id, 10))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

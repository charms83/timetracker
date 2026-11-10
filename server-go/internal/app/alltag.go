package app

import (
	"context"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

/* Gemeinsame Hilfen für den Arbeitsalltag (Parität Gruppe D): Arbeitstage, Kalenderwochen, Monatssperre, Zahlen wie Node. */

var (
	clockRE    = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)
	weekdaysDE = [7]string{"Sonntag", "Montag", "Dienstag", "Mittwoch", "Donnerstag", "Freitag", "Samstag"}
	weekdaysAd = [7]string{"sonntags", "montags", "dienstags", "mittwochs", "donnerstags", "freitags", "samstags"}
	monthsDE   = [12]string{"Januar", "Februar", "März", "April", "Mai", "Juni", "Juli", "August", "September", "Oktober", "November", "Dezember"}
)

// workCal: Arbeitstage einer Person = Mo–Fr ohne eingetragene Feiertage (Abwesenheit type feiertag), wie todo.js/monthend.js
type workCal struct{ hol map[string]bool }

func (c workCal) workday(d string) bool {
	t, ok := parseDay(d)
	if !ok {
		return false
	}
	w := t.Weekday()
	return w >= time.Monday && w <= time.Friday && !c.hol[d]
}

// next: nächster Arbeitstag nach d (höchstens 60 Tage weit, dann der Tag danach)
func (c workCal) next(d string) string {
	t, ok := parseDay(d)
	if !ok {
		return d
	}
	for i := 0; i < 60; i++ {
		t = t.AddDate(0, 0, 1)
		if c.workday(dayStr(t)) {
			return dayStr(t)
		}
	}
	return dayStr(t)
}

// workCalendar lädt die Feiertage im Zeitraum [from, to]
func (a *App) workCalendar(ctx context.Context, uid int64, from, to string) (workCal, error) {
	c := workCal{hol: map[string]bool{}}
	rows, err := a.DB.Query(ctx, `SELECT date_from, date_to FROM absences WHERE user_id=$1 AND type='feiertag' AND date_to>=$2 AND date_from<=$3`, uid, from, to)
	if err != nil {
		return c, err
	}
	defer rows.Close()
	for rows.Next() {
		var f, t string
		if err := rows.Scan(&f, &t); err != nil {
			return c, err
		}
		fd, ok1 := parseDay(f)
		td, ok2 := parseDay(t)
		if !ok1 || !ok2 || td.Sub(fd) > 400*24*time.Hour {
			continue
		}
		for d := fd; !d.After(td); d = d.AddDate(0, 0, 1) {
			c.hol[dayStr(d)] = true
		}
	}
	return c, rows.Err()
}

// isoWeekOf: „2026-W45“ (ISO-Woche, Jahr der Woche) wie isoWeek in todo.js
func isoWeekOf(d string) string {
	t, ok := parseDay(d)
	if !ok {
		return ""
	}
	y, w := t.ISOWeek()
	return fmt.Sprintf("%d-W%02d", y, w)
}

// weekEnd: Sonntag der Woche von d
func weekEnd(d string) string {
	t, ok := parseDay(d)
	if !ok {
		return d
	}
	wd := int(t.Weekday())
	if wd == 0 {
		wd = 7
	}
	return dayStr(t.AddDate(0, 0, 7-wd))
}

func addDays(d string, n int) string {
	t, ok := parseDay(d)
	if !ok {
		return d
	}
	return dayStr(t.AddDate(0, 0, n))
}

// fmtDE: Zahl wie toLocaleString("de-DE") (Dezimalkomma, höchstens 3 Nachkommastellen, ohne Tausenderpunkt bei < 1000)
func fmtDE(x float64) string {
	s := strconv.FormatFloat(math.Round(x*1000)/1000, 'f', -1, 64)
	return strings.Replace(s, ".", ",", 1)
}

func hhmm(m int) string { return fmt.Sprintf("%02d:%02d", m/60, m%60) }

// ddmm: „2026-11-05“ → „05.11.“
func ddmm(d string) string {
	if len(d) < 10 {
		return d
	}
	return d[8:10] + "." + d[5:7] + "."
}

func r1(x float64) float64 { return math.Round(x*10) / 10 }

// isMonthLocked: Fehler stammt aus der Monatssperre der Datenbank (ZP001, Migration 013/018)
func isMonthLocked(err error) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && pe.Code == "ZP001"
}

// lockedStatuses: Status, die einen Monat für die Person sperren (wie zp_month_locked, Migration 018)
const lockedStatusSQL = `('submitted','approved','closed')`

// monthStatusLocked: Status sperrt den Monat für die Person selbst (geschlossen zählt wie freigegeben)
func monthStatusLocked(st string) bool {
	return st == "submitted" || st == "approved" || st == "closed"
}

// lockedMonths: geschlossene/eingereichte/freigegebene Monate der Person (nur lesen, ohne Sperre – für Anzeigen)
func (a *App) lockedMonths(ctx context.Context, uid int64) (map[string]bool, error) {
	out := map[string]bool{}
	rows, err := a.DB.Query(ctx, `SELECT month FROM month_approvals WHERE user_id=$1 AND status IN `+lockedStatusSQL, uid)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var m string
		if err := rows.Scan(&m); err != nil {
			return out, err
		}
		out[m] = true
	}
	return out, rows.Err()
}

// monthLockedTx: Monatssperre in der Transaktion prüfen (nimmt die gemeinsame Sperre wie die Trigger)
func monthLockedTx(ctx context.Context, tx pgx.Tx, uid int64, date string) (bool, error) {
	var locked bool
	err := tx.QueryRow(ctx, `SELECT zp_month_locked($1, $2)`, uid, date).Scan(&locked)
	return locked, err
}

func monthLockedBody(date string) map[string]any {
	m := date
	if len(m) >= 7 {
		m = m[:7]
	}
	return map[string]any{"error": "Monat " + m + " ist geschlossen, eingereicht oder freigegeben – Nachtragen nur durch Teamleitung oder Personal", "code": "month_locked"}
}

// setUserSetting schreibt eine Einstellung der Person (Fehler werden gemeldet, anders als saveUserSetting)
func (a *App) setUserSetting(ctx context.Context, q interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}, uid int64, key, val string) error {
	_, err := q.Exec(ctx, `INSERT INTO user_settings(user_id,key,value) VALUES($1,$2,$3) ON CONFLICT (user_id,key) DO UPDATE SET value=EXCLUDED.value`, uid, key, val)
	return err
}

// balanceAt: Kontostand am Ende des Tages (wie balanceAt in server.js); nil, wenn nicht berechenbar
func (a *App) balanceAt(ctx context.Context, uid int64, st map[string]string, date string) any {
	res, err := a.computeOverview(ctx, uid, st, date, date)
	if err != nil || res == nil || len(res.Days) == 0 {
		return nil
	}
	return res.Days[len(res.Days)-1].CumulativeBal
}

// projectByJiraKey = sources.projectByJiraKey (Node): verknüpfte Jira-Quelle, sonst Projekt mit diesem Jira-Schlüssel; abgeschlossen → Nachfolger
func (a *App) projectByJiraKey(ctx context.Context, uid int64, prefix string) any {
	k := strings.ToUpper(strings.TrimSpace(prefix))
	if k == "" {
		return nil
	}
	var id int64
	for _, q := range []string{
		`SELECT project_id FROM project_sources WHERE user_id=$1 AND system='jira' AND status='linked' AND UPPER(key)=$2 AND project_id IS NOT NULL ORDER BY id LIMIT 1`,
		`SELECT id FROM projects WHERE user_id=$1 AND UPPER(jira_project)=$2 ORDER BY id LIMIT 1`,
		`SELECT id FROM projects WHERE user_id=$1 AND UPPER(jira_key)=$2 AND COALESCE(status,'')<>'archived' AND UPPER(COALESCE(plane_identifier,''))<>$2 ORDER BY id LIMIT 1`,
		`SELECT id FROM projects WHERE user_id=$1 AND UPPER(jira_key)=$2 ORDER BY CASE WHEN status='archived' THEN 1 ELSE 0 END, id LIMIT 1`,
	} {
		if err := a.DB.QueryRow(ctx, q, uid, k).Scan(&id); err == nil {
			p, _, _ := a.redirect(ctx, uid, id, nil)
			return p
		}
	}
	return nil
}

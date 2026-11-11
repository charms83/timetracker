package app

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

/* Tempo-Abwesenheitsprojekt → is_absence + Vorschläge (wie backend/absence-sync.js fromTempo).
   Nach dem Tempo→lokal-Abgleich: HAP-Buchungen zählen nicht als Arbeitszeit;
   erkannte Zeiträume landen in absence_hints (Assistent „übernehmen?“). */

var absenceTitleRange = regexp.MustCompile(`(?i)(\d{1,2})\.(\d{1,2})\.?(\d{2,4})?\s*[-–]\s*(\d{1,2})\.(\d{1,2})\.(\d{2,4})?`)

func absenceTypeOf(title string) string {
	switch {
	case regexp.MustCompile(`(?i)vacation|urlaub|holiday|ferien`).MatchString(title):
		return "urlaub"
	case regexp.MustCompile(`(?i)sick|krank|illness`).MatchString(title):
		return "krankheit"
	case regexp.MustCompile(`(?i)training|fortbildung|schulung|seminar`).MatchString(title):
		return "fortbildung"
	default:
		return "sonstiges"
	}
}

func rangeFromAbsenceTitle(title string, fallbackYear int) (from, to string, ok bool) {
	m := absenceTitleRange.FindStringSubmatch(title)
	if m == nil {
		return "", "", false
	}
	d1, mo1 := atoiDef(m[1], 0), atoiDef(m[2], 0)
	d2, mo2 := atoiDef(m[4], 0), atoiDef(m[5], 0)
	if d1 < 1 || mo1 < 1 || d2 < 1 || mo2 < 1 {
		return "", "", false
	}
	y2 := fallbackYear
	if m[6] != "" {
		y2 = parseYear2or4(m[6], fallbackYear)
	}
	y1 := y2
	if m[3] != "" {
		y1 = parseYear2or4(m[3], fallbackYear)
	} else if mo1 > mo2 {
		y1 = y2 - 1
	}
	from = fmt.Sprintf("%04d-%02d-%02d", y1, mo1, d1)
	to = fmt.Sprintf("%04d-%02d-%02d", y2, mo2, d2)
	if from > to {
		return "", "", false
	}
	return from, to, true
}

func atoiDef(s string, def int) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return def
	}
	return n
}

func parseYear2or4(s string, fallback int) int {
	n := atoiDef(s, 0)
	if len(s) == 2 {
		return 2000 + n
	}
	if n > 1900 {
		return n
	}
	return fallback
}

// fromTempoAbsences: nach Abgleich aufrufen (nur schreiben, wenn Tempo-Modul an).
func (a *App) fromTempoAbsences(ctx context.Context, uid int64) (marked, hints int) {
	if a.DB == nil {
		return 0, 0
	}
	st, err := a.userSettings(ctx, uid)
	if err != nil {
		return 0, 0
	}
	key := strings.ToUpper(jsOr(strings.TrimSpace(st["tempo_absence_project"]), "HAP"))
	since := st["tempo_sync_from"]
	if !isoDateRE.MatchString(since) {
		since = "2000-01-01"
	}
	tag, err := a.DB.Exec(ctx, `UPDATE time_entries SET is_absence=1
		WHERE user_id=$1 AND COALESCE(is_absence,0)=0
		  AND task_id IN (SELECT id FROM tasks WHERE user_id=$1 AND UPPER(jira_ticket) LIKE $2)`, uid, key+"-%")
	if err == nil {
		marked = int(tag.RowsAffected())
	}
	_, _ = a.DB.Exec(ctx, `DELETE FROM absences WHERE user_id=$1 AND source='tempo' AND note LIKE '%(aus Tempo)'`, uid)

	rows, err := a.maps(ctx, `SELECT UPPER(t.jira_ticket) AS k, COALESCE(t.title,'') AS title, MIN(e.date) AS a, MAX(e.date) AS b
		FROM time_entries e JOIN tasks t ON t.id=e.task_id AND t.user_id=e.user_id
		WHERE e.user_id=$1 AND e.date>=$2 AND UPPER(t.jira_ticket) LIKE $3
		GROUP BY UPPER(t.jira_ticket), t.title`, uid, since, key+"-%")
	if err != nil {
		return marked, 0
	}
	for _, r := range rows {
		k := asStr(r["k"])
		title := asStr(r["title"])
		a0, b0 := asStr(r["a"]), asStr(r["b"])
		from, to := a0, b0
		year := 0
		if len(b0) >= 4 {
			year = atoiDef(b0[:4], 0)
		}
		if f, t, ok := rangeFromAbsenceTitle(title, year); ok {
			from, to = f, t
		}
		if from == "" || to == "" {
			continue
		}
		var covered, exists bool
		_ = a.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM absences WHERE user_id=$1 AND type<>'feiertag' AND date_from<=$2 AND date_to>=$3)`, uid, from, to).Scan(&covered)
		if covered {
			continue
		}
		hintKey := "tempo:" + k
		_ = a.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM absence_hints WHERE user_id=$1 AND key=$2)`, uid, hintKey).Scan(&exists)
		if exists {
			continue
		}
		typ := absenceTypeOf(title)
		if _, err := a.DB.Exec(ctx, `INSERT INTO absence_hints(user_id,key,title,date_from,date_to,type,calendar,status)
			VALUES($1,$2,$3,$4,$5,$6,'Tempo','new')`, uid, hintKey, truncRunes(jsOr(title, k), 200), from, to, typ); err == nil {
			hints++
		}
	}
	if marked > 0 || hints > 0 {
		a.sysLog(ctx, uid, "info", "abwesenheit",
			fmt.Sprintf("Tempo-Abwesenheiten: %d Buchungen ohne Arbeitszeit, %d Vorschläge", marked, hints))
	}
	return marked, hints
}

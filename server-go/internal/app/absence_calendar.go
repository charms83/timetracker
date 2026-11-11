package app

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"regexp"
	"time"
)

/* Kalender: ganztägige Abwesenheits-Termine → Vorschlag (wie backend/absence-sync.js fromCalendar).
   Der Mac meldet Termine an POST /api/meetings/calendar; ganztägige „Urlaub/Krank/OOO…“
   landen in absence_hints (Assistent „übernehmen?“), nie als automatische Buchung. */

var absenceCalWords = regexp.MustCompile(`(?i)\b(urlaub|vacation|holiday|ferien|abwesend|abwesenheit|ooo|out of office|krank|sick|elternzeit|fortbildung|schulung)\b`)

type calAbsenceEvent struct {
	Title, Calendar string
	From, To        string // lokale Kalendertage (Ende inklusiv)
}

// fromCalendarAbsences: nach Kalender-Ingest aufrufen; events = bereits gefilterte ganztägige Kandidaten.
func (a *App) fromCalendarAbsences(ctx context.Context, uid int64, events []calAbsenceEvent) int {
	if a.DB == nil || len(events) == 0 {
		return 0
	}
	n := 0
	for _, e := range events {
		if e.From == "" || e.To == "" || e.To < e.From {
			continue
		}
		var covered, exists bool
		_ = a.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM absences WHERE user_id=$1 AND type<>'feiertag' AND date_from<=$2 AND date_to>=$3)`,
			uid, e.From, e.To).Scan(&covered)
		if covered {
			continue
		}
		key := absenceCalKey(e.Title, e.From, e.To, e.Calendar)
		_ = a.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM absence_hints WHERE user_id=$1 AND key=$2)`, uid, key).Scan(&exists)
		if exists {
			continue
		}
		typ := absenceTypeOf(e.Title)
		title := truncRunes(e.Title, 200)
		if _, err := a.DB.Exec(ctx, `INSERT INTO absence_hints(user_id,key,title,date_from,date_to,type,calendar,status)
			VALUES($1,$2,$3,$4,$5,$6,$7,'new')`, uid, key, title, e.From, e.To, typ, nz(e.Calendar)); err == nil {
			n++
		}
	}
	if n > 0 {
		a.sysLog(ctx, uid, "info", "abwesenheit", fmt.Sprintf("Kalender: %d Abwesenheits-Vorschläge", n))
	}
	return n
}

func absenceCalKey(title, from, to, calendar string) string {
	sum := sha1.Sum([]byte(title + "|" + from + "|" + to + "|" + calendar))
	return hex.EncodeToString(sum[:])[:16]
}

func absenceCalMatch(title string) bool {
	return absenceCalWords.MatchString(title)
}

// absenceCalRange: ganztägiges Ende ist exklusiv → einen Tick zurück, dann lokales Datum.
func (a *App) absenceCalRange(startMs, endMs int64) (from, to string, ok bool) {
	if startMs == 0 || endMs == 0 || endMs <= startMs {
		return "", "", false
	}
	from = time.UnixMilli(startMs).In(a.Loc).Format("2006-01-02")
	to = time.UnixMilli(endMs - 1000).In(a.Loc).Format("2006-01-02")
	if to < from {
		return "", "", false
	}
	return from, to, true
}

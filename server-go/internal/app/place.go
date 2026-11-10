package app

import (
	"context"
	"fmt"
	"sort"
	"time"
)

/* Platzierung von Dauer-Buchungen auf einen Tag (wie Tempo: nur Stunden angeben).
   Findet die erste passende Lücke zwischen bestehenden Buchungen/Pausen;
   sonst hinter der letzten Buchung; leerer Tag ab 08:00. Markierung: time_precise=false. */

const (
	placeDayStartMin = 8 * 60  // 08:00
	placeDayEndMin   = 22 * 60 // 22:00 harte Grenze
)

// placeInDay: pure Logik – taken = belegte Intervalle [start,end) in Minuten ab Mitternacht.
// Rückgabe Start/Ende in Minuten; ok=false wenn nichts passt.
func placeInDay(taken [][2]int, durMin, preferStart, dayEnd int) (s, e int, ok bool) {
	if durMin <= 0 {
		return 0, 0, false
	}
	if preferStart < 0 {
		preferStart = placeDayStartMin
	}
	if dayEnd <= preferStart {
		dayEnd = placeDayEndMin
	}
	type iv struct{ s, e int }
	list := make([]iv, 0, len(taken))
	for _, t := range taken {
		if t[1] <= t[0] {
			continue
		}
		list = append(list, iv{t[0], t[1]})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].s < list[j].s })
	merged := []iv{}
	for _, x := range list {
		if n := len(merged); n > 0 && x.s <= merged[n-1].e {
			if x.e > merged[n-1].e {
				merged[n-1].e = x.e
			}
			continue
		}
		merged = append(merged, x)
	}
	cur := preferStart
	for _, b := range merged {
		if b.e <= cur {
			continue
		}
		if b.s > cur && b.s-cur >= durMin {
			return cur, cur + durMin, true
		}
		if b.s > cur {
			cur = b.e
		} else if b.e > cur {
			cur = b.e
		}
	}
	if dayEnd-cur >= durMin {
		return cur, cur + durMin, true
	}
	// Notfalls hinter dem Tag weiter (Anzeige bleibt möglich); trotzdem ok
	if cur < placeDayEndMin+4*60 {
		return cur, cur + durMin, true
	}
	return 0, 0, false
}

func minutesToLocalISO(date string, mins int, loc *time.Location) (string, error) {
	day, err := time.ParseInLocation("2006-01-02", date, loc)
	if err != nil {
		return "", err
	}
	t := day.Add(time.Duration(mins) * time.Minute)
	return t.UTC().Format(isoMillis), nil
}

// placeDurationOnDay: legt start/end für eine Dauer-Buchung am Tag fest (ungenaue Uhrzeit).
func (a *App) placeDurationOnDay(ctx context.Context, uid int64, date string, durSec int64) (start, end string, err error) {
	if durSec < 60 {
		return "", "", fmt.Errorf("Dauer mindestens 1 Minute")
	}
	durMin := int((durSec + 30) / 60) // kaufmännisch auf Minuten
	if durMin < 1 {
		durMin = 1
	}
	taken := [][2]int{}
	rs, err := a.maps(ctx, `SELECT start_time, end_time FROM time_entries
		WHERE user_id=$1 AND date=$2 AND end_time IS NOT NULL AND COALESCE(is_absence,0)=0`, uid, date)
	if err != nil {
		return "", "", err
	}
	for _, e := range rs {
		s, ok1 := a.minOfDay(asStr(e["start_time"]))
		en, ok2 := a.minOfDay(asStr(e["end_time"]))
		if ok1 && ok2 && en > s {
			taken = append(taken, [2]int{s, en})
		}
	}
	ps, err := a.maps(ctx, `SELECT start_time, end_time FROM pauses WHERE user_id=$1 AND date=$2 AND end_time IS NOT NULL`, uid, date)
	if err != nil {
		return "", "", err
	}
	for _, p := range ps {
		s, ok1 := a.minOfDay(asStr(p["start_time"]))
		en, ok2 := a.minOfDay(asStr(p["end_time"]))
		if ok1 && ok2 && en > s {
			taken = append(taken, [2]int{s, en})
		}
	}
	// laufender Timer am selben Tag zählt als belegt
	var tStart *string
	_ = a.DB.QueryRow(ctx, `SELECT start_time FROM timer_state WHERE user_id=$1 AND start_time IS NOT NULL`, uid).Scan(&tStart)
	if tStart != nil && a.dateOf(*tStart) == date {
		if s, ok := a.minOfDay(*tStart); ok {
			now := time.Now().In(a.Loc)
			en := now.Hour()*60 + now.Minute()
			if a.today() != date {
				en = placeDayEndMin
			}
			if en > s {
				taken = append(taken, [2]int{s, en})
			}
		}
	}
	sm, em, ok := placeInDay(taken, durMin, placeDayStartMin, placeDayEndMin)
	if !ok {
		return "", "", fmt.Errorf("kein freier Platz am Tag für %d min", durMin)
	}
	start, err = minutesToLocalISO(date, sm, a.Loc)
	if err != nil {
		return "", "", err
	}
	end, err = minutesToLocalISO(date, em, a.Loc)
	return start, end, err
}

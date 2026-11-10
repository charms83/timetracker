package app

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

/* Monatsschluss mit Nachfrist (Regel 1): Nach Monatsende darf die Person nur noch bis zum Ende des n-ten Werktags danach
   (Mo–Fr ohne Feiertage; Servereinstellung month_grace_workdays, Standard 1, 0–5) nachtragen. Danach schließt ein Hintergrundlauf
   den Monat: month_approvals.status='closed', Kommentar „automatisch geschlossen“, Verlauf 'auto_close', Audit.
   Lauf: beim Start und alle 15 min, nur eine Instanz zugleich (Advisory-Sperre), idempotent (ON CONFLICT DO NOTHING unter der
   exklusiven Sperre je Person). Betroffen: zeiterfassende, nicht gesperrte Konten; zurückliegende Monate ab Erfassungsbeginn,
   höchstens die letzten 3. Monate mit Status (eingereicht, freigegeben, abgelehnt = zur Korrektur zurückgegeben) bleiben, wie sie sind.
   Laufender Timer, der im Monat begann: Schließen wird aufgeschoben (Hinweis an die Person), höchstens timerDeferDays Tage nach
   Fristende; danach wird geschlossen und das Stoppen speichert den Eintrag nicht mehr (stopTimerTx, Systemlog) – der Timer hängt nie. */

const (
	monthCloseLockKey = int64(727005) // eine Instanz schließt (Sitzungs-Sperre auf eigener Verbindung)
	closeLookback     = 3             // höchstens so viele zurückliegende Monate
	timerDeferDays    = 14            // so lange wartet das Schließen auf einen laufenden Timer aus dem Monat
	autoCloseComment  = "automatisch geschlossen"
)

// Ereignisse für notifyApproval (Hook für den späteren E-Mail-Baustein)
const (
	evClosingSoon = "closing_soon" // Nachfrist endet heute – an die Person (Lauf, je Prozess höchstens einmal je Person/Monat)
	evClosed      = "closed"       // Monat automatisch geschlossen – an die Person
	evRejected    = "rejected"     // Monat abgelehnt (mit Kommentar) – an die Person
	evNachtrag    = "nachtrag"     // Teamleitung/Personal hat im gesperrten Monat nachgetragen – an die Person
	evPending     = "pending"      // Monat eingereicht, Freigabe offen – an das Personal (laut approval_mode)
)

// notifyApproval: Stelle für Benachrichtigungen außerhalb der App (E-Mail folgt als eigener Baustein). Heute bewusst leer:
// die In-App-Hinweise entstehen in approvalNotices aus dem Datenbestand. Der Baustein muss selbst entprellen
// (mehrere Instanzen, Wiederholungen nach Neustart) und darf nie blockieren (Hintergrund, Zeitlimit).
func (a *App) notifyApproval(ctx context.Context, event string, userID int64, month string) {
	_, _, _, _ = ctx, event, userID, month
}

// graceWorkdays: Servereinstellung month_grace_workdays (0–5, Standard 1)
func (a *App) graceWorkdays(ctx context.Context) int {
	return parseGrace(a.conf(ctx, "month_grace_workdays"))
}

func parseGrace(v string) int {
	var n int
	if _, err := fmt.Sscan(strings.TrimSpace(v), &n); err != nil || n < 0 || n > 5 {
		return 1
	}
	return n
}

// graceLastDay: letzter Tag, an dem die Person den Monat noch selbst ändern darf (bis Tagesende).
// n=0: letzter Tag des Monats; sonst der n-te Werktag (Mo–Fr, kein Feiertag) nach Monatsende. ok=false: Monat ungültig.
func graceLastDay(month string, n int, isHoliday func(string) bool) (string, bool) {
	_, last, ok := monthBounds(month)
	if !ok {
		return "", false
	}
	if n <= 0 {
		return last, true
	}
	t, _ := parseDay(last)
	for i := 0; i < 60; i++ {
		t = t.AddDate(0, 0, 1)
		d := dayStr(t)
		if wd := t.Weekday(); wd == time.Saturday || wd == time.Sunday || (isHoliday != nil && isHoliday(d)) {
			continue
		}
		if n--; n == 0 {
			return d, true
		}
	}
	return dayStr(t), true
}

// closeCandidates: zurückliegende Monate vor dem laufenden, ab dem Monat von from (Erfassungsbeginn), höchstens lookback
func closeCandidates(from, today string, lookback int) []string {
	t, ok := parseDay(today)
	if !ok {
		return nil
	}
	first := ""
	if len(from) >= 7 && isoDateRE.MatchString(from) {
		first = from[:7]
	}
	var out []string
	cur := time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
	for i := lookback; i >= 1; i-- {
		m := cur.AddDate(0, -i, 0).Format("2006-01")
		if first != "" && m < first {
			continue
		}
		out = append(out, m)
	}
	return out
}

// shouldAutoClose: reine Regel für einen Monat ohne Status. timerStart = Tag des laufenden Timers ("" = keiner).
// Ergebnis: close, defer (wegen Timer aufgeschoben)
func shouldAutoClose(month, deadline, today, timerStart string) (bool, bool) {
	if deadline == "" || today <= deadline {
		return false, false
	}
	if timerStart != "" && strings.HasPrefix(timerStart, month) && today <= addDays(deadline, timerDeferDays) {
		return false, true
	}
	return true, false
}

// holidayFn: Feiertage der Person (Abwesenheit „feiertag“) und Niedersachsen-Liste zusammen – verlängert die Frist eher, als sie zu kürzen
func (a *App) holidayFn(ctx context.Context, uid int64, from, to string) func(string) bool {
	cal, err := a.workCalendar(ctx, uid, from, to)
	if err != nil {
		cal = workCal{hol: map[string]bool{}}
	}
	return func(d string) bool { return cal.hol[d] || niHoliday(d) }
}

var niHolidaySet = func() map[string]bool {
	m := map[string]bool{}
	for _, h := range niHolidays {
		m[h.date] = true
	}
	return m
}()

func niHoliday(d string) bool { return niHolidaySet[d] }

// graceDeadline: letzter eigener Schreibtag für den Monat der Person
func (a *App) graceDeadline(ctx context.Context, uid int64, month string) string {
	_, last, ok := monthBounds(month)
	if !ok {
		return ""
	}
	d, _ := graceLastDay(month, a.graceWorkdays(ctx), a.holidayFn(ctx, uid, addDays(last, 1), addDays(last, 60)))
	return d
}

// runningTimerDay: Tag, an dem der laufende Timer der Person begann ("" = keiner)
func (a *App) runningTimerDay(ctx context.Context, uid int64) string {
	var start *string
	if err := a.DB.QueryRow(ctx, `SELECT start_time FROM timer_state WHERE user_id=$1`, uid).Scan(&start); err != nil || start == nil || *start == "" {
		return ""
	}
	return a.dateOf(*start)
}

/* ── Hintergrundlauf ─────────────────────────────────────────────── */

func (a *App) startMonthCloseCron(ctx context.Context) {
	go func() {
		t := time.NewTicker(15 * time.Minute)
		defer t.Stop()
		for {
			a.safely("monatsschluss", func() { a.monthCloseTick(ctx) })
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
}

var (
	soonMu   sync.Mutex
	soonSent = map[string]bool{}
)

// monthCloseTick: ein Durchlauf über alle zeiterfassenden Konten (nur eine Instanz zugleich)
func (a *App) monthCloseTick(ctx context.Context) {
	if a.DB == nil {
		return
	}
	conn, err := a.DB.Acquire(ctx)
	if err != nil {
		return
	}
	defer conn.Release()
	var got bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, monthCloseLockKey).Scan(&got); err != nil || !got {
		return // andere Instanz läuft gerade
	}
	defer func() {
		uctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := conn.Exec(uctx, `SELECT pg_advisory_unlock($1)`, monthCloseLockKey); err != nil {
			_ = conn.Conn().Close(uctx) // Sperre hängt an der Verbindung
		}
	}()
	rows, err := a.DB.Query(ctx, `SELECT id, to_char(created_at AT TIME ZONE 'UTC','YYYY-MM-DD') FROM users
		WHERE NOT disabled AND role<>'breakglass' AND NOT (kind='local' AND NOT productive) ORDER BY id`)
	if err != nil {
		a.Log.Warn("Monatsschluss: Konten nicht lesbar", "err", err)
		return
	}
	type acc struct {
		id      int64
		created string
	}
	var list []acc
	for rows.Next() {
		var x acc
		if rows.Scan(&x.id, &x.created) == nil {
			list = append(list, x)
		}
	}
	rows.Close()
	today := a.today()
	n := a.graceWorkdays(ctx)
	for _, x := range list {
		if ctx.Err() != nil {
			return
		}
		a.closeMonthsFor(ctx, x.id, x.created, n, today)
	}
}

// closeMonthsFor schließt die fälligen Monate einer Person (Fehler nur ins Log; nächster Lauf versucht es erneut)
func (a *App) closeMonthsFor(ctx context.Context, uid int64, created string, n int, today string) {
	st, err := a.userSettings(ctx, uid)
	if err != nil {
		return
	}
	from, err := a.trackFrom(ctx, uid, st, created)
	if err != nil {
		return
	}
	months := closeCandidates(from, today, closeLookback)
	if len(months) == 0 {
		return
	}
	have := map[string]bool{}
	rows, err := a.DB.Query(ctx, `SELECT month FROM month_approvals WHERE user_id=$1 AND month = ANY($2)`, uid, months)
	if err != nil {
		return
	}
	for rows.Next() {
		var m string
		if rows.Scan(&m) == nil {
			have[m] = true
		}
	}
	rows.Close()
	first, _, _ := monthBounds(months[0])
	isHol := a.holidayFn(ctx, uid, first, addDays(today, 60))
	timerDay := a.runningTimerDay(ctx, uid)
	for _, m := range months {
		if have[m] {
			continue
		}
		deadline, ok := graceLastDay(m, n, isHol)
		if !ok {
			continue
		}
		if deadline == today {
			k := fmt.Sprintf("%d|%s", uid, m)
			soonMu.Lock()
			first := !soonSent[k]
			soonSent[k] = true
			soonMu.Unlock()
			if first {
				a.notifyApproval(ctx, evClosingSoon, uid, m)
			}
		}
		doClose, _ := shouldAutoClose(m, deadline, today, timerDay)
		if !doClose {
			continue
		}
		closed, err := a.autoCloseMonth(ctx, uid, m)
		if err != nil {
			a.Log.Warn("Monatsschluss fehlgeschlagen", "person", uid, "monat", m, "err", err)
			continue
		}
		if closed {
			a.notifyApproval(ctx, evClosed, uid, m)
			a.tempoMonthHook(uid)
		}
	}
}

// autoCloseMonth: Status 'closed' setzen, wenn es noch keinen gibt (unter der exklusiven Sperre der Person: laufende Schreibzugriffe enden vorher)
func (a *App) autoCloseMonth(ctx context.Context, uid int64, month string) (bool, error) {
	tctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	tx, err := a.DB.Begin(tctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(tctx) //nolint:errcheck
	if err = lockApproval(tctx, tx, uid); err != nil {
		return false, err
	}
	tag, err := tx.Exec(tctx, `INSERT INTO month_approvals(user_id,month,status,decided_at,comment) VALUES($1,$2,'closed',now(),$3) ON CONFLICT (user_id,month) DO NOTHING`,
		uid, month, autoCloseComment)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 0 {
		return false, nil // inzwischen eingereicht o. Ä.
	}
	if _, err = tx.Exec(tctx, `INSERT INTO month_approval_log(user_id,month,action,actor_id,comment) VALUES($1,$2,'auto_close',NULL,$3)`, uid, month, autoCloseComment); err != nil {
		return false, err
	}
	if _, err = tx.Exec(tctx, `INSERT INTO audit_log(user_id,action,detail) VALUES($1,'approval.auto_close',$2)`, uid, "Monat "+month+" "+autoCloseComment); err != nil {
		return false, err
	}
	return true, tx.Commit(tctx)
}

/* ── Hinweise ────────────────────────────────────────────────────── */

// closeNotices: „Monat X schließt am …“, „Monat X ist geschlossen“, Timer hält das Schließen auf, Nachträge durch Teamleitung/Personal
func (a *App) closeNotices(ctx context.Context, u User) []string {
	var out []string
	today := a.today()
	t, _ := parseDay(today)
	cur := today[:7]
	prev := time.Date(t.Year(), t.Month()-1, 1, 0, 0, 0, 0, time.UTC).Format("2006-01")
	timerDay := a.runningTimerDay(ctx, u.ID)
	for _, m := range []string{prev, cur} {
		var st, decided string
		err := a.DB.QueryRow(ctx, `SELECT status, COALESCE(to_char(decided_at,'YYYY-MM-DD'),'') FROM month_approvals WHERE user_id=$1 AND month=$2`, u.ID, m).Scan(&st, &decided)
		switch {
		case err == nil && st == "closed" && decided >= addDays(today, -7):
			out = append(out, "Monat "+m+" ist geschlossen – Nachtragen nur noch durch Teamleitung oder Personal.")
		case err != nil: // kein Status: Frist anzeigen, sobald sie naht
			deadline := a.graceDeadline(ctx, u.ID, m)
			_, last, _ := monthBounds(m)
			if text := closingNotice(m, deadline, last, today, timerDay); text != "" {
				out = append(out, text)
			}
		}
	}
	rows, err := a.DB.Query(ctx, `SELECT month, COUNT(*) FROM month_approval_log WHERE user_id=$1 AND action='nachtrag' AND at > now() - interval '14 days'
		GROUP BY month ORDER BY month`, u.ID)
	if err == nil {
		for rows.Next() {
			var m string
			var n int
			if rows.Scan(&m, &n) == nil {
				out = append(out, fmt.Sprintf("Im Monat %s wurde durch Teamleitung oder Personal nachgetragen (%d Änderung(en)) – Verlauf im Monatsstatus.", m, n))
			}
		}
		rows.Close()
	}
	return out
}

// closingNotice: reine Regel für den Hinweis vor dem Schließen ("" = keiner)
func closingNotice(month, deadline, lastOfMonth, today, timerDay string) string {
	if deadline == "" {
		return ""
	}
	if today > deadline {
		if timerDay != "" && strings.HasPrefix(timerDay, month) {
			return "Der Timer läuft seit " + ddmmyyyy(timerDay) + " – Monat " + month + " wird erst nach dem Stoppen geschlossen. Bitte den Timer beenden."
		}
		return "" // schließt beim nächsten Lauf
	}
	if today > lastOfMonth || addDays(today, 3) >= deadline {
		return "Monat " + month + " schließt am " + ddmmyyyy(deadline) + " – danach nur noch Nachtragen durch Teamleitung"
	}
	return ""
}

// ddmmyyyy: „2026-12-01“ → „01.12.2026“
func ddmmyyyy(d string) string {
	if len(d) < 10 {
		return d
	}
	return d[8:10] + "." + d[5:7] + "." + d[:4]
}

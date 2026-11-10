package app

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/jackc/pgx/v5"
)

/* Übergangs-Ticket je Person, Jira-Instanz und Monat (Spec §3, Node ensureMonthTicket/updateMonthDescription/closeMonthTickets).
   „Übergang Zeiten <jira_username> <YYYY-MM>“ im Projekt für neue Tickets der Instanz, der Person zugewiesen.
   Angelegt beim ersten Übertragen einer Plane-Buchung des Monats (Modus month); vorher per JQL + exakter Überschrift gesucht,
   damit ein abgebrochener Lauf kein zweites anlegt (der Lauf hält die Sperre je Person).
   Geschlossen nach Freigabe (month_approvals approved), Tempo-Sperre oder abgelaufener Nachfrist ohne Offenes; wieder
   geöffnet, wenn Personal den Monat wiedereröffnet bzw. er abgelehnt oder neu eingereicht ist (monthWantClosed).
   Jira-Fehler blockieren die Freigabe nie: sie stehen in close_error, im Selbsttest und werden später wiederholt. */

type monthKey struct {
	inst  int64
	month string
}

// ensureMonthTicket: vorhandenes Übergangs-Ticket oder neu anlegen (nur im Lauf, unter der Sperre je Person)
func (a *App) ensureMonthTicket(ctx context.Context, uid int64, ti *tempoInst, month string) (string, bool, error) {
	c := ti.C
	if c.Worker == "" || c.NewProject == "" {
		return "", false, errors.New("Jira-Benutzer oder Projekt für neue Tickets fehlt (Einstellungen der Instanz)")
	}
	var key string
	err := a.DB.QueryRow(ctx, `SELECT issue_key FROM tempo_month_tickets WHERE user_id=$1 AND instance_id=$2 AND month=$3 AND worker=$4`, uid, ti.ID, month, c.Worker).Scan(&key)
	if err == nil && key != "" {
		return key, false, nil
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return "", false, err
	}
	summary := monthTicketSummary(c.Worker, month)
	key, err = a.jiraFindSummary(ctx, uid, c.jira(), c.NewProject, summary)
	if err != nil { // ohne gelungene Suche nie anlegen (sonst nach Absturz ein zweites Übergangs-Ticket)
		return "", false, err
	}
	created := false
	if key == "" {
		types, _ := a.jiraIssueTypes(ctx, uid, c.jira(), c.NewProject)
		fields := map[string]any{"project": map[string]any{"key": c.NewProject}, "summary": summary, "issuetype": map[string]any{"name": pickIssueType(types, c.IssueType)},
			"description": "Übergangs-Ticket für Zeiten von " + c.Worker + " im Monat " + month + ", die in Plane gebucht wurden.\nJeder Worklog nennt das Plane-Ticket, den Titel, das Projekt und den Link; die Tabelle unten wird von Zeitprotokoll aktuell gehalten.\nDer Kunde steht im Tempo-Account des Worklogs. Wird nach der Monatsfreigabe automatisch geschlossen."}
		if key, err = a.jiraCreateIssue(ctx, uid, c.jira(), fields, c.Worker, c.Account); err != nil {
			return "", false, err
		}
		created = true
		a.sysLog(ctx, uid, "success", "tempo", fmt.Sprintf("Übergangs-Ticket %s für %s angelegt (%s)", key, month, ti.Name))
	}
	if _, err := a.DB.Exec(ctx, `INSERT INTO tempo_month_tickets(user_id,instance_id,month,worker,issue_key) VALUES($1,$2,$3,$4,$5)
		ON CONFLICT (user_id,instance_id,month,worker) DO UPDATE SET issue_key=EXCLUDED.issue_key`, uid, ti.ID, month, c.Worker, key); err != nil {
		// Ticket existiert in Jira; der nächste Lauf findet es per Überschrift wieder
		a.sysLog(ctx, uid, "error", "tempo", fmt.Sprintf("Übergangs-Ticket %s angelegt, nicht gespeichert (%v) – wird beim nächsten Lauf wiedergefunden", key, err))
		return "", false, err
	}
	return key, created, nil
}

// updateMonthDescription: Tabelle aller Plane-Tickets des Monats; nur bei geänderter Tabelle (desc_hash)
func (a *App) updateMonthDescription(ctx context.Context, uid int64, ti *tempoInst, month string) {
	var key, hash string
	if err := a.DB.QueryRow(ctx, `SELECT issue_key, COALESCE(desc_hash,'') FROM tempo_month_tickets WHERE user_id=$1 AND instance_id=$2 AND month=$3 AND worker=$4`,
		uid, ti.ID, month, ti.C.Worker).Scan(&key, &hash); err != nil {
		return
	}
	from, to, ok := monthBounds(month)
	if !ok {
		return
	}
	rows, err := a.DB.Query(ctx, `SELECT COALESCE(t.jira_ticket,''), COALESCE(t.title,''), COALESCE(p.name,''), e.date, COALESCE(e.duration_seconds,0)::bigint
		FROM time_entries e LEFT JOIN tasks t ON t.id=e.task_id AND t.user_id=e.user_id LEFT JOIN projects p ON p.id=e.project_id AND p.user_id=e.user_id
		WHERE e.user_id=$1 AND e.tempo_push_instance=$2 AND e.tempo_push_target=$3 AND e.date BETWEEN $4 AND $5 ORDER BY e.date, e.start_time`, uid, ti.ID, key, from, to)
	if err != nil {
		return
	}
	var list []monthRow
	for rows.Next() {
		var r monthRow
		if rows.Scan(&r.Ticket, &r.Title, &r.Project, &r.Date, &r.Secs) == nil {
			list = append(list, r)
		}
	}
	rows.Close()
	if len(list) == 0 {
		return
	}
	desc := monthDescription(ti.C.Worker, month, ti.C.PlaneURL, list)
	h := descHash(desc)
	if h == hash {
		return
	}
	if err := a.jiraSetDescription(ctx, uid, ti.C.jira(), key, desc); err != nil {
		a.sysLog(ctx, uid, "warn", "tempo", fmt.Sprintf("Beschreibung von %s nicht aktualisiert: %v", key, err))
		return
	}
	_, _ = a.DB.Exec(ctx, `UPDATE tempo_month_tickets SET desc_hash=$5 WHERE user_id=$1 AND instance_id=$2 AND month=$3 AND worker=$4`, uid, ti.ID, month, ti.C.Worker, h)
}

// reconcileMonthTickets: Übergangs-Tickets der Person auf den gewünschten Zustand bringen (schließen/wiederöffnen)
func (a *App) reconcileMonthTickets(ctx context.Context, uid int64, s *tempoSetup) {
	st, err := a.userSettings(ctx, uid)
	if err != nil {
		return
	}
	closeDays := 5
	if v := st["tempo_month_close_days"]; v != "" {
		closeDays = min(31, max(0, leadingInt(v)))
	}
	now := time.Now().In(a.Loc)
	today, cur := now.Format("2006-01-02"), now.Format("2006-01")
	rows, err := a.maps(ctx, `SELECT mt.instance_id, mt.month, mt.worker, mt.issue_key, COALESCE(mt.closed_at,'') AS closed_at, COALESCE(mt.close_error,'') AS close_error,
		COALESCE(ma.status,'') AS approval, EXISTS(SELECT 1 FROM tempo_months tm WHERE tm.user_id=mt.user_id AND tm.instance_id=mt.instance_id AND tm.month=mt.month AND tm.locked=1) AS tlocked
		FROM tempo_month_tickets mt LEFT JOIN month_approvals ma ON ma.user_id=mt.user_id AND ma.month=mt.month
		WHERE mt.user_id=$1 AND mt.month < $2 ORDER BY mt.month`, uid, cur)
	if err != nil {
		return
	}
	var lk tempoLookup
	var items []tempoItem
	loaded := false
	for _, r := range rows {
		ti := s.byID[int64(asFloat(r["instance_id"]))]
		if ti == nil || ti.Problem != "" || ti.aborted != "" || asStr(r["worker"]) != ti.C.Worker {
			continue
		}
		m, key := asStr(r["month"]), asStr(r["issue_key"])
		closed := asStr(r["closed_at"]) != ""
		approval, tlocked := asStr(r["approval"]), r["tlocked"] == true
		grace := monthGraceOver(m, closeDays, today)
		open := false
		if !closed && grace && approval == "" && !tlocked { // offene Nachträge nur prüfen, wenn sie entscheiden
			if !loaded {
				lk, _ = a.tempoLookups(ctx, uid, s)
				items, _ = a.tempoItems(ctx, uid, s, st, lk, "")
				loaded = true
			}
			open = monthHasOpen(items, ti.ID, m, lk)
		}
		want := monthWantClosed(approval, tlocked, grace, open, closed)
		if want == closed {
			continue
		}
		var err error
		if want {
			a.updateMonthDescription(ctx, uid, ti, m)
			err = a.monthTransition(ctx, uid, ti, key, true)
		} else {
			err = a.monthTransition(ctx, uid, ti, key, false)
		}
		if err != nil {
			if asStr(r["close_error"]) != err.Error() {
				a.sysLog(ctx, uid, "warn", "tempo", fmt.Sprintf("Übergangs-Ticket %s (%s) nicht %s: %v", key, m, map[bool]string{true: "geschlossen", false: "wieder geöffnet"}[want], err))
			}
			_, _ = a.DB.Exec(ctx, `UPDATE tempo_month_tickets SET close_error=$5 WHERE user_id=$1 AND instance_id=$2 AND month=$3 AND worker=$4`, uid, ti.ID, m, ti.C.Worker, truncRunes(err.Error(), 300))
			continue
		}
		if want {
			_, _ = a.DB.Exec(ctx, `UPDATE tempo_month_tickets SET closed_at=$5, close_error=NULL WHERE user_id=$1 AND instance_id=$2 AND month=$3 AND worker=$4`, uid, ti.ID, m, ti.C.Worker, nowStamp())
			a.sysLog(ctx, uid, "success", "tempo", fmt.Sprintf("Übergangs-Ticket %s (%s) geschlossen", key, m))
		} else {
			_, _ = a.DB.Exec(ctx, `UPDATE tempo_month_tickets SET closed_at=NULL, reopened_at=$5, close_error=NULL WHERE user_id=$1 AND instance_id=$2 AND month=$3 AND worker=$4`, uid, ti.ID, m, ti.C.Worker, nowStamp())
			a.sysLog(ctx, uid, "info", "tempo", fmt.Sprintf("Übergangs-Ticket %s (%s) wieder geöffnet (Monat wiedereröffnet/abgelehnt)", key, m))
		}
	}
}

// monthHasOpen: noch nicht übertragene Plane-Buchungen des Monats für dieses Übergangs-Ticket
func monthHasOpen(items []tempoItem, inst int64, month string, lk tempoLookup) bool {
	for _, it := range items {
		if monthOfDate(it.E.Date) != month || (it.P.Kind != "month" && it.P.Kind != "create-month") || (it.Inst != nil && it.Inst.ID != inst) {
			continue
		}
		switch tempoItemStatus(it, nil) {
		case "new", "changed", "error", "needs":
			return true
		}
	}
	return false
}

func (a *App) monthTransition(ctx context.Context, uid int64, ti *tempoInst, key string, close bool) error {
	// schon im Zielzustand (z. B. von Hand in Jira geschlossen)? Dann nur merken
	if r, err := a.jiraDo(ctx, uid, ti.C.jira(), "GET", "/rest/api/2/issue/"+url.PathEscape(key)+"?fields=status", nil); err == nil {
		if cat := asStr(jm(jm(jm(jm(r)["fields"])["status"])["statusCategory"])["key"]); cat != "" && (cat == "done") == close {
			return nil
		}
	}
	list, err := a.jiraTransitions(ctx, uid, ti.C.jira(), key)
	if err != nil {
		return err
	}
	id := pickTransition(list, close)
	if id == "" {
		if close {
			return errors.New("kein Übergang nach „Erledigt“ gefunden")
		}
		return errors.New("kein Übergang zurück in einen offenen Status gefunden")
	}
	return a.jiraDoTransition(ctx, uid, ti.C.jira(), key, id)
}

// tempoMonthHook: nach Freigabe/Ablehnung/Wiedereröffnen/Einreichen – im Hintergrund, blockiert die Freigabe nie
func (a *App) tempoMonthHook(uid int64) {
	if a.DB == nil {
		return
	}
	go a.safely("tempo-uebergang", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		if !a.tempoMonthReconcileUser(ctx, uid) { // Push hält die Sperre: einmal später nachholen (sonst der 6-h-Zeitplan)
			if sleepCtx(ctx, 90*time.Second) == nil {
				a.tempoMonthReconcileUser(ctx, uid)
			}
		}
	})
}

// tempoMonthReconcileUser: unter der Sperre je Person; läuft gerade ein Push, gleicht dieser am Ende selbst ab
// false = Sperre war belegt (erneut versuchen)
func (a *App) tempoMonthReconcileUser(ctx context.Context, uid int64) bool {
	if !a.tempoModuleOn(ctx) {
		return true
	}
	var has bool
	if err := a.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tempo_month_tickets WHERE user_id=$1)`, uid).Scan(&has); err != nil || !has {
		return true
	}
	s, _, err := a.tempoSetup(ctx, uid)
	if err != nil {
		return true
	}
	got, release, err := a.tempoAdvisory(ctx, uid)
	if err != nil {
		return true
	}
	if !got {
		return false
	}
	defer release()
	a.reconcileMonthTickets(ctx, uid, s)
	return true
}

// startTempoMonthCron: alle 6 h für alle Personen mit Übergangs-Tickets (wiederholt auch fehlgeschlagene)
func (a *App) startTempoMonthCron(ctx context.Context) {
	go func() {
		t := time.NewTicker(6 * time.Hour)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				a.safely("tempo-uebergang-zeitplan", func() {
					rows, err := a.DB.Query(ctx, `SELECT DISTINCT mt.user_id FROM tempo_month_tickets mt JOIN users u ON u.id=mt.user_id AND NOT u.disabled`)
					if err != nil {
						return
					}
					var ids []int64
					for rows.Next() {
						var id int64
						if rows.Scan(&id) == nil {
							ids = append(ids, id)
						}
					}
					rows.Close()
					for _, id := range ids {
						a.tempoMonthReconcileUser(ctx, id)
					}
				})
			}
		}
	}()
}

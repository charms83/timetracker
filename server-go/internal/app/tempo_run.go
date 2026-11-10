package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

/* Tempo-Ausgang, Ausführung mit Datenbank (wie backend/tempo-push.js push/checkMonth), je Jira-Instanz.
   Jede Abfrage filtert user_id. Ein Ticket gehört zu der Instanz, deren Projektkürzel (je Person und Instanz) zum
   Ticket-Key passt; passt keine oder mehrere → „needs“ mit Hinweis (nie raten, nie an die falsche Instanz senden).
   Schon übertragene Buchungen merken sich ihre Instanz (time_entries.tempo_push_instance) und bleiben dort.
   Schutz gegen Doppelbuchung:
   • je Person läuft höchstens ein Lauf (Speicher-Sperre + PostgreSQL-Advisory-Lock, auch über mehrere Instanzen des Servers),
   • bevor ein Worklog NEU angelegt wird, wird die Buchung atomar beansprucht (tempo_push_id = „pending:…“);
     nur wer den Anspruch hält, speichert die neue Id. Ist unklar, ob Tempo angelegt hat (Netzfehler, 5xx, keine Id,
     Absturz), wird NICHT erneut angelegt: Die Buchung bekommt den Hinweis „unterbrochen: …“ (POST …/dup löst ihn).
   Stufe 1: nur Ausgang. Nicht enthalten: Anlegen von Übergangs-/Gegenstück-/Sammeltickets, Tempo→lokal, Grundimport. */

var tempoSlots = make(chan struct{}, 4)

const (
	tempoLockKey   = int64(7270040000000000) // + Benutzer-Id: ein Lauf je Person
	tempoStampFmt  = "2006-01-02 15:04:05"   // wie SQLite datetime('now'), UTC
	tempoAutoDelay = 60 * time.Second
)

type tempoState struct {
	mu      sync.Mutex
	pushing bool
	again   bool
	timer   *time.Timer
}

func (a *App) tempoStateFor(uid int64) *tempoState {
	v, _ := a.tempoRun.LoadOrStore(uid, &tempoState{})
	return v.(*tempoState)
}

func (a *App) tempoModuleOn(ctx context.Context) bool { return a.conf(ctx, "module_tempo") == "1" }

/* ── Einstellungen ──────────────────────────────────────────────── */

type tempoInst struct {
	jiraInstance
	C       tempoCfg // Einstellungen dieser Instanz (Token, Benutzer, Account …)
	Problem string   // warum nicht nutzbar ("" = nutzbar)
	aborted string   // Lauf für diese Instanz abgebrochen (Netz/Token/Rechte)
}

type tempoSetup struct {
	Person tempoCfg // Schalter der Person und vereinte Werte für die Zielbestimmung
	Insts  []*tempoInst
	byID   map[int64]*tempoInst
}

func (s *tempoSetup) usable() []*tempoInst {
	var out []*tempoInst
	for _, i := range s.Insts {
		if i.Problem == "" {
			out = append(out, i)
		}
	}
	return out
}

func (s *tempoSetup) keys() []instKeys {
	out := make([]instKeys, 0, len(s.Insts))
	for _, i := range s.Insts {
		out = append(out, instKeys{ID: i.ID, Name: i.Name, Keys: i.C.JiraKeys, NewProject: i.C.NewProject, Usable: i.Problem == ""})
	}
	return out
}

func splitKeys(csv string) map[string]bool {
	m := map[string]bool{}
	for _, k := range strings.Split(csv, ",") {
		if k = strings.ToUpper(strings.TrimSpace(k)); k != "" {
			m[k] = true
		}
	}
	return m
}

// tempoSetup lädt Einstellungen der Person und aller Jira-Instanzen (Tokens nur für eingeschaltete)
func (a *App) tempoSetup(ctx context.Context, uid int64) (*tempoSetup, map[string]string, error) {
	st, err := a.userSettings(ctx, uid)
	if err != nil {
		return nil, nil, err
	}
	list, err := a.JiraInstances(ctx)
	if err != nil {
		return nil, nil, err
	}
	firstOfMonth := time.Now().In(a.Loc).Format("2006-01") + "-01"
	p := tempoCfg{Mode: "manual", PlaneMode: "month", JiraKeys: map[string]bool{}}
	p.Enabled = st["tempo_push_enabled"] == "1" || st["tempo_push_enabled"] == "true"
	if st["tempo_push_mode"] == "auto" {
		p.Mode = "auto"
	}
	if st["tempo_plane_mode"] == "ticket" {
		p.PlaneMode = "ticket"
	}
	if pb, ws := strings.TrimRight(a.conf(ctx, "plane_base_url"), "/"), normWS(a.conf(ctx, "plane_workspace")); pb != "" && ws != "" {
		p.PlaneURL = pb + "/" + ws
	}
	personSince := st["tempo_push_since"]
	if !isoDateRE.MatchString(personSince) {
		personSince = firstOfMonth
	}
	p.Since = personSince
	s := &tempoSetup{byID: map[int64]*tempoInst{}}
	issues, projects := map[string]bool{}, map[string]bool{}
	for _, ji := range list {
		g := func(k string) string { return st[instKey(ji.ID, k)] }
		c := tempoCfg{Mode: p.Mode, Enabled: p.Enabled, PlaneMode: p.PlaneMode, PlaneURL: p.PlaneURL, BaseFrom: "instance"}
		c.Worker = strings.TrimSpace(g("jira_username"))
		c.WorkerKey = strings.TrimSpace(jsOr(g("jira_userkey"), g("jira_username")))
		c.Issue = strings.ToUpper(strings.TrimSpace(g("tempo_booking_issue")))
		c.NewProject = strings.ToUpper(strings.TrimSpace(g("jira_new_project")))
		c.Account = strings.TrimSpace(g("tempo_account"))
		if c.AccountAttrID = leadingInt(g("tempo_account_attr_id")); c.AccountAttrID <= 0 {
			c.AccountAttrID = 2
		}
		c.IssueType = jsOr(strings.TrimSpace(g("jira_new_issuetype")), "Task")
		c.Since = g("tempo_push_since")
		if !isoDateRE.MatchString(c.Since) {
			c.Since = personSince
		}
		c.JiraKeys = splitKeys(g("jira_project_keys"))
		ti := &tempoInst{jiraInstance: ji}
		switch b, err := a.validateJiraURL(ji.BaseURL); {
		case !ji.Enabled:
			ti.Problem = "Jira-Instanz „" + ji.Name + "“ ist ausgeschaltet"
		case !instanceUsable(ji.Kind, ji.Tempo):
			ti.Problem = "„" + ji.Name + "“: " + instanceNote(ji.Kind, ji.Tempo)
		case ji.Tempo == "none":
			ti.Problem = "„" + ji.Name + "“ hat kein Tempo"
		case err != nil || b == "":
			ti.Problem = "„" + ji.Name + "“: Adresse ungültig"
		default:
			c.Base = b
			tok, err := a.jiraToken(ctx, uid, ji)
			c.Token = strings.TrimSpace(tok)
			switch {
			case err != nil:
				ti.Problem = "Jira-Token für „" + ji.Name + "“ nicht lesbar – bitte neu eintragen"
			case c.Token == "":
				ti.Problem = "Kein Jira-Token für „" + ji.Name + "“ (Profil)"
			}
		}
		ti.C = c
		s.Insts = append(s.Insts, ti)
		s.byID[ji.ID] = ti
		for k := range c.JiraKeys {
			p.JiraKeys[k] = true
		}
		if ti.Problem == "" {
			if c.Since < p.Since {
				p.Since = c.Since
			}
			if c.Issue != "" {
				issues[c.Issue] = true
			}
			if c.NewProject != "" {
				projects[c.NewProject] = true
			}
			if p.Worker == "" {
				p.Worker = c.Worker
			}
		}
	}
	// globale Ziele nur, wenn über alle nutzbaren Instanzen eindeutig (sonst „needs“)
	if len(issues) == 1 {
		for k := range issues {
			p.Issue = k
		}
	}
	if len(projects) == 1 {
		for k := range projects {
			p.NewProject = k
		}
	}
	s.Person = p
	return s, st, nil
}

// leadingInt wie parseInt: führende Ziffern, sonst 0
func leadingInt(s string) int {
	s = strings.TrimSpace(s)
	n := 0
	for i, r := range s {
		if r < '0' || r > '9' || i > 6 {
			break
		}
		n = n*10 + int(r-'0')
	}
	return n
}

func (a *App) saveUserSetting(ctx context.Context, uid int64, key, val string) {
	if a.DB == nil { // ohne Datenbank (Client-Tests) nichts speichern
		return
	}
	if _, err := a.DB.Exec(ctx, `INSERT INTO user_settings(user_id,key,value) VALUES($1,$2,$3) ON CONFLICT (user_id,key) DO UPDATE SET value=EXCLUDED.value`, uid, key, val); err != nil {
		a.Log.Error("Einstellung speichern", "key", key, "err", err)
	}
}

func (a *App) saveJiraMe(ctx context.Context, uid int64, ti *tempoInst, me jiraMe) {
	if me.Name != "" {
		a.saveUserSetting(ctx, uid, instKey(ti.ID, "jira_username"), me.Name)
		ti.C.Worker = me.Name
	}
	if me.Key != "" {
		a.saveUserSetting(ctx, uid, instKey(ti.ID, "jira_userkey"), me.Key)
	}
	if k := jsOr(me.Key, me.Name); k != "" {
		ti.C.WorkerKey = k
	}
}

// refreshProjectKeys: Jira-Projektkürzel der Instanz holen (Zuordnung Ticket → Instanz)
func (a *App) refreshProjectKeys(ctx context.Context, uid int64, ti *tempoInst) error {
	ps, err := a.jiraDo(ctx, uid, ti.C.jira(), "GET", "/rest/api/2/project", nil)
	if err != nil {
		return err
	}
	var keys []string
	for _, p := range listOf(ps) {
		if k := strings.ToUpper(asStr(p["key"])); k != "" && len(keys) < 5000 {
			keys = append(keys, k)
		}
	}
	csv := truncRunes(strings.Join(keys, ","), 60000)
	a.saveUserSetting(ctx, uid, instKey(ti.ID, "jira_project_keys"), csv)
	a.saveUserSetting(ctx, uid, instKey(ti.ID, "jira_project_keys_at"), nowStamp())
	ti.C.JiraKeys = splitKeys(csv)
	return nil
}

/* ── Buchungen laden ────────────────────────────────────────────── */

// tempoEntries = Node candidates(): abgeschlossen, Dauer > 0, nicht aus Tempo, date >= since, nur eigene Daten
func (a *App) tempoEntries(ctx context.Context, uid int64, since string, st map[string]string, extra string, args ...any) ([]tempoEntry, error) {
	q := `SELECT e.id, e.date, e.start_time, COALESCE(e.end_time,''), COALESCE(e.duration_seconds,0)::bigint, COALESCE(e.comment,''),
		COALESCE(e.task_id,0), COALESCE(e.project_id,0), COALESCE(t.jira_ticket,''), COALESCE(t.title,''), COALESCE(p.name,''),
		COALESCE(p.tempo_issue,''), COALESCE(p.tempo_account,''), COALESCE(t.jira_link,''), COALESCE(t.jira_origin,''),
		COALESCE(t.plane_work_item_id,''), COALESCE(t.ticket_system,''), COALESCE(e.tempo_target,''), COALESCE(e.tempo_account,''),
		COALESCE(e.tempo_push_id,''), COALESCE(e.tempo_push_hash,''), COALESCE(e.tempo_push_target,''), COALESCE(e.tempo_push_error,''),
		COALESCE(e.tempo_push_synced_at,''), COALESCE(e.tempo_dup,''), COALESCE(e.tempo_skip,0)<>0, COALESCE(e.check_flags,''), COALESCE(e.check_note,''),
		COALESCE(e.tempo_push_instance,0), COALESCE(e.tempo_target_instance,0), COALESCE(t.jira_instance,0), COALESCE(p.tempo_issue_instance,0),
		COALESCE(NULLIF(p.output_rule,'default'), $3),
		(p.plane_project_id IS NOT NULL AND NOT EXISTS (SELECT 1 FROM project_sources s WHERE s.user_id=e.user_id AND s.project_id=p.id AND s.system='plane' AND s.status='linked' AND s.gone=1))
		FROM time_entries e
		LEFT JOIN tasks t ON t.id=e.task_id AND t.user_id=e.user_id
		LEFT JOIN projects p ON p.id=e.project_id AND p.user_id=e.user_id
		WHERE e.user_id=$1 AND e.end_time IS NOT NULL AND COALESCE(e.duration_seconds,0)>0
		  AND COALESCE(e.source,'manual') NOT IN ('tempo','tempo_csv','plane_import') AND e.date >= $2`
	if extra != "" {
		q += " AND " + extra
	}
	q += " ORDER BY e.start_time, e.id"
	rows, err := a.DB.Query(ctx, q, append([]any{uid, since, a.defaultRule(st)}, args...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []tempoEntry
	for rows.Next() {
		var e tempoEntry
		var rule string
		var inPlane bool
		if err := rows.Scan(&e.ID, &e.Date, &e.StartTime, &e.EndTime, &e.Duration, &e.Comment, &e.TaskID, &e.ProjectID, &e.Ticket, &e.TaskTitle,
			&e.ProjectName, &e.ProjIssue, &e.ProjAccount, &e.JiraLink, &e.JiraOrigin, &e.PlaneWI, &e.TicketSys, &e.TempoTarget, &e.TempoAccount,
			&e.PushID, &e.PushHash, &e.PushTarget, &e.PushErr, &e.SyncedAt, &e.Dup, &e.Skip, &e.CheckFlags, &e.CheckNote, &e.PushInstance,
			&e.TargetInst, &e.TaskInst, &e.ProjIssueInst, &rule, &inPlane); err != nil {
			return nil, err
		}
		e.AllowsTempo = ruleAllows(rule, "bridge", "tempo")
		e.AllowsPlane = ruleAllows(rule, "bridge", "plane") && inPlane
		out = append(out, e)
	}
	return out, rows.Err()
}

func (a *App) tempoLookups(ctx context.Context, uid int64, s *tempoSetup) (tempoLookup, error) {
	lk := tempoLookup{MonthTickets: map[string]string{}, Mapped: map[string]string{}, MonthInst: map[string]int64{}, MonthAmbig: map[string]bool{}}
	rows, err := a.DB.Query(ctx, `SELECT month, issue_key, instance_id, worker FROM tempo_month_tickets WHERE user_id=$1 ORDER BY month, instance_id`, uid)
	if err != nil {
		return lk, err
	}
	for rows.Next() {
		var m, k, w string
		var inst int64
		if err := rows.Scan(&m, &k, &inst, &w); err != nil {
			rows.Close()
			return lk, err
		}
		ti := s.byID[inst]
		if ti == nil || ti.C.Worker == "" || ti.C.Worker != w {
			continue
		}
		if old, has := lk.MonthTickets[m]; has && old != k {
			lk.MonthAmbig[m] = true
			continue
		}
		lk.MonthTickets[m], lk.MonthInst[m] = k, inst
	}
	rows.Close()
	for m := range lk.MonthAmbig {
		delete(lk.MonthTickets, m)
		delete(lk.MonthInst, m)
	}
	rows, err = a.DB.Query(ctx, `SELECT UPPER(plane_key), jira_key FROM ticket_map WHERE user_id=$1 AND status='mapped' AND plane_key IS NOT NULL ORDER BY mapped_at, jira_key`, uid)
	if err != nil {
		return lk, err
	}
	defer rows.Close()
	for rows.Next() {
		var pk, jk string
		if err := rows.Scan(&pk, &jk); err != nil {
			return lk, err
		}
		if _, has := lk.Mapped[pk]; !has && jk != "" {
			lk.Mapped[pk] = jk
		}
	}
	return lk, rows.Err()
}

// tempoItem: Buchung mit Plan und Instanz
type tempoItem struct {
	E       tempoEntry
	P       tempoPlan
	Inst    *tempoInst // nil = nicht zuzuordnen
	Problem string
}

// tempoItems: Plan je Buchung – Ziel mit den vereinten Werten, Instanz über das Kürzel, dann Account/Benutzer der Instanz
func (a *App) tempoItems(ctx context.Context, uid int64, s *tempoSetup, st map[string]string, lk tempoLookup, extra string, args ...any) ([]tempoItem, error) {
	list, err := a.tempoEntries(ctx, uid, s.Person.Since, st, extra, args...)
	if err != nil {
		return nil, err
	}
	keys := s.keys()
	out := make([]tempoItem, 0, len(list))
	for _, e := range list {
		if it, ok := s.assign(e, lk, keys, a.Loc); ok {
			out = append(out, it)
		}
	}
	return out, nil
}

// assign (ohne Datenbank): Ziel mit den vereinten Werten, Instanz über das Kürzel; schon übertragene bleiben in ihrer
// Instanz. false = Buchung liegt vor der Grenze (tempo_push_since) ihrer Instanz.
func (s *tempoSetup) assign(e tempoEntry, lk tempoLookup, keys []instKeys, loc *time.Location) (tempoItem, bool) {
	target, kind := tempoTargetOf(e, s.Person, lk)
	it := tempoItem{E: e}
	id, problem := resolveTempoInstance(target, kind, lk.MonthInst[monthOfDate(e.Date)], keys)
	if lk.MonthAmbig[monthOfDate(e.Date)] && kind != "ticket" && e.isPlaneBooking() && s.Person.PlaneMode == "month" {
		problem = "Übergangs-Tickets des Monats in mehreren Jira-Instanzen – nicht eindeutig"
	}
	// von der Person gewählte Instanz (bei mehrdeutigem Kürzel) gilt vor dem Kürzel
	if x := explicitInstance(e, target); x != 0 && s.byID[x] != nil {
		id, problem = x, ""
	}
	// schon übertragen: bleibt in seiner Instanz (Kürzellisten schwanken; ein gleich benanntes Projekt woanders ist ein anderes Ticket)
	if e.PushID != "" && !isClaim(e.PushID) && e.PushInstance != 0 && s.byID[e.PushInstance] != nil {
		problem = ""
		if id != 0 && id != e.PushInstance {
			problem = "Worklog liegt in „" + s.byID[e.PushInstance].Name + "“, das Ticket passt jetzt zu „" + s.byID[id].Name + "“ – nicht umgezogen (Ticket/Administrator prüfen)"
		}
		id = e.PushInstance
	}
	c := s.Person
	if ti := s.byID[id]; ti != nil {
		it.Inst = ti
		if problem == "" {
			problem = ti.Problem
		}
		c.Account, c.AccountAttrID, c.WorkerKey = ti.C.Account, ti.C.AccountAttrID, ti.C.WorkerKey
		if e.Date < ti.C.Since { // vor der Grenze dieser Instanz: nicht nach Tempo
			return it, false
		}
	}
	if e.PushID != "" && !isClaim(e.PushID) && e.PushInstance == 0 {
		problem = "Worklog gehört zu einer gelöschten oder unbekannten Jira-Instanz – wird nicht erneut übertragen"
	}
	it.P = tempoPlanOf(e, c, lk, loc)
	it.Problem = problem
	return it, true
}

// tempoItemStatus: Status wie Node, ergänzt um die Instanz (unklare Instanz → „needs“; nie Umzug in eine andere Instanz)
func tempoItemStatus(it tempoItem, locks map[int64]map[string]bool) string {
	locked := it.Inst != nil && locks[it.Inst.ID][monthOfDate(it.E.Date)]
	st := tempoStatusOf(it.E, it.P, locked)
	switch st {
	case "skipped", "norule", "review", "dup":
		return st
	}
	if isClaim(it.E.PushID) {
		return "new"
	}
	if it.E.PushID != "" && it.Inst != nil && it.E.PushInstance != it.Inst.ID {
		return "needs" // nie in eine andere Instanz umziehen (tempoItems hält schon übertragene in ihrer Instanz)
	}
	if st == "synced" && it.E.PushInstance != 0 {
		return "synced"
	}
	if it.Problem != "" {
		return "needs"
	}
	return st
}

func (a *App) tempoLockedMonths(ctx context.Context, uid int64) (map[int64]map[string]bool, error) {
	rows, err := a.DB.Query(ctx, `SELECT instance_id, month FROM tempo_months WHERE user_id=$1 AND locked=1`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]map[string]bool{}
	for rows.Next() {
		var i int64
		var m string
		if err := rows.Scan(&i, &m); err != nil {
			return nil, err
		}
		if out[i] == nil {
			out[i] = map[string]bool{}
		}
		out[i][m] = true
	}
	return out, rows.Err()
}

func nowStamp() string { return time.Now().UTC().Format(tempoStampFmt) }

/* ── Monat prüfen: Sperre + vorhandene Worklogs (Duplikat-Hinweis) ── */

// tempoCheckMonth prüft einen Monat in einer Instanz; items = Buchungen dieses Monats, die zu dieser Instanz gehören
func (a *App) tempoCheckMonth(ctx context.Context, uid int64, ti *tempoInst, month string, items []tempoItem) (map[string]any, error) {
	from, to, ok := monthBounds(month)
	if !ok {
		return nil, errors.New("Monat ungültig")
	}
	c := ti.C
	locked, reason := 0, ""
	if s, err := a.tempoApproval(ctx, uid, c.jira(), from, c.WorkerKey); err == nil { // Fehler (auch 401/403) sperren nicht
		if l, r := approvalLock(s); l {
			locked, reason = 1, r
		}
	}
	var remote, checked any // checked_at nur nach erfolgreicher Suche: sonst bald erneut prüfen (Duplikate)
	if wl, err := a.tempoSearch(ctx, uid, c.jira(), from, to, c.WorkerKey); err == nil {
		own := map[string]bool{}
		rows, err := a.DB.Query(ctx, `SELECT tempo_push_id FROM time_entries WHERE user_id=$1 AND tempo_push_id IS NOT NULL AND tempo_push_instance=$2`, uid, ti.ID)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id string
			if rows.Scan(&id) == nil {
				own[id] = true
			}
		}
		rows.Close()
		others := remoteWorklogs(wl, own, a.Loc)
		remote, checked = len(others), nowStamp()
		for _, it := range items {
			e := it.E
			if e.PushID != "" || e.Dup == "ok" || strings.HasPrefix(e.Dup, interruptedPrefix) || e.Date < from || e.Date > to {
				continue
			}
			var label any
			s, ok1 := parseTimeIn(e.StartTime, a.Loc)
			en, ok2 := parseTimeIn(e.EndTime, a.Loc)
			if ok1 && ok2 {
				if hit := dupHit(s, en, others); hit != nil {
					label = dupLabel(hit, a.Loc)
				}
			}
			if _, err := a.DB.Exec(ctx, `UPDATE time_entries SET tempo_dup=$3::text WHERE id=$1 AND user_id=$2 AND tempo_push_id IS NULL
				AND COALESCE(tempo_dup,'') NOT LIKE 'unterbrochen:%' AND tempo_dup IS DISTINCT FROM $3::text`, e.ID, uid, label); err != nil {
				return nil, err
			}
		}
	}
	if _, err := a.DB.Exec(ctx, `INSERT INTO tempo_months(user_id,instance_id,month,locked,reason,remote_count,checked_at) VALUES($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT (user_id,instance_id,month) DO UPDATE SET locked=EXCLUDED.locked, reason=EXCLUDED.reason, remote_count=EXCLUDED.remote_count, checked_at=EXCLUDED.checked_at`,
		uid, ti.ID, month, locked, nz(reason), remote, checked); err != nil {
		return nil, err
	}
	return map[string]any{"month": month, "instance_id": ti.ID, "locked": locked == 1, "reason": nz(reason), "remote": remote}, nil
}

func (a *App) tempoLockFromError(ctx context.Context, uid, instID int64, month, msg string) {
	if _, err := a.DB.Exec(ctx, `INSERT INTO tempo_months(user_id,instance_id,month,locked,reason,checked_at) VALUES($1,$2,$3,1,$4,$5)
		ON CONFLICT (user_id,instance_id,month) DO UPDATE SET locked=1, reason=EXCLUDED.reason, checked_at=EXCLUDED.checked_at`,
		uid, instID, month, "Tempo lehnt ab: "+truncRunes(msg, 160), nowStamp()); err != nil {
		a.Log.Error("Tempo-Monatssperre speichern", "err", err)
	}
}

// tempoMonthDue: Monat in dieser Instanz seit 30 min nicht (erfolgreich) geprüft?
func (a *App) tempoMonthDue(ctx context.Context, uid, instID int64, month string) bool {
	var at string
	if err := a.DB.QueryRow(ctx, `SELECT COALESCE(checked_at,'') FROM tempo_months WHERE user_id=$1 AND instance_id=$2 AND month=$3`, uid, instID, month).Scan(&at); err != nil {
		return true
	}
	t, err := time.Parse(tempoStampFmt, at)
	return err != nil || time.Since(t) > 30*time.Minute
}

/* ── Account-Fallback (Standard-Account des Jira-Projekts) ──────── */

type accCached struct {
	key string
	at  time.Time
}

func (a *App) tempoAccountKeyFor(ctx context.Context, uid int64, c tempoCfg, projectKey string) string {
	ck := fmt.Sprintf("%d|%s|%s", uid, c.Base, projectKey)
	if v, ok := a.tempoAccCache.Load(ck); ok && time.Since(v.(accCached).at) < time.Hour {
		return jsOr(v.(accCached).key, c.Account)
	}
	key, err := a.tempoProjectAccount(ctx, uid, c.jira(), projectKey)
	if err == nil {
		a.tempoAccCache.Store(ck, accCached{key, time.Now()})
	}
	return jsOr(key, c.Account)
}

/* ── Übertragen ─────────────────────────────────────────────────── */

type tempoOpts struct {
	IDs    []int64
	Before time.Time // nur Buchungen, deren Ende davor liegt (Automatik)
}

var errTempoSkip = errors.New("Buchung wurde während der Übertragung geändert")

// tempoAdvisory: Datenbank-Sperre je Person (gilt auch über mehrere Server-Instanzen); release gibt sie frei
func (a *App) tempoAdvisory(ctx context.Context, uid int64) (bool, func(), error) {
	conn, err := a.DB.Acquire(ctx)
	if err != nil {
		return false, nil, err
	}
	var got bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, tempoLockKey+uid).Scan(&got); err != nil || !got {
		conn.Release()
		return false, nil, err
	}
	return true, func() {
		uctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := conn.Exec(uctx, `SELECT pg_advisory_unlock($1)`, tempoLockKey+uid); err != nil {
			_ = conn.Conn().Close(uctx) // Sperre hängt an der Verbindung: lieber Verbindung schließen
		}
		conn.Release()
	}, nil
}

func (a *App) tempoPush(ctx context.Context, uid int64, o tempoOpts) (map[string]any, error) {
	s, st, err := a.tempoSetup(ctx, uid)
	if err != nil {
		return nil, err
	}
	if !s.Person.Enabled {
		return nil, &jiraErr{Status: 400, Msg: "Tempo-Übertragung ist ausgeschaltet (Einstellungen › Jira & Tempo)"}
	}
	if len(s.usable()) == 0 {
		msg := "Keine nutzbare Jira-Instanz (Administrator) bzw. kein Jira-Token eingetragen"
		if len(s.Insts) > 0 {
			msg = s.Insts[0].Problem
		}
		return nil, &jiraErr{Status: 400, Msg: msg}
	}
	ts := a.tempoStateFor(uid)
	ts.mu.Lock()
	if ts.pushing {
		ts.again = true
		ts.mu.Unlock()
		return map[string]any{"busy": true}, nil
	}
	ts.pushing = true
	ts.mu.Unlock()
	defer func() {
		ts.mu.Lock()
		ts.pushing = false
		again := ts.again
		ts.again = false
		ts.mu.Unlock()
		if again {
			a.tempoAuto(uid)
		}
	}()
	select { // höchstens 4 Läufe gleichzeitig (je Lauf eine Datenbankverbindung für die Sperre)
	case tempoSlots <- struct{}{}:
		defer func() { <-tempoSlots }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	got, release, err := a.tempoAdvisory(ctx, uid)
	if err != nil {
		return nil, err
	}
	if !got {
		return map[string]any{"busy": true}, nil
	}
	defer release()

	out := map[string]any{"ok": 0, "errors": 0, "deleted": 0, "messages": []string{}}
	res := &tempoResult{out: out}
	for _, ti := range s.usable() { // Benutzer-Schlüssel (JIRAUSER…) und Projektkürzel je Instanz
		if st[instKey(ti.ID, "jira_userkey")] == "" || ti.C.Worker == "" {
			if me, err := a.jiraMyself(ctx, uid, ti.C.jira()); err == nil {
				a.saveJiraMe(ctx, uid, ti, me)
			} else if tempoAbort(jiraStatus(err), jiraDetail(err)) {
				ti.aborted = err.Error()
			}
		}
		if ti.aborted == "" && instKeysStale(st, ti.ID) {
			if err := a.refreshProjectKeys(ctx, uid, ti); err != nil && tempoAbort(jiraStatus(err), jiraDetail(err)) {
				ti.aborted = err.Error()
			}
		}
		if ti.aborted == "" && ti.C.Worker == "" {
			ti.Problem = "Jira-Benutzer für „" + ti.Name + "“ unbekannt – „Verbindung testen“"
		}
		if ti.aborted != "" {
			res.inc("errors")
			res.msg(ti.Name + ": " + ti.aborted)
		}
	}
	a.tempoRecoverClaims(ctx, uid)
	a.tempoDeletions(ctx, uid, s, res)
	if err := a.tempoEntriesPush(ctx, uid, s, st, o, res); err != nil {
		return nil, err
	}
	a.reconcileMonthTickets(ctx, uid, s) // schließen/wiederöffnen nach Freigabe, Sperre, Nachfrist
	a.tempoLogResult(ctx, uid, res)
	return out, nil
}

type tempoResult struct{ out map[string]any }

func (r *tempoResult) inc(k string) { r.out[k] = asInt(r.out[k]) + 1 }

func (r *tempoResult) msg(m string) {
	l := r.out["messages"].([]string)
	if len(l) < 20 {
		r.out["messages"] = append(l, m)
	}
}

func asInt(v any) int {
	n, _ := v.(int)
	return n
}

func (a *App) tempoLogResult(ctx context.Context, uid int64, r *tempoResult) {
	o := r.out
	if asInt(o["ok"])+asInt(o["errors"])+asInt(o["deleted"])+asInt(o["locked"]) == 0 {
		return // nichts geschehen: kein Eintrag (Automatik läuft alle 10 min)
	}
	lvl := "success"
	if asInt(o["errors"]) > 0 {
		lvl = "warn"
	}
	first := ""
	if l := o["messages"].([]string); len(l) > 0 {
		first = " – " + l[0]
	}
	a.sysLog(ctx, uid, lvl, "tempo", fmt.Sprintf("Tempo-Übertragung: %d Worklogs, %d Fehler, %d gesperrt, %d gelöscht%s", asInt(o["ok"]), asInt(o["errors"]), asInt(o["locked"]), asInt(o["deleted"]), first))
}

// tempoRecoverClaims: Ansprüche eines abgebrochenen Laufs (wir halten die Sperre, also läuft keiner mehr) → Hinweis statt neu anlegen
func (a *App) tempoRecoverClaims(ctx context.Context, uid int64) {
	tag, err := a.DB.Exec(ctx, `UPDATE time_entries SET tempo_push_id=NULL, tempo_push_hash=NULL, tempo_dup=$2,
		tempo_push_error='Übertragung unterbrochen – nicht erneut angelegt, bitte in Tempo prüfen'
		WHERE user_id=$1 AND tempo_push_id LIKE 'pending:%'`, uid, interruptedDup)
	if err != nil {
		a.Log.Error("Tempo-Ansprüche aufräumen", "err", err)
		return
	}
	if n := tag.RowsAffected(); n > 0 {
		a.sysLog(ctx, uid, "warn", "tempo", fmt.Sprintf("%d Buchung(en) mit unterbrochener Übertragung – nicht erneut angelegt, bitte in Tempo prüfen", n))
	}
}

// tempoDeletions: vorgemerkte Löschungen, je in ihrer Instanz; Netz/Token/Rechte brechen nur diese Instanz ab
func (a *App) tempoDeletions(ctx context.Context, uid int64, s *tempoSetup, r *tempoResult) {
	rows, err := a.maps(ctx, `SELECT id, worklog_id, COALESCE(label,'') AS label, COALESCE(instance_id,0) AS instance_id FROM tempo_push_deletions WHERE user_id=$1 ORDER BY id`, uid)
	if err != nil {
		r.inc("errors")
		r.msg("Löschliste nicht lesbar")
		a.Log.Error("Tempo-Löschliste", "err", err)
		return
	}
	for _, d := range rows {
		id, wl := d["id"], asStr(d["worklog_id"])
		if isClaim(wl) { // Anlegen lief noch: keine Id zu löschen (eine danach angelegte Id merkt sich der Lauf selbst vor)
			_, _ = a.DB.Exec(ctx, `DELETE FROM tempo_push_deletions WHERE id=$1 AND user_id=$2`, id, uid)
			a.sysLog(ctx, uid, "warn", "tempo", "Gelöschte Buchung war mitten in der Übertragung ("+asStr(d["label"])+") – falls in Tempo ein Worklog ohne Buchung steht, dort löschen")
			continue
		}
		ti := s.byID[int64(asFloat(d["instance_id"]))]
		if ti == nil || ti.Problem != "" || ti.aborted != "" {
			msg := "Jira-Instanz unbekannt oder gelöscht – Worklog bitte in Tempo selbst löschen"
			if ti != nil {
				msg = jsOr(ti.aborted, ti.Problem)
			}
			_, _ = a.DB.Exec(ctx, `UPDATE tempo_push_deletions SET error=$3 WHERE id=$1 AND user_id=$2`, id, uid, truncRunes(msg, 300))
			continue
		}
		if err := a.tempoDelete(ctx, uid, ti.C.jira(), wl); err != nil {
			_, _ = a.DB.Exec(ctx, `UPDATE tempo_push_deletions SET error=$3 WHERE id=$1 AND user_id=$2`, id, uid, shortErr(err))
			r.inc("errors")
			r.msg(ti.Name + ": " + err.Error())
			// im gesperrten Zeitraum bleibt die Löschung vorgemerkt, blockiert aber die übrigen Buchungen nicht
			if tempoAbort(jiraStatus(err), jiraDetail(err)) {
				ti.aborted = err.Error()
			}
			if ctx.Err() != nil {
				return
			}
			continue
		}
		_, _ = a.DB.Exec(ctx, `DELETE FROM tempo_push_deletions WHERE id=$1 AND user_id=$2`, id, uid)
		r.inc("deleted")
	}
}

func jiraDetail(err error) string {
	var je *jiraErr
	if errors.As(err, &je) {
		return je.Detail
	}
	return err.Error()
}

func (a *App) tempoEntriesPush(ctx context.Context, uid int64, s *tempoSetup, st map[string]string, o tempoOpts, r *tempoResult) error {
	extra, args := "", []any{}
	if len(o.IDs) > 0 {
		extra, args = "e.id = ANY($4)", []any{o.IDs}
	}
	lk, err := a.tempoLookups(ctx, uid, s)
	if err != nil {
		return err
	}
	items, err := a.tempoItems(ctx, uid, s, st, lk, extra, args...)
	if err != nil {
		return err
	}
	want := map[int64]bool{}
	type im struct {
		inst  int64
		month string
	}
	months := map[im][]tempoItem{}
	for _, it := range items {
		if !o.Before.IsZero() {
			if t, ok := parseTimeIn(it.E.EndTime, a.Loc); !ok || !t.Before(o.Before) {
				continue
			}
		}
		want[it.E.ID] = true
		if !it.E.Skip && it.Inst != nil && it.Problem == "" {
			k := im{it.Inst.ID, monthOfDate(it.E.Date)}
			months[k] = append(months[k], it)
		}
	}
	keys := make([]im, 0, len(months))
	for k := range months {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].inst != keys[j].inst {
			return keys[i].inst < keys[j].inst
		}
		return keys[i].month < keys[j].month
	})
	for _, k := range keys { // höchstens alle 30 min je Instanz und Monat
		if ti := s.byID[k.inst]; ti.aborted == "" && a.tempoMonthDue(ctx, uid, k.inst, k.month) {
			_, _ = a.tempoCheckMonth(ctx, uid, ti, k.month, months[k])
		}
	}
	fresh, err := a.tempoItems(ctx, uid, s, st, lk, extra, args...) // tempo_dup neu laden
	if err != nil {
		return err
	}
	locks, err := a.tempoLockedMonths(ctx, uid)
	if err != nil {
		return err
	}
	touched := map[monthKey]bool{}
	monthFailed := map[monthKey]string{} // Übergangs-Ticket im Lauf nicht anlegbar/auffindbar → Monat überspringen
	defer func() {                       // Beschreibung der berührten Übergangs-Tickets aktuell halten
		for k := range touched {
			if ti := s.byID[k.inst]; ti != nil && ti.aborted == "" {
				a.updateMonthDescription(ctx, uid, ti, k.month)
			}
		}
	}()
	for _, it := range fresh {
		if !want[it.E.ID] {
			continue
		}
		e, p := it.E, it.P
		switch tempoItemStatus(it, locks) {
		case "synced", "skipped", "dup", "norule":
			continue
		case "review":
			r.inc("review")
			continue
		case "locked":
			r.inc("locked")
			continue
		case "needs":
			r.inc("needs")
			continue
		}
		ti := it.Inst
		if isClaim(e.PushID) || ti == nil || ti.aborted != "" {
			continue
		}
		if p.Kind == "create-month" { // erste Plane-Buchung des Monats → Übergangs-Ticket anlegen bzw. wiederfinden
			m := monthOfDate(e.Date)
			if msg, failed := monthFailed[monthKey{ti.ID, m}]; failed { // nicht je Buchung erneut suchen
				_, _ = a.DB.Exec(ctx, `UPDATE time_entries SET tempo_push_error=$3 WHERE id=$1 AND user_id=$2`, e.ID, uid, truncRunes(msg, 500))
				continue
			}
			key, created, err := a.ensureMonthTicket(ctx, uid, ti, m)
			if err != nil {
				msg := "Übergangs-Ticket " + m + ": " + err.Error()
				monthFailed[monthKey{ti.ID, m}] = msg
				_, _ = a.DB.Exec(ctx, `UPDATE time_entries SET tempo_push_error=$3 WHERE id=$1 AND user_id=$2`, e.ID, uid, truncRunes(msg, 500))
				r.inc("errors")
				r.msg(ti.Name + ": " + msg)
				if tempoAbort(jiraStatus(err), jiraDetail(err)) {
					ti.aborted = msg
				}
				continue
			}
			if created {
				r.inc("created")
			}
			lk.MonthTickets[m], lk.MonthInst[m] = key, ti.ID
			re, ok := s.assign(e, lk, s.keys(), a.Loc)
			if !ok || re.Inst != ti || re.Problem != "" || re.P.Kind != "month" {
				continue
			}
			p = re.P
		}
		if p.Kind == "month" {
			touched[monthKey{ti.ID, monthOfDate(e.Date)}] = true
		}
		if p.Started == "" {
			_, _ = a.DB.Exec(ctx, `UPDATE time_entries SET tempo_push_error=$3 WHERE id=$1 AND user_id=$2`, e.ID, uid, "Startzeit nicht lesbar")
			r.inc("errors")
			continue
		}
		err := a.tempoPushEntry(ctx, uid, ti, e, p)
		switch {
		case err == nil:
			r.inc("ok")
		case errors.Is(err, errTempoSkip):
		default:
			status, detail, msg := jiraStatus(err), jiraDetail(err), err.Error()
			if status == 400 && p.Kind == "ticket" && originErrRE.MatchString(msg) {
				msg = fmt.Sprintf("Ticket %s gibt es in Jira nicht – ist es ein Plane-Ticket? Dann Work-Item verknüpfen oder per Stift ein Jira-Ticket wählen (%s)", p.Target, msg)
			}
			_, _ = a.DB.Exec(ctx, `UPDATE time_entries SET tempo_push_error=$3 WHERE id=$1 AND user_id=$2`, e.ID, uid, truncRunes(msg, 500))
			if tempoLockError(status, detail) { // gesperrter Zeitraum → ganzen Monat dieser Instanz überspringen
				a.tempoLockFromError(ctx, uid, ti.ID, monthOfDate(e.Date), msg)
				if locks[ti.ID] == nil {
					locks[ti.ID] = map[string]bool{}
				}
				locks[ti.ID][monthOfDate(e.Date)] = true
				r.inc("locked")
				continue
			}
			r.inc("errors")
			r.msg(ti.Name + ": " + msg)
			if status == 0 || status == 401 || status == 403 { // Netz/Token/Rechte: diese Instanz für den Lauf abbrechen
				ti.aborted = msg
			}
		}
		if sleepCtx(ctx, 120*time.Millisecond) != nil {
			return nil
		}
	}
	return nil
}

// tempoClaim: Buchung atomar beanspruchen (nur wenn tempo_push_id noch old ist); errTempoSkip = jemand anderes hat sie geändert
func (a *App) tempoClaim(ctx context.Context, uid, id int64, old string) (string, error) {
	claim := claimPrefix + strconv.FormatInt(time.Now().UnixNano(), 10)
	tag, err := a.DB.Exec(ctx, `UPDATE time_entries SET tempo_push_id=$3 WHERE id=$1 AND user_id=$2 AND COALESCE(tempo_push_id,'')=$4 AND COALESCE(tempo_skip,0)=0`, id, uid, claim, old)
	if err != nil {
		return "", err
	}
	if tag.RowsAffected() == 0 {
		return "", errTempoSkip
	}
	return claim, nil
}

// tempoUnclaim: Anspruch zurückgeben (Tempo hat sicher NICHT angelegt) bzw. als „unterbrochen“ markieren (unklar).
// restore/restoreInst: alte Worklog-Id und ihre Instanz, die wieder eingetragen wird
func (a *App) tempoUnclaim(ctx context.Context, uid, id int64, claim, restore string, restoreInst int64, unsure bool) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	var err error
	if unsure {
		_, err = a.DB.Exec(ctx, `UPDATE time_entries SET tempo_push_id=NULL, tempo_push_hash=NULL, tempo_dup=$4 WHERE id=$1 AND user_id=$2 AND tempo_push_id=$3`, id, uid, claim, interruptedDup)
	} else {
		var tag pgconn.CommandTag
		tag, err = a.DB.Exec(ctx, `UPDATE time_entries SET tempo_push_id=$4 WHERE id=$1 AND user_id=$2 AND tempo_push_id=$3`, id, uid, claim, nz(restore))
		if err == nil && tag.RowsAffected() == 0 && restore != "" && !isClaim(restore) {
			// Buchung inzwischen gelöscht: der alte Worklog besteht noch → selbst zum Löschen vormerken
			if _, err = a.DB.Exec(ctx, `INSERT INTO tempo_push_deletions(user_id,worklog_id,label,instance_id) VALUES($1,$2,$3,$4)`, uid, restore, "Buchung während der Übertragung gelöscht", nzID(restoreInst)); err != nil {
				a.sysLog(ctx, uid, "error", "tempo", fmt.Sprintf("Worklog %s verwaist, Löschauftrag nicht gespeichert (%v) – in Tempo selbst löschen", restore, err))
			}
		}
	}
	if err != nil {
		a.Log.Error("Tempo-Anspruch freigeben", "err", err) // bleibt stehen → nächster Lauf setzt „unterbrochen“
	}
}

func nzID(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}

// definitiv abgelehnt (4xx): Tempo hat sicher nichts angelegt
func tempoRejected(err error) bool {
	s := jiraStatus(err)
	return s >= 400 && s < 500
}

// tempoPushEntry überträgt eine Buchung in die Instanz ti (übertragene nur in ihrer eigenen Instanz)
func (a *App) tempoPushEntry(ctx context.Context, uid int64, ti *tempoInst, e tempoEntry, p tempoPlan) error {
	c := ti.C
	body := tempoBody(c, p)
	id := e.PushID
	cross := id != "" && e.PushInstance != ti.ID
	prev := e.PushTarget
	if prev == "" && id != "" {
		prev = c.Issue // ohne gespeichertes Ziel: früher immer aufs globale Sammelticket
	}
	claim, restore := "", "" // restore: Id, die bei sicherer Ablehnung zurückgeschrieben wird (nur PUT-404-Zweig)
	if cross {               // Schutz: tempoItems hält übertragene Buchungen in ihrer Instanz
		return &jiraErr{Status: 409, Msg: "Worklog liegt in einer anderen Jira-Instanz – nicht umgezogen"}
	}
	if id != "" && prev != "" && prev != p.Target {
		// Ticketwechsel: alten Worklog löschen, dann neu anlegen
		cl, err := a.tempoClaim(ctx, uid, e.ID, id)
		if err != nil {
			return err
		}
		if err := a.tempoDelete(ctx, uid, c.jira(), id); err != nil {
			a.tempoUnclaim(ctx, uid, e.ID, cl, id, e.PushInstance, false)
			return err
		}
		claim, id = cl, ""
	}
	if id != "" {
		err := a.tempoUpdate(ctx, uid, c.jira(), id, body)
		if err == nil {
			return a.tempoSaved(ctx, uid, e.ID, id, id, ti.ID, p)
		}
		if jiraStatus(err) != 404 {
			return err
		}
		cl, err := a.tempoClaim(ctx, uid, e.ID, id) // in Tempo gelöscht → neu anlegen
		if err != nil {
			return err
		}
		// Lehnt Tempo das Anlegen ab, bleibt die alte Id stehen (wie Node): nächster Lauf PUT → 404 → erneut
		claim, restore, id = cl, id, ""
	}
	if claim == "" {
		cl, err := a.tempoClaim(ctx, uid, e.ID, "")
		if err != nil {
			return err
		}
		claim = cl
	}
	newID, err := a.tempoCreate(ctx, uid, c.jira(), body)
	if err != nil && jiraStatus(err) == 400 && p.Account == "" && accountErrRE.MatchString(err.Error()) {
		// Tempo verlangt einen Account → Standard-Account des Jira-Projekts (sonst eingestellter Account der Instanz)
		key := a.tempoAccountKeyFor(ctx, uid, ti.C, jiraProject(p.Target))
		if key == "" {
			a.tempoUnclaim(ctx, uid, e.ID, claim, restore, e.PushInstance, false)
			return &jiraErr{Status: 400, Msg: "Tempo verlangt einen Account – unter Tempo-Übertragung „Automatisch einrichten“ einen Standard-Account wählen"}
		}
		body["attributes"] = tempoAccountAttr(c, key)
		newID, err = a.tempoCreate(ctx, uid, c.jira(), body)
	}
	if err != nil {
		a.tempoUnclaim(ctx, uid, e.ID, claim, restore, e.PushInstance, !tempoRejected(err))
		return err
	}
	if newID == "" {
		a.tempoUnclaim(ctx, uid, e.ID, claim, "", 0, true)
		return &jiraErr{Status: 502, Msg: "Tempo hat keine Worklog-ID geliefert"}
	}
	return a.tempoSaved(ctx, uid, e.ID, claim, newID, ti.ID, p)
}

// tempoSaved speichert Worklog-Id und Instanz – nur, wenn die Buchung noch so dasteht wie beansprucht (expect)
func (a *App) tempoSaved(ctx context.Context, uid, entryID int64, expect, wlID string, instID int64, p tempoPlan) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second) // nach erfolgreichem POST nicht verlieren
	defer cancel()
	tag, err := a.DB.Exec(ctx, `UPDATE time_entries SET tempo_push_id=$3, tempo_push_hash=$4, tempo_push_target=$5, tempo_push_synced_at=$6, tempo_push_error=NULL,
		tempo_push_instance=$8 WHERE id=$1 AND user_id=$2 AND tempo_push_id=$7`, entryID, uid, wlID, p.Hash, p.Target, nowStamp(), expect, instID)
	if err != nil { // Anspruch bleibt → nächster Lauf: „unterbrochen“, kein zweites Anlegen; Id sichtbar festhalten
		a.Log.Error("Tempo: Worklog angelegt, Id nicht gespeichert", "worklog", wlID, "buchung", entryID, "err", err)
		a.sysLog(ctx, uid, "error", "tempo", fmt.Sprintf("Worklog %s angelegt, Buchung #%d nicht aktualisiert (%v) – in Tempo prüfen", wlID, entryID, err))
		return err
	}
	if tag.RowsAffected() == 0 && isClaim(expect) {
		// Buchung inzwischen gelöscht/geändert: angelegten Worklog wieder zum Löschen vormerken
		if _, err := a.DB.Exec(ctx, `INSERT INTO tempo_push_deletions(user_id,worklog_id,label,instance_id) VALUES($1,$2,$3,$4)`, uid, wlID, "verwaist nach Übertragung", instID); err != nil {
			a.sysLog(ctx, uid, "error", "tempo", fmt.Sprintf("Worklog %s verwaist, Löschauftrag nicht gespeichert (%v) – in Tempo selbst löschen", wlID, err))
		}
		a.sysLog(ctx, uid, "warn", "tempo", fmt.Sprintf("Worklog %s angelegt, Buchung #%d wurde inzwischen geändert – zum Löschen vorgemerkt", wlID, entryID))
		return errTempoSkip
	}
	return nil
}

/* ── Automatik ──────────────────────────────────────────────────── */

// tempoAuto stößt (entprellt, 60 s) die Übertragung an; ob sie läuft, entscheidet erst der Zeitpunkt (Modul, Modus „auto“)
func (a *App) tempoAuto(uid int64) { a.tempoAutoAfter(uid, tempoAutoDelay) }

func (a *App) tempoAutoAfter(uid int64, delay time.Duration) {
	ts := a.tempoStateFor(uid)
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if ts.timer != nil {
		ts.timer.Stop()
	}
	ts.timer = time.AfterFunc(delay, func() { a.safely("tempo-automatik", func() { a.tempoAutoRun(uid) }) })
}

func (a *App) tempoAutoRun(uid int64) {
	if a.DB == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	if !a.tempoModuleOn(ctx) {
		return
	}
	s, _, err := a.tempoSetup(ctx, uid)
	if err != nil || !s.Person.Enabled || s.Person.Mode != "auto" || len(s.usable()) == 0 {
		return
	}
	if _, err := a.tempoPush(ctx, uid, tempoOpts{Before: time.Now().Add(-5 * time.Minute)}); err != nil {
		a.sysLog(ctx, uid, "warn", "tempo", "Tempo (automatisch): "+err.Error())
	}
}

// tempoCronTick: alle 10 min für Personen mit „automatisch“ (Buchungen, deren Ende > 5 min zurückliegt)
func (a *App) tempoCronTick(ctx context.Context) {
	if !a.tempoModuleOn(ctx) {
		return
	}
	a.expireTicketRequests(ctx, 0)
	rows, err := a.DB.Query(ctx, `SELECT m.user_id FROM user_settings m JOIN user_settings e ON e.user_id=m.user_id AND e.key='tempo_push_enabled' AND e.value IN ('1','true')
		JOIN users u ON u.id=m.user_id AND NOT u.disabled WHERE m.key='tempo_push_mode' AND m.value='auto'`)
	if err != nil {
		a.Log.Error("Tempo-Automatik", "err", err)
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
	for i, id := range ids { // Starts verteilen (je 10 s), nicht alle zugleich
		a.tempoAutoAfter(id, tempoAutoDelay+time.Duration(i%50)*10*time.Second)
	}
}

func (a *App) startTempoCron(ctx context.Context) {
	go func() {
		t := time.NewTicker(10 * time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				a.safely("tempo-zeitplan", func() { a.tempoCronTick(ctx) })
			}
		}
	}()
}

/* ── Routen ─────────────────────────────────────────────────────── */

func (a *App) routesTempo(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/integrations/tempo-push/status", a.tempoGuard(a.tempoStatusRoute))
	mux.HandleFunc("GET /api/integrations/tempo-push/settings", a.tempoGuard(a.tempoSettingsRoute))
	mux.HandleFunc("GET /api/integrations/tempo-push/outbox", a.tempoGuard(a.tempoOutbox))
	mux.HandleFunc("POST /api/integrations/tempo-push/push", a.tempoGuard(a.tempoPushRoute))
	mux.HandleFunc("POST /api/integrations/tempo-push/dup", a.tempoGuard(a.tempoDupRoute))
	mux.HandleFunc("DELETE /api/integrations/tempo-push/months/{month}", a.tempoGuard(a.tempoMonthUnlock))
	mux.HandleFunc("DELETE /api/integrations/tempo-push/deletions/{id}", a.tempoGuard(a.tempoDeletionDrop))
}

// tempoGuard: angemeldet UND Modul eingeschaltet (module_tempo = "1"), sonst 404
func (a *App) tempoGuard(h func(http.ResponseWriter, *http.Request, User)) http.HandlerFunc {
	return a.auth(func(w http.ResponseWriter, r *http.Request, u User) {
		if !a.tempoModuleOn(r.Context()) {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "Modul ist abgeschaltet"})
			return
		}
		h(w, r, u)
	})
}

func (a *App) tempoFail(w http.ResponseWriter, err error) {
	var je *jiraErr
	if errors.As(err, &je) {
		code := http.StatusBadGateway
		if je.Status > 0 && je.Status < 500 {
			code = http.StatusBadRequest
		}
		writeJSON(w, code, map[string]any{"error": je.Msg})
		return
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		writeJSON(w, http.StatusGatewayTimeout, map[string]any{"error": "Zeitüberschreitung"})
		return
	}
	a.fail(w, err)
}

// tempoStatusRoute = Node GET …/status, je Instanz: Verbindungstest Jira + Tempo, Buchungstickets, Jira-Benutzer und Kürzel merken
func (a *App) tempoStatusRoute(w http.ResponseWriter, r *http.Request, u User) {
	ctx := r.Context()
	s, _, err := a.tempoSetup(ctx, u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	insts := []map[string]any{}
	allOK, user := true, ""
	for _, ti := range s.Insts {
		if !ti.Enabled {
			continue
		}
		o := map[string]any{"id": ti.ID, "name": ti.Name, "ok": false}
		insts = append(insts, o)
		if ti.Problem != "" {
			o["message"] = ti.Problem
			allOK = false
			continue
		}
		jc := ti.C.jira()
		me, err := a.jiraMyself(ctx, u.ID, jc)
		if err != nil {
			o["message"] = "Jira: " + err.Error()
			allOK = false
			continue
		}
		a.saveJiraMe(ctx, u.ID, ti, me)
		name := jsOr(me.Display, me.Name)
		user = jsOr(user, name)
		o["ok"], o["user"], o["message"] = true, name, "Jira OK – "+name
		today := time.Now().In(a.Loc).Format("2006-01-02")
		if _, err := a.tempoSearch(ctx, u.ID, jc, today, today, ti.C.WorkerKey); err != nil {
			msg := err.Error()
			if jiraStatus(err) == 401 {
				msg += " – Tempo-Lizenz prüfen"
			}
			o["tempo"], o["ok"] = map[string]any{"ok": false, "error": msg}, false
		} else {
			o["tempo"] = map[string]any{"ok": true}
		}
		if err := a.refreshProjectKeys(ctx, u.ID, ti); err != nil {
			o["projects"] = map[string]any{"ok": false, "error": err.Error()}
		} else {
			o["projects"] = map[string]any{"ok": true, "count": len(ti.C.JiraKeys)}
		}
		if ti.C.NewProject != "" {
			if pr, err := a.jiraDo(ctx, u.ID, jc, "GET", "/rest/api/2/project/"+url.PathEscape(ti.C.NewProject), nil); err != nil {
				o["new_project"], o["ok"] = map[string]any{"key": ti.C.NewProject, "ok": false, "error": err.Error()}, false
			} else {
				o["new_project"] = map[string]any{"key": ti.C.NewProject, "ok": true, "name": asStr(jm(pr)["name"])}
			}
		}
		o["issues"] = []map[string]any{}
		if o["ok"] != true {
			allOK = false
		}
	}
	// Buchungstickets (global je Instanz und je Projekt) in der Instanz prüfen, zu der sie gehören
	seen := map[string]bool{}
	var targets []string
	add := func(k string) {
		if k = strings.ToUpper(strings.TrimSpace(k)); k != "" && !seen[k] && len(targets) < 20 {
			seen[k] = true
			targets = append(targets, k)
		}
	}
	for _, ti := range s.Insts {
		add(ti.C.Issue)
	}
	if rows, err := a.DB.Query(ctx, `SELECT DISTINCT tempo_issue FROM projects WHERE user_id=$1 AND tempo_issue IS NOT NULL AND TRIM(tempo_issue)<>'' ORDER BY 1`, u.ID); err == nil {
		for rows.Next() {
			var k string
			if rows.Scan(&k) == nil {
				add(k)
			}
		}
		rows.Close()
	}
	keys := s.keys()
	unassigned := []map[string]any{}
	for _, k := range targets {
		if !jiraKeyRE.MatchString(k) {
			unassigned = append(unassigned, map[string]any{"key": k, "ok": false, "error": "kein Ticket-Key (ABC-123)"})
			allOK = false
			continue
		}
		id, problem := resolveTempoInstance(k, "ticket", 0, keys)
		ti := s.byID[id]
		var o map[string]any
		for _, x := range insts {
			if x["id"] == id {
				o = x
			}
		}
		if ti == nil || o == nil || ti.Problem != "" || o["user"] == nil {
			unassigned = append(unassigned, map[string]any{"key": k, "ok": false, "error": jsOr(problem, "Jira-Instanz nicht erreichbar")})
			allOK = false
			continue
		}
		i, err := a.jiraDo(ctx, u.ID, ti.C.jira(), "GET", "/rest/api/2/issue/"+k+"?fields=summary,status", nil)
		entry := map[string]any{"key": k, "ok": err == nil}
		if err != nil {
			entry["error"], o["ok"], allOK = err.Error(), false, false
		} else {
			entry["summary"] = asStr(jm(jm(i)["fields"])["summary"])
		}
		o["issues"] = append(o["issues"].([]map[string]any), entry)
	}
	msg := "Jira OK"
	switch {
	case len(insts) == 0:
		msg, allOK = "Keine Jira-Instanz eingeschaltet (Administrator)", false
	case !allOK:
		msg = "Nicht alles in Ordnung – Details je Instanz"
	}
	if user != "" && allOK {
		msg += " – " + user
	}
	// rückwärtskompatibel (Oberfläche wie Node): issues/new_project oben, zusätzlich zu den Feldern je Instanz
	flat := append([]map[string]any{}, unassigned...)
	var newProject any
	for _, o := range insts {
		if l, ok := o["issues"].([]map[string]any); ok {
			flat = append(flat, l...)
		}
		if newProject == nil && o["new_project"] != nil {
			newProject = o["new_project"]
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": allOK, "user": nz(user), "message": msg, "issues": flat, "new_project": newProject, "instances": insts, "unassigned": unassigned})
}

// tempoSettingsRoute: wirksame Einstellungen (ohne Token) – der Person und je Instanz
func (a *App) tempoSettingsRoute(w http.ResponseWriter, r *http.Request, u User) {
	s, _, err := a.tempoSetup(r.Context(), u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	insts := []map[string]any{}
	for _, ti := range s.Insts {
		if !ti.Enabled {
			continue
		}
		c := ti.C
		insts = append(insts, map[string]any{"id": ti.ID, "name": ti.Name, "kind": ti.Kind, "tempo": ti.Tempo, "usable": ti.Problem == "", "problem": nz(ti.Problem),
			"token_set": c.Token != "", "worker": nz(c.Worker), "worker_key": nz(c.WorkerKey), "since": c.Since, "issue": nz(c.Issue),
			"new_project": nz(c.NewProject), "issuetype": c.IssueType, "account": nz(c.Account), "account_attr_id": c.AccountAttrID, "jira_project_keys": len(c.JiraKeys)})
	}
	p := s.Person
	writeJSON(w, http.StatusOK, map[string]any{"module": true, "enabled": p.Enabled, "mode": p.Mode, "since": p.Since, "plane_mode": p.PlaneMode,
		"issue": nz(p.Issue), "new_project": nz(p.NewProject), "instances": insts, "creates_tickets": tempoCanCreate})
}

// tempoOutbox = Node GET …/outbox (ohne Vorschläge und Tageslücken): Status je Buchung mit Instanz, nach Tagen
func (a *App) tempoOutbox(w http.ResponseWriter, r *http.Request, u User) {
	ctx := r.Context()
	s, st, err := a.tempoSetup(ctx, u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	lk, err := a.tempoLookups(ctx, u.ID, s)
	if err != nil {
		a.fail(w, err)
		return
	}
	items, err := a.tempoItems(ctx, u.ID, s, st, lk, "")
	if err != nil {
		a.fail(w, err)
		return
	}
	locks, err := a.tempoLockedMonths(ctx, u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	counts, mins := map[string]int{}, map[string]int{}
	byDay := map[string][]map[string]any{}
	for _, it := range items {
		e, p := it.E, it.P
		status := tempoItemStatus(it, locks)
		m := int(p.Secs / 60)
		counts[status]++
		mins[status] += m
		system := any(nil)
		if e.PlaneWI != "" {
			system = "plane"
		} else if jiraKeyRE.MatchString(strings.ToUpper(e.Ticket)) {
			system = "jira"
		}
		var dup, instID, instName any
		if status == "dup" {
			dup = e.Dup
		}
		if it.Inst != nil {
			instID, instName = it.Inst.ID, it.Inst.Name
		}
		tempoID := e.PushID
		if isClaim(tempoID) {
			tempoID = ""
		}
		byDay[e.Date] = append(byDay[e.Date], map[string]any{"id": e.ID, "date": e.Date, "start_time": e.StartTime, "end_time": e.EndTime, "minutes": m,
			"status": status, "target": nz(p.Target), "kind": p.Kind, "ticket": nz(e.Ticket), "project_name": nz(e.ProjectName), "comment": p.Comment,
			"error": nz(e.PushErr), "tempo_id": nz(tempoID), "synced_at": nz(e.SyncedAt), "dup": dup, "account": nz(p.Account), "account_from": p.AccountFrom,
			"project_id": asID(e.ProjectID), "task_id": asID(e.TaskID), "task_title": nz(e.TaskTitle), "note": nz(jsTrim(e.Comment)), "system": system,
			"instance_id": instID, "instance": instName, "pushed_instance_id": nzID(e.PushInstance), "instance_problem": nz(it.Problem)})
	}
	// Vorschläge (lokal, schnell) wie Node für Buchungen ohne festes Ziel – nur wenn die Vorschlagsfunktion an ist
	if on, _ := a.suggestAllowed(ctx, u.ID); on {
		now := time.Now().In(a.Loc)
		if corpus, err := a.tempoCorpus(ctx, u.ID, now); err == nil {
			for _, es := range byDay {
				for _, x := range es {
					if k := asStr(x["kind"]); x["status"] == "needs" || k == "create" || k == "create-collector" {
						l := suggestLocal(strings.TrimSpace(asStr(x["task_title"])+" "+asStr(x["note"])), int64(asFloat(x["project_id"])), corpus, now)
						if len(l) > 3 {
							l = l[:3]
						}
						x["suggest"] = l
					}
				}
			}
		}
	}
	dates := make([]string, 0, len(byDay))
	for d := range byDay {
		dates = append(dates, d)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(dates)))
	days := []map[string]any{}
	for _, d := range dates {
		es := byDay[d]
		sum, open, needs := 0, 0, 0
		for _, x := range es {
			st := asStr(x["status"])
			if st != "skipped" {
				sum += x["minutes"].(int)
			}
			switch st {
			case "new", "changed", "error":
				open++
			case "needs":
				needs++
			}
		}
		days = append(days, map[string]any{"date": d, "entries": es, "minutes": sum, "open": open, "needs": needs, "gaps": []any{}})
	}
	monthTickets, err := a.maps(ctx, `SELECT instance_id, month, worker, issue_key, created_at, closed_at, close_error FROM tempo_month_tickets WHERE user_id=$1 ORDER BY month DESC LIMIT 24`, u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	deletions, err := a.maps(ctx, `SELECT id, instance_id, worklog_id, label, created_at, error FROM tempo_push_deletions WHERE user_id=$1 ORDER BY id`, u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	months, err := a.maps(ctx, `SELECT instance_id, month, locked, reason, remote_count, checked_at FROM tempo_months WHERE user_id=$1 AND month>=$2 ORDER BY month DESC, instance_id`, u.ID, monthOfDate(s.Person.Since))
	if err != nil {
		a.fail(w, err)
		return
	}
	instances := []map[string]any{}
	for _, ti := range s.Insts {
		if ti.Enabled {
			instances = append(instances, map[string]any{"id": ti.ID, "name": ti.Name, "usable": ti.Problem == "", "problem": nz(ti.Problem)})
		}
	}
	var oldest *string
	_ = a.DB.QueryRow(ctx, `SELECT MIN(date) FROM time_entries WHERE user_id=$1 AND end_time IS NOT NULL AND COALESCE(source,'manual') NOT IN ('tempo','tempo_csv')`, u.ID).Scan(&oldest)
	p := s.Person
	writeJSON(w, http.StatusOK, map[string]any{"month_tickets": monthTickets, "plane_mode": p.PlaneMode, "configured": len(s.usable()) > 0, "enabled": p.Enabled,
		"mode": p.Mode, "issue": nz(p.Issue), "new_project": nz(p.NewProject), "since": p.Since, "worker": nz(p.Worker), "counts": counts, "minutes": mins,
		"days": days, "deletions": deletions, "months": months, "instances": instances, "oldest": oldest})
}

func (a *App) tempoPushRoute(w http.ResponseWriter, r *http.Request, u User) {
	var in body
	if r.ContentLength != 0 && !readJSON(w, r, &in) {
		return
	}
	var ids []int64
	if l, ok := in["ids"].([]any); ok {
		for _, x := range l {
			if n := int64(asFloat(x)); n > 0 && len(ids) < 5000 {
				ids = append(ids, n)
			}
		}
	}
	// nicht mit dem Request abbrechen: nach einem erfolgreichen POST muss die Worklog-Id noch gespeichert werden
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 10*time.Minute)
	defer cancel()
	res, err := a.tempoPush(ctx, u.ID, tempoOpts{IDs: ids})
	if err != nil {
		a.tempoFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// tempoDupRoute = Node POST …/dup: Duplikat-Hinweis „trotzdem übertragen“ (push) oder „ausnehmen“ (skip)
func (a *App) tempoDupRoute(w http.ResponseWriter, r *http.Request, u User) {
	var in body
	if !readJSON(w, r, &in) {
		return
	}
	var ids []int64
	if l, ok := in["ids"].([]any); ok {
		for _, x := range l {
			if n := int64(asFloat(x)); n > 0 {
				ids = append(ids, n)
			}
		}
	}
	sql := ""
	switch asStr(in["action"]) {
	case "push":
		sql = `UPDATE time_entries SET tempo_dup='ok', tempo_push_error=NULL WHERE user_id=$1 AND id = ANY($2) AND tempo_push_id IS NULL`
	case "skip":
		sql = `UPDATE time_entries SET tempo_skip=1 WHERE user_id=$1 AND id = ANY($2)`
	default:
		badRequest(w, "action: push oder skip")
		return
	}
	if len(ids) > 0 {
		if _, err := a.DB.Exec(r.Context(), sql, u.ID, ids); err != nil {
			a.fail(w, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// tempoMonthUnlock = Node DELETE …/months/{month} (?instance=Id: nur diese Instanz): Sperre aufheben, Fehler der ungepushten Buchungen löschen
func (a *App) tempoMonthUnlock(w http.ResponseWriter, r *http.Request, u User) {
	m := r.PathValue("month")
	if !monthRE.MatchString(m) {
		badRequest(w, "Monat im Format JJJJ-MM")
		return
	}
	var inst any
	if v := r.URL.Query().Get("instance"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			badRequest(w, "instance: Id der Jira-Instanz")
			return
		}
		inst = n
	}
	err := a.inTx(r.Context(), func(tx pgx.Tx) error {
		if _, err := tx.Exec(r.Context(), `DELETE FROM tempo_months WHERE user_id=$1 AND month=$2 AND ($3::bigint IS NULL OR instance_id=$3::bigint)`, u.ID, m, inst); err != nil {
			return err
		}
		_, err := tx.Exec(r.Context(), `UPDATE time_entries SET tempo_push_error=NULL WHERE user_id=$1 AND substr(date,1,7)=$2 AND tempo_push_id IS NULL`, u.ID, m)
		return err
	})
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *App) tempoDeletionDrop(w http.ResponseWriter, r *http.Request, u User) {
	id, ok := pathID(r)
	if !ok {
		notFound(w)
		return
	}
	if _, err := a.DB.Exec(r.Context(), `DELETE FROM tempo_push_deletions WHERE id=$1 AND user_id=$2`, id, u.ID); err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

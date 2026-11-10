package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
)

/* Tempo-Ausgang, Einrichtung und Pflege (wie backend/tempo-push.js): Accounts, Account je Buchung, Ausnehmen,
   Zeitraum prüfen, Sammelticket/Account je Projekt, „Automatisch einrichten“. Werte, die zur Jira-Instanz gehören
   (Projekt für neue Tickets, Vorgangstyp, Standard-Account, Account-Attribut, „ab Datum“), stehen je Instanz (i<Id>:…).
   Änderungen, die gebuchte Zeit eingereichter/freigegebener Monate umleiten würden, lehnt der Server ab (Wiedereröffnen). */

func (a *App) routesTempoSetup(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/integrations/tempo-push/accounts", a.tempoGuard(a.tempoAccountsRoute))
	mux.HandleFunc("POST /api/integrations/tempo-push/entry-account", a.tempoGuard(a.tempoEntryAccount))
	mux.HandleFunc("POST /api/integrations/tempo-push/skip", a.tempoGuard(a.tempoSkipRoute))
	mux.HandleFunc("POST /api/integrations/tempo-push/check", a.tempoGuard(a.tempoCheckRoute))
	mux.HandleFunc("PUT /api/integrations/tempo-push/project/{id}", a.tempoGuard(a.tempoProjectRoute))
	mux.HandleFunc("GET /api/integrations/tempo-push/autosetup", a.tempoGuard(a.tempoAutosetupGet))
	mux.HandleFunc("POST /api/integrations/tempo-push/autosetup", a.tempoGuard(a.tempoAutosetupPost))
}

// oneInstance: ?instance=<Id> bzw. instance_id oder die einzige nutzbare Instanz
func oneInstance(s *tempoSetup, raw string) (*tempoInst, error) {
	insts, why := pickInstances(s, raw)
	switch {
	case len(insts) == 0:
		return nil, errUser(why)
	case len(insts) > 1:
		return nil, errUser("Mehrere Jira-Instanzen – bitte eine wählen (instance)")
	}
	return insts[0], nil
}

/* ── Accounts ───────────────────────────────────────────────────── */

type accountsCached struct {
	at   time.Time
	list []map[string]any
}

var tempoAccountsCache sync.Map // „Person|Instanz“ → accountsCached (1 h)

func openAccounts(r any) []map[string]any {
	out := []map[string]any{}
	for _, x := range listOf(r) {
		if st := asStr(x["status"]); st == "" || strings.Contains(strings.ToLower(st), "open") {
			out = append(out, map[string]any{"key": asStr(x["key"]), "name": x["name"]})
		}
	}
	return out
}

// tempoAccountsRoute = Node GET …/accounts: offene Accounts, meistgenutzte zuerst (Stunden in „used“)
func (a *App) tempoAccountsRoute(w http.ResponseWriter, r *http.Request, u User) {
	ctx := r.Context()
	s, _, err := a.tempoSetup(ctx, u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	ti, err := oneInstance(s, r.URL.Query().Get("instance"))
	if err != nil {
		a.jiraFail(w, err)
		return
	}
	ck := fmt.Sprintf("%d|%d", u.ID, ti.ID)
	if v, ok := tempoAccountsCache.Load(ck); ok && time.Since(v.(accountsCached).at) < time.Hour {
		writeJSON(w, http.StatusOK, v.(accountsCached).list)
		return
	}
	res, err := a.jiraDo(ctx, u.ID, ti.C.jira(), "GET", "/rest/tempo-accounts/1/account", nil)
	if err != nil {
		badRequest(w, err.Error())
		return
	}
	list := openAccounts(res)
	use := map[string]float64{}
	since := time.Now().Add(-183 * 24 * time.Hour).In(a.Loc).Format("2006-01-02")
	if rows, err := a.DB.Query(ctx, `SELECT COALESCE(e.tempo_account, p.tempo_account), COALESCE(SUM(e.duration_seconds),0)::float8 FROM time_entries e
		LEFT JOIN projects p ON p.id=e.project_id AND p.user_id=e.user_id WHERE e.user_id=$1 AND e.date>=$2 GROUP BY 1`, u.ID, since); err == nil {
		for rows.Next() {
			var k *string
			var s float64
			if rows.Scan(&k, &s) == nil && k != nil && *k != "" {
				use[*k] += s
			}
		}
		rows.Close()
	}
	if ti.C.Account != "" {
		use[ti.C.Account]++
	}
	sort.SliceStable(list, func(i, j int) bool {
		ui, uj := use[asStr(list[i]["key"])], use[asStr(list[j]["key"])]
		if ui != uj {
			return ui > uj
		}
		return asStr(list[i]["key"]) < asStr(list[j]["key"])
	})
	for _, x := range list {
		x["used"] = float64(int(use[asStr(x["key"])]/360+0.5)) / 10
	}
	tempoAccountsCache.Store(ck, accountsCached{time.Now(), list})
	writeJSON(w, http.StatusOK, list)
}

// tempoEntryAccount = Node POST …/entry-account {ids, account, remember:"project"}
func (a *App) tempoEntryAccount(w http.ResponseWriter, r *http.Request, u User) {
	var in body
	if !readJSON(w, r, &in) {
		return
	}
	ids := idList(in["ids"], 500)
	acc := strings.TrimSpace(asStr(in["account"]))
	if len(acc) > 100 {
		badRequest(w, "Account zu lang")
		return
	}
	ctx := r.Context()
	err := a.inTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE time_entries SET tempo_account=$3 WHERE user_id=$1 AND id = ANY($2)`, u.ID, ids, nz(acc)); err != nil {
			return err // ZP001: Monat eingereicht/freigegeben
		}
		if asStr(in["remember"]) != "project" || len(ids) == 0 {
			return nil
		}
		var pid *int64
		if err := tx.QueryRow(ctx, `SELECT project_id FROM time_entries WHERE user_id=$1 AND id=$2`, u.ID, ids[0]).Scan(&pid); err != nil || pid == nil {
			return nil
		}
		if err := a.projectLockCheck(ctx, tx, u.ID, *pid, ""); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE projects SET tempo_account=$3 WHERE id=$1 AND user_id=$2`, *pid, u.ID, nz(acc)); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE time_entries SET tempo_account=NULL WHERE user_id=$1 AND project_id=$2 AND tempo_account=$3`, u.ID, *pid, acc)
		return err
	})
	if err != nil {
		a.setupFail(w, err)
		return
	}
	a.sysLog(ctx, u.ID, "info", "tempo", fmt.Sprintf("Tempo-Account %s für %d Buchung(en)%s", jsOr(acc, "(Standard)"), len(ids), map[bool]string{true: " – fürs Projekt gemerkt", false: ""}[asStr(in["remember"]) == "project"]))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *App) setupFail(w http.ResponseWriter, err error) {
	var ce errConflict
	var ue errUser
	switch {
	case errors.As(err, &ce):
		writeJSON(w, http.StatusConflict, map[string]any{"error": string(ce), "code": "month_locked"})
	case errors.As(err, &ue):
		badRequest(w, string(ue))
	default:
		a.fail(w, err) // ZP001 → 409 month_locked
	}
}

// projectLockCheck: Projekt-Werte (Account, Sammelticket) ändern nur, wenn keine Buchung eines eingereichten/freigegebenen
// Monats dadurch anders gebucht würde; key = neues Sammelticket ("" = jede Buchung zählt)
func (a *App) projectLockCheck(ctx context.Context, tx pgx.Tx, uid, pid int64, key string) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock_shared($1)`, int64(7270030000000000)+uid); err != nil {
		return err
	}
	return a.lockedConflict(ctx, tx, uid, "project", []ticketEntry{{ProjectID: pid}}, key)
}

// tempoSkipRoute = Node POST …/skip {ids, skip}: ausnehmen (schon übertragene in Tempo löschen) bzw. wieder aufnehmen
func (a *App) tempoSkipRoute(w http.ResponseWriter, r *http.Request, u User) {
	var in body
	if !readJSON(w, r, &in) {
		return
	}
	skip := true
	if b, ok := in["skip"].(bool); ok {
		skip = b
	}
	ids := idList(in["ids"], 500)
	ctx := r.Context()
	err := a.inTx(ctx, func(tx pgx.Tx) error {
		for _, id := range ids {
			var push, label *string
			var inst *int64
			err := tx.QueryRow(ctx, `SELECT tempo_push_id, date || ' ' || LEFT(COALESCE(comment,''),60), tempo_push_instance FROM time_entries WHERE id=$1 AND user_id=$2 FOR UPDATE`, id, u.ID).Scan(&push, &label, &inst)
			if errors.Is(err, pgx.ErrNoRows) {
				continue
			}
			if err != nil {
				return err
			}
			if push != nil && isClaim(*push) {
				return errConflict("Buchung wird gerade übertragen – bitte gleich nochmal")
			}
			if skip && push != nil && *push != "" { // Löschung vormerken, Übertragungsfelder leeren
				if _, err := tx.Exec(ctx, `INSERT INTO tempo_push_deletions(user_id,worklog_id,label,instance_id) VALUES($1,$2,$3,$4)`, u.ID, *push, label, inst); err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, `UPDATE time_entries SET tempo_push_id=NULL, tempo_push_hash=NULL, tempo_push_synced_at=NULL, tempo_push_instance=NULL WHERE id=$1 AND user_id=$2`, id, u.ID); err != nil {
					return err
				}
			}
			if _, err := tx.Exec(ctx, `UPDATE time_entries SET tempo_skip=$3, tempo_push_error=NULL WHERE id=$1 AND user_id=$2`, id, u.ID, map[bool]int{true: 1, false: 0}[skip]); err != nil {
				return err // ZP001: Monat eingereicht/freigegeben (dann auch die Löschung zurück)
			}
		}
		return nil
	})
	if err != nil {
		a.setupFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// tempoCheckRoute = Node POST …/check {since?}: Sperren und Duplikate je Monat prüfen (je Instanz); optional „ab Datum“ setzen
func (a *App) tempoCheckRoute(w http.ResponseWriter, r *http.Request, u User) {
	var in body
	if r.ContentLength != 0 && !readJSON(w, r, &in) {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Minute)
	defer cancel()
	if since := asStr(in["since"]); since != "" {
		if !isoDateRE.MatchString(since) {
			badRequest(w, "Datum im Format JJJJ-MM-TT")
			return
		}
		a.saveUserSetting(ctx, u.ID, "tempo_push_since", since)
		_, _ = a.DB.Exec(ctx, `DELETE FROM user_settings WHERE user_id=$1 AND key LIKE 'i%:tempo_push_since'`, u.ID) // gilt dann für alle Instanzen
	}
	s, st, err := a.tempoSetup(ctx, u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	us := s.usable()
	if len(us) == 0 {
		badRequest(w, "Jira-Adresse und Token fehlen")
		return
	}
	got, release, err := a.tempoAdvisory(ctx, u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	if !got {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "Übertragung läuft gerade – bitte gleich nochmal"})
		return
	}
	defer release()
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
	now := time.Now().In(a.Loc).Format("2006-01")
	out := []map[string]any{}
	for _, ti := range us {
		if ti.C.Worker == "" {
			if me, err := a.jiraMyself(ctx, u.ID, ti.C.jira()); err == nil {
				a.saveJiraMe(ctx, u.ID, ti, me)
			} else {
				out = append(out, map[string]any{"instance_id": ti.ID, "error": "Erst „Verbindung testen“: " + err.Error()})
				continue
			}
		}
		for m := monthOfDate(ti.C.Since); m <= now; m = nextMonth(m) {
			var mine []tempoItem
			for _, it := range items {
				if it.Inst == ti && monthOfDate(it.E.Date) == m {
					mine = append(mine, it)
				}
			}
			res, err := a.tempoCheckMonth(ctx, u.ID, ti, m, mine)
			if err != nil {
				out = append(out, map[string]any{"month": m, "instance_id": ti.ID, "error": err.Error()})
				continue
			}
			out = append(out, res)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "since": s.Person.Since, "months": out})
}

func nextMonth(m string) string {
	t, err := time.Parse("2006-01", m)
	if err != nil {
		return "9999-12"
	}
	return t.AddDate(0, 1, 0).Format("2006-01")
}

var keyFormatRE = regexp.MustCompile(`^[A-Z][A-Z0-9_]*-[0-9]+$`)

// tempoProjectRoute = Node PUT …/project/:id {issue, account}: Sammelticket (leer = globales) und Account des Projekts
func (a *App) tempoProjectRoute(w http.ResponseWriter, r *http.Request, u User) {
	id, ok := pathID(r)
	if !ok {
		notFound(w)
		return
	}
	var in body
	if !readJSON(w, r, &in) {
		return
	}
	v := strings.ToUpper(strings.TrimSpace(asStr(in["issue"])))
	if v != "" && !keyFormatRE.MatchString(v) {
		badRequest(w, "Ticket-Key im Format ABC-123 angeben")
		return
	}
	ctx := r.Context()
	var inst any
	if raw := asID(in["instance_id"]); raw != nil {
		s, _, err := a.tempoSetup(ctx, u.ID)
		if err != nil {
			a.fail(w, err)
			return
		}
		if ti := s.byID[raw.(int64)]; ti == nil || ti.Problem != "" {
			badRequest(w, "Jira-Instanz unbekannt oder nicht nutzbar")
			return
		}
		inst = raw
	}
	err := a.inTx(ctx, func(tx pgx.Tx) error {
		var cur, curAcc string
		var curInst *int64
		if err := tx.QueryRow(ctx, `SELECT COALESCE(tempo_issue,''), COALESCE(tempo_account,''), tempo_issue_instance FROM projects WHERE id=$1 AND user_id=$2 FOR UPDATE`, id, u.ID).Scan(&cur, &curAcc, &curInst); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return errUser("Projekt nicht gefunden")
			}
			return err
		}
		_, hasAcc := in["account"]
		acc := strings.TrimSpace(asStr(in["account"]))
		instChanged := fmt.Sprint(inst) != fmt.Sprint(derefID(curInst))
		if v != cur || instChanged || (hasAcc && acc != curAcc) {
			key := v
			if (hasAcc && acc != curAcc) || instChanged {
				key = "" // anderer Account bzw. andere Instanz ändert jede Buchung des Projekts
			}
			if err := a.projectLockCheck(ctx, tx, u.ID, id, key); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE projects SET tempo_issue=$3, tempo_issue_instance=$4 WHERE id=$1 AND user_id=$2`, id, u.ID, nz(v), inst); err != nil {
			return err
		}
		if hasAcc {
			_, err := tx.Exec(ctx, `UPDATE projects SET tempo_account=$3 WHERE id=$1 AND user_id=$2`, id, u.ID, nz(acc))
			return err
		}
		return nil
	})
	if err != nil {
		a.setupFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "issue": nz(v)})
}

/* ── Automatisch einrichten ─────────────────────────────────────── */

var projectPrefs = []*regexp.Regexp{regexp.MustCompile(`^ZEIT`), regexp.MustCompile(`^TIME`), regexp.MustCompile(`^ZP$`), regexp.MustCompile(`^ORGA`),
	regexp.MustCompile(`^INT`), regexp.MustCompile(`^PRJB`), regexp.MustCompile(`^ADM`), regexp.MustCompile(`^IT$`)}

// rankProjects (ohne Netz) = Node: bekannte Namen (ZEIT, TIME, ZP, ORGA, INT, PRJB, ADM, IT) zuerst, höchstens 8
func rankProjects(ps []map[string]any) []map[string]any {
	rank := func(p map[string]any) int {
		for i, re := range projectPrefs {
			if re.MatchString(strings.ToUpper(asStr(p["key"]))) || re.MatchString(strings.ToUpper(asStr(p["name"]))) {
				return i
			}
		}
		return 99
	}
	out := append([]map[string]any{}, ps...)
	sort.SliceStable(out, func(i, j int) bool { return rank(out[i]) < rank(out[j]) })
	if len(out) > 8 {
		out = out[:8]
	}
	return out
}

// worklogAccount: Account eines Tempo-Worklogs (_Account_ oder erstes Attribut mit „account“)
func worklogAccount(w map[string]any) string {
	attrs := jm(w["attributes"])
	if v := asStr(jm(attrs["_Account_"])["value"]); v != "" {
		return v
	}
	keys := make([]string, 0, len(attrs))
	for k := range attrs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		x := jm(attrs[k])
		if strings.Contains(strings.ToLower(asStr(x["name"])+asStr(x["key"])), "account") {
			return asStr(x["value"])
		}
	}
	return ""
}

func topKey(m map[string]float64) string {
	best, bv := "", -1.0
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if m[k] > bv {
			best, bv = k, m[k]
		}
	}
	return best
}

// tempoAutosetupGet = Node GET …/autosetup: Vorschläge aus Jira/Tempo und der eigenen Tempo-Historie (eine Instanz)
func (a *App) tempoAutosetupGet(w http.ResponseWriter, r *http.Request, u User) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Minute)
	defer cancel()
	s, st, err := a.tempoSetup(ctx, u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	ti, err := oneInstance(s, r.URL.Query().Get("instance"))
	if err != nil {
		badRequest(w, "Erst Jira-Adresse und Token eintragen (Zugang & Projekte): "+err.Error())
		return
	}
	c := ti.C.jira()
	me, err := a.jiraMyself(ctx, u.ID, c)
	if err != nil {
		badRequest(w, "Jira: "+err.Error())
		return
	}
	a.saveJiraMe(ctx, u.ID, ti, me)
	notes := []string{}
	out := map[string]any{"instance_id": ti.ID, "instance": ti.Name}
	var projects []map[string]any
	if ps, err := a.jiraDo(ctx, u.ID, c, "GET", "/rest/api/2/project", nil); err == nil {
		projects = listOf(ps)
	}
	cands := rankProjects(projects)
	pl := []map[string]any{}
	for _, p := range cands {
		pl = append(pl, map[string]any{"key": p["key"], "name": p["name"]})
	}
	out["projects"] = pl
	tryList := cands
	if ti.C.NewProject != "" {
		tryList = append([]map[string]any{{"key": ti.C.NewProject}}, cands...)
	}
	for _, p := range tryList {
		types, err := a.jiraIssueTypes(ctx, u.ID, c, asStr(p["key"]))
		if err == nil && len(types) > 0 {
			out["new_project"], out["issuetype"], out["issuetypes"] = asStr(p["key"]), pickIssueType(types, ""), types
			break
		}
	}
	if out["new_project"] == nil && len(cands) > 0 {
		out["new_project"], out["issuetype"] = jsOr(ti.C.NewProject, asStr(cands[0]["key"])), ti.C.IssueType
		notes = append(notes, "Anlegerechte konnten nicht geprüft werden – Projekt bitte kontrollieren")
	}
	if id, err := a.tempoAccountAttrID(ctx, u.ID, c); err == nil && id > 0 {
		out["account_attr_id"] = id
	}
	accounts := []map[string]any{}
	if res, err := a.jiraDo(ctx, u.ID, c, "GET", "/rest/tempo-accounts/1/account", nil); err == nil {
		accounts = openAccounts(res)
	}
	if len(accounts) > 300 {
		accounts = accounts[:300]
	}
	out["accounts"] = accounts
	total, perProj := map[string]float64{}, map[int64]map[string]float64{}
	from := time.Now().Add(-183 * 24 * time.Hour).In(a.Loc).Format("2006-01-02")
	if wl, err := a.tempoSearch(ctx, u.ID, c, from, time.Now().In(a.Loc).Format("2006-01-02"), jsOr(me.Key, me.Name)); err == nil {
		for _, x := range wl {
			acc := worklogAccount(x)
			if acc == "" {
				continue
			}
			secs := asFloat(x["timeSpentSeconds"])
			total[acc] += secs
			pk := jiraProject(jsOr(asStr(jm(x["issue"])["key"]), asStr(x["originTaskId"])))
			var pid int64
			if pk != "" && a.DB.QueryRow(ctx, `SELECT id FROM projects WHERE user_id=$1 AND (UPPER(jira_project)=$2 OR UPPER(jira_key)=$2) ORDER BY id LIMIT 1`, u.ID, strings.ToUpper(pk)).Scan(&pid) == nil {
				if perProj[pid] == nil {
					perProj[pid] = map[string]float64{}
				}
				perProj[pid][acc] += secs
			}
		}
	} else {
		notes = append(notes, "Tempo-Historie nicht lesbar – Accounts bitte selbst wählen")
	}
	out["account"] = jsOr(ti.C.Account, topKey(total))
	usage := []map[string]any{}
	for k, v := range total {
		usage = append(usage, map[string]any{"key": k, "hours": float64(int(v/360+0.5)) / 10})
	}
	sort.SliceStable(usage, func(i, j int) bool { return usage[i]["hours"].(float64) > usage[j]["hours"].(float64) })
	if len(usage) > 10 {
		usage = usage[:10]
	}
	out["account_usage"] = usage
	pa := []map[string]any{}
	for pid, m := range perProj {
		var name, cur string
		if a.DB.QueryRow(ctx, `SELECT name, COALESCE(tempo_account,'') FROM projects WHERE id=$1 AND user_id=$2`, pid, u.ID).Scan(&name, &cur) != nil {
			continue
		}
		if sug := topKey(m); sug != "" && sug != cur && sug != asStr(out["account"]) {
			pa = append(pa, map[string]any{"project_id": pid, "name": name, "current": nz(cur), "suggest": sug})
		}
	}
	out["project_accounts"] = pa
	// ab wann: ältester nicht gesperrter Monat der letzten 3 (prüft Sperren und Duplikate)
	now := time.Now().In(a.Loc)
	var months []string
	for i := 2; i >= 0; i-- {
		months = append(months, now.AddDate(0, -i, 0).Format("2006-01"))
	}
	since := ""
	if got, release, err := a.tempoAdvisory(ctx, u.ID); err == nil && got {
		for _, m := range months {
			if res, err := a.tempoCheckMonth(ctx, u.ID, ti, m, nil); err == nil && res["locked"] != true && since == "" {
				since = m + "-01"
			}
		}
		release()
	}
	switch {
	case ti.C.Since != "" && (since == "" || ti.C.Since >= since):
		out["since"] = ti.C.Since
	case since != "":
		out["since"] = since
	default:
		out["since"] = ti.C.Since
	}
	ms, _ := a.maps(ctx, `SELECT instance_id, month, locked, reason, remote_count, checked_at FROM tempo_months WHERE user_id=$1 AND instance_id=$2 AND month>=$3 ORDER BY month`, u.ID, ti.ID, months[0])
	out["months"], out["plane_mode"], out["worker"], out["notes"] = ms, s.Person.PlaneMode, jsOr(me.Name, ti.C.Worker), notes
	_ = st
	writeJSON(w, http.StatusOK, out)
}

// tempoAutosetupPost = Node POST …/autosetup: übernehmen (Werte der Instanz, Plane-Modus/Einschalten der Person, Accounts je Projekt)
func (a *App) tempoAutosetupPost(w http.ResponseWriter, r *http.Request, u User) {
	var in body
	if !readJSON(w, r, &in) {
		return
	}
	ctx := r.Context()
	s, _, err := a.tempoSetup(ctx, u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	ti, err := oneInstance(s, asStr(in["instance_id"]))
	if err != nil {
		a.jiraFail(w, err)
		return
	}
	if v := asStr(in["since"]); v != "" && !isoDateRE.MatchString(v) {
		badRequest(w, "since: Datum JJJJ-MM-TT")
		return
	}
	set := func(k, v string) {
		if v = strings.TrimSpace(v); v != "" && len(v) <= 200 {
			a.saveUserSetting(ctx, u.ID, instKey(ti.ID, k), v)
		}
	}
	set("jira_new_project", strings.ToUpper(asStr(in["new_project"])))
	set("jira_new_issuetype", asStr(in["issuetype"]))
	set("tempo_account", asStr(in["account"]))
	if n := leadingInt(asStr(in["account_attr_id"])); n > 0 {
		set("tempo_account_attr_id", asStr(n))
	}
	set("tempo_push_since", asStr(in["since"]))
	if pm := asStr(in["plane_mode"]); pm != "" {
		a.saveUserSetting(ctx, u.ID, "tempo_plane_mode", map[bool]string{true: "ticket", false: "month"}[pm == "ticket"])
	}
	if truthy(in["enable"]) {
		a.saveUserSetting(ctx, u.ID, "tempo_push_enabled", "1")
	}
	var skipped []string
	if l, ok := in["project_accounts"].([]any); ok {
		for _, x := range l {
			m := jm(x)
			pid := asID(m["project_id"])
			if pid == nil {
				continue
			}
			err := a.inTx(ctx, func(tx pgx.Tx) error {
				if err := a.projectLockCheck(ctx, tx, u.ID, pid.(int64), ""); err != nil {
					return err
				}
				_, err := tx.Exec(ctx, `UPDATE projects SET tempo_account=$3 WHERE id=$1 AND user_id=$2`, pid, u.ID, nz(strings.TrimSpace(asStr(m["account"]))))
				return err
			})
			if err != nil {
				skipped = append(skipped, fmt.Sprint(pid)+": "+err.Error())
			}
		}
	}
	a.sysLog(ctx, u.ID, "info", "tempo", fmt.Sprintf("Tempo automatisch eingerichtet (%s)", ti.Name))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "instance_id": ti.ID, "skipped": skipped})
}

func derefID(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}

package app

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
)

/* Tempo-Ausgang Stufe 2: Buchungen ohne Ticket bekommen erst beim Übertragen eins – nie automatisch:
   • POST …/suggest: Vorschläge (lokale Historie/Aufgaben/Zuordnung + Jira-Suche je Instanz, Plane-Suche als Hinweis),
   • POST …/target:  Person ordnet ein vorhandenes Ticket zu (wählt bei Mehrdeutigkeit die Instanz),
   • POST …/ticket:  Person bestätigt „neues Jira-Ticket anlegen“ – idempotent je Auftrag (tempo_ticket_requests).
   Ohne Bestätigung bleibt die Buchung „needs“. Plane-Tickets legt weiter die Plane-Logik an (POST /api/integrations/plane/work-items,
   Entwürfe in plane_tickets.go). Nachträge in eingereichte/freigegebene Monate lehnt die Datenbank-Sperre ab (Wiedereröffnen durch Personal). */

func (a *App) routesTempoTickets(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/integrations/tempo-push/suggest", a.tempoGuard(a.tempoSuggestRoute))
	mux.HandleFunc("POST /api/integrations/tempo-push/target", a.tempoGuard(a.tempoTargetRoute))
	mux.HandleFunc("POST /api/integrations/tempo-push/ticket", a.tempoGuard(a.tempoTicketRoute))
}

/* ── Vorschläge (rein) ──────────────────────────────────────────── */

type corpusItem struct {
	Key, Title string
	Comments   []string
	Projects   map[int64]int
	Count      int
	Last       string // Datum JJJJ-MM-TT
	Instance   int64  // gewählte Instanz (tasks.jira_instance), 0 = über das Kürzel
}

type suggestion struct {
	Key        string   `json:"key"`
	Summary    string   `json:"summary"`
	Score      int      `json:"score"`
	Reasons    []string `json:"reasons"`
	Source     string   `json:"source"`
	InstanceID any      `json:"instance_id"`
	Instance   any      `json:"instance"`
	Problem    any      `json:"instance_problem"`
}

// suggestLocal = Node suggestLocal: 0,6·Text + 0,22 gleiches Projekt + 0,1·Häufigkeit + 0,08·Aktualität, Schwelle 0,3
func suggestLocal(text string, projectID int64, corpus []corpusItem, today time.Time) []suggestion {
	var out []suggestion
	for _, c := range corpus {
		simTitle := textSim(text, c.Title)
		simCom := 0.0
		for _, cm := range c.Comments {
			if v := textSim(text, cm); v > simCom {
				simCom = v
			}
			if simCom > .95 {
				break
			}
		}
		sim := math.Max(simTitle, simCom*0.9)
		projHits := 0
		if projectID != 0 {
			projHits = c.Projects[projectID]
		}
		if sim < 0.2 && projHits == 0 {
			continue
		}
		freq := math.Min(1, math.Log(1+float64(c.Count))/math.Log(30))
		days := 999.0
		if t, err := time.Parse("2006-01-02", c.Last); err == nil {
			days = today.Sub(t.Add(12*time.Hour)).Hours() / 24
		}
		rec := 0.0
		if days < 400 {
			rec = math.Max(0, 1-days/120)
		}
		score := 0.6*sim + 0.1*freq + 0.08*rec
		if projHits > 0 {
			score += 0.22
		}
		if score < 0.3 {
			continue
		}
		var reasons []string
		if sim >= 0.3 {
			if simTitle >= simCom*0.9 {
				reasons = append(reasons, fmt.Sprintf("Titel ähnlich (%d %%)", int(math.Round(simTitle*100))))
			} else {
				reasons = append(reasons, "ähnlicher Tempo-Kommentar")
			}
		}
		if projHits > 0 {
			reasons = append(reasons, fmt.Sprintf("im selben Projekt %d× gebucht", projHits))
		} else if c.Count > 0 {
			reasons = append(reasons, fmt.Sprintf("%d× gebucht", c.Count))
		}
		if c.Last != "" && days < 30 {
			reasons = append(reasons, "kürzlich")
		}
		sum := c.Title
		if strings.EqualFold(sum, c.Key) {
			sum = ""
		}
		out = append(out, suggestion{Key: c.Key, Summary: sum, Score: int(math.Round(math.Min(1, score) * 100)), Reasons: reasons, Source: "historie", InstanceID: nzID(c.Instance)})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	if len(out) > 5 {
		out = out[:5]
	}
	return out
}

// mergeRemote = Node: Treffer aus Jira dazunehmen; schon bekannte bekommen +8 („auch in Jira gefunden“)
func mergeRemote(list, more []suggestion) []suggestion {
	for _, m := range more {
		found := false
		for i := range list {
			if list[i].Key == m.Key && (list[i].InstanceID == nil || m.InstanceID == nil || fmt.Sprint(list[i].InstanceID) == fmt.Sprint(m.InstanceID)) {
				if list[i].InstanceID == nil { // Jira-Treffer kennt die Instanz
					list[i].InstanceID, list[i].Instance = m.InstanceID, m.Instance
				}
				list[i].Score = min(99, max(list[i].Score, m.Score)+8)
				list[i].Reasons = append(list[i].Reasons, "auch in Jira gefunden")
				found = true
				break
			}
		}
		if !found {
			list = append(list, m)
		}
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].Score > list[j].Score })
	if len(list) > 5 {
		list = list[:5]
	}
	return list
}

/* ── Vorschläge (Datenbank, Jira, Plane) ────────────────────────── */

func (a *App) tempoCorpus(ctx context.Context, uid int64, today time.Time) ([]corpusItem, error) {
	by := map[string]*corpusItem{}
	add := func(k, title string, pid int64, comment, date string, inst int64) {
		k = upTrim(k)
		if !jiraKeyRE.MatchString(k) {
			return
		}
		ck := k + "|" + strconv.FormatInt(inst, 10)
		o := by[ck]
		if o == nil {
			o = &corpusItem{Key: k, Projects: map[int64]int{}, Instance: inst}
			by[ck] = o
		}
		if title != "" && (o.Title == "" || strings.EqualFold(o.Title, k)) {
			o.Title = title
		}
		if comment = jsTrim(comment); comment != "" && len(o.Comments) < 40 && !contains(o.Comments, comment) {
			o.Comments = append(o.Comments, comment)
		}
		if pid != 0 {
			o.Projects[pid]++
		}
		if date != "" {
			o.Count++
			if date > o.Last {
				o.Last = date
			}
		}
	}
	since := today.AddDate(0, 0, -400).Format("2006-01-02")
	rows, err := a.DB.Query(ctx, `SELECT COALESCE(t.jira_link, t.jira_origin, t.jira_ticket, ''), COALESCE(t.title,''), COALESCE(e.project_id,0), COALESCE(e.comment,''), e.date, COALESCE(t.jira_instance,0)
		FROM time_entries e JOIN tasks t ON t.id=e.task_id AND t.user_id=e.user_id
		WHERE e.user_id=$1 AND e.source IN ('tempo','tempo_csv') AND e.date >= $2 LIMIT 20000`, uid, since)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var k, title, comment, date string
		var pid, inst int64
		if rows.Scan(&k, &title, &pid, &comment, &date, &inst) == nil {
			add(k, title, pid, comment, date, inst)
		}
	}
	rows.Close()
	rows, err = a.DB.Query(ctx, `SELECT jira_link, COALESCE(title,''), COALESCE(project_id,0), COALESCE(jira_instance,0) FROM tasks
		WHERE user_id=$1 AND jira_link IS NOT NULL AND (jira_mine=1 OR ticket_system='jira') LIMIT 5000`, uid)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var k, title string
		var pid, inst int64
		if rows.Scan(&k, &title, &pid, &inst) == nil {
			add(k, title, pid, "", "", inst)
		}
	}
	rows.Close()
	rows, err = a.DB.Query(ctx, `SELECT jira_key, COALESCE(title,'') FROM ticket_map WHERE user_id=$1 AND status='mapped' LIMIT 5000`, uid)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var k, title string
		if rows.Scan(&k, &title) == nil {
			add(k, title, 0, "", "", 0)
		}
	}
	rows.Close()
	out := make([]corpusItem, 0, len(by))
	for _, v := range by {
		out = append(out, *v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

type remoteCached struct {
	at   time.Time
	list []suggestion
}

var remoteSuggestCache sync.Map // Person|Instanz|Wörter → remoteCached (5 min)

func (a *App) suggestRemote(ctx context.Context, uid int64, ti *tempoInst, text string) []suggestion {
	ws := []string{}
	seen := map[string]bool{}
	for _, w := range toks(text) {
		if !seen[w] && len(ws) < 4 {
			seen[w] = true
			ws = append(ws, w)
		}
	}
	if len(ws) == 0 {
		return nil
	}
	ck := fmt.Sprintf("%d|%d|%s", uid, ti.ID, strings.Join(ws, " "))
	if v, ok := remoteSuggestCache.Load(ck); ok && time.Since(v.(remoteCached).at) < 5*time.Minute {
		return v.(remoteCached).list
	}
	list, err := a.jiraSearch(ctx, uid, ti.C.jira(), `text ~ "`+jqlQuote(strings.Join(ws, " "))+`" ORDER BY updated DESC`, 8)
	if err != nil {
		return nil
	}
	var out []suggestion
	for _, i := range list {
		sum := asStr(jm(i["fields"])["summary"])
		sim := textSim(text, sum)
		out = append(out, suggestion{Key: asStr(i["key"]), Summary: sum, Score: int(math.Round(math.Min(1, 0.7*sim+0.1) * 100)), Source: "jira",
			Reasons: []string{"Jira-Suche in " + ti.Name, fmt.Sprintf("Titel ähnlich (%d %%)", int(math.Round(sim*100)))}, InstanceID: ti.ID, Instance: ti.Name})
	}
	dropExpired(&remoteSuggestCache, 5*time.Minute)
	remoteSuggestCache.Store(ck, remoteCached{time.Now(), out})
	return out
}

// suggestOn: Vorschläge nur, wenn Server an (module_suggest, Standard an) UND Person nicht vom Administrator gesperrt
// UND Person an (suggest_enabled, Standard an). Grund für den Hinweis bei „aus“.
func suggestOn(server string, blocked bool, user string) (bool, string) {
	return suggestOnFor("Ticket-Vorschläge", true, server, blocked, user)
}

// tracksTime: Konto erfasst Zeiten = nicht (lokales Konto UND nicht produktiv). LDAP/OIDC immer; Admin mit productive=true auch.
// Das technische Admin-/Testkonto (local, productive=false) bekommt keine Vorschläge und keinen Assistenten.
func tracksTime(kind string, productive bool) bool { return kind != "local" || productive }

// suggestOnFor: dieselben Stufen für jede „Light“-Vorschlagsart (Ticket, To-do, Nachtragen, „Jetzt vermutlich“, Assistent):
// Konto erfasst Zeiten · Servermodul · Admin-Sperre je Person · Einstellung der Person.
// Bewusst ein gemeinsamer Schalter: eine Person bzw. der Administrator schaltet „Vorschläge aus eigenen Buchungen“ als Ganzes ab.
func suggestOnFor(what string, tracking bool, server string, blocked bool, user string) (bool, string) {
	switch {
	case !tracking:
		return false, what + " gibt es nur für Konten, die Zeiten erfassen (technisches Konto)"
	case server == "0":
		return false, what + " sind auf dem Server abgeschaltet"
	case blocked:
		return false, what + " sind für dich vom Administrator abgeschaltet"
	case user == "0":
		return false, what + " sind in deinen Einstellungen abgeschaltet (suggest_enabled)"
	}
	return true, ""
}

func (a *App) suggestAllowed(ctx context.Context, uid int64) (bool, string) {
	return a.suggestAllowedFor(ctx, uid, "Ticket-Vorschläge")
}

func (a *App) suggestAllowedFor(ctx context.Context, uid int64, what string) (bool, string) {
	var blocked, productive bool
	var user, kind string
	if err := a.DB.QueryRow(ctx, `SELECT u.suggest_blocked, u.kind, u.productive, COALESCE((SELECT value FROM user_settings s WHERE s.user_id=u.id AND s.key='suggest_enabled'),'1')
		FROM users u WHERE u.id=$1`, uid).Scan(&blocked, &kind, &productive, &user); err != nil {
		return false, what + " gerade nicht verfügbar" // im Zweifel aus
	}
	if !tracksTime(kind, productive) { // vor allen weiteren Abfragen
		return suggestOnFor(what, false, "", false, "")
	}
	return suggestOnFor(what, true, a.conf(ctx, "module_suggest"), blocked, user)
}

// remoteSuggestions: Jira-Suche je Instanz – bei „aus“ kein einziger ausgehender Aufruf
func (a *App) remoteSuggestions(ctx context.Context, uid int64, on bool, insts []*tempoInst, text string, list []suggestion) []suggestion {
	if !on {
		return list
	}
	for _, ti := range insts {
		list = mergeRemote(list, a.suggestRemote(ctx, uid, ti, text))
	}
	return list
}

// tempoSuggestRoute: „Light“-Vorschlag (nur Textabgleich). Ein Client (z. B. Mac mit KI) kann eigene Vorschläge
// machen und übernimmt sie wie jede Zuordnung über POST …/target (key, ids, instance_id, source: "client").
func (a *App) tempoSuggestRoute(w http.ResponseWriter, r *http.Request, u User) {
	ctx := r.Context()
	if on, why := a.suggestAllowed(ctx, u.ID); !on { // vor jeder Abfrage: aus = keine Textabgleiche, keine Aufrufe
		writeJSON(w, http.StatusNotFound, map[string]any{"error": why, "suggest": false, "suggestions": map[string]any{}})
		return
	}
	var in body
	if !readJSON(w, r, &in) {
		return
	}
	ids := idList(in["ids"], 40)
	remote := true
	if b, ok := in["remote"].(bool); ok {
		remote = b
	}
	s, st, err := a.tempoSetup(ctx, u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	now := time.Now().In(a.Loc)
	corpus, err := a.tempoCorpus(ctx, u.ID, now)
	if err != nil {
		a.fail(w, err)
		return
	}
	lk, err := a.tempoLookups(ctx, u.ID, s)
	if err != nil {
		a.fail(w, err)
		return
	}
	items := []tempoItem{}
	if len(ids) > 0 {
		if items, err = a.tempoItems(ctx, u.ID, s, st, lk, "e.id = ANY($4)", ids); err != nil {
			a.fail(w, err)
			return
		}
	}
	keys := s.keys()
	var pc planeCfg
	if pcfg, err := a.planeCfg(ctx, u.ID); err == nil && pcfg.ok() {
		pc = pcfg
	}
	out := map[string]any{}
	byTask := map[string][]suggestion{}
	for _, it := range items {
		e := it.E
		tk := "e" + strconv.FormatInt(e.ID, 10)
		if e.TaskID != 0 {
			tk = "t" + strconv.FormatInt(e.TaskID, 10)
		}
		if l, ok := byTask[tk]; ok {
			out[strconv.FormatInt(e.ID, 10)] = l
			continue
		}
		text := strings.TrimSpace(e.TaskTitle + " " + e.Comment)
		list := suggestLocal(text, e.ProjectID, corpus, now)
		if remote && (len(list) == 0 || list[0].Score < 70) {
			list = a.remoteSuggestions(ctx, u.ID, true, s.usable(), text, list)
		}
		for i := range list { // Instanz je Vorschlag (gewählt oder über das Kürzel)
			if list[i].InstanceID == nil {
				id, problem := resolveTempoInstance(list[i].Key, "ticket", 0, keys)
				list[i].InstanceID, list[i].Problem = nzID(id), nz(problem)
			}
			if id, ok := list[i].InstanceID.(int64); ok && s.byID[id] != nil {
				list[i].Instance = s.byID[id].Name
			}
		}
		if remote && pc.ok() && len(list) < 5 && len([]rune(text)) >= 3 { // Plane-Tickets: zuordnen über POST /api/plane/ticket/{key}/task
			q := strings.Join(firstN(toks(text), 4), " ")
			if res, err := a.planeDo(ctx, u.ID, pc, "GET", pc.wsPath()+"/work-items/search/?search="+url.QueryEscape(q)+"&limit=3&workspace_search=true", nil, planeOpts{Interactive: true}); err == nil {
				for _, i := range listOf(res) {
					list = append(list, suggestion{Key: asStr(i["project__identifier"]) + "-" + asStr(i["sequence_id"]), Summary: asStr(i["name"]), Score: int(math.Round(textSim(text, asStr(i["name"])) * 60)),
						Source: "plane", Reasons: []string{"Plane-Suche – zuordnen über die Plane-Aufgabe, dann geht die Zeit aufs Übergangs-Ticket"}})
				}
			}
		}
		byTask[tk] = list
		out[strconv.FormatInt(e.ID, 10)] = list
	}
	instances := []map[string]any{}
	for _, ti := range s.usable() {
		instances = append(instances, map[string]any{"id": ti.ID, "name": ti.Name, "new_project": nz(ti.C.NewProject)})
	}
	writeJSON(w, http.StatusOK, map[string]any{"suggestions": out, "instances": instances})
}

func firstN(l []string, n int) []string {
	if len(l) > n {
		return l[:n]
	}
	return l
}

func idList(v any, max int) []int64 {
	var ids []int64
	if l, ok := v.([]any); ok {
		for _, x := range l {
			if n := int64(asFloat(x)); n > 0 && len(ids) < max {
				ids = append(ids, n)
			}
		}
	}
	return ids
}

/* ── Zuordnen und Anlegen ───────────────────────────────────────── */

type ticketEntry struct {
	ID, TaskID, ProjectID int64
	Date, PlaneWI         string
	Ticket, Title         string
	Comment, PushTarget   string
}

func (a *App) ticketEntries(ctx context.Context, uid int64, ids []int64) ([]ticketEntry, error) {
	rows, err := a.DB.Query(ctx, `SELECT e.id, COALESCE(e.task_id,0), COALESCE(e.project_id,0), e.date, COALESCE(t.plane_work_item_id,''), COALESCE(t.jira_ticket,''),
		COALESCE(t.title,''), COALESCE(e.comment,''), COALESCE(e.tempo_push_target,'')
		FROM time_entries e LEFT JOIN tasks t ON t.id=e.task_id AND t.user_id=e.user_id WHERE e.user_id=$1 AND e.id = ANY($2) ORDER BY e.start_time`, uid, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ticketEntry
	for rows.Next() {
		var t ticketEntry
		if err := rows.Scan(&t.ID, &t.TaskID, &t.ProjectID, &t.Date, &t.PlaneWI, &t.Ticket, &t.Title, &t.Comment, &t.PushTarget); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// ticketScope: „task“ nur, wenn alle Buchungen dieselbe Aufgabe haben; sonst „entry“
func ticketScope(want string, list []ticketEntry) (string, error) {
	if want == "" || want == "task" {
		var task int64
		for _, e := range list {
			if e.TaskID == 0 || (task != 0 && e.TaskID != task) {
				if want == "task" {
					return "", errUser("scope task: alle Buchungen brauchen dieselbe Aufgabe")
				}
				return "entry", nil
			}
			task = e.TaskID
		}
		return "task", nil
	}
	if want != "entry" && want != "project" {
		return "", errUser("scope: task, entry oder project")
	}
	return want, nil
}

// lockedConflict: Buchungen in geschlossenen/eingereichten/freigegebenen Monaten, deren Ziel sich änderte → erst Wiedereröffnen durch Personal
// key "" = jede Buchung im Umfang zählt (neues Ticket)
func (a *App) lockedConflict(ctx context.Context, q Queryer, uid int64, scope string, list []ticketEntry, key string) error {
	var where string
	var arg any
	switch scope {
	case "task":
		where, arg = "e.task_id = ANY($3) OR e.task_id IN (SELECT t2.id FROM tasks t1 JOIN tasks t2 ON t2.user_id=t1.user_id AND t2.plane_work_item_id=t1.plane_work_item_id WHERE t1.id = ANY($3) AND t1.user_id=$1)", taskIDs(list)
	case "project":
		ps := []int64{}
		for _, e := range list {
			if e.ProjectID != 0 {
				ps = append(ps, e.ProjectID)
			}
		}
		where, arg = "e.project_id = ANY($3)", ps
	default:
		ids := []int64{}
		for _, e := range list {
			ids = append(ids, e.ID)
		}
		where, arg = "e.id = ANY($3)", ids
	}
	var month string
	err := q.QueryRow(ctx, `SELECT substr(e.date,1,7) FROM time_entries e JOIN month_approvals ma ON ma.user_id=e.user_id AND ma.month=substr(e.date,1,7) AND ma.status IN `+lockedStatusSQL+`
		WHERE e.user_id=$1 AND ($2 = '' OR COALESCE(e.tempo_push_target,'') <> $2) AND COALESCE(e.source,'manual') NOT IN ('tempo','tempo_csv') AND (`+where+`) LIMIT 1`, uid, key, arg).Scan(&month)
	if err == nil {
		return errConflict("Monat " + month + " ist geschlossen, eingereicht oder freigegeben – Nachträge erst nach Wiedereröffnen durch Personal (oder nur einzelne Buchungen anderer Monate zuordnen)")
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	return err
}

func taskIDs(list []ticketEntry) []int64 {
	var out []int64
	for _, e := range list {
		if e.TaskID != 0 && !containsID(out, e.TaskID) {
			out = append(out, e.TaskID)
		}
	}
	return out
}

// applyTarget: Ticket (mit gewählter Instanz) an Aufgabe, Buchungen oder Projekt hängen
func (a *App) applyTarget(ctx context.Context, uid int64, scope string, list []ticketEntry, key string, inst int64) (int64, error) {
	var n int64
	err := a.inTx(ctx, func(tx pgx.Tx) error {
		// gemeinsame Sperre wie zp_month_locked: Einreichen/Freigeben warten, Prüfen und Schreiben sind eins
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock_shared($1)`, int64(7270030000000000)+uid); err != nil {
			return err
		}
		if err := a.lockedConflict(ctx, tx, uid, scope, list, key); err != nil {
			return err
		}
		switch scope {
		case "task": // gleiche Plane-Work-Items teilen das Gegenstück (Node P:448)
			tag, err := tx.Exec(ctx, `UPDATE tasks SET jira_link=$3, jira_instance=$4 WHERE user_id=$1 AND (id = ANY($2)
				OR (plane_work_item_id IS NOT NULL AND plane_work_item_id IN (SELECT plane_work_item_id FROM tasks WHERE user_id=$1 AND id = ANY($2) AND plane_work_item_id IS NOT NULL)))`,
				uid, taskIDs(list), key, inst)
			n = tag.RowsAffected()
			if err != nil {
				return err
			}
			_, err = tx.Exec(ctx, `UPDATE time_entries SET tempo_push_error=NULL WHERE user_id=$1 AND task_id = ANY($2) AND tempo_push_id IS NULL`, uid, taskIDs(list))
			return err
		case "project":
			ps := []int64{}
			for _, e := range list {
				if e.ProjectID != 0 && !containsID(ps, e.ProjectID) {
					ps = append(ps, e.ProjectID)
				}
			}
			tag, err := tx.Exec(ctx, `UPDATE projects SET tempo_issue=$3, tempo_issue_instance=$4 WHERE user_id=$1 AND id = ANY($2)`, uid, ps, key, inst)
			n = tag.RowsAffected()
			return err
		}
		ids := []int64{}
		for _, e := range list {
			ids = append(ids, e.ID)
		}
		tag, err := tx.Exec(ctx, `UPDATE time_entries SET tempo_target=$3, tempo_target_instance=$4, tempo_push_error=NULL WHERE user_id=$1 AND id = ANY($2)`, uid, ids, key, inst)
		n = tag.RowsAffected()
		return err
	})
	return n, err
}

// pickInstance: gewählte Instanz (muss nutzbar sein) oder eindeutig über das Kürzel
func pickInstance(s *tempoSetup, raw any, key string) (*tempoInst, error) {
	if id := int64(asFloat(raw)); id != 0 {
		ti := s.byID[id]
		if ti == nil || !ti.Enabled {
			return nil, errUser("Jira-Instanz unbekannt oder ausgeschaltet")
		}
		if ti.Problem != "" {
			return nil, errUser(ti.Problem)
		}
		return ti, nil
	}
	if key != "" {
		id, problem := resolveTempoInstance(key, "ticket", 0, s.keys())
		if problem != "" {
			return nil, errUser(problem + " – instance_id angeben")
		}
		if ti := s.byID[id]; ti != nil && ti.Problem == "" {
			return ti, nil
		} else if ti != nil {
			return nil, errUser(ti.Problem)
		}
	}
	u := s.usable()
	if len(u) == 1 {
		return u[0], nil
	}
	return nil, errUser("Mehrere Jira-Instanzen – instance_id angeben")
}

func (a *App) ticketFail(w http.ResponseWriter, err error) {
	var ue errUser
	var ce errConflict
	switch {
	case errors.As(err, &ue):
		badRequest(w, string(ue))
	case errors.As(err, &ce):
		writeJSON(w, http.StatusConflict, map[string]any{"error": string(ce), "code": "month_locked"})
	default:
		a.tempoFail(w, err)
	}
}

// tempoTargetRoute = Node POST …/target: vorhandenes Ticket zuordnen (scope task|entry|project, instance_id bei Mehrdeutigkeit)
func (a *App) tempoTargetRoute(w http.ResponseWriter, r *http.Request, u User) {
	var in body
	if !readJSON(w, r, &in) {
		return
	}
	key := upTrim(asStr(in["key"]))
	ids := idList(in["ids"], 500)
	if !jiraKeyRE.MatchString(key) {
		badRequest(w, "Ticket-Key im Format ABC-123")
		return
	}
	if len(ids) == 0 {
		badRequest(w, "ids fehlen")
		return
	}
	ctx := r.Context()
	s, _, err := a.tempoSetup(ctx, u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	ti, err := pickInstance(s, in["instance_id"], key)
	if err != nil {
		a.ticketFail(w, err)
		return
	}
	list, err := a.ticketEntries(ctx, u.ID, ids)
	if err != nil || len(list) == 0 {
		notFound(w)
		return
	}
	scope, err := ticketScope(asStr(in["scope"]), list)
	if err != nil {
		a.ticketFail(w, err)
		return
	}
	summary, err := a.jiraIssueSummary(ctx, u.ID, ti.C.jira(), key)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": key + " in „" + ti.Name + "“ nicht gefunden: " + err.Error()})
		return
	}
	n, err := a.applyTarget(ctx, u.ID, scope, list, key, ti.ID) // prüft die Monatssperre in derselben Transaktion
	if err != nil {
		var ce errConflict
		if errors.As(err, &ce) {
			a.ticketFail(w, err)
			return
		}
		a.fail(w, err) // ZP001 (Monat gesperrt) → 409
		return
	}
	src := "Person"
	if asStr(in["source"]) == "client" {
		src = "Vorschlag des Clients"
	}
	a.sysLog(ctx, u.ID, "info", "tempo", fmt.Sprintf("Tempo-Ziel %s (%s, %s) für %d zugeordnet (%s, %s)", key, summary, ti.Name, n, scope, src))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "key": key, "summary": summary, "instance_id": ti.ID, "scope": scope, "n": n})
}

// requestKeyOf: Auftragsschlüssel – vom Client (Doppelklick-Schutz) oder aus Instanz, Buchungen und Überschrift
func requestKeyOf(client string, inst int64, ids []int64, summary string) string {
	if c := strings.TrimSpace(client); c != "" {
		return truncRunes(c, 100)
	}
	s := append([]int64{}, ids...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	h := sha1.Sum([]byte(fmt.Sprintf("%d|%v|%s", inst, s, jsTrim(summary))))
	return "auto:" + hex.EncodeToString(h[:])[:24]
}

// tempoTicketRoute: neues Jira-Ticket NUR auf Bestätigung der Person anlegen und zuordnen (idempotent je Auftrag)
func (a *App) tempoTicketRoute(w http.ResponseWriter, r *http.Request, u User) {
	var in body
	if !readJSON(w, r, &in) {
		return
	}
	if in["confirm"] != true {
		badRequest(w, "Anlegen nur mit Bestätigung (confirm: true)")
		return
	}
	ids := idList(in["ids"], 500)
	if len(ids) == 0 {
		badRequest(w, "ids fehlen")
		return
	}
	// nicht mit dem Request abbrechen: ein angelegtes Ticket muss noch gespeichert werden
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 2*time.Minute)
	defer cancel()
	s, _, err := a.tempoSetup(ctx, u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	ti, err := pickInstance(s, in["instance_id"], "")
	if err != nil {
		a.ticketFail(w, err)
		return
	}
	if ti.C.NewProject == "" {
		badRequest(w, "Für „"+ti.Name+"“ ist kein Jira-Projekt für neue Tickets eingestellt (jira_new_project)")
		return
	}
	list, err := a.ticketEntries(ctx, u.ID, ids)
	if err != nil || len(list) == 0 {
		notFound(w)
		return
	}
	scope, err := ticketScope(asStr(in["scope"]), list)
	if err != nil || scope == "project" {
		a.ticketFail(w, errUser("scope: task oder entry"))
		return
	}
	summary := jsTrim(asStr(in["summary"]))
	if summary == "" { // Gegenstück zum Plane-Ticket: „[KEY] Titel“ (Node ensureJira), sonst Titel/Kommentar
		first := list[0]
		if k := upTrim(first.Ticket); first.PlaneWI != "" && jiraKeyRE.MatchString(k) {
			summary = jsTrim("[" + k + "] " + map[bool]string{true: "", false: first.Title}[strings.EqualFold(first.Title, k)])
		} else {
			summary = jsOr(jsTrim(first.Title), jsTrim(first.Comment))
		}
	}
	summary = jsSlice(jsCollapse(summary), 250)
	if summary == "" {
		badRequest(w, "Überschrift fehlt")
		return
	}
	if err := a.lockedConflict(ctx, a.DB, u.ID, scope, list, ""); err != nil { // vor dem Anlegen; endgültig in applyTarget
		a.ticketFail(w, err)
		return
	}
	rk := requestKeyOf(asStr(in["request_key"]), ti.ID, ids, summary)
	key, created, err := a.createTicketOnce(ctx, u.ID, ti, rk, summary, ticketSpec{})
	if err != nil {
		a.ticketFail(w, err)
		return
	}
	n, err := a.applyTarget(ctx, u.ID, scope, list, key, ti.ID)
	if err != nil {
		a.sysLog(ctx, u.ID, "error", "tempo", fmt.Sprintf("Jira-Ticket %s angelegt, Zuordnung nicht gespeichert (%v) – erneut zuordnen", key, err))
		var ce errConflict
		if errors.As(err, &ce) {
			a.ticketFail(w, err)
			return
		}
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "key": key, "created": created, "instance_id": ti.ID, "scope": scope, "n": n, "request_key": rk})
}

// ticketRequestTTL: so lange darf ein Auftrag „pending“ bleiben, danach ist er als fehlgeschlagen sichtbar
// (ein erneuter Versuch sucht trotzdem zuerst nach der Überschrift – kein zweites Ticket)
const ticketRequestTTL = 24 * time.Hour

// expireTicketRequests: hängende Aufträge nach der Frist als fehlgeschlagen kennzeichnen (uid 0 = alle)
func (a *App) expireTicketRequests(ctx context.Context, uid int64) {
	_, _ = a.DB.Exec(ctx, `UPDATE tempo_ticket_requests SET status='failed', updated_at=now(),
		error=COALESCE(error,'') || CASE WHEN COALESCE(error,'')='' THEN '' ELSE ' · ' END || 'Antwort unklar – bitte in Jira prüfen, ob das Ticket schon existiert'
		WHERE status='pending' AND updated_at < now() - make_interval(secs => $2) AND ($1 = 0 OR user_id=$1)`, uid, ticketRequestTTL.Seconds())
}

// createTicketOnce: ein Auftrag = höchstens ein Ticket. Läuft derselbe Auftrag schon → 409; ist er fertig → derselbe Key.
// Hängt ein Auftrag (Absturz), wird nach 10 min erst per Überschrift gesucht und nur dann neu angelegt.
// ticketSpec: wo und wie angelegt wird (Projekt, Vorgangstyp, Beschreibung); leer = Werte der Instanz
type ticketSpec struct{ Project, IssueType, Description string }

func (a *App) createTicketOnce(ctx context.Context, uid int64, ti *tempoInst, rk, summary string, spec ticketSpec) (string, bool, error) {
	a.expireTicketRequests(ctx, uid)
	tag, err := a.DB.Exec(ctx, `INSERT INTO tempo_ticket_requests(user_id,request_key,instance_id,summary) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, uid, rk, ti.ID, summary)
	if err != nil {
		return "", false, err
	}
	search := false
	if tag.RowsAffected() == 0 {
		var status, key string
		var inst int64
		if err := a.DB.QueryRow(ctx, `SELECT status, COALESCE(issue_key,''), COALESCE(instance_id,0) FROM tempo_ticket_requests WHERE user_id=$1 AND request_key=$2`, uid, rk).Scan(&status, &key, &inst); err != nil {
			return "", false, err
		}
		if inst != 0 && inst != ti.ID { // Auftrag gehört zu einer anderen Instanz: nie dort suchen und hier anlegen
			return "", false, errConflict("Dieser Auftrag gehört zu einer anderen Jira-Instanz")
		}
		if status == "done" && key != "" {
			return key, false, nil
		}
		// Auftrag übernehmen: fehlgeschlagen oder seit 10 min hängend
		tg, err := a.DB.Exec(ctx, `UPDATE tempo_ticket_requests SET status='pending', error=NULL, updated_at=now()
			WHERE user_id=$1 AND request_key=$2 AND (status='failed' OR (status='pending' AND updated_at < now() - interval '10 minutes'))`, uid, rk)
		if err != nil {
			return "", false, err
		}
		if tg.RowsAffected() == 0 {
			return "", false, errConflict("Das Ticket wird gerade angelegt – bitte kurz warten")
		}
		search = true // übernommener Auftrag (hängend, abgelaufen oder fehlgeschlagen): vielleicht schon angelegt → erst suchen
	}
	// dieselbe Überschrift für Suche UND Anlegen: die beim ersten Mal gespeicherte (nie überschrieben – ein unklar
	// gebliebenes Anlegen fände die Suche sonst nicht); eine andere Überschrift braucht einen neuen Auftrag
	_ = a.DB.QueryRow(ctx, `SELECT summary FROM tempo_ticket_requests WHERE user_id=$1 AND request_key=$2`, uid, rk).Scan(&summary)
	finish := func(key, errText string) {
		st := "done"
		if errText != "" {
			st = "failed"
		}
		if _, err := a.DB.Exec(ctx, `UPDATE tempo_ticket_requests SET status=$3, issue_key=NULLIF($4,''), error=NULLIF($5,''), updated_at=now() WHERE user_id=$1 AND request_key=$2`,
			uid, rk, st, key, truncRunes(errText, 300)); err != nil && key != "" {
			a.sysLog(ctx, uid, "error", "tempo", fmt.Sprintf("Jira-Ticket %s angelegt, Auftrag nicht gespeichert (%v)", key, err))
		}
	}
	c := ti.C
	project := jsOr(strings.ToUpper(strings.TrimSpace(spec.Project)), c.NewProject)
	if project == "" {
		return "", false, errUser("Jira-Projekt fehlt")
	}
	if search { // erst suchen; scheitert die Suche, NICHT anlegen (Auftrag bleibt offen, später erneut)
		key, err := a.jiraFindSummary(ctx, uid, c.jira(), project, summary)
		if err != nil {
			_, _ = a.DB.Exec(ctx, `UPDATE tempo_ticket_requests SET error=$3 WHERE user_id=$1 AND request_key=$2`, uid, rk, truncRunes("Suche fehlgeschlagen: "+err.Error(), 300))
			return "", false, err
		}
		if key != "" {
			finish(key, "")
			return key, false, nil
		}
	}
	types, _ := a.jiraIssueTypes(ctx, uid, c.jira(), project)
	fields := map[string]any{"project": map[string]any{"key": project}, "summary": summary,
		"issuetype":   map[string]any{"name": pickIssueType(types, jsOr(strings.TrimSpace(spec.IssueType), c.IssueType))},
		"description": jsOr(strings.TrimSpace(spec.Description), "Angelegt von Zeitprotokoll für die Zeitbuchung in Tempo (auf Bestätigung der Person).")}
	key, err := a.jiraCreateIssue(ctx, uid, c.jira(), fields, c.Worker, c.Account)
	if err != nil {
		if jiraStatus(err) == 0 || jiraStatus(err) >= 500 { // unklar, ob angelegt: hängen lassen → späterer Versuch sucht zuerst
			a.sysLog(ctx, uid, "warn", "tempo", "Jira-Ticket anlegen: Antwort unklar ("+err.Error()+") – erneuter Versuch sucht zuerst nach der Überschrift")
			return "", false, err
		}
		finish("", err.Error())
		return "", false, err
	}
	finish(key, "")
	a.sysLog(ctx, uid, "success", "tempo", fmt.Sprintf("Jira-Ticket %s „%s“ in %s angelegt (bestätigt)", key, summary, ti.Name))
	return key, true, nil
}

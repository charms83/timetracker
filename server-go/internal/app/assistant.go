package app

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"
)

/* Assistent – Verhalten wie backend/assistant.js: regelbasiert (kein KI-Modell), nur Datenbank-Abfragen, kein Netz.
   Daher als „Light“-Variante portiert. Abschaltbar wie alle Vorschläge (Konto erfasst Zeiten · Servermodul · Admin je Person ·
   suggest_enabled) und zusätzlich mit dem eigenen Schalter der Person (assistant_enabled, wie Node).
   Abgeschaltet: GET /api/assistant liefert nur {enabled:false, reason}, /tasks eine leere Liste – ohne weitere Abfragen.
   Bewusst anders (Mehrbenutzer): Plane-Adresse und Jira-Instanzen legt der Administrator fest; „konfiguriert“ heißt hier
   eigener Plane-Schlüssel bzw. nutzbare Jira-Instanz mit eigenem Token. TLS-Pins gibt es im Server nicht (pins = 0).
   Nicht portierte Aufgaben (Daten fehlen im Server): tempo_sync, duplicates, cleanup, tempo_noticket, selfcheck (Betrieb). */

var ruleOfMode = map[string]string{"jira": "tempo", "bridge": "bridge", "plane": "plane"}

var assistantCache sync.Map // Person → assistantCached (2 Minuten, wie Node)

type assistantCached struct {
	at   time.Time
	data []hint
}

func (a *App) routesAssistant(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/assistant", a.auth(a.assistantGet))
	mux.HandleFunc("PUT /api/assistant", a.auth(a.assistantPut))
	mux.HandleFunc("GET /api/assistant/tasks", a.auth(a.assistantTasks))
	mux.HandleFunc("POST /api/assistant/reset", a.auth(a.assistantReset))
}

// assistantOn: Vorschlags-Schalter (vier Stufen) UND eigener Schalter der Person
func (a *App) assistantOn(ctx context.Context, uid int64, st map[string]string) (bool, string) {
	if on, why := a.suggestAllowedFor(ctx, uid, "Assistent und Hinweise"); !on {
		return false, why
	}
	if st["assistant_enabled"] == "0" {
		return false, "Assistent ist ausgeschaltet (Einstellungen › System)"
	}
	return true, ""
}

type asstFacts struct {
	m       map[string]any
	mode    string
	plane   bool
	jiraOn  bool
	jira    bool
	pending int
	scan    bool
	gone    []string
	onlyJ   []string
	inPlane int
	active  int
	push    bool
	newProj string
	targets bool
}

func (a *App) assistantFacts(ctx context.Context, uid int64, st map[string]string) (*asstFacts, error) {
	f := &asstFacts{}
	var done map[string]any
	_ = json.Unmarshal([]byte(st["assistant_done"]), &done)
	if done == nil {
		done = map[string]any{}
	}
	pc, err := a.planeCfg(ctx, uid)
	f.plane = err == nil && pc.ok()
	f.jiraOn = jiraOn(st) && a.tempoModuleOn(ctx)
	var user, account, since, issue any = nz(st["jira_username"]), "", "", ""
	if s, _, err := a.tempoSetup(ctx, uid); err == nil {
		if us := s.usable(); len(us) > 0 {
			f.jira = true
			c := us[0].C // bei mehreren Instanzen: die erste nutzbare (Anzeige)
			account, since, issue, f.newProj = c.Account, c.Since, c.Issue, c.NewProject
			if user == nil {
				user = nz(c.Worker)
			}
		}
	}
	mode := st["assistant_mode"]
	if mode == "" {
		switch {
		case f.plane && f.jiraOn && f.jira:
			mode = "bridge"
		case f.plane && !f.jiraOn:
			mode = "plane"
		case f.jira:
			mode = "jira"
		}
	}
	f.mode = mode
	n := func(sql string, args ...any) int {
		var c int
		_ = a.DB.QueryRow(ctx, sql, append([]any{uid}, args...)...).Scan(&c)
		return c
	}
	active, err := a.maps(ctx, `SELECT id, name FROM projects WHERE user_id=$1 AND COALESCE(status,'')<>'archived' AND COALESCE(muted,0)=0 AND merged_into IS NULL ORDER BY id`, uid)
	if err != nil {
		return nil, err
	}
	withJira := map[int64]bool{}
	rows, err := a.DB.Query(ctx, `SELECT project_id FROM project_sources WHERE user_id=$1 AND system='jira' AND status='linked' AND project_id IS NOT NULL`, uid)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			withJira[id] = true
		}
	}
	rows.Close()
	noSource := []string{}
	f.onlyJ = []string{}
	for _, p := range active {
		id, _ := p["id"].(int64)
		ip := a.inPlane(ctx, uid, id)
		switch {
		case ip:
			f.inPlane++
		case withJira[id]:
			f.onlyJ = append(f.onlyJ, asStr(p["name"]))
		default:
			noSource = append(noSource, asStr(p["name"]))
		}
	}
	f.active = len(active)
	gone, err := a.maps(ctx, `SELECT p.name FROM projects p WHERE p.user_id=$1 AND COALESCE(p.status,'')<>'archived' AND COALESCE(p.gone_ack,0)=0
		AND EXISTS (SELECT 1 FROM project_sources s WHERE s.user_id=p.user_id AND s.project_id=p.id AND s.status='linked')
		AND NOT EXISTS (SELECT 1 FROM project_sources s WHERE s.user_id=p.user_id AND s.project_id=p.id AND s.status='linked' AND s.gone=0) ORDER BY p.id`, uid)
	if err != nil {
		return nil, err
	}
	f.gone = []string{}
	for _, g := range gone {
		f.gone = append(f.gone, asStr(g["name"]))
	}
	f.pending = n(`SELECT COUNT(*) FROM project_sources WHERE user_id=$1 AND status='new' AND gone=0`)
	f.scan = st["sources_last_scan"] != ""
	tg := dayTargets(st)
	f.targets = tg[1] > 0 || tg[2] > 0 || tg[3] > 0 || tg[4] > 0 || tg[5] > 0
	f.push = st["tempo_push_enabled"] == "1" || st["tempo_push_enabled"] == "true"
	off := func(k string) bool { return st[k] != "0" }
	f.m = map[string]any{
		"enabled": true, "done": done, "mode": nz(mode), "mode_set": st["assistant_mode"] != "", "started": st["assistant_started"] != "",
		"plane": map[string]any{"configured": f.plane},
		"jira":  map[string]any{"enabled": f.jiraOn, "configured": f.jira, "user": user, "pins": 0},
		"sources": map[string]any{"last_scan": nz(st["sources_last_scan"]), "pending": f.pending,
			"ignored":      n(`SELECT COUNT(*) FROM project_sources WHERE user_id=$1 AND status='ignored'`),
			"linked_plane": n(`SELECT COUNT(*) FROM project_sources WHERE user_id=$1 AND system='plane' AND status='linked'`),
			"linked_jira":  n(`SELECT COUNT(*) FROM project_sources WHERE user_id=$1 AND system='jira' AND status='linked'`)},
		"projects": map[string]any{"active": f.active, "in_plane": f.inPlane, "only_jira": f.onlyJ, "no_source": noSource,
			"muted": n(`SELECT COUNT(*) FROM projects WHERE user_id=$1 AND COALESCE(muted,0)=1 AND merged_into IS NULL`), "gone_open": f.gone},
		"rule_default": jsOr(st["output_rule_default"], "bridge"),
		"tempo": map[string]any{"push": f.push, "mode": map[bool]string{true: "auto", false: "manual"}[st["tempo_push_mode"] == "auto"],
			"account": account, "since": since, "new_project": f.newProj, "issue": issue, "my_tickets": off("jira_my_tickets")},
		// Erkennung von Terminen/SSH bleibt im Client; hier stehen nur dessen Einstellungen
		"meetings": map[string]any{"detect": off("meeting_detect"), "calendar": off("meeting_calendar"), "browser": off("meeting_browser"), "ask": off("meeting_ask")},
		"ssh":      map[string]any{"enabled": off("ssh_watch_enabled")},
		"worktime": map[string]any{"targets": f.targets, "start": st["tracking_start_date"], "vacation": st["vacation_days_per_year"]},
		"entries":  n(`SELECT COUNT(*) FROM time_entries WHERE user_id=$1`),
	}
	return f, nil
}

type openPoint struct {
	Step  string `json:"step"`
	Level string `json:"level"`
	Text  string `json:"text"`
}

// openPoints: was der Assistent auch nach der Einrichtung anmahnt (reine Logik, getestet)
func openPoints(f *asstFacts) []openPoint {
	o := []openPoint{}
	m := f.mode
	add := func(step, level, text string) { o = append(o, openPoint{step, level, text}) }
	if m == "" {
		add("mode", "todo", "Arbeitsweise wählen: nur Jira, Übergang Plane + Jira oder nur Plane")
	}
	if (m == "bridge" || m == "plane") && !f.plane {
		add("plane", "todo", "Plane-Zugang fehlt")
	}
	if (m == "bridge" || m == "jira") && !f.jira {
		add("jira", "todo", "Jira-/Tempo-Zugang fehlt")
	}
	if m == "plane" && f.jiraOn {
		add("mode", "info", "Betriebsart „nur Plane“, aber Jira ist noch eingeschaltet")
	}
	if f.pending == 1 {
		add("projects", "todo", "1 neues Projekt aus Plane/Jira wartet auf Entscheidung")
	} else if f.pending > 1 {
		add("projects", "todo", fmt.Sprintf("%d neue Projekte aus Plane/Jira warten auf Entscheidung", f.pending))
	}
	if len(f.gone) > 0 {
		t := strings.Join(f.gone[:min(3, len(f.gone))], ", ")
		if len(f.gone) > 3 {
			t += " …"
		}
		add("projects", "warn", "Quelle abgeschlossen: "+t)
	}
	if !f.scan && (f.plane || f.jira) {
		add("projects", "todo", "Projekte noch nie eingelesen")
	}
	if (m == "bridge" || m == "jira") && f.jira && !f.push {
		add("tempo", "todo", "Tempo-Übertragung ist aus – Zeiten kommen nicht in Tempo an")
	}
	if (m == "bridge" || m == "jira") && f.push && f.newProj == "" {
		add("tempo", "todo", "Jira-Projekt für Übergangs- und Sammeltickets fehlt – „Automatisch einrichten“ hilft")
	}
	if m == "bridge" && len(f.onlyJ) > 0 {
		add("migration", "info", fmt.Sprintf("%d %s noch nicht in Plane (Zeit geht nur nach Jira/Tempo)", len(f.onlyJ), map[bool]string{true: "Projekt ist", false: "Projekte sind"}[len(f.onlyJ) == 1]))
	}
	if m == "bridge" && f.active > 0 && len(f.onlyJ) == 0 && f.inPlane == f.active {
		add("migration", "info", "Alle aktiven Projekte sind in Plane – Umstieg auf „nur Plane“ möglich")
	}
	if !f.targets {
		add("worktime", "todo", "Sollstunden fehlen (Saldo stimmt sonst nicht)")
	}
	return o
}

func (a *App) assistantReply(w http.ResponseWriter, ctx context.Context, uid int64) {
	st, err := a.userSettings(ctx, uid)
	if err != nil {
		a.fail(w, err)
		return
	}
	if on, why := a.assistantOn(ctx, uid, st); !on {
		writeJSON(w, http.StatusOK, assistantOff(why)) // volles Gerüst ohne Abfragen: die Oberfläche greift auf alle Felder zu
		return
	}
	f, err := a.assistantFacts(ctx, uid, st)
	if err != nil {
		a.fail(w, err)
		return
	}
	f.m["open"] = openPoints(f)
	writeJSON(w, http.StatusOK, f.m)
}

// assistantOff: Antwort bei abgeschaltetem Assistenten – Form wie facts(), Werte leer; started=true, damit die Oberfläche nichts nachschreibt
func assistantOff(why string) map[string]any {
	return map[string]any{
		"enabled": false, "reason": why, "done": map[string]any{}, "mode": nil, "mode_set": false, "started": true, "open": []any{},
		"plane":        map[string]any{"configured": false},
		"jira":         map[string]any{"enabled": false, "configured": false, "user": nil, "pins": 0},
		"sources":      map[string]any{"last_scan": nil, "pending": 0, "ignored": 0, "linked_plane": 0, "linked_jira": 0},
		"projects":     map[string]any{"active": 0, "in_plane": 0, "only_jira": []any{}, "no_source": []any{}, "muted": 0, "gone_open": []any{}},
		"rule_default": "bridge",
		"tempo":        map[string]any{"push": false, "mode": "manual", "account": "", "since": "", "new_project": "", "issue": "", "my_tickets": false},
		"meetings":     map[string]any{"detect": false, "calendar": false, "browser": false, "ask": false},
		"ssh":          map[string]any{"enabled": false},
		"worktime":     map[string]any{"targets": false, "start": "", "vacation": ""},
		"entries":      0,
	}
}

func (a *App) assistantGet(w http.ResponseWriter, r *http.Request, u User) {
	a.assistantReply(w, r.Context(), u.ID)
}

// assistantPut: {enabled?, done?:{step:true|false}, mode?, started?} – mode setzt die Standard-Regel gleich mit
func (a *App) assistantPut(w http.ResponseWriter, r *http.Request, u User) {
	var in body
	if !readJSON(w, r, &in) {
		return
	}
	ctx := r.Context()
	if v, has := in["mode"]; has {
		if ruleOfMode[asStr(v)] == "" {
			badRequest(w, "Unbekannte Betriebsart")
			return
		}
	}
	d, _ := in["done"].(map[string]any)
	if len(d) > 50 {
		badRequest(w, "Zu viele Schritte")
		return
	}
	set := func(k, v string) bool {
		if err := a.setUserSetting(ctx, a.DB, u.ID, k, v); err != nil {
			a.fail(w, err)
			return false
		}
		return true
	}
	if v, has := in["enabled"]; has { // eigener Schalter der Person; Server-/Admin-Sperre bleibt davon unberührt
		if !set("assistant_enabled", map[bool]string{true: "1", false: "0"}[truthy(v)]) {
			return
		}
		a.sysLog(ctx, u.ID, "info", "assistent", map[bool]string{true: "Assistent eingeschaltet", false: "Assistent ausgeschaltet"}[truthy(v)])
	}
	if truthy(in["started"]) && !set("assistant_started", time.Now().UTC().Format(isoMillis)) {
		return
	}
	if d != nil {
		st, err := a.userSettings(ctx, u.ID)
		if err != nil {
			a.fail(w, err)
			return
		}
		var cur map[string]any
		_ = json.Unmarshal([]byte(st["assistant_done"]), &cur)
		if cur == nil {
			cur = map[string]any{}
		}
		for k, v := range d {
			if !settingKeyRE.MatchString(k) {
				continue
			}
			if truthy(v) {
				cur[k] = time.Now().UTC().Format(isoMillis)
			} else {
				delete(cur, k)
			}
		}
		b, _ := json.Marshal(cur)
		if !set("assistant_done", string(b)) {
			return
		}
	}
	if v, has := in["mode"]; has {
		m := asStr(v)
		if !set("assistant_mode", m) || !set("output_rule_default", ruleOfMode[m]) {
			return
		}
		if m == "plane" {
			if !set("jira_enabled", "0") || !set("tempo_push_enabled", "0") {
				return
			}
		} else if !set("jira_enabled", "1") {
			return
		}
		a.sysLog(ctx, u.ID, "info", "assistent", "Betriebsart: "+m+" (Standard-Regel "+ruleOfMode[m]+")")
	}
	assistantCache.Delete(u.ID)
	a.assistantReply(w, ctx, u.ID)
}

func (a *App) assistantReset(w http.ResponseWriter, r *http.Request, u User) {
	ctx := r.Context()
	if err := a.setUserSetting(ctx, a.DB, u.ID, "assistant_done", "{}"); err != nil {
		a.fail(w, err)
		return
	}
	if _, err := a.DB.Exec(ctx, `DELETE FROM user_settings WHERE user_id=$1 AND key='assistant_started'`, u.ID); err != nil {
		a.fail(w, err)
		return
	}
	assistantCache.Delete(u.ID)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// monthEndHint: Aufgabe „monthend“ aus dem Monatsende-Stand (wie tasks() in assistant.js)
func monthEndHint(me *monthEnd) *hint {
	fh := func(h float64) string { return fmtDE(math.Round(h*4)/4) + " h" }
	pl := func(n int, one, many string) string {
		if n == 1 {
			return one
		}
		return many
	}
	bal, _ := me.BalanceH.(float64)
	switch {
	case me.Prev != nil:
		n := len(me.Prev.Days)
		return &hint{"monthend", "warn", me.Prev.MonthName + " noch nicht vollständig",
			fmt.Sprintf("an %d %s fehlen %s – jetzt nachtragen, dann den Monat einreichen", n, pl(n, "Tag", "Tagen"), fh(me.Prev.MissingH)), n}
	case me.Remind:
		n := len(me.OpenDays)
		level := "info"
		if n > 0 && !me.Optional {
			level = "warn"
		}
		lw := asStr(me.LastWorkday)
		title := fmt.Sprintf("Monatsende %s: noch %d %s (bis %s)", me.MonthName, me.WorkdaysLeft, pl(me.WorkdaysLeft, "Arbeitstag", "Arbeitstage"), ddmm(lw))
		text := "bisher alles eingetragen ✓ – bis zum letzten Arbeitstag weiter buchen"
		if n > 0 {
			text = fmt.Sprintf("Zeiten eintragen – an %d %s fehlen %s", n, pl(n, "Tag", "Tagen"), fh(me.MissingH))
			if me.Optional {
				text += " · Konto +" + fh(bal) + ", nur ein Hinweis"
			}
		}
		return &hint{"monthend", level, title, text, max(n, 1)}
	case len(me.OpenDays) > 0:
		n := len(me.OpenDays)
		text := fmt.Sprintf("an %d %s fehlen %s", n, pl(n, "Tag", "Tagen"), fh(me.MissingH))
		if me.Optional {
			text += " – Konto steht bei +" + fh(bal) + ", nur ein Hinweis"
		}
		return &hint{"monthend", "info", "Zeiten nachtragen", text, n}
	}
	return nil
}

// assistantTasks: Pflege-Aufgaben – nur Datenbank, kein Netz, zwischengespeichert (2 Minuten, ?fresh=1 rechnet neu)
func (a *App) assistantTasks(w http.ResponseWriter, r *http.Request, u User) {
	ctx := r.Context()
	st, err := a.userSettings(ctx, u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	if on, _ := a.assistantOn(ctx, u.ID, st); !on {
		assistantCache.Delete(u.ID)
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	if v, ok := assistantCache.Load(u.ID); ok && r.URL.Query().Get("fresh") != "1" && time.Since(v.(assistantCached).at) < 2*time.Minute {
		writeJSON(w, http.StatusOK, v.(assistantCached).data)
		return
	}
	out := []hint{}
	add := func(h hint) { out = append(out, h) }
	n := func(sql string, args ...any) int {
		var c int
		_ = a.DB.QueryRow(ctx, sql, append([]any{u.ID}, args...)...).Scan(&c)
		return c
	}
	since := st["tempo_push_since"]
	if !isoDateRE.MatchString(since) {
		since = a.today()[:8] + "01"
	}
	// Abwesenheiten: gespeicherte Hinweise / in Tempo fehlt
	if hs, err := a.maps(ctx, `SELECT title, date_from FROM absence_hints WHERE user_id=$1 AND status='new' ORDER BY date_from`, u.ID); err == nil && len(hs) > 0 {
		var l []string
		for _, h := range hs[:min(3, len(hs))] {
			l = append(l, asStr(h["title"])+" "+ddmm(asStr(h["date_from"])))
		}
		add(hint{"absence_hints", "todo", "Urlaub/Abwesenheit erkannt – übernehmen?", strings.Join(l, ", "), len(hs)})
	}
	if mt, err := a.missingInTempo(r, u); err == nil && len(mt) > 0 {
		var l []string
		for _, x := range mt[:min(3, len(mt))] {
			l = append(l, ddmm(asStr(x["from"]))+"–"+ddmm(asStr(x["to"])))
		}
		add(hint{"absence_tempo", "info", "Urlaub fehlt in Tempo", strings.Join(l, ", ") + " – dort auf dem Abwesenheitsprojekt eintragen", len(mt)})
	}
	if ph, err := a.planningHints(ctx, u.ID); err == nil {
		out = append(out, ph...)
	}
	if me, err := a.monthEndStatus(ctx, u.ID, true); err == nil {
		if h := monthEndHint(me); h != nil {
			add(*h)
		}
	}
	if c := n(`SELECT COUNT(*) FROM project_sources WHERE user_id=$1 AND status='new' AND gone=0`); c > 0 {
		add(hint{"pending", "todo", "Neue Projekte aus Plane/Jira", "übernehmen oder stumm schalten", c})
	}
	if c := n(`SELECT COUNT(*) FROM time_entries WHERE user_id=$1 AND check_flags IS NOT NULL AND check_flags<>'overlap' AND check_note IS NULL AND date>=$2`, since); c > 0 {
		add(hint{"review", "warn", "Buchungen bitte prüfen", "fallen aus dem Rahmen (z. B. über 10 h, Urlaubstag)", c})
	}
	if sd, ok := parseDay(since); ok {
		days := max(1, min(90, int(math.Ceil(time.Since(sd).Hours()/24))+1))
		if ov, err := a.overlapPairs(ctx, u.ID, days); err == nil && len(ov) > 0 {
			add(hint{"overlaps", "warn", "Überschneidende Buchungen", "vermutlich doppelt gebucht", len(ov)})
		}
	}
	if c := n(`SELECT COUNT(*) FROM time_entries WHERE user_id=$1 AND tempo_push_error IS NOT NULL AND tempo_push_id IS NULL AND date>=$2`, since); c > 0 {
		add(hint{"tempo_errors", "warn", "Fehler bei der Übertragung nach Tempo", "unter Übertragen ansehen", c})
	}
	if f, err := a.assistantFacts(ctx, u.ID, st); err == nil {
		for _, o := range openPoints(f) {
			if o.Step != "projects" {
				add(hint{"setup:" + o.Step, o.Level, o.Text, "", 1})
			}
		}
	}
	assistantCache.Store(u.ID, assistantCached{time.Now(), out})
	writeJSON(w, http.StatusOK, out)
}

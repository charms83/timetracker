package app

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

/* Tempo-Ausgang, Kern (wie backend/tempo-push.js targetOf/describe/plan/statusOf): reine Funktionen ohne
   Datenbank und Netz. Jede abgeschlossene lokale Buchung = genau EIN Tempo-Worklog. Texte, Kürzung und Hash
   sind JavaScript-kompatibel gehalten (Leerraum wie \s, Kürzung in UTF-16-Einheiten, JSON wie JSON.stringify),
   damit vom Node-System übernommene tempo_push_hash-Werte gleich bleiben (keine Massen-„geändert“).
   Ausführung mit Datenbank: tempo_run.go. */

var (
	jiraKeyRE      = regexp.MustCompile(`^[A-Z][A-Z0-9_]*-[0-9]+$`)
	tempoLockRE    = regexp.MustCompile(`(?i)period|periode|zeitraum|gesperrt|locked|approved|genehmigt|submitted|eingereicht|frozen`)
	http4xxRE      = regexp.MustCompile(`HTTP 4[0-9][0-9]`)
	approvalLockRE = regexp.MustCompile(`approved|waiting|submitted`)
	originErrRE    = regexp.MustCompile(`(?i)originTaskId|issue not found`)
	accountErrRE   = regexp.MustCompile(`(?i)account`)
	isoDateRE      = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}$`)
)

const (
	claimPrefix = "pending:" // Anspruch auf eine Buchung während des Anlegens (tempo_push_id)
	// Übergangs-Ticket legt der Push selbst an; andere „NEU:…“ bleiben „needs“ bis zur Bestätigung
	tempoCanCreate = true
	// Marker in tempo_dup nach unterbrochenem Anlegen: bleibt stehen, bis die Person „trotzdem“ oder „ausnehmen“ wählt
	interruptedPrefix = "unterbrochen:"
	interruptedDup    = interruptedPrefix + " Übertragung abgebrochen – in Tempo prüfen, ob der Worklog schon da ist"
)

type tempoCfg struct {
	Base, BaseFrom, BaseErr, Token string
	Worker, WorkerKey              string
	Enabled                        bool
	Mode, Issue, Since, NewProject string
	Account, IssueType, PlaneMode  string
	PlaneURL                       string
	AccountAttrID                  int
	JiraKeys                       map[string]bool
}

func (c tempoCfg) jira() jiraCfg { return jiraCfg{Base: c.Base, Token: c.Token} }

// tempoEntry: eine lokale Buchung mit Aufgabe/Projekt (Felder wie die Node-Abfrage rows())
type tempoEntry struct {
	ID                                       int64
	Date, StartTime, EndTime                 string
	Duration                                 int64
	Comment                                  string
	TaskID, ProjectID                        int64
	Ticket, TaskTitle, ProjectName           string
	ProjIssue, ProjAccount                   string
	JiraLink, JiraOrigin, PlaneWI, TicketSys string
	TempoTarget, TempoAccount                string
	PushID, PushHash, PushTarget, PushErr    string
	SyncedAt, Dup                            string
	PushInstance                             int64 // Jira-Instanz des vorhandenen Worklogs (0 = unbekannt)
	TargetInst, TaskInst, ProjIssueInst      int64 // von der Person gewählte Instanz für tempo_target / jira_link / tempo_issue
	Skip                                     bool
	CheckFlags, CheckNote                    string
	AllowsTempo, AllowsPlane                 bool
}

// tempoLookup: Übergangs-Tickets je Monat und Zuordnung Plane-Key → Jira-Key (ticket_map, status mapped)
type tempoLookup struct {
	MonthTickets map[string]string
	Mapped       map[string]string
	MonthInst    map[string]int64 // Instanz des Übergangs-Tickets je Monat
	MonthAmbig   map[string]bool  // Übergangs-Tickets in mehreren Instanzen → nicht eindeutig
}

// instKeys: Jira-Projektkürzel einer Instanz (je Person)
type instKeys struct {
	ID         int64
	Name       string
	Keys       map[string]bool
	NewProject string // Jira-Projekt für neue Tickets (Übergangs-Ticket)
	Usable     bool
}

// resolveTempoInstance: zu welcher Jira-Instanz gehört das Ziel? Nur eindeutig über das Projektkürzel (bzw. das
// Übergangs-Ticket des Monats) – passt keine oder mehrere, gibt es einen Hinweis statt einer Vermutung.
func resolveTempoInstance(target, kind string, monthInst int64, insts []instKeys) (int64, string) {
	if target == "" || (strings.HasPrefix(target, "NEU:") && kind != "create-month") {
		return 0, ""
	}
	if kind == "month" {
		if monthInst == 0 {
			return 0, "Übergangs-Ticket " + target + " ist keiner Jira-Instanz zugeordnet"
		}
		return monthInst, ""
	}
	if kind == "create-month" { // neues Übergangs-Ticket: nutzbare Instanz mit diesem Projekt für neue Tickets
		proj := strings.Fields(strings.TrimPrefix(target, "NEU:"))
		var hits []instKeys
		for _, i := range insts {
			if i.Usable && len(proj) > 0 && i.NewProject == proj[0] {
				hits = append(hits, i)
			}
		}
		if len(hits) == 1 {
			return hits[0].ID, ""
		}
		return 0, "Übergangs-Ticket: Jira-Instanz nicht eindeutig (Projekt für neue Tickets in keiner oder mehreren Instanzen)"
	}
	pk := jiraProject(target)
	var hits []instKeys
	for _, i := range insts {
		if i.Keys[pk] {
			hits = append(hits, i)
		}
	}
	switch len(hits) {
	case 1:
		return hits[0].ID, ""
	case 0:
		return 0, "Ticket " + target + ": Projekt " + pk + " ist keiner Jira-Instanz bekannt – je Instanz „Verbindung testen“ (Token nötig)"
	}
	names := make([]string, 0, len(hits))
	for _, h := range hits {
		names = append(names, h.Name)
	}
	return 0, "Ticket " + target + ": Projekt " + pk + " gibt es in mehreren Jira-Instanzen (" + strings.Join(names, ", ") + ") – nicht eindeutig, bitte Administrator fragen"
}

type tempoPlan struct {
	Target, Kind, Started, Comment, Account, AccountFrom, Hash string
	Secs                                                       int64
}

/* ── JavaScript-kompatible Texthilfen ───────────────────────────── */

// isJSSpace entspricht \s bzw. String.prototype.trim in JavaScript
func isJSSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xa0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
		return true
	}
	return r >= 0x2000 && r <= 0x200a
}

func jsTrim(s string) string { return strings.TrimFunc(s, isJSSpace) }

// jsCollapse = s.replace(/\s+/g, " ")
func jsCollapse(s string) string {
	var b strings.Builder
	sp := false
	for _, r := range s {
		if isJSSpace(r) {
			if !sp {
				b.WriteByte(' ')
			}
			sp = true
			continue
		}
		sp = false
		b.WriteRune(r)
	}
	return b.String()
}

// jsSlice = s.slice(0, n) in UTF-16-Einheiten; ein Ersatzpaar wird nie halbiert
func jsSlice(s string, n int) string {
	units := 0
	for i, r := range s {
		w := 1
		if r >= 0x10000 {
			w = 2
		}
		if units+w > n {
			return s[:i]
		}
		units += w
	}
	return s
}

func upTrim(s string) string { return strings.ToUpper(jsTrim(s)) }

// jsOr = a || b für Texte
func jsOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// jsJSONString wie JSON.stringify für Texte (nur " \ und Steuerzeichen werden maskiert)
func jsJSONString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(&b, `\u%04x`, r)
			} else {
				b.WriteRune(r) // ungültige Bytes werden zu U+FFFD
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

/* ── Regeln ─────────────────────────────────────────────────────── */

func monthOfDate(d string) string {
	if len(d) >= 7 {
		return d[:7]
	}
	return d
}

func jiraProject(key string) string {
	if i := strings.IndexByte(key, '-'); i > 0 {
		return key[:i]
	}
	return key
}

func (e tempoEntry) isPlaneBooking() bool {
	return e.PlaneWI != "" || (e.ProjectID != 0 && e.AllowsPlane)
}

// tempoTargetOf: Zielticket in der Reihenfolge von Node targetOf (Regel 29.10.2026: keine Jira-Buchung ohne Ticket)
func tempoTargetOf(e tempoEntry, c tempoCfg, lk tempoLookup) (target, kind string) {
	isJira := func(k string) bool {
		return jiraKeyRE.MatchString(k) && (e.TicketSys == "jira" || c.JiraKeys[jiraProject(k)])
	}
	fallback := func() (string, string) {
		// nur Plane-Buchungen dürfen aufs Sammelticket; alles andere braucht erst ein Jira-Ticket
		if !e.isPlaneBooking() {
			return "", "no-ticket"
		}
		if t := upTrim(jsOr(e.ProjIssue, c.Issue)); t != "" {
			return t, "collector"
		}
		if c.NewProject != "" && e.ProjectID != 0 {
			return "NEU:" + c.NewProject, "create-collector"
		}
		return "", "none"
	}
	// 1. von Hand/per Vorschlag gewählt
	if t := upTrim(e.TempoTarget); jiraKeyRE.MatchString(t) {
		return t, "ticket"
	}
	// 2. eigenes Jira-Ticket (kein Plane-Work-Item) → immer aufs Ticket selbst
	if e.TaskID != 0 && e.PlaneWI == "" {
		jl, jo, k := upTrim(e.JiraLink), upTrim(e.JiraOrigin), upTrim(e.Ticket)
		switch {
		case jiraKeyRE.MatchString(jl):
			return jl, "ticket"
		case jiraKeyRE.MatchString(jo):
			return jo, "ticket"
		case isJira(k):
			return k, "ticket"
		}
	}
	// 3. Plane-Buchung im Modus „month“ → Übergangs-Ticket; früher anders übertragen → dort lassen
	if c.PlaneMode == "month" && e.isPlaneBooking() {
		m := monthOfDate(e.Date)
		mt := lk.MonthTickets[m]
		if e.PushID != "" && e.PushTarget != "" && e.PushTarget != mt {
			return upTrim(e.PushTarget), "ticket"
		}
		if mt != "" {
			return mt, "month"
		}
		if c.NewProject != "" && c.Worker != "" {
			return "NEU:" + c.NewProject + " Monat " + m, "create-month"
		}
		return "", "none"
	}
	// 4. ohne Aufgabe
	if e.TaskID == 0 {
		return fallback()
	}
	if t := upTrim(e.JiraLink); jiraKeyRE.MatchString(t) {
		return t, "ticket"
	}
	if t := upTrim(e.JiraOrigin); jiraKeyRE.MatchString(t) {
		return t, "ticket"
	}
	if e.PlaneWI != "" {
		if m := lk.Mapped[upTrim(e.Ticket)]; m != "" {
			return upTrim(m), "ticket"
		}
		if c.NewProject != "" && jiraKeyRE.MatchString(upTrim(e.Ticket)) {
			return "NEU:" + c.NewProject, "create"
		}
		return fallback()
	}
	// 5. Ticket-Key mit Jira-Kürzel
	if k := upTrim(e.Ticket); isJira(k) {
		return k, "ticket"
	}
	// 6. Sammelticket oder kein Ticket
	return fallback()
}

// tempoDescribe = Node describe: „[TICKET] Titel – Kommentar“, ≤ 900
func tempoDescribe(e tempoEntry) string {
	key := upTrim(e.Ticket)
	head := ""
	if key != "" {
		head = "[" + key + "]"
	} else if e.ProjectName != "" {
		head = "[" + e.ProjectName + "]"
	}
	title := jsTrim(e.TaskTitle)
	showTitle := title != "" && strings.ToUpper(title) != key
	text := jsTrim(e.Comment)
	var parts []string
	if head != "" {
		parts = append(parts, head)
	}
	if showTitle {
		parts = append(parts, title)
	}
	if text != "" {
		if showTitle {
			text = "– " + text
		}
		parts = append(parts, text)
	}
	s := jsTrim(strings.Join(parts, " "))
	if s == "" {
		s = "Arbeit"
	}
	return jsSlice(jsCollapse(s), 900)
}

// tempoMonthComment: „describe · Projekt · Plane: <Link>“ (Übergangs-Ticket)
func tempoMonthComment(e tempoEntry, c tempoCfg) string {
	parts := []string{tempoDescribe(e)}
	if e.ProjectName != "" {
		parts = append(parts, "· "+e.ProjectName)
	}
	if key := upTrim(e.Ticket); c.PlaneURL != "" && jiraKeyRE.MatchString(key) {
		parts = append(parts, "· Plane: "+c.PlaneURL+"/browse/"+key+"/")
	}
	return jsSlice(strings.Join(parts, " "), 900)
}

// tempoSecs: max(60, auf Minute gerundet)
func tempoSecs(d int64) int64 {
	return max(60, int64(math.Floor(float64(d)/60+0.5))*60)
}

// tempoStamp: lokale Zeit ohne Zeitzone, „YYYY-MM-DDTHH:MM:SS.000“; "" wenn nicht lesbar
func tempoStamp(iso string, loc *time.Location) string {
	t, ok := parseTimeIn(iso, loc)
	if !ok {
		return ""
	}
	return t.In(loc).Format("2006-01-02T15:04:05") + ".000"
}

func parseTimeIn(s string, loc *time.Location) (time.Time, bool) {
	for _, l := range timeLayouts {
		if t, err := time.ParseInLocation(l, s, loc); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// tempoHash = sha1(JSON.stringify([target, started, secs, comment, account]))[:16]
func tempoHash(target, started string, secs int64, comment, account string) string {
	s := "[" + jsJSONString(target) + "," + jsJSONString(started) + "," + strconv.FormatInt(secs, 10) + "," + jsJSONString(comment) + "," + jsJSONString(account) + "]"
	h := sha1.Sum([]byte(s))
	return hex.EncodeToString(h[:])[:16]
}

// tempoPlanOf = Node plan: Ziel, Dauer, Start, Kommentar, Account (Buchung > Projekt > eigenes Ticket leer > Standard), Hash
func tempoPlanOf(e tempoEntry, c tempoCfg, lk tempoLookup, loc *time.Location) tempoPlan {
	target, kind := tempoTargetOf(e, c, lk)
	p := tempoPlan{Target: target, Kind: kind, Secs: tempoSecs(e.Duration), Started: tempoStamp(e.StartTime, loc)}
	switch kind {
	case "month", "create-month":
		p.Comment = tempoMonthComment(e, c)
	case "collector":
		p.Comment = tempoDescribe(e)
	default: // eigenes Ticket: nur Kommentar, sonst Titel
		p.Comment = jsSlice(jsCollapse(jsOr(jsOr(jsTrim(e.Comment), jsTrim(e.TaskTitle)), "Arbeit")), 900)
	}
	own := kind == "ticket"
	raw := jsOr(e.TempoAccount, e.ProjAccount)
	if raw == "" && !own {
		raw = c.Account
	}
	p.Account = jsTrim(raw)
	switch {
	case e.TempoAccount != "":
		p.AccountFrom = "Buchung"
	case e.ProjAccount != "":
		p.AccountFrom = "Projekt"
	case own:
		p.AccountFrom = "Ticket"
	case c.Account != "":
		p.AccountFrom = "Standard"
	}
	// früher ging der Standard-Account immer mit → Hash rückwärtskompatibel
	hashAcc := p.Account
	if hashAcc == "" && own {
		hashAcc = c.Account
	}
	p.Hash = tempoHash(p.Target, p.Started, p.Secs, p.Comment, hashAcc)
	return p
}

func isClaim(id string) bool { return strings.HasPrefix(id, claimPrefix) }

// tempoNeedsTicket: ohne Ticket wird nicht gebucht. Automatisch angelegt wird nur das Übergangs-Ticket des Monats;
// Gegenstück- und Sammeltickets nur auf ausdrückliche Bestätigung der Person (POST …/ticket).
func tempoNeedsTicket(p tempoPlan) bool {
	return p.Target == "" || (strings.HasPrefix(p.Target, "NEU:") && (!tempoCanCreate || p.Kind != "create-month"))
}

// tempoStatusOf = Node statusOf (Prüfreihenfolge unverändert)
func tempoStatusOf(e tempoEntry, p tempoPlan, monthLocked bool) string {
	if e.Skip {
		return "skipped"
	}
	if isClaim(e.PushID) { // wird gerade angelegt
		return "new"
	}
	if e.PushID == "" && !e.AllowsTempo {
		return "norule"
	}
	if e.PushID == "" && e.CheckFlags != "" && e.CheckNote == "" {
		return "review"
	}
	if e.PushID != "" && e.PushHash == p.Hash {
		return "synced"
	}
	if monthLocked || (tempoLockRE.MatchString(e.PushErr) && http4xxRE.MatchString(e.PushErr)) {
		return "locked"
	}
	if e.PushID == "" && e.Dup != "" && e.Dup != "ok" {
		return "dup"
	}
	if tempoNeedsTicket(p) {
		return "needs"
	}
	if e.PushID == "" {
		if e.PushErr != "" {
			return "error"
		}
		return "new"
	}
	if e.PushHash != p.Hash {
		return "changed"
	}
	return "synced"
}

// tempoBody: Worklog für Tempo DC; Account-Attribut nur, wenn ein Account bestimmt ist
func tempoBody(c tempoCfg, p tempoPlan) map[string]any {
	b := map[string]any{"worker": c.WorkerKey, "originTaskId": p.Target, "started": p.Started, "timeSpentSeconds": p.Secs, "comment": p.Comment}
	if p.Account != "" {
		b["attributes"] = tempoAccountAttr(c, p.Account)
	}
	return b
}

func tempoAccountAttr(c tempoCfg, account string) map[string]any {
	id := c.AccountAttrID
	if id <= 0 {
		id = 2
	}
	return map[string]any{"_Account_": map[string]any{"name": "Account", "workAttributeId": id, "value": account}}
}

// tempoLockError: Tempo lehnt wegen gesperrtem Zeitraum ab (400/403 + Sperrtext des Servers, deutsch/englisch)
func tempoLockError(status int, serverText string) bool {
	return (status == 400 || status == 403) && tempoLockRE.MatchString(serverText)
}

// tempoAbort: Netz/Token/Rechte – der ganze Lauf bricht ab (403 nur ohne Sperrtext)
func tempoAbort(status int, serverText string) bool {
	return status == 0 || status == 401 || (status == 403 && !tempoLockRE.MatchString(serverText))
}

// approvalLock: Stundenzettel-Status → gesperrt + Grund
func approvalLock(status string) (bool, string) {
	s := strings.ToLower(status)
	if !approvalLockRE.MatchString(s) {
		return false, ""
	}
	if strings.Contains(s, "approved") {
		return true, "Stundenzettel genehmigt"
	}
	return true, "Stundenzettel eingereicht"
}

/* ── Duplikate ──────────────────────────────────────────────────── */

type remoteWL struct {
	ID        string
	S, E      time.Time
	Key, Text string
}

var remoteLayouts = []string{"2006-01-02T15:04:05.000-0700", "2006-01-02T15:04:05-0700", time.RFC3339Nano}

// parseRemoteStarted: Tempo liefert „YYYY-MM-DD HH:MM:SS.000“, mit „T“ oder mit „+0000“; ohne Zone = lokale Zeit
func parseRemoteStarted(s string, loc *time.Location) (time.Time, bool) {
	s = strings.Replace(strings.TrimSpace(s), " ", "T", 1)
	for _, l := range remoteLayouts {
		if t, err := time.Parse(l, s); err == nil {
			return t, true
		}
	}
	return parseTimeIn(s, loc)
}

// remoteWorklogs: fremde (nicht von uns angelegte) Worklogs mit lesbarer Zeit
func remoteWorklogs(list []map[string]any, own map[string]bool, loc *time.Location) []remoteWL {
	var out []remoteWL
	for _, w := range list {
		id := asStr(w["tempoWorklogId"])
		if id == "" {
			id = asStr(w["id"])
		}
		if own[id] {
			continue
		}
		s, ok := parseRemoteStarted(asStr(w["started"]), loc)
		if !ok {
			continue
		}
		key := asStr(jm(w["issue"])["key"])
		if key == "" {
			key = asStr(w["originTaskId"])
		}
		out = append(out, remoteWL{ID: id, S: s, E: s.Add(time.Duration(asFloat(w["timeSpentSeconds"])) * time.Second), Key: key, Text: asStr(w["comment"])})
	}
	return out
}

// dupHit: erster Worklog mit Überlappung ≥ 50 % der kürzeren Dauer (Überlappung muss > 0 sein)
func dupHit(s, e time.Time, others []remoteWL) *remoteWL {
	for i := range others {
		w := &others[i]
		end, start := e, s
		if w.E.Before(end) {
			end = w.E
		}
		if w.S.After(start) {
			start = w.S
		}
		ov := end.Sub(start)
		if ov <= 0 {
			continue
		}
		if ov >= min(e.Sub(s), w.E.Sub(w.S))/2 {
			return w
		}
	}
	return nil
}

func dupLabel(w *remoteWL, loc *time.Location) string {
	return jsTrim(w.Key + " " + w.S.In(loc).Format("15:04") + "–" + w.E.In(loc).Format("15:04") + " " + jsSlice(w.Text, 80))
}

// firstOfMonth / lastOfMonth für „YYYY-MM“
func monthBounds(m string) (string, string, bool) {
	t, err := time.Parse("2006-01", m)
	if err != nil {
		return "", "", false
	}
	return t.Format("2006-01-02"), t.AddDate(0, 1, -1).Format("2006-01-02"), true
}

// explicitInstance: von der Person gewählte Instanz für genau das Ziel (Buchung > Aufgabe > Projekt-Sammelticket)
func explicitInstance(e tempoEntry, target string) int64 {
	switch {
	case target == "":
		return 0
	case e.TargetInst != 0 && upTrim(e.TempoTarget) == target:
		return e.TargetInst
	case e.TaskInst != 0 && (upTrim(e.JiraLink) == target || upTrim(e.JiraOrigin) == target || upTrim(e.Ticket) == target):
		return e.TaskInst
	case e.ProjIssueInst != 0 && upTrim(e.ProjIssue) == target:
		return e.ProjIssueInst
	}
	return 0
}

/* ── Übergangs-Ticket je Person, Instanz und Monat (Spec §3) ─────── */

func monthTicketSummary(worker, month string) string {
	return "Übergang Zeiten " + worker + " " + month
}

type monthRow struct {
	Ticket, Title, Project, Date string
	Secs                         int64
}

// monthDescription = Node updateMonthDescription: Jira-Wiki-Tabelle je Plane-Ticket, Summe, Link
func monthDescription(worker, month, planeURL string, rows []monthRow) string {
	type agg struct {
		key, title, project, last string
		secs                      int64
		n                         int
	}
	by := map[string]*agg{}
	var order []string
	var total int64
	for _, r := range rows {
		k := upTrim(r.Ticket)
		if k == "" {
			k = "(ohne Ticket)"
		}
		x := by[k]
		if x == nil {
			x = &agg{key: k, title: r.Title, project: r.Project}
			by[k] = x
			order = append(order, k)
		}
		x.secs += r.Secs
		x.n++
		x.last = r.Date
		total += r.Secs
	}
	list := make([]*agg, 0, len(order))
	for _, k := range order {
		list = append(list, by[k])
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].secs > list[j].secs })
	h := func(s int64) string {
		return strings.Replace(strconv.FormatFloat(math.Round(float64(s)/360)/10, 'f', -1, 64), ".", ",", 1)
	}
	esc := func(t string) string {
		return strings.TrimSpace(strings.NewReplacer("|", " ", "[", " ", "]", " ", "{", " ", "}", " ").Replace(t))
	}
	or := func(s string) string {
		if s == "" {
			return " "
		}
		return s
	}
	lines := []string{"Übergangs-Ticket für Zeiten von " + worker + " im Monat " + month + ", die in Plane gebucht wurden. Wird von Zeitprotokoll aktuell gehalten – bitte nicht von Hand ändern.",
		"", fmt.Sprintf("*Summe: %s h in %d Buchungen*", h(total), len(rows)), "", "||Plane-Ticket||Titel||Projekt||Stunden||Buchungen||zuletzt||"}
	for _, x := range list {
		cell := esc(x.key)
		if planeURL != "" && jiraKeyRE.MatchString(x.key) {
			cell = "[" + x.key + "|" + planeURL + "/browse/" + x.key + "/]"
		}
		lines = append(lines, fmt.Sprintf("|%s|%s|%s|%s|%d|%s|", cell, or(esc(x.title)), or(esc(x.project)), h(x.secs), x.n, x.last))
	}
	return strings.Join(lines, "\n")
}

// monthWantClosed: soll das Übergangs-Ticket geschlossen sein?
// Freigabe (approved) schließt; wiedereröffnet/abgelehnt öffnet (Nachträge); eingereicht lässt den Zustand, wie er ist
// (nach Ablehnung offen, sonst kein Auf/Zu bis zur Freigabe); sonst schließt eine Tempo-Sperre oder die Nachfrist ohne Offenes.
func monthWantClosed(approval string, tempoLocked, graceOver, open, closed bool) bool {
	switch approval {
	case "approved", "closed": // freigegeben oder automatisch geschlossen (Nachfrist abgelaufen)
		return true
	case "rejected":
		return false
	case "submitted":
		return closed
	}
	return tempoLocked || (graceOver && !open)
}

var (
	doneRE   = regexp.MustCompile(`(?i)erledigt|fertig|done|closed|geschlossen|resolved|abgeschlossen`)
	reopenRE = regexp.MustCompile(`(?i)wieder|reopen|öffnen|offen|open|to do|in arbeit|in progress|backlog`)
)

// pickTransition: Übergang nach „Erledigt“ (close) bzw. zurück in einen offenen Status (reopen); "" = keiner
func pickTransition(list []map[string]any, close bool) string {
	cat := func(t map[string]any) string { return asStr(jm(jm(jm(t["to"])["statusCategory"]))["key"]) }
	name := func(t map[string]any) string { return asStr(t["name"]) + " " + asStr(jm(t["to"])["name"]) }
	for _, t := range list {
		if c := cat(t); (close && c == "done") || (!close && c != "" && c != "done") {
			if close || reopenRE.MatchString(name(t)) {
				return asStr(t["id"])
			}
		}
	}
	for _, t := range list {
		if close && doneRE.MatchString(name(t)) {
			return asStr(t["id"])
		}
		if !close && cat(t) != "done" && reopenRE.MatchString(name(t)) && !doneRE.MatchString(asStr(jm(t["to"])["name"])) {
			return asStr(t["id"])
		}
	}
	if !close { // irgendein Übergang in eine nicht erledigte Kategorie
		for _, t := range list {
			if c := cat(t); c != "" && c != "done" {
				return asStr(t["id"])
			}
		}
	}
	return ""
}

// monthGraceOver: Nachfrist (Tage nach Monatsende) vorbei?
func monthGraceOver(month string, days int, today string) bool {
	_, last, ok := monthBounds(month)
	if !ok {
		return false
	}
	t, _ := time.Parse("2006-01-02", last)
	return today > t.AddDate(0, 0, days).Format("2006-01-02")
}

// jqlQuote: Text für JQL "…" (Anführungszeichen und Backslash entfernen, wie Node)
func jqlQuote(s string) string { return strings.NewReplacer(`"`, " ", `\`, " ").Replace(s) }

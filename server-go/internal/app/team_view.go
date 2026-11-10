package app

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

/* Übersichten für die Oberflächen „Teamleiter“ (/team) und „Personal“ (/personal) – nur lesen.
   Teamleiter: Mitglieder der Teams, die sie leiten (team_members.is_lead, wie canSee in visibility.go, nie mehr).
   Personal (is_hr): alle zeiterfassenden, aktiven Personen. Unberechtigte bekommen 404 (keine Auskunft).
   Soll/Ist/Saldo kommen aus computeOverview (konto.go), damit die Zahlen exakt der eigenen Ansicht der Person entsprechen;
   alles andere (Status, Prüfhinweise, Nachträge, Einstellungen) wird gebündelt für alle Personen der Seite gelesen.
   Der Blick auf die Übersicht wird nicht einzeln protokolliert; Einzelbuchungen gibt es weiter über ?user= (asTarget, protokolliert). */

const (
	maxOverviewPeople = 200 // höchstens so viele Personen je Antwort
	overviewWorkers   = 4   // gleichzeitige Kontoberechnungen (Pool hat ≥ 20 Verbindungen)
)

// knownStatus: Monatsstatus aus month_approvals (013/018); kein Eintrag = open
var knownStatus = map[string]bool{"open": true, "submitted": true, "approved": true, "rejected": true, "closed": true}

var statusRawRE = regexp.MustCompile(`^[a-z_]{1,32}$`)

// normStatus: unbekannte Werte (z. B. aus einer neueren Migration) werden als „unknown“ gemeldet, nie durchgereicht ohne Prüfung
func normStatus(s *string) (status string, raw any) {
	if s == nil || *s == "" {
		return "open", nil
	}
	if knownStatus[*s] {
		return *s, nil
	}
	if statusRawRE.MatchString(*s) {
		return "unknown", *s
	}
	return "unknown", nil
}

// overviewMonth: ?month=JJJJ-MM (Standard: laufender Monat); Rückgabe erster/letzter Tag
func (a *App) overviewMonth(r *http.Request) (month, from, to string, ok bool) {
	month = r.URL.Query().Get("month")
	if month == "" {
		month = time.Now().In(a.loc()).Format("2006-01")
	}
	return monthRange(month)
}

func monthRange(month string) (string, string, string, bool) {
	if !monthRE.MatchString(month) {
		return month, "", "", false
	}
	t, err := time.Parse("2006-01", month)
	if err != nil || t.Year() < 2000 || t.Year() > 2200 {
		return month, "", "", false
	}
	return month, dayStr(t), dayStr(t.AddDate(0, 1, -1)), true
}

func (a *App) loc() *time.Location {
	if a.Loc == nil {
		return time.UTC
	}
	return a.Loc
}

// paging: limit 1…200 (Standard def), offset ≥ 0
func paging(r *http.Request, def int) (int, int) {
	q := r.URL.Query()
	limit, err := strconv.Atoi(q.Get("limit"))
	if err != nil || limit <= 0 {
		limit = def
	}
	limit = min(limit, maxOverviewPeople)
	offset, _ := strconv.Atoi(q.Get("offset"))
	offset = max(0, min(offset, 1000000))
	return limit, offset
}

// likeArg: Suchtext für ILIKE … ESCAPE '\' (Platzhalter des Nutzers wirken nicht)
func likeArg(q string) string {
	q = strings.TrimSpace(q)
	if r := []rune(q); len(r) > 100 {
		q = string(r[:100])
	}
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(q)
}

func hidden(w http.ResponseWriter) {
	writeJSON(w, http.StatusNotFound, map[string]any{"error": "nicht gefunden"})
}

// leadAllowed / hrAllowed: harte Rollenprüfung vor jeder Abfrage
func leadAllowed(u User) bool { return u.IsLead && u.Role != "breakglass" && u.timeAccount() }
func hrAllowed(u User) bool   { return u.IsHR && u.Role != "breakglass" && u.timeAccount() }

// activeSQL: zeiterfassende, aktive Personen (kein reines Admin-Testkonto, kein Notfallzugang, nicht gesperrt)
const activeSQL = `u.role<>'breakglass' AND NOT u.disabled AND NOT (u.kind='local' AND NOT u.productive)`

type teamRef struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	IsLead bool   `json:"is_lead"`
}

// personRow: eine Zeile der Übersicht (Antwortform für /api/team/overview und /api/personal/overview)
type personRow struct {
	ID           int64     `json:"id"`
	Name         string    `json:"name"`
	Team         string    `json:"team"`
	Teams        []teamRef `json:"teams"`
	Self         bool      `json:"self"`
	Status       string    `json:"status"`
	StatusRaw    any       `json:"status_raw,omitempty"`
	SubmittedAt  any       `json:"submitted_at"`
	DecidedAt    any       `json:"decided_at"`
	SollH        float64   `json:"soll_h"`
	SollToDateH  float64   `json:"soll_bis_heute_h"`
	IstH         float64   `json:"ist_h"`
	SaldoH       float64   `json:"saldo_h"`
	KontoH       any       `json:"konto_h"`
	ChecksOpen   int64     `json:"checks_open"`
	LateEntries  int64     `json:"late_entries"`
	Nachtraege   int64     `json:"nachtraege"`
	Nachgetragen bool      `json:"nachgetragen"`
}

func tsOrNil(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC().Format("2006-01-02 15:04:05")
}

func (p *personRow) setTeams(ts []teamRef) {
	if ts == nil {
		ts = []teamRef{}
	}
	sort.SliceStable(ts, func(i, j int) bool { return ts[i].Name < ts[j].Name })
	p.Teams = ts
	names := make([]string, 0, len(ts))
	for _, t := range ts {
		names = append(names, t.Name)
	}
	p.Team = strings.Join(names, ", ")
}

// monthFigures: Soll (ganzer Monat / bis heute), Ist, Saldo des Monats und Kontostand aus den Tageswerten von computeOverview
func monthFigures(days []ovDay) (soll, sollToDate, ist, saldo float64, konto any) {
	for _, d := range days {
		soll += d.TargetH
		if d.IsFuture {
			continue
		}
		sollToDate += d.TargetH
		if !d.IsBeforeTracking {
			if d.Absence != nil {
				ist += d.AbsenceH
			} else {
				ist += d.WorkH
			}
		}
		saldo += d.BalanceH
		konto = d.CumulativeBal
	}
	return r2(soll), r2(sollToDate), r2(ist), r2(saldo), konto
}

// fillFigures ergänzt die Zeilen gebündelt: Einstellungen, Prüfhinweise, Nachträge; Konto je Person über computeOverview (begrenzt parallel)
func (a *App) fillFigures(ctx context.Context, rows []*personRow, month, from, to string) error {
	if len(rows) == 0 {
		return nil
	}
	ids := make([]int64, len(rows))
	byID := make(map[int64]*personRow, len(rows))
	for i, p := range rows {
		ids[i] = p.ID
		byID[p.ID] = p
	}
	// Einstellungen aller Personen in einer Abfrage (gleiche Vorgaben wie userSettings)
	st := make(map[int64]map[string]string, len(ids))
	for _, id := range ids {
		m := make(map[string]string, len(settingDefaults))
		for k, v := range settingDefaults {
			m[k] = v
		}
		st[id] = m
	}
	srows, err := a.DB.Query(ctx, `SELECT user_id, key, COALESCE(value,'') FROM user_settings WHERE user_id = ANY($1)`, ids)
	if err != nil {
		return err
	}
	for srows.Next() {
		var id int64
		var k, v string
		if err := srows.Scan(&id, &k, &v); err != nil {
			srows.Close()
			return err
		}
		st[id][k] = v
	}
	srows.Close()
	if err := srows.Err(); err != nil {
		return err
	}
	// offene Prüfhinweise (wie /api/checks/open) und nach Monatsende angelegte Buchungen
	monthEnd, _ := time.ParseInLocation("2006-01-02", to, a.loc())
	monthEnd = monthEnd.AddDate(0, 0, 1)
	crows, err := a.DB.Query(ctx, `SELECT user_id,
		COUNT(*) FILTER (WHERE check_flags IS NOT NULL AND check_note IS NULL),
		COUNT(*) FILTER (WHERE created_at >= $4)
		FROM time_entries WHERE user_id = ANY($1) AND date >= $2 AND date <= $3 GROUP BY user_id`, ids, from, to, monthEnd)
	if err != nil {
		return err
	}
	for crows.Next() {
		var id, c, l int64
		if err := crows.Scan(&id, &c, &l); err != nil {
			crows.Close()
			return err
		}
		byID[id].ChecksOpen, byID[id].LateEntries = c, l
	}
	crows.Close()
	if err := crows.Err(); err != nil {
		return err
	}
	// Nachträge durch Teamleitung/Personal (month_approval_log 'nachtrag', Migration 018 / nachtrag.go)
	nrows, err := a.DB.Query(ctx, `SELECT user_id, COUNT(*) FROM month_approval_log WHERE user_id = ANY($1) AND month=$2 AND action='nachtrag' GROUP BY user_id`, ids, month)
	if err != nil {
		return err
	}
	for nrows.Next() {
		var id, n int64
		if err := nrows.Scan(&id, &n); err != nil {
			nrows.Close()
			return err
		}
		byID[id].Nachtraege = n
	}
	nrows.Close()
	if err := nrows.Err(); err != nil {
		return err
	}
	for _, p := range rows {
		p.Nachgetragen = p.Nachtraege > 0 || p.LateEntries > 0
	}
	// Konto je Person: dieselbe Rechnung wie /api/overview (bewusst nicht nachgebaut)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan *personRow)
	var wg sync.WaitGroup
	var once sync.Once
	var firstErr error
	for range min(overviewWorkers, len(rows)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for p := range jobs {
				res, err := a.computeOverview(ctx, p.ID, st[p.ID], from, to)
				if err != nil {
					once.Do(func() { firstErr = err; cancel() })
					continue
				}
				p.SollH, p.SollToDateH, p.IstH, p.SaldoH, p.KontoH = monthFigures(res.Days)
			}
		}()
	}
	for _, p := range rows {
		if ctx.Err() != nil {
			break
		}
		jobs <- p
	}
	close(jobs)
	wg.Wait()
	return firstErr
}

func decodeTeams(raw []byte) []teamRef {
	var ts []teamRef
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &ts)
	}
	return ts
}

/* ── Teamleiter ─────────────────────────────────────────────────── */

func (a *App) routesTeamView(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/team/overview", a.auth(a.teamOverview))
	mux.HandleFunc("GET /api/team/members", a.auth(a.teamMembers))
	mux.HandleFunc("GET /api/personal/overview", a.auth(a.personalOverview))
	mux.HandleFunc("GET /api/personal/people", a.auth(a.personalPeople))
}

// optTeam: ?team=<id> (optional); ok=false bei ungültigem Wert
func optTeam(r *http.Request) (any, bool, bool) {
	s := r.URL.Query().Get("team")
	switch s {
	case "":
		return nil, false, true
	case "none":
		return nil, true, true
	}
	id, err := strconv.ParseInt(s, 10, 64)
	if err != nil || id <= 0 {
		return nil, false, false
	}
	return id, false, true
}

// teamOverview: GET /api/team/overview?month=JJJJ-MM[&team=<id>] – Mitglieder der eigenen Teams mit Monatsstatus und Konto
func (a *App) teamOverview(w http.ResponseWriter, r *http.Request, u User) {
	if !leadAllowed(u) {
		hidden(w)
		return
	}
	month, from, to, ok := a.overviewMonth(r)
	if !ok {
		badRequest(w, "month: JJJJ-MM")
		return
	}
	team, noTeam, ok := optTeam(r)
	if !ok || noTeam {
		badRequest(w, "team ungültig")
		return
	}
	ctx := r.Context()
	// Nur Teams, die u leitet (m1.is_lead); angezeigt werden nur diese Teams, nicht weitere Teams des Mitglieds
	q, err := a.DB.Query(ctx, `SELECT u.id, u.name,
		json_agg(json_build_object('id', t.id, 'name', t.name, 'is_lead', m2.is_lead) ORDER BY t.name)::text,
		a.status, a.submitted_at, a.decided_at
		FROM team_members m1 JOIN team_members m2 ON m2.team_id=m1.team_id JOIN teams t ON t.id=m1.team_id JOIN users u ON u.id=m2.user_id
		LEFT JOIN month_approvals a ON a.user_id=u.id AND a.month=$2
		WHERE m1.user_id=$1 AND m1.is_lead AND `+activeSQL+` AND ($3::bigint IS NULL OR m1.team_id=$3)
		GROUP BY u.id, u.name, a.status, a.submitted_at, a.decided_at ORDER BY u.name, u.id LIMIT $4`, u.ID, month, team, maxOverviewPeople+1)
	if err != nil {
		a.fail(w, err)
		return
	}
	rows, err := scanPeople(q, u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	truncated := len(rows) > maxOverviewPeople
	if truncated {
		rows = rows[:maxOverviewPeople]
	}
	if err := a.fillFigures(ctx, rows, month, from, to); err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"month": month, "from": from, "to": to, "count": len(rows), "truncated": truncated, "people": rows})
}

type rowsScanner interface {
	Next() bool
	Scan(dest ...any) error
	Close()
	Err() error
}

func scanPeople(q rowsScanner, self int64) ([]*personRow, error) {
	defer q.Close()
	out := []*personRow{}
	for q.Next() {
		p := &personRow{}
		var teams []byte
		var st *string
		var sub, dec *time.Time
		if err := q.Scan(&p.ID, &p.Name, &teams, &st, &sub, &dec); err != nil {
			return nil, err
		}
		p.setTeams(decodeTeams(teams))
		p.Status, p.StatusRaw = normStatus(st)
		p.SubmittedAt, p.DecidedAt = tsOrNil(sub), tsOrNil(dec)
		p.Self = p.ID == self
		out = append(out, p)
	}
	return out, q.Err()
}

// teamMembers: GET /api/team/members – je Mitgliedschaft in einem Team, das u leitet: id, name, team_id, team, is_lead
func (a *App) teamMembers(w http.ResponseWriter, r *http.Request, u User) {
	if !leadAllowed(u) {
		hidden(w)
		return
	}
	rs, err := a.maps(r.Context(), `SELECT u.id, u.name, t.id AS team_id, t.name AS team, m2.is_lead
		FROM team_members m1 JOIN team_members m2 ON m2.team_id=m1.team_id JOIN teams t ON t.id=m1.team_id JOIN users u ON u.id=m2.user_id
		WHERE m1.user_id=$1 AND m1.is_lead AND `+activeSQL+` ORDER BY t.name, u.name, u.id LIMIT 2000`, u.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": rs})
}

/* ── Personal ───────────────────────────────────────────────────── */

// personalOverview: GET /api/personal/overview?month=&team=<id|none>&status=&q=&limit=&offset= – alle Personen mit Status und Konto
func (a *App) personalOverview(w http.ResponseWriter, r *http.Request, u User) {
	if !hrAllowed(u) {
		hidden(w)
		return
	}
	month, from, to, ok := a.overviewMonth(r)
	if !ok {
		badRequest(w, "month: JJJJ-MM")
		return
	}
	team, noTeam, ok := optTeam(r)
	if !ok {
		badRequest(w, "team ungültig")
		return
	}
	status := r.URL.Query().Get("status")
	if status != "" && !knownStatus[status] {
		badRequest(w, "status: open, submitted, approved, rejected oder closed")
		return
	}
	limit, offset := paging(r, 50)
	ctx := r.Context()
	q, err := a.DB.Query(ctx, `SELECT u.id, u.name,
		(SELECT json_agg(json_build_object('id', t.id, 'name', t.name, 'is_lead', m.is_lead) ORDER BY t.name) FROM team_members m JOIN teams t ON t.id=m.team_id WHERE m.user_id=u.id)::text,
		a.status, a.submitted_at, a.decided_at, COUNT(*) OVER ()
		FROM users u LEFT JOIN month_approvals a ON a.user_id=u.id AND a.month=$1
		WHERE `+activeSQL+`
		  AND ($2 = '' OR u.name ILIKE '%' || $2 || '%' ESCAPE '\')
		  AND ($3::bigint IS NULL OR EXISTS (SELECT 1 FROM team_members m WHERE m.user_id=u.id AND m.team_id=$3))
		  AND (NOT $4 OR NOT EXISTS (SELECT 1 FROM team_members m WHERE m.user_id=u.id))
		  AND ($5 = '' OR COALESCE(a.status,'open') = $5)
		ORDER BY u.name, u.id LIMIT $6 OFFSET $7`, month, likeArg(r.URL.Query().Get("q")), team, noTeam, status, limit, offset)
	if err != nil {
		a.fail(w, err)
		return
	}
	rows := []*personRow{}
	total := int64(0)
	func() {
		defer q.Close()
		for q.Next() {
			p := &personRow{}
			var teams []byte
			var st *string
			var sub, dec *time.Time
			if err = q.Scan(&p.ID, &p.Name, &teams, &st, &sub, &dec, &total); err != nil {
				return
			}
			p.setTeams(decodeTeams(teams))
			p.Status, p.StatusRaw = normStatus(st)
			p.SubmittedAt, p.DecidedAt = tsOrNil(sub), tsOrNil(dec)
			p.Self = p.ID == u.ID
			rows = append(rows, p)
		}
		err = q.Err()
	}()
	if err != nil {
		a.fail(w, err)
		return
	}
	if err := a.fillFigures(ctx, rows, month, from, to); err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"month": month, "from": from, "to": to, "total": total, "limit": limit, "offset": offset, "people": rows})
}

// personalPeople: GET /api/personal/people[?q=] – Auswahlliste: id, name, teams
func (a *App) personalPeople(w http.ResponseWriter, r *http.Request, u User) {
	if !hrAllowed(u) {
		hidden(w)
		return
	}
	rs, err := a.maps(r.Context(), `SELECT u.id, u.name,
		COALESCE((SELECT json_agg(json_build_object('id', t.id, 'name', t.name, 'is_lead', m.is_lead) ORDER BY t.name) FROM team_members m JOIN teams t ON t.id=m.team_id WHERE m.user_id=u.id), '[]'::json) AS teams
		FROM users u WHERE `+activeSQL+` AND ($1 = '' OR u.name ILIKE '%' || $1 || '%' ESCAPE '\')
		ORDER BY u.name, u.id LIMIT 2000`, likeArg(r.URL.Query().Get("q")))
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": rs})
}

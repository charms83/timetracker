package app

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

/* Client für Jira Data Center + Tempo Timesheets DC (wie backend/tempo-push.js requestOnce/request).
   Auth nur per Bearer-PAT (jira_token je Person), Zeitlimit 20 s, Umleitungen werden nie gefolgt, kein Proxy.
   Jira steht im Firmennetz: private Adressen sind erlaubt, gesperrt sind nur Link-Local/Metadaten-Adressen.
   Zertifikate: System-Zertifikate, optional zusätzlich die Datei aus ZP_EXTRA_CA_FILE (Firmen-CA).
   Bremse je Person: bei HTTP 429 warten alle Anfragen dieser Person (Retry-After), höchstens 3 Wiederholungen. */

type jiraCfg struct{ Base, Token string }

func (c jiraCfg) ok() bool { return c.Base != "" && c.Token != "" }

// jiraErr: Status 0 = Netz/Zeitüberschreitung/Umleitung (Lauf abbrechen); Detail = Text des Servers ohne eigenen Hinweis
type jiraErr struct {
	Status     int
	Msg        string
	Detail     string
	RetryAfter time.Duration
}

func (e *jiraErr) Error() string { return e.Msg }

func jiraStatus(err error) int {
	var je *jiraErr
	if errors.As(err, &je) {
		return je.Status
	}
	return 0
}

// validateJiraURL: https Pflicht (der Token geht bei jeder Anfrage mit); http nur für localhost oder mit ZP_ALLOW_HTTP=1
func (a *App) validateJiraURL(raw string) (string, error) {
	v := strings.TrimRight(strings.TrimSpace(raw), "/")
	if v == "" {
		return "", nil
	}
	u, err := url.Parse(v)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && u.Scheme != "http") {
		return "", errors.New("Jira-Adresse ungültig (ohne Benutzer:Passwort, ohne ?…)")
	}
	if u.Scheme == "http" && !a.AllowHTTP {
		h := u.Hostname()
		if h != "localhost" && h != "127.0.0.1" && h != "::1" && !strings.HasSuffix(h, ".localhost") {
			return "", errors.New("Jira-Adresse muss mit https:// beginnen (http nur für localhost; sonst ZP_ALLOW_HTTP=1)")
		}
	}
	return v, nil
}

/* ── Verbindung ─────────────────────────────────────────────────── */

// denyLinkLocal: Verbindungen zu Link-Local-/Metadaten-Adressen sperren – geprüft nach der DNS-Auflösung
func denyLinkLocal(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	if ip := net.ParseIP(host); ip == nil || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() || metadataIPs[ip.String()] {
		return errors.New("Zieladresse nicht erlaubt")
	}
	return nil
}

// jiraHTTPClient wird einmal je Prozess gebaut (Zertifikate aus System + ZP_EXTRA_CA_FILE)
func (a *App) jiraHTTPClient() (*http.Client, error) {
	a.jiraInit.Do(func() {
		roots, err := x509.SystemCertPool()
		if err != nil || roots == nil {
			roots = x509.NewCertPool()
		}
		if f := strings.TrimSpace(a.ExtraCAFile); f != "" {
			b, err := readSmallFile(f, 1<<20)
			if err != nil || !roots.AppendCertsFromPEM(b) {
				a.jiraCAErr = errors.New("Zusatz-Zertifikate (ZP_EXTRA_CA_FILE) nicht lesbar oder ohne PEM-Zertifikat")
				return
			}
		}
		dial := (&net.Dialer{Timeout: 10 * time.Second, Control: denyLinkLocal}).DialContext
		if hook := a.jiraDialHook; hook != nil { // nur Tests (ohne Netz): gleiche Adress-Sperre, Verbindung im Speicher
			dial = func(ctx context.Context, network, addr string) (net.Conn, error) {
				if err := denyLinkLocal(network, addr, nil); err != nil {
					return nil, err
				}
				return hook(ctx, network, addr)
			}
		}
		tr := &http.Transport{
			Proxy:                 nil, // kein Proxy: Er würde die Zieladress-Sperre umgehen
			DialContext:           dial,
			TLSClientConfig:       &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12},
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 20 * time.Second,
			MaxIdleConnsPerHost:   4,
		}
		a.jiraClient = &http.Client{
			Transport:     tr,
			Timeout:       20 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		}
	})
	return a.jiraClient, a.jiraCAErr
}

func readSmallFile(path string, max int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, max))
}

/* ── Bremse je Person ───────────────────────────────────────────── */

type jiraGate struct {
	mu    sync.Mutex
	until time.Time
}

func (a *App) jiraGateFor(uid int64) *jiraGate {
	v, _ := a.jiraGates.LoadOrStore(uid, &jiraGate{})
	return v.(*jiraGate)
}

func (g *jiraGate) wait(ctx context.Context) error {
	return g.waitShared(ctx, nil, 0)
}

func (g *jiraGate) waitShared(ctx context.Context, a *App, uid int64) error {
	if a != nil && a.DB != nil {
		if until := a.rateBlockUntil(ctx, "jira", strconv.FormatInt(uid, 10)); !until.IsZero() {
			if d := time.Until(until); d > 0 {
				return sleepCtx(ctx, d)
			}
		}
	}
	g.mu.Lock()
	d := time.Until(g.until)
	g.mu.Unlock()
	if d <= 0 {
		return nil
	}
	return sleepCtx(ctx, d)
}

func (g *jiraGate) block(d time.Duration) {
	g.blockShared(nil, 0, d)
}

func (g *jiraGate) blockShared(a *App, uid int64, d time.Duration) {
	until := time.Now().Add(d)
	g.mu.Lock()
	if until.After(g.until) {
		g.until = until
	}
	g.mu.Unlock()
	if a != nil && a.DB != nil {
		a.rateSetBlock(context.Background(), "jira", strconv.FormatInt(uid, 10), until)
	}
}

var jiraMinWait = 2 * time.Second // Tests setzen es kleiner

// jiraRetryWait wie Node: min(30 s, max(2 s, Retry-After oder 4 s·(Versuch+1)))
func jiraRetryWait(retryAfter time.Duration, attempt int) time.Duration {
	w := retryAfter
	if w <= 0 {
		w = time.Duration(4*(attempt+1)) * time.Second
	}
	return min(30*time.Second, max(jiraMinWait, w))
}

/* ── Anfragen ───────────────────────────────────────────────────── */

// jiraDo: Anfrage mit Wiederholung nur bei HTTP 429 (höchstens 3). Antwort: JSON (map, Liste) oder nil.
func (a *App) jiraDo(ctx context.Context, uid int64, c jiraCfg, method, path string, body any) (any, error) {
	if !c.ok() {
		return nil, &jiraErr{Status: 0, Msg: "Jira-URL oder Token fehlt (Einstellungen › Jira & Tempo)"}
	}
	g := a.jiraGateFor(uid)
	for attempt := 0; ; attempt++ {
		if err := g.waitShared(ctx, a, uid); err != nil {
			return nil, &jiraErr{Status: 0, Msg: "abgebrochen"}
		}
		res, err := a.jiraOnce(ctx, c, method, path, body)
		var je *jiraErr
		if !errors.As(err, &je) || je.Status != 429 || attempt >= 3 {
			return res, err
		}
		w := jiraRetryWait(je.RetryAfter, attempt)
		g.blockShared(a, uid, w)
		a.sysLogSafe(ctx, uid, "info", "tempo", fmt.Sprintf("Jira/Tempo-Anfragelimit erreicht – Pause %d s", int(w.Seconds())))
	}
}

// sysLogSafe: Protokoll nur, wenn eine Datenbank da ist (Client-Tests laufen ohne)
func (a *App) sysLogSafe(ctx context.Context, uid int64, level, cat, msg string) {
	if a.DB != nil {
		a.sysLog(ctx, uid, level, cat, msg)
	}
}

func jiraKind(path string) string {
	if strings.HasPrefix(path, "/rest/api/") {
		return "Jira"
	}
	return "Tempo"
}

func (a *App) jiraOnce(ctx context.Context, c jiraCfg, method, path string, body any) (any, error) {
	client, err := a.jiraHTTPClient()
	if err != nil {
		return nil, &jiraErr{Status: 0, Msg: err.Error()}
	}
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.Base+path, rd)
	if err != nil {
		return nil, &jiraErr{Status: 0, Msg: "Jira-Adresse ungültig"}
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			if ue.Timeout() {
				return nil, &jiraErr{Status: 0, Msg: "Zeitüberschreitung (20 s)"}
			}
			err = ue.Err // URL nicht mit ausgeben
		}
		msg := jiraKind(path) + " nicht erreichbar: " + scrub(err.Error(), c.Token)
		var ce *tls.CertificateVerificationError
		var ua x509.UnknownAuthorityError
		if errors.As(err, &ce) || errors.As(err, &ua) {
			msg += " – Zertifikat nicht vertrauenswürdig (Firmen-CA im System oder per ZP_EXTRA_CA_FILE hinterlegen)"
		}
		return nil, &jiraErr{Status: 0, Msg: msg}
	}
	defer resp.Body.Close()
	const maxBody = 8 << 20
	data, rerr := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	var out any
	jerr := error(nil)
	if len(data) > 0 {
		jerr = json.Unmarshal(data, &out)
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		// abgeschnitten, unlesbar oder kein JSON (z. B. Anmeldeseite eines Proxys): nicht als Erfolg werten
		if rerr != nil || len(data) > maxBody || jerr != nil {
			return nil, &jiraErr{Status: 0, Msg: fmt.Sprintf("%s: unerwartete Antwort (HTTP %d, kein JSON) – Adresse prüfen", jiraKind(path), resp.StatusCode)}
		}
		return out, nil
	}
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		// Umleitung (z. B. Anmeldeseite, http→https) wird nicht gefolgt: Token bliebe sonst nicht beim Ziel
		return nil, &jiraErr{Status: 0, Msg: fmt.Sprintf("%s leitet um (HTTP %d) – Adresse prüfen (https://, Kontextpfad)", jiraKind(path), resp.StatusCode)}
	}
	msg := scrub(jiraErrText(out, data), c.Token)
	hint := ""
	switch resp.StatusCode {
	case 401:
		hint = " – Token prüfen"
	case 403:
		hint = " – keine Berechtigung (Tempo-Rolle prüfen)" // bewusst ohne Sperr-Wörter, sonst gälte jede 403 als Monatssperre
	case 404:
		hint = " – Adresse, Ticket oder Worklog nicht gefunden"
	}
	full := fmt.Sprintf("%s HTTP %d", jiraKind(path), resp.StatusCode)
	if msg != "" {
		full += ": " + truncRunes(msg, 220)
	}
	je := &jiraErr{Status: resp.StatusCode, Msg: full + hint, Detail: msg}
	if resp.StatusCode == 429 {
		if ra, err := strconv.ParseFloat(strings.TrimSpace(resp.Header.Get("Retry-After")), 64); err == nil && ra > 0 {
			je.RetryAfter = time.Duration(min(ra, 120) * float64(time.Second))
		}
	}
	return nil, je
}

// jiraErrText wie Node: message || errorMessages.join(", ") || error || JSON(errors) || Rohtext (200)
func jiraErrText(out any, data []byte) string {
	if m, ok := out.(map[string]any); ok {
		if s := asStr(m["message"]); s != "" {
			return s
		}
		if l, ok := m["errorMessages"].([]any); ok && len(l) > 0 {
			parts := make([]string, 0, len(l))
			for _, x := range l {
				parts = append(parts, fmt.Sprint(x))
			}
			if s := strings.Join(parts, ", "); s != "" {
				return s
			}
		}
		if s := asStr(m["error"]); s != "" {
			return s
		}
		if e, has := m["errors"]; has && e != nil {
			b, _ := json.Marshal(e)
			return string(b)
		}
	}
	return truncRunes(string(data), 200)
}

func truncRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// scrub entfernt den Token aus Texten (falls ein Server ihn zurückspiegelt)
func scrub(s, token string) string {
	if t := strings.TrimSpace(token); len(t) >= 4 {
		s = strings.ReplaceAll(s, t, "•••")
	}
	return s
}

/* ── Aufrufe ────────────────────────────────────────────────────── */

const tempoWL = "/rest/tempo-timesheets/4/worklogs"

type jiraMe struct{ Name, Key, Display string }

func (a *App) jiraMyself(ctx context.Context, uid int64, c jiraCfg) (jiraMe, error) {
	r, err := a.jiraDo(ctx, uid, c, "GET", "/rest/api/2/myself", nil)
	if err != nil {
		return jiraMe{}, err
	}
	m := jm(r)
	return jiraMe{Name: asStr(m["name"]), Key: asStr(m["key"]), Display: asStr(m["displayName"])}, nil
}

// tempoSearch: Worklogs einer Person im Zeitraum (POST …/worklogs/search {from,to,worker:[key]})
func (a *App) tempoSearch(ctx context.Context, uid int64, c jiraCfg, from, to, workerKey string) ([]map[string]any, error) {
	r, err := a.jiraDo(ctx, uid, c, "POST", tempoWL+"/search", map[string]any{"from": from, "to": to, "worker": []string{workerKey}})
	if err != nil {
		return nil, err
	}
	if _, ok := r.([]any); !ok { // nur eine Liste ist eine gültige Antwort (sonst würden Duplikat-Hinweise gelöscht)
		return nil, &jiraErr{Status: 0, Msg: "Tempo: unerwartete Antwort der Worklog-Suche"}
	}
	return listOf(r), nil
}

// tempoCreate: POST mit Slash am Ende (Tempo DC); liefert die Worklog-Id oder "" (keine geliefert)
func (a *App) tempoCreate(ctx context.Context, uid int64, c jiraCfg, body map[string]any) (string, error) {
	r, err := a.jiraDo(ctx, uid, c, "POST", tempoWL+"/", body)
	if err != nil {
		return "", err
	}
	return worklogIDOf(r), nil
}

func (a *App) tempoUpdate(ctx context.Context, uid int64, c jiraCfg, id string, body map[string]any) error {
	_, err := a.jiraDo(ctx, uid, c, "PUT", tempoWL+"/"+url.PathEscape(id), body)
	return err
}

// tempoDelete: 404 (schon weg) gilt als erledigt
func (a *App) tempoDelete(ctx context.Context, uid int64, c jiraCfg, id string) error {
	if strings.TrimSpace(id) == "" {
		return errors.New("Worklog-Id fehlt")
	}
	_, err := a.jiraDo(ctx, uid, c, "DELETE", tempoWL+"/"+url.PathEscape(id), nil)
	if jiraStatus(err) == 404 {
		return nil
	}
	return err
}

// worklogIDOf wie Node idOf: tempoWorklogId ?? id ?? worklogId, auch Erstelement einer Liste
func worklogIDOf(r any) string {
	if l, ok := r.([]any); ok {
		if len(l) == 0 {
			return ""
		}
		r = l[0]
	}
	m := jm(r)
	for _, k := range []string{"tempoWorklogId", "id", "worklogId"} {
		if v, has := m[k]; has && v != nil {
			return asStr(v)
		}
	}
	return ""
}

// tempoApproval: Stundenzettel-Status des Monats (approved/waiting/submitted = gesperrt); Text klein geschrieben
func (a *App) tempoApproval(ctx context.Context, uid int64, c jiraCfg, monthStart, userKey string) (string, error) {
	r, err := a.jiraDo(ctx, uid, c, "GET", "/rest/tempo-timesheets/4/timesheet-approval/current?periodStartDate="+url.QueryEscape(monthStart)+"&userKey="+url.QueryEscape(userKey), nil)
	if err != nil {
		return "", err
	}
	m := jm(r)
	s := ""
	if st := jm(m["status"]); st != nil {
		s = asStr(st["key"])
	} else {
		s = asStr(m["status"])
	}
	return strings.ToLower(s), nil
}

// tempoProjectAccount: Standard-Account (Key) des Jira-Projekts: project/{key} → link/project/{id} → account/{id}
func (a *App) tempoProjectAccount(ctx context.Context, uid int64, c jiraCfg, projectKey string) (string, error) {
	pr, err := a.jiraDo(ctx, uid, c, "GET", "/rest/api/2/project/"+url.PathEscape(projectKey), nil)
	if err != nil {
		return "", err
	}
	pid := asStr(jm(pr)["id"])
	if pid == "" {
		return "", nil
	}
	lr, err := a.jiraDo(ctx, uid, c, "GET", "/rest/tempo-accounts/1/link/project/"+url.PathEscape(pid), nil)
	if err != nil {
		return "", err
	}
	links := listOf(lr)
	var pick map[string]any
	for _, l := range links {
		if l["defaultAccount"] == true {
			pick = l
			break
		}
	}
	if pick == nil && len(links) > 0 {
		pick = links[0]
	}
	id := asStr(pick["accountId"])
	if id == "" {
		id = asStr(jm(pick["account"])["id"])
	}
	if id == "" {
		return "", nil
	}
	ar, err := a.jiraDo(ctx, uid, c, "GET", "/rest/tempo-accounts/1/account/"+url.PathEscape(id), nil)
	if err != nil {
		return "", err
	}
	return asStr(jm(ar)["key"]), nil
}

// tempoAccountAttrID: Id des Work-Attributs „Account“ (_Account_), 0 = nicht gefunden
func (a *App) tempoAccountAttrID(ctx context.Context, uid int64, c jiraCfg) (int, error) {
	r, err := a.jiraDo(ctx, uid, c, "GET", "/rest/tempo-core/1/work-attribute", nil)
	if err != nil {
		return 0, err
	}
	for _, x := range listOf(r) {
		if asStr(x["key"]) == "_Account_" || strings.Contains(strings.ToLower(asStr(x["name"])), "account") {
			if n := int(asFloat(x["id"])); n > 0 {
				return n, nil
			}
		}
	}
	return 0, nil
}

/* ── Tickets (Stufe 2: Anlegen nur auf Bestätigung, Übergangs-Ticket) ── */

var accountFieldRE = regexp.MustCompile(`(?i)"(customfield_[0-9]+)"\s*:\s*"[^"]*account[^"]*"`)

// jiraSearch: JQL-Suche (Treffer: key, summary)
func (a *App) jiraSearch(ctx context.Context, uid int64, c jiraCfg, jql string, max int) ([]map[string]any, error) {
	r, err := a.jiraDo(ctx, uid, c, "GET", "/rest/api/2/search?maxResults="+strconv.Itoa(max)+"&fields=summary,project,status&jql="+url.QueryEscape(jql), nil)
	if err != nil {
		return nil, err
	}
	return listOf(r), nil
}

// jiraFindSummary: vorhandenes Ticket mit genau dieser Überschrift im Projekt (JQL ist unscharf → exakt nachprüfen)
func (a *App) jiraFindSummary(ctx context.Context, uid int64, c jiraCfg, project, summary string) (string, error) {
	jql := `project = "` + jqlQuote(project) + `" AND summary ~ "\"` + jqlQuote(summary) + `\""`
	list, err := a.jiraSearch(ctx, uid, c, jql, 10)
	if err != nil {
		return "", err
	}
	for _, i := range list {
		if strings.TrimSpace(asStr(jm(i["fields"])["summary"])) == strings.TrimSpace(summary) {
			return asStr(i["key"]), nil
		}
	}
	return "", nil
}

// jiraIssueTypes: anlegbare Vorgangstypen (createmeta, ohne Unteraufgaben)
func (a *App) jiraIssueTypes(ctx context.Context, uid int64, c jiraCfg, project string) ([]string, error) {
	r, err := a.jiraDo(ctx, uid, c, "GET", "/rest/api/2/issue/createmeta/"+url.PathEscape(project)+"/issuetypes", nil)
	if err != nil {
		return nil, err
	}
	m := jm(r)
	raw, _ := m["values"].([]any)
	if raw == nil {
		raw, _ = m["issueTypes"].([]any)
	}
	var out []string
	for _, x := range raw {
		if t := jm(x); t != nil && t["subtask"] != true && asStr(t["name"]) != "" {
			out = append(out, asStr(t["name"]))
		}
	}
	return out, nil
}

// pickIssueType: eingestellter Typ, sonst Task/Aufgabe, sonst der erste (wie Node autosetup)
func pickIssueType(types []string, want string) string {
	for _, t := range types {
		if strings.EqualFold(t, want) {
			return t
		}
	}
	for _, t := range types {
		if strings.EqualFold(t, "task") || strings.EqualFold(t, "aufgabe") {
			return t
		}
	}
	if len(types) > 0 {
		return types[0]
	}
	return want
}

// tempoAccountID: Id des Standard-Accounts des Jira-Projekts, sonst des Accounts mit Schlüssel fallbackKey
func (a *App) tempoAccountID(ctx context.Context, uid int64, c jiraCfg, project, fallbackKey string) string {
	if pr, err := a.jiraDo(ctx, uid, c, "GET", "/rest/api/2/project/"+url.PathEscape(project), nil); err == nil {
		if lr, err := a.jiraDo(ctx, uid, c, "GET", "/rest/tempo-accounts/1/link/project/"+url.PathEscape(asStr(jm(pr)["id"])), nil); err == nil {
			links := listOf(lr)
			var pick map[string]any
			for _, l := range links {
				if l["defaultAccount"] == true {
					pick = l
					break
				}
			}
			if pick == nil && len(links) > 0 {
				pick = links[0]
			}
			if id := jsOr(asStr(pick["accountId"]), asStr(jm(pick["account"])["id"])); id != "" {
				return id
			}
		}
	}
	if fallbackKey != "" {
		if ar, err := a.jiraDo(ctx, uid, c, "GET", "/rest/tempo-accounts/1/account/key/"+url.PathEscape(fallbackKey), nil); err == nil {
			return asStr(jm(ar)["id"])
		}
	}
	return ""
}

// jiraCreateIssue wie Node createIssue: erst mit Zuweisung an worker, bei 400 ohne; verlangt Jira ein Account-Feld,
// wird der Account (Zahl, Text, {id}) gesetzt. Liefert den Ticket-Key.
func (a *App) jiraCreateIssue(ctx context.Context, uid int64, c jiraCfg, fields map[string]any, worker, account string) (string, error) {
	tries := []map[string]any{fields}
	if worker != "" {
		withA := map[string]any{"assignee": map[string]any{"name": worker}}
		for k, v := range fields {
			withA[k] = v
		}
		tries = []map[string]any{withA, fields}
	}
	project := asStr(jm(fields["project"])["key"])
	var lastErr error
	for _, f := range tries {
		r, err := a.jiraDo(ctx, uid, c, "POST", "/rest/api/2/issue", map[string]any{"fields": f})
		if err == nil {
			if k := asStr(jm(r)["key"]); k != "" {
				return k, nil
			}
			return "", &jiraErr{Status: 502, Msg: "Jira hat keinen Ticket-Key geliefert"}
		}
		lastErr = err
		if jiraStatus(err) != 400 {
			return "", err
		}
		if m := accountFieldRE.FindStringSubmatch(err.Error()); m != nil {
			id := a.tempoAccountID(ctx, uid, c, project, account)
			if id == "" {
				return "", &jiraErr{Status: 400, Msg: "Jira verlangt einen Tempo-Account für Tickets in " + project + " – Standard-Account der Instanz einstellen"}
			}
			n, _ := strconv.ParseFloat(id, 64)
			for _, v := range []any{n, id, map[string]any{"id": id}} {
				g := map[string]any{m[1]: v}
				for k, x := range f {
					g[k] = x
				}
				r, err := a.jiraDo(ctx, uid, c, "POST", "/rest/api/2/issue", map[string]any{"fields": g})
				if err == nil {
					if k := asStr(jm(r)["key"]); k != "" {
						return k, nil
					}
					return "", &jiraErr{Status: 502, Msg: "Jira hat keinen Ticket-Key geliefert"}
				}
				lastErr = err
				if jiraStatus(err) != 400 {
					return "", err
				}
			}
		}
	}
	return "", lastErr
}

func (a *App) jiraTransitions(ctx context.Context, uid int64, c jiraCfg, key string) ([]map[string]any, error) {
	r, err := a.jiraDo(ctx, uid, c, "GET", "/rest/api/2/issue/"+url.PathEscape(key)+"/transitions", nil)
	if err != nil {
		return nil, err
	}
	l, _ := jm(r)["transitions"].([]any)
	out := []map[string]any{}
	for _, x := range l {
		if m := jm(x); m != nil {
			out = append(out, m)
		}
	}
	return out, nil
}

func (a *App) jiraDoTransition(ctx context.Context, uid int64, c jiraCfg, key, id string) error {
	_, err := a.jiraDo(ctx, uid, c, "POST", "/rest/api/2/issue/"+url.PathEscape(key)+"/transitions", map[string]any{"transition": map[string]any{"id": id}})
	return err
}

func (a *App) jiraSetDescription(ctx context.Context, uid int64, c jiraCfg, key, desc string) error {
	_, err := a.jiraDo(ctx, uid, c, "PUT", "/rest/api/2/issue/"+url.PathEscape(key), map[string]any{"fields": map[string]any{"description": desc}})
	return err
}

// jiraIssueSummary: Ticket vorhanden? (Überschrift)
func (a *App) jiraIssueSummary(ctx context.Context, uid int64, c jiraCfg, key string) (string, error) {
	r, err := a.jiraDo(ctx, uid, c, "GET", "/rest/api/2/issue/"+url.PathEscape(key)+"?fields=summary", nil)
	if err != nil {
		return "", err
	}
	return asStr(jm(jm(r)["fields"])["summary"]), nil
}

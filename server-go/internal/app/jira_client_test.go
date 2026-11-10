package app

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const testToken = "geheim-PAT-1234567890"

/* Fake-Jira/Tempo als httptest.Server – ohne offenen Port: Verbindungen laufen im Speicher (net.Pipe),
   damit die Tests auch dort laufen, wo kein lokaler Port geöffnet werden darf. */

type memLn struct {
	addr   memAddr
	ch     chan net.Conn
	closed chan struct{}
	once   sync.Once
}

type memAddr string

func (m memAddr) Network() string { return "tcp" }
func (m memAddr) String() string  { return string(m) }

func (l *memLn) Accept() (net.Conn, error) {
	select {
	case c := <-l.ch:
		return c, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}
func (l *memLn) Close() error   { l.once.Do(func() { close(l.closed) }); return nil }
func (l *memLn) Addr() net.Addr { return l.addr }

var (
	memMu   sync.Mutex
	memNets = map[string]*memLn{}
	memPort = 20000
)

func memDial(ctx context.Context, _, addr string) (net.Conn, error) {
	memMu.Lock()
	l := memNets[addr]
	memMu.Unlock()
	if l == nil {
		return nil, errors.New("connect: connection refused")
	}
	c1, c2 := net.Pipe()
	select {
	case l.ch <- c2:
		return c1, nil
	case <-l.closed:
		return nil, errors.New("connect: connection refused")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func startFake(t *testing.T, h http.Handler, useTLS bool) *httptest.Server {
	t.Helper()
	memMu.Lock()
	memPort++
	addr := fmt.Sprintf("127.0.0.1:%d", memPort)
	l := &memLn{addr: memAddr(addr), ch: make(chan net.Conn), closed: make(chan struct{})}
	memNets[addr] = l
	memMu.Unlock()
	srv := &httptest.Server{Listener: l, Config: &http.Server{Handler: h}}
	if useTLS {
		srv.StartTLS()
	} else {
		srv.Start()
	}
	t.Cleanup(func() {
		srv.Close()
		memMu.Lock()
		delete(memNets, addr)
		memMu.Unlock()
	})
	return srv
}

func memApp() *App { return &App{jiraDialHook: memDial} }

func fakeJira(t *testing.T, h http.HandlerFunc) (*App, jiraCfg) {
	t.Helper()
	srv := startFake(t, h, false)
	return memApp(), jiraCfg{Base: srv.URL, Token: testToken}
}

func TestJiraClientAuthAndPaths(t *testing.T) {
	var seen []string
	a, c := fakeJira(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+testToken || r.Header.Get("Accept") != "application/json" {
			w.WriteHeader(401)
			return
		}
		if r.Method != "GET" && r.Method != "DELETE" && r.Header.Get("Content-Type") != "application/json" {
			w.WriteHeader(415)
			return
		}
		seen = append(seen, r.Method+" "+r.URL.RequestURI())
		switch {
		case r.URL.Path == "/rest/api/2/myself":
			_, _ = io.WriteString(w, `{"name":"user01","key":"JIRAUSER123","displayName":"C. Mustermann"}`)
		case r.Method == "POST" && r.URL.Path == "/rest/tempo-timesheets/4/worklogs/":
			var b map[string]any
			_ = json.NewDecoder(r.Body).Decode(&b)
			if b["worker"] != "JIRAUSER123" || b["originTaskId"] != "AB-1" || b["timeSpentSeconds"] != float64(60) {
				w.WriteHeader(400)
				return
			}
			_, _ = io.WriteString(w, `[{"tempoWorklogId":4711}]`)
		case r.Method == "POST" && r.URL.Path == "/rest/tempo-timesheets/4/worklogs/search":
			_, _ = io.WriteString(w, `[{"tempoWorklogId":1,"started":"2026-10-06 09:00:00.000","timeSpentSeconds":60}]`)
		case r.Method == "PUT" && r.URL.Path == "/rest/tempo-timesheets/4/worklogs/4711":
			w.WriteHeader(200)
		case r.Method == "PUT" || r.Method == "DELETE":
			w.WriteHeader(404)
		case r.URL.Path == "/rest/tempo-timesheets/4/timesheet-approval/current":
			_, _ = io.WriteString(w, `{"status":{"key":"APPROVED"}}`)
		default:
			w.WriteHeader(404)
		}
	})
	ctx := context.Background()
	me, err := a.jiraMyself(ctx, 1, c)
	if err != nil || me.Key != "JIRAUSER123" || me.Name != "user01" {
		t.Fatalf("myself: %+v %v", me, err)
	}
	id, err := a.tempoCreate(ctx, 1, c, map[string]any{"worker": "JIRAUSER123", "originTaskId": "AB-1", "timeSpentSeconds": 60})
	if err != nil || id != "4711" {
		t.Fatalf("create: %q %v", id, err)
	}
	if err := a.tempoUpdate(ctx, 1, c, "4711", map[string]any{"x": 1}); err != nil {
		t.Errorf("update: %v", err)
	}
	if err := a.tempoUpdate(ctx, 1, c, "999", map[string]any{"x": 1}); jiraStatus(err) != 404 {
		t.Errorf("PUT 404 muss als 404 ankommen (→ neu anlegen): %v", err)
	}
	if err := a.tempoDelete(ctx, 1, c, "999"); err != nil {
		t.Errorf("DELETE 404 gilt als erledigt: %v", err)
	}
	wl, err := a.tempoSearch(ctx, 1, c, "2026-10-01", "2026-10-31", "JIRAUSER123")
	if err != nil || len(wl) != 1 {
		t.Errorf("search: %v %v", wl, err)
	}
	st, err := a.tempoApproval(ctx, 1, c, "2026-10-01", "JIRAUSER123")
	if err != nil || st != "approved" {
		t.Errorf("approval: %q %v", st, err)
	}
	want := []string{"GET /rest/api/2/myself", "POST /rest/tempo-timesheets/4/worklogs/", "PUT /rest/tempo-timesheets/4/worklogs/4711",
		"PUT /rest/tempo-timesheets/4/worklogs/999", "DELETE /rest/tempo-timesheets/4/worklogs/999", "POST /rest/tempo-timesheets/4/worklogs/search",
		"GET /rest/tempo-timesheets/4/timesheet-approval/current?periodStartDate=2026-10-01&userKey=JIRAUSER123"}
	if strings.Join(seen, "\n") != strings.Join(want, "\n") {
		t.Errorf("Aufrufe:\n%s", strings.Join(seen, "\n"))
	}
}

func TestJiraClientErrors(t *testing.T) {
	a, c := fakeJira(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rest/api/2/myself":
			w.WriteHeader(401)
			_, _ = io.WriteString(w, `{"errorMessages":["Nicht angemeldet"]}`)
		case "/rest/tempo-timesheets/4/worklogs/":
			w.WriteHeader(403) // Server spiegelt den Kopf zurück: darf nie in Fehlern auftauchen
			_, _ = io.WriteString(w, `{"message":"denied for `+r.Header.Get("Authorization")+`"}`)
		case "/rest/tempo-timesheets/4/worklogs/1":
			w.WriteHeader(400)
			_, _ = io.WriteString(w, `{"errors":{"started":"Period is locked"}}`)
		default:
			w.WriteHeader(500)
			_, _ = io.WriteString(w, "kaputt")
		}
	})
	ctx := context.Background()
	_, err := a.jiraMyself(ctx, 1, c)
	if jiraStatus(err) != 401 || err.Error() != "Jira HTTP 401: Nicht angemeldet – Token prüfen" {
		t.Errorf("401: %v", err)
	}
	_, err = a.tempoCreate(ctx, 1, c, map[string]any{})
	if jiraStatus(err) != 403 || !strings.HasPrefix(err.Error(), "Tempo HTTP 403") || strings.Contains(err.Error(), testToken) || strings.Contains(jiraDetail(err), testToken) {
		t.Errorf("403/Token: %v", err)
	}
	if !tempoAbort(jiraStatus(err), jiraDetail(err)) {
		t.Error("403 ohne Sperrtext bricht ab")
	}
	err = a.tempoUpdate(ctx, 1, c, "1", map[string]any{})
	if !tempoLockError(jiraStatus(err), jiraDetail(err)) || !strings.Contains(err.Error(), `{"started":"Period is locked"}`) {
		t.Errorf("Sperre aus errors: %v", err)
	}
	_, err = a.jiraDo(ctx, 1, c, "GET", "/rest/tempo-core/1/x", nil)
	if jiraStatus(err) != 500 || err.Error() != "Tempo HTTP 500: kaputt" {
		t.Errorf("500: %v", err)
	}
	if _, err := a.jiraDo(ctx, 1, jiraCfg{Base: c.Base}, "GET", "/rest/api/2/myself", nil); jiraStatus(err) != 0 || err == nil {
		t.Errorf("ohne Token: %v", err)
	}
	_, err = a.jiraDo(ctx, 1, jiraCfg{Base: "http://127.0.0.1:1", Token: testToken}, "GET", "/rest/api/2/myself", nil) // niemand hört dort
	if jiraStatus(err) != 0 || strings.Contains(err.Error(), testToken) || !strings.Contains(err.Error(), "nicht erreichbar") {
		t.Errorf("Netzfehler: %v", err)
	}
}

func TestJiraClientRetry429(t *testing.T) {
	old := jiraMinWait
	jiraMinWait = time.Millisecond
	defer func() { jiraMinWait = old }()
	var n atomic.Int32
	a, c := fakeJira(t, func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) <= 2 {
			w.Header().Set("Retry-After", "0.01")
			w.WriteHeader(429)
			return
		}
		_, _ = io.WriteString(w, `{"name":"x","key":"K"}`)
	})
	if me, err := a.jiraMyself(context.Background(), 1, c); err != nil || me.Key != "K" || n.Load() != 3 {
		t.Errorf("429 wiederholen: %v %v n=%d", me, err, n.Load())
	}
	n.Store(-100) // immer 429
	_, err := a.jiraMyself(context.Background(), 1, c)
	if jiraStatus(err) != 429 || n.Load() != -96 {
		t.Errorf("höchstens 3 Wiederholungen: %v n=%d", err, n.Load()+100)
	}
	var m atomic.Int32
	a2, c2 := fakeJira(t, func(w http.ResponseWriter, r *http.Request) { m.Add(1); w.WriteHeader(503) })
	if _, err := a2.jiraMyself(context.Background(), 1, c2); jiraStatus(err) != 503 || m.Load() != 1 {
		t.Errorf("5xx nicht wiederholen: n=%d", m.Load())
	}
	if jiraRetryWait(0, 0) != 4*time.Second || jiraRetryWait(0, 9) != 30*time.Second || jiraRetryWait(5*time.Second, 0) != 5*time.Second {
		t.Error("Wartezeit wie Node")
	}
	jiraMinWait = 2 * time.Second
	if jiraRetryWait(time.Millisecond, 0) != 2*time.Second {
		t.Error("mindestens 2 s")
	}
}

func TestJiraClientNoRedirect(t *testing.T) {
	var hits atomic.Int32
	target := startFake(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }), false)
	a, c := fakeJira(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/login", http.StatusFound)
	})
	_, err := a.tempoCreate(context.Background(), 1, c, map[string]any{"a": 1})
	if err == nil || jiraStatus(err) != 0 || !strings.Contains(err.Error(), "leitet um") || hits.Load() != 0 {
		t.Errorf("Umleitung darf nicht gefolgt werden: %v hits=%d", err, hits.Load())
	}
}

func TestJiraClientTLSExtraCA(t *testing.T) {
	srv := startFake(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"name":"x","key":"K"}`)
	}), true)
	c := jiraCfg{Base: srv.URL, Token: testToken}
	if _, err := memApp().jiraMyself(context.Background(), 1, c); err == nil || !strings.Contains(err.Error(), "Zertifikat") {
		t.Errorf("unbekannte CA muss scheitern: %v", err)
	}
	f := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(f, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	if me, err := (&App{ExtraCAFile: f, jiraDialHook: memDial}).jiraMyself(context.Background(), 1, c); err != nil || me.Key != "K" {
		t.Errorf("mit ZP_EXTRA_CA_FILE: %v", err)
	}
	bad := filepath.Join(t.TempDir(), "leer.pem")
	_ = os.WriteFile(bad, []byte("kein Zertifikat"), 0o600)
	if _, err := (&App{ExtraCAFile: bad, jiraDialHook: memDial}).jiraMyself(context.Background(), 1, c); err == nil || !strings.Contains(err.Error(), "ZP_EXTRA_CA_FILE") {
		t.Errorf("kaputte CA-Datei: %v", err)
	}
}

func TestJiraAccountHelpers(t *testing.T) {
	a, c := fakeJira(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rest/api/2/project/AB":
			_, _ = io.WriteString(w, `{"id":"10001","key":"AB"}`)
		case "/rest/tempo-accounts/1/link/project/10001":
			_, _ = io.WriteString(w, `[{"accountId":5},{"accountId":7,"defaultAccount":true}]`)
		case "/rest/tempo-accounts/1/account/7":
			_, _ = io.WriteString(w, `{"id":7,"key":"IMPROVEMENTS"}`)
		case "/rest/tempo-core/1/work-attribute":
			_, _ = io.WriteString(w, `[{"id":1,"key":"_Typ_","name":"Typ"},{"id":3,"key":"_Account_","name":"Account"}]`)
		default:
			w.WriteHeader(404)
		}
	})
	ctx := context.Background()
	if k, err := a.tempoProjectAccount(ctx, 1, c, "AB"); err != nil || k != "IMPROVEMENTS" {
		t.Errorf("Standard-Account: %q %v", k, err)
	}
	if id, err := a.tempoAccountAttrID(ctx, 1, c); err != nil || id != 3 {
		t.Errorf("Attribut-Id: %d %v", id, err)
	}
}

func TestJiraDialAndURL(t *testing.T) {
	for addr, ok := range map[string]bool{"127.0.0.1:443": true, "10.1.2.3:443": true, "192.168.0.5:8080": true, "[::1]:443": true,
		"169.254.169.254:80": false, "[fe80::1]:80": false, "100.100.100.200:80": false, "0.0.0.0:80": false, "[fd00:ec2::254]:80": false} {
		if err := denyLinkLocal("tcp", addr, nil); (err == nil) != ok {
			t.Errorf("%s: erlaubt=%v, Fehler %v", addr, ok, err)
		}
	}
	a := &App{}
	for in, want := range map[string]string{"https://jira.firma.local/": "https://jira.firma.local", "https://jira.x/jira": "https://jira.x/jira",
		"http://localhost:8080": "http://localhost:8080", "": ""} {
		if got, err := a.validateJiraURL(in); err != nil || got != want {
			t.Errorf("%q → %q %v", in, got, err)
		}
	}
	for _, in := range []string{"http://jira.firma.local", "https://u:p@jira.x", "ftp://jira.x", "https://jira.x/?a=1", "jira.x"} {
		if _, err := a.validateJiraURL(in); err == nil {
			t.Errorf("%q müsste abgelehnt werden", in)
		}
	}
	if v, err := (&App{AllowHTTP: true}).validateJiraURL("http://jira.intern"); err != nil || v != "http://jira.intern" {
		t.Errorf("ZP_ALLOW_HTTP: %v", err)
	}
}

func TestJiraClientNonJSONSuccess(t *testing.T) {
	a, c := fakeJira(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/rest/tempo-timesheets/4/worklogs/search" {
			_, _ = io.WriteString(w, `{"results":[]}`) // keine Liste
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, "<html>Anmeldung</html>")
	})
	ctx := context.Background()
	if err := a.tempoDelete(ctx, 1, c, "5"); err == nil || jiraStatus(err) != 0 {
		t.Errorf("HTML mit 200 darf kein Erfolg sein: %v", err)
	}
	if _, err := a.tempoSearch(ctx, 1, c, "2026-10-01", "2026-10-31", "K"); err == nil {
		t.Error("Suche ohne Liste muss Fehler sein (sonst gehen Duplikat-Hinweise verloren)")
	}
}

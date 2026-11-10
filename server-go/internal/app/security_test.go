package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"example/zeitprotokoll-server/internal/crypto"
)

func TestSecurityHeaders(t *testing.T) {
	a := &App{}
	h := a.Protect(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	for _, p := range []string{"/api/me", "/admin", "/setup", "/index.html"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", "http://localhost:8090"+p, nil))
		for _, k := range []string{"X-Content-Type-Options", "Referrer-Policy", "X-Frame-Options", "Content-Security-Policy"} {
			if rec.Header().Get(k) == "" {
				t.Errorf("%s: Kopf %s fehlt", p, k)
			}
		}
		if !strings.Contains(rec.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") {
			t.Errorf("%s: frame-ancestors fehlt", p)
		}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "http://localhost:8090/api/x", nil))
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Error("API muss no-store senden")
	}
}

func TestHostCheckWithPublicURL(t *testing.T) {
	a := &App{PublicURL: "https://zeit.example.com"}
	h := a.Protect(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	do := func(host, path string) int {
		req := httptest.NewRequest("GET", "http://"+host+path, nil)
		req.Host = host
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	if c := do("zeit.example.com", "/api/me"); c != 204 {
		t.Errorf("richtiger Host muss durchgehen, war %d", c)
	}
	if c := do("evil.example", "/api/me"); c != http.StatusMisdirectedRequest {
		t.Errorf("fremder Host muss abgelehnt werden, war %d", c)
	}
	if c := do("10.1.2.3:8080", "/healthz"); c != 204 {
		t.Errorf("Probe /healthz muss mit beliebigem Host gehen, war %d", c)
	}
}

func TestRateLimit(t *testing.T) {
	l := newRateLimiter(1, 3)
	ok := 0
	for i := 0; i < 10; i++ {
		if l.allow("1.2.3.4") {
			ok++
		}
	}
	if ok != 3 {
		t.Errorf("Spitze 3 erwartet, war %d", ok)
	}
	if !l.allow("5.6.7.8") {
		t.Error("andere Adresse darf nicht betroffen sein")
	}
}

func TestFailLimiter(t *testing.T) {
	f := newFailLimiter(3, time.Minute)
	for i := 0; i < 3; i++ {
		if f.blocked("x") {
			t.Fatal("zu früh gesperrt")
		}
		f.fail("x")
	}
	if !f.blocked("x") {
		t.Error("nach 3 Fehlversuchen muss gesperrt sein")
	}
	if f.blocked("y") {
		t.Error("andere Adresse darf nicht gesperrt sein")
	}
}

func TestHostLoopback(t *testing.T) {
	for h, want := range map[string]bool{"localhost:8090": true, "127.0.0.1:1": true, "[::1]:8090": true, "evil.example": false, "evil.example:80": false, "localhost.evil.com": false} {
		if hostLoopback(h) != want {
			t.Errorf("hostLoopback(%q) sollte %v sein", h, want)
		}
	}
}

func TestValidatePlaneURL(t *testing.T) {
	a := &App{}
	for raw, ok := range map[string]bool{
		"https://plane.example.com": true, "https://plane.example.com/": true, "http://localhost:8000": true,
		"http://plane.example.com": false, "https://user:pw@plane.example.com": false, "ftp://x": false, "javascript:alert(1)": false,
	} {
		_, err := a.validatePlaneURL(raw)
		if (err == nil) != ok {
			t.Errorf("validatePlaneURL(%q): erwartet gültig=%v, Fehler=%v", raw, ok, err)
		}
	}
	if _, err := (&App{AllowHTTP: true}).validatePlaneURL("http://plane.intern"); err != nil {
		t.Error("mit ZP_ALLOW_HTTP muss http gehen")
	}
}

func TestSecretBinding(t *testing.T) {
	key := make([]byte, 32)
	blob, err := crypto.SealAAD(key, []byte("geheim"), secretAAD(1, "plane_key"))
	if err != nil {
		t.Fatal(err)
	}
	if p, err := crypto.OpenAAD(key, blob, secretAAD(1, "plane_key")); err != nil || string(p) != "geheim" {
		t.Fatalf("Entschlüsseln mit passender Bindung: %v %q", err, p)
	}
	if _, err := crypto.OpenAAD(key, blob, secretAAD(2, "plane_key")); err == nil {
		t.Error("Chiffrat einer anderen Person darf nicht lesbar sein")
	}
	if _, err := crypto.OpenAAD(key, blob, secretAAD(1, "jira_token")); err == nil {
		t.Error("Chiffrat anderer Art darf nicht lesbar sein")
	}
	other := make([]byte, 32)
	other[0] = 1
	if _, err := crypto.OpenAAD(other, blob, secretAAD(1, "plane_key")); err == nil {
		t.Error("falscher Schlüssel darf nicht lesbar sein")
	}
	// alte Chiffrate ohne Bindung bleiben lesbar (Übernahme)
	old, _ := crypto.Seal(key, []byte("alt"))
	if p, err := crypto.OpenAAD(key, old, secretAAD(1, "plane_key")); err != nil || string(p) != "alt" {
		t.Errorf("altes Format muss lesbar bleiben: %v", err)
	}
}

func TestSettingKeyValidation(t *testing.T) {
	for k, ok := range map[string]bool{"target_mon": true, "presence_track": true, "A": false, "x y": false, "a;drop": false, "": false} {
		if settingKeyRE.MatchString(k) != ok {
			t.Errorf("Einstellungsschlüssel %q: erwartet %v", k, ok)
		}
	}
}

func TestClientIPFromRightmostForwardedFor(t *testing.T) {
	a := &App{TrustProxy: true, TrustedProxies: 1}
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "10.0.0.1:5555"
	req.Header.Set("X-Forwarded-For", "6.6.6.6, 1.2.3.4") // vorne gefälscht, hinten vom Router
	if ip := a.clientIP(req); ip != "1.2.3.4" {
		t.Errorf("erwartet die vom Router angehängte Adresse 1.2.3.4, war %s", ip)
	}
	b := &App{}
	if ip := b.clientIP(req); ip != "10.0.0.1" {
		t.Errorf("ohne TrustProxy zählt RemoteAddr, war %s", ip)
	}
}

func TestRateLimiterHardCap(t *testing.T) {
	l := newRateLimiter(1, 1)
	for i := 0; i < 30000; i++ {
		l.allow(string(rune(i)) + "x" + time.Now().String())
	}
	l.mu.Lock()
	n := len(l.m)
	l.mu.Unlock()
	if n > 20001 {
		t.Errorf("Obergrenze überschritten: %d", n)
	}
}

func TestPlaneScopeDetectsSchemeChange(t *testing.T) {
	if planeScope("https://Plane.Example.com/") != planeScope("https://plane.example.com") {
		t.Error("Großschreibung/Schrägstrich dürfen keinen Wechsel ergeben")
	}
	if planeScope("https://plane.example.com") == planeScope("http://plane.example.com") {
		t.Error("https→http muss als Wechsel gelten")
	}
	if planeScope("https://plane.example.com") == planeScope("https://plane.example.com:8443") {
		t.Error("anderer Port muss als Wechsel gelten")
	}
}

// Der Notfallzugang darf nur Betrieb: nie Zeitdaten, Projekte, Tickets, Berichte, Plane-Übertragung …
func TestEmergencyAllowListHasNoDataRoutes(t *testing.T) {
	forbidden := []string{"/api/time-entries", "/api/projects", "/api/tasks", "/api/overview", "/api/report", "/api/timesheet", "/api/grid",
		"/api/plane", "/api/integrations", "/api/sources", "/api/plan", "/api/absences", "/api/month-closings", "/api/settings",
		"/api/profile", "/api/timer", "/api/day-overview", "/api/checks", "/api/overlaps", "/api/balance-corrections", "/api/admin/users/", "/api/admin/local-users"}
	for pattern := range emergencyAllowed {
		path := pattern[strings.Index(pattern, " ")+1:]
		for _, f := range forbidden {
			if strings.HasPrefix(path, f) {
				t.Errorf("Notfallzugang darf %q nicht erlauben", pattern)
			}
		}
		if !strings.HasPrefix(path, "/api/admin/") && path != "/api/me" && !strings.HasPrefix(path, "/api/auth/") {
			t.Errorf("unerwartete Route in der Allow-List: %q", pattern)
		}
	}
	for _, must := range []string{"GET /api/admin/status", "GET /api/admin/logs", "POST /api/admin/emergency/close-month"} {
		if !emergencyAllowed[must] {
			t.Errorf("%q muss erlaubt sein", must)
		}
	}
}

func TestUsernamePolicy(t *testing.T) {
	for u, ok := range map[string]bool{"user01": true, "a.b-c_d": true, "ab": false, "Admin": false, "x y": false, "-abc": false, "a;drop table": false} {
		if usernameRE.MatchString(u) != ok {
			t.Errorf("Benutzername %q: erwartet %v", u, ok)
		}
	}
}

func TestFailLimiterReserveIsAtomicAndEvictsPartially(t *testing.T) {
	f := newFailLimiter(3, time.Minute)
	got := 0
	for i := 0; i < 10; i++ {
		if f.reserve("a") {
			got++
		}
	}
	if got != 3 {
		t.Errorf("reserve darf höchstens 3-mal gelingen, war %d", got)
	}
	f.release("a")
	if !f.reserve("a") {
		t.Error("nach release muss wieder ein Versuch frei sein")
	}
	// Überlauf: ein echtes Konto bleibt nicht zwingend, aber der Speicher wächst nie über die Grenze und wird nie komplett geleert
	g := newFailLimiter(5, time.Hour)
	for i := 0; i < 25000; i++ {
		g.reserve(string(rune(i + 1000)))
	}
	g.mu.Lock()
	n := len(g.m)
	g.mu.Unlock()
	if n > 20001 || n == 0 {
		t.Errorf("Speicher begrenzt und nicht leer erwartet, war %d", n)
	}
}

func TestPasswordByteLimit(t *testing.T) {
	long := strings.Repeat("ä", 150) + "Aa1" // 153 Zeichen, aber über 300 Bytes
	if err := ValidatePassword(long, "x"); err != nil {
		t.Errorf("langes Passwort mit Umlauten bis 400 Bytes muss gehen: %v", err)
	}
	tooLong := strings.Repeat("ä", 199) + "Aa1" // 202 Zeichen
	if ValidatePassword(tooLong, "x") == nil {
		t.Error("über 200 Zeichen muss abgelehnt werden")
	}
}

// Die Seiten mit eigenem Skript (Einrichtung, Administration, Anmeldung) brauchen script-src, sonst tut der Browser nichts.
func TestPagesAllowOwnScript(t *testing.T) {
	a := &App{}
	for _, p := range []string{"/setup", "/admin", "/login"} {
		w := httptest.NewRecorder()
		a.securityHeaders(w, httptest.NewRequest("GET", p, nil))
		if csp := w.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "script-src 'self' 'unsafe-inline'") {
			t.Errorf("%s: kein script-src in der CSP: %s", p, csp)
		}
	}
}

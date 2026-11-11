package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIsBrowserExtensionOrigin(t *testing.T) {
	ok := []string{
		"chrome-extension://abcdefghijklmnopqrstuvwxyz",
		"moz-extension://12345678-1234-1234-1234-123456789abc",
		"safari-web-extension://abc",
	}
	for _, o := range ok {
		if !isBrowserExtensionOrigin(o) {
			t.Fatalf("erwartet ok: %s", o)
		}
	}
	for _, o := range []string{"", "https://evil.example", "chrome-extension://", "null"} {
		if isBrowserExtensionOrigin(o) {
			t.Fatalf("erwartet nein: %s", o)
		}
	}
}

func TestAddonCSRFExempt(t *testing.T) {
	for _, p := range []string{"/api/pair/start", "/api/ext/state", "/api/todo", "/api/ticket/start"} {
		if !addonCSRFExempt(p) {
			t.Fatalf("exempt fehlt: %s", p)
		}
	}
	if addonCSRFExempt("/api/admin/users") || addonCSRFExempt("/api/entries") {
		t.Fatal("Admin/Einträge dürfen nicht exempt sein")
	}
}

func TestProtectAllowsExtensionPairStart(t *testing.T) {
	a := &App{}
	var hit bool
	h := a.Protect(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest("POST", "/api/pair/start", strings.NewReader(`{"client":"Firefox"}`))
	req.Host = "zeit.example"
	req.Header.Set("Origin", "chrome-extension://abcdefghijklmnopqrstuvwxyz")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if !hit || rr.Code != http.StatusNoContent {
		t.Fatalf("Pair-Start vom Add-on muss durch Protect: code=%d hit=%v body=%s", rr.Code, hit, rr.Body.String())
	}
}

func TestProtectRejectsExtensionOnAdmin(t *testing.T) {
	a := &App{}
	h := a.Protect(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("darf nicht durchkommen")
	}))
	req := httptest.NewRequest("POST", "/api/admin/users", strings.NewReader(`{}`))
	req.Host = "zeit.example"
	req.Header.Set("Origin", "chrome-extension://abcdefghijklmnopqrstuvwxyz")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("erwartet 403, got %d", rr.Code)
	}
}

func TestHasDeviceAuthHeader(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	if hasDeviceAuthHeader(req) {
		t.Fatal("leer")
	}
	req.Header.Set("X-ZP-Token", "zpdev_x")
	if !hasDeviceAuthHeader(req) {
		t.Fatal("X-ZP-Token")
	}
}

func TestPairStartPollApproveShape(t *testing.T) {
	// Nur In-Memory-Pfad ohne DB: Start + Poll pending
	a := &App{PublicURL: "https://zeit.example"}
	mux := http.NewServeMux()
	a.routesPairing(mux)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/pair/start", strings.NewReader(`{"client":"Firefox","ext_version":"1.0"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "moz-extension://abcd")
	mux.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("start %d %s", rr.Code, rr.Body.String())
	}
	var start map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &start); err != nil {
		t.Fatal(err)
	}
	nonce, _ := start["nonce"].(string)
	if nonce == "" || !strings.Contains(asStr(start["approve_url"]), "/#koppeln/") {
		t.Fatalf("start body %+v", start)
	}

	rr2 := httptest.NewRecorder()
	mux.ServeHTTP(rr2, httptest.NewRequest("GET", "/api/pair/poll?nonce="+nonce, nil))
	var poll map[string]any
	_ = json.Unmarshal(rr2.Body.Bytes(), &poll)
	if rr2.Code != 200 || poll["pending"] != true {
		t.Fatalf("poll pending: %d %+v", rr2.Code, poll)
	}
}

func TestDeviceUserAcceptsXZPToken(t *testing.T) {
	a := &App{}
	req := httptest.NewRequest("GET", "/api/ext/state", nil)
	req.Header.Set("X-ZP-Token", "not-a-real-token")
	if _, _, ok := a.deviceUser(req); ok {
		t.Fatal("ungültiges Token darf nicht ok sein")
	}
	// Prefix-Prüfung: ohne zpdev_ sofort nein
	req2 := httptest.NewRequest("GET", "/", nil)
	req2.Header.Set("X-ZP-Token", "deadbeef")
	if _, _, ok := a.deviceUser(req2); ok {
		t.Fatal("hex ohne Prefix")
	}
}

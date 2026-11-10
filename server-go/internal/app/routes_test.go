package app

import (
	"net/http"
	"testing"
)

// Alle Routen müssen sich ohne Muster-Konflikt registrieren lassen (ein Konflikt wäre ein Panic beim Start).
func TestRoutesRegister(t *testing.T) {
	a := &App{}
	mux := http.NewServeMux()
	a.Routes(mux)
	for _, p := range [][2]string{
		{"POST", "/api/absences/import-holidays"}, {"PUT", "/api/absences/5"}, {"GET", "/api/plan/today"},
		{"GET", "/api/integrations/plane/status"}, {"POST", "/api/time-entries/resolve-overlap"},
		{"GET", "/api/admin/plane-workspaces"}, {"POST", "/api/admin/plane-workspaces"},
		{"GET", "/api/admin/update"}, {"GET", "/api/admin/status"},
		{"GET", "/api/admin/integrations"}, {"GET", "/api/integrations/projects"}, {"GET", "/api/integrations/payroll"},
		{"GET", "/api/plane/workspaces"}, {"PUT", "/api/plane/workspaces/1/select"}, {"GET", "/api/admin/ldap"}, {"PUT", "/api/admin/oidc"},
	} {
		req, _ := http.NewRequest(p[0], p[1], nil)
		if _, pat := mux.Handler(req); pat == "" {
			t.Errorf("%s %s: keine passende Route", p[0], p[1])
		}
	}
}

func TestProtectBlocksForeignOrigin(t *testing.T) {
	a := &App{}
	h := a.Protect(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	do := func(method, origin string) int {
		req, _ := http.NewRequest(method, "http://localhost:8090/api/x", nil)
		req.Host = "localhost:8090"
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		rec := &recorder{}
		h.ServeHTTP(rec, req)
		return rec.code
	}
	if c := do("POST", ""); c != 204 {
		t.Errorf("ohne Origin (curl/App) muss durchgehen, war %d", c)
	}
	if c := do("POST", "http://localhost:8090"); c != 204 {
		t.Errorf("gleiche Adresse muss durchgehen, war %d", c)
	}
	if c := do("POST", "https://evil.example"); c != 403 {
		t.Errorf("fremde Adresse muss 403 sein, war %d", c)
	}
	if c := do("PUT", "null"); c != 403 {
		t.Errorf("Origin null muss 403 sein, war %d", c)
	}
	if c := do("GET", "https://evil.example"); c != 204 {
		t.Errorf("GET wird nicht geprüft, war %d", c)
	}
}

type recorder struct {
	code int
	hdr  http.Header
}

func (r *recorder) Header() http.Header {
	if r.hdr == nil {
		r.hdr = http.Header{}
	}
	return r.hdr
}
func (r *recorder) Write(b []byte) (int, error) { return len(b), nil }
func (r *recorder) WriteHeader(c int)           { r.code = c }

package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Berechtigungsmatrix: ohne passende Rolle 404 – vor jeder Datenbankabfrage (App ohne Datenbank)
func TestOverviewPermissionMatrix(t *testing.T) {
	a := &App{}
	type h func(http.ResponseWriter, *http.Request, User)
	lead, hr := []h{a.teamOverview, a.teamMembers}, []h{a.personalOverview, a.personalPeople}
	cases := []struct {
		name         string
		u            User
		leadOK, hrOK bool
	}{
		{"Mitarbeiter", User{ID: 1, Role: "user", Kind: "ldap", Productive: true}, false, false},
		{"Admin ohne Merkmale", User{ID: 2, Role: "admin", Kind: "ldap", Productive: true}, false, false},
		{"Notfallzugang mit Merkmalen", User{ID: 3, Role: "breakglass", Kind: "local", IsHR: true, IsLead: true}, false, false},
		{"Admin-Testkonto mit Merkmalen", User{ID: 4, Role: "admin", Kind: "local", Productive: false, IsHR: true, IsLead: true}, false, false},
		{"Teamleiter", User{ID: 5, Role: "user", Kind: "ldap", Productive: true, IsLead: true}, true, false},
		{"Personal", User{ID: 6, Role: "user", Kind: "oidc", Productive: true, IsHR: true}, false, true},
	}
	for _, c := range cases {
		if leadAllowed(c.u) != c.leadOK || hrAllowed(c.u) != c.hrOK {
			t.Errorf("%s: lead=%v hr=%v", c.name, leadAllowed(c.u), hrAllowed(c.u))
		}
		check := func(list []h, ok bool) {
			if ok {
				return // berechtigt: würde die Datenbank fragen
			}
			for _, f := range list {
				rec := httptest.NewRecorder()
				f(rec, httptest.NewRequest("GET", "/api/x?month=2026-10", nil), c.u)
				if rec.Code != http.StatusNotFound {
					t.Errorf("%s: erwartet 404, war %d", c.name, rec.Code)
				}
			}
		}
		check(lead, c.leadOK)
		check(hr, c.hrOK)
	}
}

func TestOverviewHelpers(t *testing.T) {
	for in, want := range map[string]string{"": "open", "submitted": "submitted", "closed": "closed", "rejected": "rejected", "exported": "unknown", "<x>": "unknown"} {
		s := in
		got, raw := normStatus(&s)
		if got != want {
			t.Errorf("normStatus(%q)=%q", in, got)
		}
		if in == "exported" && raw != "exported" || in == "<x>" && raw != nil {
			t.Errorf("status_raw %q: %v", in, raw)
		}
	}
	if s, _ := normStatus(nil); s != "open" {
		t.Error("kein Eintrag = open")
	}
	if _, f, l, ok := monthRange("2026-02"); !ok || f != "2026-02-01" || l != "2026-02-28" {
		t.Errorf("Februar: %s %s", f, l)
	}
	for _, bad := range []string{"2026-13", "1999-12", "26-01", "2026-1"} {
		if _, _, _, ok := monthRange(bad); ok {
			t.Errorf("%q müsste ungültig sein", bad)
		}
	}
	if likeArg(`a%_\b`) != `a\%\_\\b` {
		t.Errorf("likeArg: %q", likeArg(`a%_\b`))
	}
	r := httptest.NewRequest("GET", "/x?limit=5000&offset=-3", nil)
	if l, o := paging(r, 50); l != 200 || o != 0 {
		t.Errorf("paging: %d %d", l, o)
	}
	if l, _ := paging(httptest.NewRequest("GET", "/x", nil), 50); l != 50 {
		t.Error("Standard 50")
	}
	days := []ovDay{
		{TargetH: 8, WorkH: 9, BalanceH: 1, CumulativeBal: 11},
		{TargetH: 8, AbsenceH: 8, Absence: map[string]any{"type": "urlaub"}, WorkH: 2, BalanceH: 0, CumulativeBal: 11},
		{TargetH: 8, IsBeforeTracking: false, WorkH: 0, BalanceH: -8, CumulativeBal: 3, IsToday: true},
		{TargetH: 8, IsFuture: true, CumulativeBal: 3},
	}
	soll, sollHeute, ist, saldo, konto := monthFigures(days)
	if soll != 32 || sollHeute != 24 || ist != 17 || saldo != -7 || konto != 3.0 {
		t.Errorf("monthFigures: %v %v %v %v %v", soll, sollHeute, ist, saldo, konto)
	}
	if _, _, _, _, k := monthFigures([]ovDay{{TargetH: 8, IsFuture: true}}); k != nil {
		t.Error("Zukunftsmonat: kein Kontostand")
	}
}

// Antwortform einer Übersichtszeile (Feldnamen für den Oberflächen-Bau)
func TestPersonRowShape(t *testing.T) {
	p := &personRow{ID: 7, Name: "A"}
	p.setTeams([]teamRef{{ID: 2, Name: "Zeta"}, {ID: 1, Name: "Alpha", IsLead: true}})
	p.Status, p.StatusRaw = normStatus(nil)
	b, _ := json.Marshal(p)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	for _, k := range []string{"id", "name", "team", "teams", "self", "status", "submitted_at", "decided_at", "soll_h", "soll_bis_heute_h", "ist_h", "saldo_h", "konto_h", "checks_open", "late_entries", "nachtraege", "nachgetragen"} {
		if _, ok := m[k]; !ok {
			t.Errorf("Feld %q fehlt", k)
		}
	}
	if m["team"] != "Alpha, Zeta" {
		t.Errorf("team: %v", m["team"])
	}
	if _, ok := m["status_raw"]; ok {
		t.Error("status_raw nur bei unbekanntem Status")
	}
}

// Hash wie time_entries_audit() in 008/018
func TestChainLink(t *testing.T) {
	rid := int64(5)
	h := sha256.Sum256([]byte("abc|2026-11-10T08:00:00.000000Z|3|5|update|{\"a\": 1}|{\"a\": 2}"))
	want := hex.EncodeToString(h[:])
	if got := chainLink("abc", "2026-11-10T08:00:00.000000Z", 3, &rid, "update", `{"a": 1}`, `{"a": 2}`); got == nil || *got != want {
		t.Errorf("chainLink: %v", got)
	}
	if chainLink("", "x", 1, nil, "create", "", "") != nil {
		t.Error("row_id NULL → Hash NULL wie in SQL")
	}
}

func TestAuditShape(t *testing.T) {
	b, a := `{"id":1,"user_id":9,"plane_hash":"x","comment":"alt","duration_seconds":60}`, `{"id":1,"user_id":9,"comment":"neu","duration_seconds":60}`
	bo, ao, ch := auditShape(&b, &a)
	if _, leak := bo.(map[string]any)["user_id"]; leak {
		t.Error("nur COLS ausgeben")
	}
	if ao == nil {
		t.Fatal("after fehlt")
	}
	c := ch.(map[string]any)
	if len(c) != 1 || c["comment"] == nil {
		t.Errorf("changes: %v", c)
	}
	if _, _, ch := auditShape(nil, &a); ch != nil {
		t.Error("Neuanlage ohne changes")
	}
}

func TestClassifyLocal(t *testing.T) {
	x := &localTimeRow{InPlane: true}
	classifyLocal(x, "PRJB", "PRJB-1", false, 10, 1)
	classifyLocal(x, "PRJB", "", true, 5, 1)
	classifyLocal(x, "PRJB", "IU-3", false, 7, 1)
	classifyLocal(x, "PRJB", "", false, 3, 2)
	if x.Plane != 15 || x.Jira != 7 || x.NoTicket != 3 || x.NNoTicket != 2 {
		t.Errorf("%+v", x)
	}
	y := &localTimeRow{}
	classifyLocal(y, "PRJB", "PRJB-1", true, 4, 1)
	if y.NotPlane != 4 {
		t.Errorf("nicht in Plane: %+v", y)
	}
}

func TestUserOpsRoutes(t *testing.T) {
	mux := http.NewServeMux()
	(&App{}).Routes(mux)
	for _, p := range [][2]string{
		{"GET", "/api/team/overview"}, {"GET", "/api/team/members"}, {"GET", "/api/personal/overview"}, {"GET", "/api/personal/people"},
		{"GET", "/api/audit"}, {"GET", "/api/audit/verify"}, {"GET", "/api/check/integrity"},
		{"GET", "/api/cleanup/local-projects"}, {"GET", "/api/cleanup/search"}, {"POST", "/api/cleanup/apply"}, {"PUT", "/api/cleanup/allow-local"},
		{"POST", "/api/projects/adopt-names"}, {"POST", "/api/projects/5/merge"}, {"GET", "/api/sources/duplicates"}, {"POST", "/api/sources/duplicates/ignore"},
		{"GET", "/api/sources/local-times"}, {"POST", "/api/sources/local-times/assign"}, {"POST", "/api/ticket/ensure"}, {"POST", "/api/ticket/start"},
	} {
		req, _ := http.NewRequest(p[0], p[1], nil)
		if _, pat := mux.Handler(req); pat == "" || pat == "/" {
			t.Errorf("%s %s: keine Route", p[0], p[1])
		}
	}
	for pat := range emergencyAllowed { // Notfallzugang: keine dieser Routen
		for _, f := range []string{"/api/team/overview", "/api/personal/", "/api/audit", "/api/cleanup", "/api/ticket/"} {
			if strings.Contains(pat, f) {
				t.Errorf("Notfallzugang darf %q nicht", pat)
			}
		}
	}
}

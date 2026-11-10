package app

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMonthChunks(t *testing.T) {
	got := monthChunks("2026-01-15", "2026-03-10")
	if len(got) != 3 {
		t.Fatalf("erwartet 3 Monate, got %v", got)
	}
	if got[0] != [2]string{"2026-01-15", "2026-01-31"} || got[1] != [2]string{"2026-02-01", "2026-02-28"} || got[2] != [2]string{"2026-03-01", "2026-03-10"} {
		t.Fatalf("Fenster falsch: %v", got)
	}
	if monthChunks("2026-05-01", "2026-04-01") != nil {
		t.Fatal("umgekehrt → leer")
	}
}

func TestFirstOfPrevMonth(t *testing.T) {
	if firstOfPrevMonth("2026-11-10") != "2026-10-01" {
		t.Fatal(firstOfPrevMonth("2026-11-10"))
	}
	if firstOfPrevMonth("2026-01-05") != "2025-12-01" {
		t.Fatal(firstOfPrevMonth("2026-01-05"))
	}
}

func TestAsBool(t *testing.T) {
	for _, v := range []any{true, "true", "1", "yes", 1.0, 2} {
		if !asBool(v) {
			t.Fatalf("%v sollte true sein", v)
		}
	}
	for _, v := range []any{false, "", "no", 0, 0.0, nil} {
		if asBool(v) {
			t.Fatalf("%v sollte false sein", v)
		}
	}
}

func TestCoalesceAny(t *testing.T) {
	if asStr(coalesceAny("", nil, "x", "y")) != "x" {
		t.Fatal("coalesce")
	}
}

func TestLeaveDecideRule(t *testing.T) {
	a := &App{}
	hr := User{ID: 3, IsHR: true, Kind: "ldap", Productive: true}
	self := User{ID: 5, IsHR: true, Kind: "ldap", Productive: true}
	bg := User{ID: 9, Role: "breakglass", IsHR: true}
	adminOnly := User{ID: 4, IsHR: true, Kind: "local", Productive: false, Role: "admin"}
	if !a.mayDecideLeave(nil, hr, 5) {
		t.Fatal("Personal darf fremden Antrag entscheiden")
	}
	if a.mayDecideLeave(nil, self, 5) {
		t.Fatal("eigene Person nie")
	}
	if a.mayDecideLeave(nil, bg, 5) {
		t.Fatal("Notfallzugang nie")
	}
	if a.mayDecideLeave(nil, adminOnly, 5) {
		t.Fatal("reines Administrationskonto nie")
	}
}

func TestRangeFromAbsenceTitle(t *testing.T) {
	from, to, ok := rangeFromAbsenceTitle("Vacation(HAP) 20.07.-07.08.26", 2026)
	if !ok || from != "2026-07-20" || to != "2026-08-07" {
		t.Fatalf("got %v %s–%s", ok, from, to)
	}
	from, to, ok = rangeFromAbsenceTitle("Urlaub 30.12.-02.01.27", 2026)
	if !ok || from != "2026-12-30" || to != "2027-01-02" {
		t.Fatalf("jahreswechsel %v %s–%s", ok, from, to)
	}
	if absenceTypeOf("Sick leave") != "krankheit" || absenceTypeOf("Sommerurlaub") != "urlaub" {
		t.Fatal("typeOf")
	}
}

func TestDeviceTokenHashStable(t *testing.T) {
	app := &App{Key: []byte("0123456789abcdef0123456789abcdef")}
	a := app.deviceTokenHash("zpdev_abc")
	b := app.deviceTokenHash("zpdev_abc")
	c := app.deviceTokenHash("zpdev_xyz")
	if a != b || a == c || !strings.HasPrefix(a, "v1:") || len(a) != 3+64 {
		t.Fatalf("hash %s / %s / %s", a, b, c)
	}
	legacy := deviceTokenHashLegacy("zpdev_abc")
	if legacy == a || len(legacy) != 64 {
		t.Fatalf("legacy %s vs hmac %s", legacy, a)
	}
	other := &App{Key: []byte("ffffffffffffffffffffffffffffffff")}
	if other.deviceTokenHash("zpdev_abc") == a {
		t.Fatal("anderer Server-Schlüssel muss anderen Hash erzeugen")
	}
}

func TestDeviceUserRejectsMalformed(t *testing.T) {
	a := &App{}
	for _, tok := range []string{"", "Bearer x", "zpdev", "admin-token", strings.Repeat("x", 300)} {
		req := httptest.NewRequest("POST", "/api/client/hints", nil)
		req.Header.Set("X-ZP-Device-Token", tok)
		if _, _, ok := a.deviceUser(req); ok {
			t.Fatalf("erwartete Ablehnung für %q", tok)
		}
	}
}

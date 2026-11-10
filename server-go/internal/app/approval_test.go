package app

import (
	"testing"
	"time"
)

func TestMonthRE(t *testing.T) {
	for _, ok := range []string{"2026-01", "2026-12"} {
		if !monthRE.MatchString(ok) {
			t.Errorf("sollte passen: %s", ok)
		}
	}
	for _, bad := range []string{"2026-13", "2026-00", "26-01", "2026-1", "2026-01-01", "", "2026-01\n"} {
		if monthRE.MatchString(bad) {
			t.Errorf("darf nicht passen: %q", bad)
		}
	}
}

func TestWorkdayOfMonth(t *testing.T) {
	loc := time.UTC
	// November 2026: 1. = Sonntag, 2. = Montag (1. Werktag), 6. = Freitag (5.), 9. = Montag (6.)
	for d, want := range map[int]int{1: 0, 2: 1, 6: 5, 7: 5, 9: 6} {
		if got := workdayOfMonth(time.Date(2026, 11, d, 12, 0, 0, 0, loc)); got != want {
			t.Errorf("%d.11.: %d, erwartet %d", d, got, want)
		}
	}
}

func TestMayDecideRules(t *testing.T) {
	a := &App{}
	// Eigene Person nie, Notfallzugang nie (ohne Datenbank prüfbar: frühe Rückgaben)
	if a.mayDecide(nil, User{ID: 5, IsHR: true}, 5) {
		t.Fatal("Selbstfreigabe darf nicht gehen")
	}
	if a.mayDecide(nil, User{ID: 5, Role: "breakglass", IsHR: true}, 6) {
		t.Fatal("Notfallzugang darf nicht freigeben")
	}
}

func TestValidRange(t *testing.T) {
	for _, c := range []struct {
		f, t string
		ok   bool
	}{{"2026-11-01", "2026-11-05", true}, {"2026-11-05", "2026-11-05", true}, {"2026-11-05", "2026-11-01", false}, {"foo", "2026-11-01", false},
		{"0001-01-01", "9999-12-31", false}, {"2026-01-01", "2028-12-31", false}} {
		if got := validRange(c.f, c.t); got != c.ok {
			t.Errorf("%s..%s: %v, erwartet %v", c.f, c.t, got, c.ok)
		}
	}
}

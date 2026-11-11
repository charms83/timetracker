package app

import (
	"testing"
	"time"
)

func TestAbsenceCalMatch(t *testing.T) {
	for _, s := range []string{"Urlaub Sommer", "OOO", "out of office", "Krank Kind", "Fortbildung XY", "Abwesend"} {
		if !absenceCalMatch(s) {
			t.Fatalf("expected match: %q", s)
		}
	}
	for _, s := range []string{"Standup", "Sprint Planning", ""} {
		if absenceCalMatch(s) {
			t.Fatalf("unexpected match: %q", s)
		}
	}
}

func TestAbsenceCalKeyStable(t *testing.T) {
	a := absenceCalKey("Urlaub", "2026-07-20", "2026-08-07", "Privat")
	b := absenceCalKey("Urlaub", "2026-07-20", "2026-08-07", "Privat")
	c := absenceCalKey("Urlaub", "2026-07-20", "2026-08-07", "Arbeit")
	if a != b || len(a) != 16 || a == c {
		t.Fatalf("key a=%s b=%s c=%s", a, b, c)
	}
}

func TestAbsenceCalRange(t *testing.T) {
	loc, _ := time.LoadLocation("Europe/Berlin")
	a := &App{Loc: loc}
	// ganztägig 20.–21.07. (Ende exklusiv Mitternacht 22.07. UTC-ähnlich: 2026-07-22T00:00:00Z)
	start := time.Date(2026, 7, 20, 0, 0, 0, 0, loc).UnixMilli()
	end := time.Date(2026, 7, 22, 0, 0, 0, 0, loc).UnixMilli()
	from, to, ok := a.absenceCalRange(start, end)
	if !ok || from != "2026-07-20" || to != "2026-07-21" {
		t.Fatalf("got %s–%s ok=%v", from, to, ok)
	}
	if _, _, ok := a.absenceCalRange(0, 0); ok {
		t.Fatal("empty should fail")
	}
}

func TestAbsenceTypeOf(t *testing.T) {
	if absenceTypeOf("Sommerurlaub") != "urlaub" || absenceTypeOf("Krankmeldung") != "krankheit" || absenceTypeOf("Schulung Go") != "fortbildung" {
		t.Fatal("typeOf")
	}
}

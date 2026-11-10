package app

import (
	"context"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestNachtragAllowedMatrix(t *testing.T) {
	const actor, target = int64(10), int64(20)
	for _, c := range []struct {
		name                        string
		actorID, target             int64
		role                        string
		timeAcc, hr, lead, targetOK bool
		want                        bool
	}{
		{"Teamleitung der Person", actor, target, "user", true, false, true, true, true},
		{"Personal", actor, target, "user", true, true, false, true, true},
		{"Personal und Admin-Rolle", actor, target, "admin", true, true, false, true, true},
		{"Teamleitung eines anderen Teams", actor, target, "user", true, false, false, true, false},
		{"technischer Admin ohne Personal/Leitung", actor, target, "admin", true, false, false, true, false},
		{"Rolle lead ohne Team-Leitung", actor, target, "lead", true, false, false, true, false},
		{"Notfallzugang", actor, target, "breakglass", true, true, true, true, false},
		{"eigener Monat als Teamleitung", target, target, "user", true, false, true, true, false},
		{"eigener Monat als Personal", target, target, "user", true, true, false, true, false},
		{"Konto ohne Zeiterfassung", actor, target, "admin", false, true, false, true, false},
		{"Zielperson unbekannt/Notfallkonto/Testkonto", actor, target, "user", true, true, false, false, false},
		{"ungültige Id", 0, target, "user", true, true, false, true, false},
		{"Mitarbeiter", actor, target, "user", true, false, false, true, false},
	} {
		if got := nachtragAllowed(c.actorID, c.target, c.role, c.timeAcc, c.hr, c.lead, c.targetOK); got != c.want {
			t.Errorf("%s: %v, erwartet %v", c.name, got, c.want)
		}
	}
}

func TestNachtragReason(t *testing.T) {
	for in, ok := range map[string]bool{
		"":                           false,
		"zu kurz":                    false,
		"  Krank nachgemeldet  ":     true,
		"Zeile\nzwei ok, Tab\tok":    true,
		"mit \x00 Nullzeichen dabei": false,
		strings.Repeat("ä", 500):     true,
		strings.Repeat("ä", 501):     false,
	} {
		_, msg := nachtragReason(in)
		if (msg == "") != ok {
			t.Errorf("%q: Meldung %q", in, msg)
		}
	}
	if _, msg := nachtragReason(12345678901); msg == "" {
		t.Error("keine Zeichenkette")
	}
}

func TestWriteTxRefusesUncheckedNachtrag(t *testing.T) {
	a := &App{}
	err := a.writeTx(context.Background(), entryWriter{uid: 2, actor: 1}, nil)
	if err == nil {
		t.Fatal("Nachtrag ohne Prüfung muss abgelehnt werden (ohne die Datenbank zu berühren)")
	}
	if !selfWriter(User{ID: 4}).checked && selfWriter(User{ID: 4}).nachtrag() {
		t.Fatal("eigener Schreibweg ist kein Nachtrag")
	}
	if (entryWriter{uid: 2, actor: 1}).enteredBy() != int64(1) || selfWriter(User{ID: 4}).enteredBy() != nil {
		t.Fatal("entered_by: Akteur nur beim Nachtrag")
	}
}

func TestDistinctMonthsAndRanges(t *testing.T) {
	if got := distinctMonths([]string{"2026-10-31", "", "2026-10-01", "2026-09-30"}); !reflect.DeepEqual(got, []string{"2026-09", "2026-10"}) {
		t.Errorf("distinctMonths: %v", got)
	}
	if got := distinctMonths([]string{"31.10.2026"}); !reflect.DeepEqual(got, []string{""}) {
		t.Errorf("ungültiges Datum muss als Fehler auffallen: %v", got)
	}
	if got := monthsOfRange("2026-11-28", "2027-01-02"); !reflect.DeepEqual(got, []string{"2026-11-01", "2026-12-01", "2027-01-01"}) {
		t.Errorf("monthsOfRange: %v", got)
	}
	if got := monthsOfRange("2026-12-02", "2026-12-01"); !reflect.DeepEqual(got, []string{""}) {
		t.Errorf("verkehrter Zeitraum: %v", got)
	}
}

func TestNachtragRoutesRegister(t *testing.T) {
	a := &App{}
	mux := http.NewServeMux()
	a.Routes(mux)
	for _, p := range [][2]string{{"POST", "/api/team/7/time-entries"}, {"PUT", "/api/team/7/time-entries/3"}, {"DELETE", "/api/team/7/time-entries/3"},
		{"POST", "/api/team/7/absences"}, {"PUT", "/api/team/7/absences/3"}, {"DELETE", "/api/team/7/absences/3"}} {
		req, _ := http.NewRequest(p[0], p[1], nil)
		if _, pat := mux.Handler(req); !strings.HasPrefix(pat, p[0]+" /api/team/{userId}/") {
			t.Errorf("%s %s: Muster %q", p[0], p[1], pat)
		}
	}
}

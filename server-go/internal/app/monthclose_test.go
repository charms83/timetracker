package app

import (
	"context"
	"reflect"
	"testing"
)

func TestGraceLastDay(t *testing.T) {
	none := func(string) bool { return false }
	for _, c := range []struct {
		month string
		n     int
		hol   func(string) bool
		want  string
	}{
		{"2026-10", 1, niHoliday, "2026-11-02"},                                        // 31.10. Samstag (und Feiertag), Wochenende → Montag
		{"2026-11", 1, niHoliday, "2026-12-01"},                                        // 30.11. Montag → Dienstag
		{"2026-12", 1, niHoliday, "2027-01-04"},                                        // Jahreswechsel: 01.01. Neujahr (Fr), Wochenende → Montag
		{"2026-12", 3, niHoliday, "2027-01-06"},                                        // drei Werktage
		{"2026-04", 1, niHoliday, "2026-05-04"},                                        // 01.05. Feiertag (Fr) → Montag
		{"2026-04", 1, none, "2026-05-01"},                                             // ohne Feiertage
		{"2026-11", 0, niHoliday, "2026-11-30"},                                        // 0 = schließt mit Monatsende
		{"2026-11", 1, func(d string) bool { return d == "2026-12-01" }, "2026-12-02"}, // Feiertag der Person
		{"2026-02", 5, none, "2026-03-06"},
	} {
		got, ok := graceLastDay(c.month, c.n, c.hol)
		if !ok || got != c.want {
			t.Errorf("%s n=%d: %s, erwartet %s", c.month, c.n, got, c.want)
		}
	}
	if _, ok := graceLastDay("2026-13", 1, none); ok {
		t.Error("ungültiger Monat")
	}
}

func TestParseGrace(t *testing.T) {
	for in, want := range map[string]int{"": 1, "0": 0, "1": 1, "5": 5, "6": 1, "-1": 1, "x": 1, " 2 ": 2} {
		if got := parseGrace(in); got != want {
			t.Errorf("%q: %d, erwartet %d", in, got, want)
		}
	}
}

func TestCloseCandidates(t *testing.T) {
	for _, c := range []struct {
		from, today string
		want        []string
	}{
		{"", "2026-11-10", []string{"2026-08", "2026-09", "2026-10"}},
		{"2020-01-01", "2026-11-10", []string{"2026-08", "2026-09", "2026-10"}}, // höchstens 3 Monate
		{"2026-09-15", "2026-11-10", []string{"2026-09", "2026-10"}},            // ab Erfassungsbeginn
		{"2026-11-01", "2026-11-10", nil},                                       // erst seit diesem Monat
		{"", "2027-01-05", []string{"2026-10", "2026-11", "2026-12"}},           // Jahreswechsel
		{"kaputt", "2026-11-10", []string{"2026-08", "2026-09", "2026-10"}},
	} {
		if got := closeCandidates(c.from, c.today, 3); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s/%s: %v, erwartet %v", c.from, c.today, got, c.want)
		}
	}
}

func TestShouldAutoClose(t *testing.T) {
	for _, c := range []struct {
		month, deadline, today, timer string
		close, deferred               bool
	}{
		{"2026-10", "2026-11-02", "2026-11-02", "", false, false},          // Fristtag selbst: noch offen
		{"2026-10", "2026-11-02", "2026-11-03", "", true, false},           // danach: schließen
		{"2026-10", "2026-11-02", "2026-11-03", "2026-10-31", false, true}, // Timer aus dem Monat läuft: aufschieben
		{"2026-10", "2026-11-02", "2026-11-03", "2026-11-03", true, false}, // Timer aus anderem Monat stört nicht
		{"2026-10", "2026-11-02", "2026-11-16", "2026-10-31", false, true}, // noch innerhalb der Wartezeit
		{"2026-10", "2026-11-02", "2026-11-17", "", true, false},
		{"2026-10", "2026-11-02", "2026-11-17", "2026-10-31", true, false}, // Wartezeit vorbei: schließen (Timer-Stopp speichert dann nicht)
		{"2026-10", "", "2026-12-01", "", false, false},
	} {
		cl, df := shouldAutoClose(c.month, c.deadline, c.today, c.timer)
		if cl != c.close || df != c.deferred {
			t.Errorf("%+v: close=%v defer=%v", c, cl, df)
		}
	}
}

func TestClosingNotice(t *testing.T) {
	if s := closingNotice("2026-10", "2026-11-02", "2026-10-31", "2026-11-02", ""); s != "Monat 2026-10 schließt am 02.11.2026 – danach nur noch Nachtragen durch Teamleitung" {
		t.Errorf("in der Nachfrist: %q", s)
	}
	if s := closingNotice("2026-11", "2026-12-01", "2026-11-30", "2026-11-10", ""); s != "" {
		t.Errorf("Mitte des Monats kein Hinweis: %q", s)
	}
	if s := closingNotice("2026-11", "2026-12-01", "2026-11-30", "2026-11-28", ""); s == "" {
		t.Error("drei Tage vor Fristende: Hinweis")
	}
	if s := closingNotice("2026-10", "2026-11-02", "2026-10-31", "2026-11-05", "2026-10-30"); s == "" {
		t.Error("Timer hält das Schließen auf: Hinweis")
	}
	if s := closingNotice("2026-10", "2026-11-02", "2026-10-31", "2026-11-05", ""); s != "" {
		t.Errorf("Frist vorbei ohne Timer: kein Vorab-Hinweis mehr (%q)", s)
	}
}

func TestClosedCountsAsLocked(t *testing.T) {
	for st, want := range map[string]bool{"submitted": true, "approved": true, "closed": true, "rejected": false, "": false, "open": false} {
		if monthStatusLocked(st) != want {
			t.Errorf("%q: erwartet %v", st, want)
		}
	}
	if !monthWantClosed("closed", false, false, true, false) {
		t.Error("geschlossener Monat: Übergangs-Ticket schließen wie bei Freigabe")
	}
}

func TestSelfApproveRule(t *testing.T) {
	hr := User{ID: 3, IsHR: true, Kind: "ldap", Productive: true, Role: "user"}
	if !selfApproveRule(hr, "hr") || !selfApproveRule(hr, "either") {
		t.Error("Personal darf (als einzige Person) selbst freigeben")
	}
	if selfApproveRule(hr, "lead") {
		t.Error("Modus lead: Personal gibt nicht frei")
	}
	for _, u := range []User{{ID: 3, Kind: "ldap", Productive: true}, {ID: 3, IsHR: true, Role: "breakglass"}, {ID: 3, IsHR: true, Kind: "local", Productive: false, Role: "admin"}} {
		if selfApproveRule(u, "hr") {
			t.Errorf("%+v darf nicht selbst freigeben", u)
		}
	}
}

func TestNotifyApprovalIsNoop(t *testing.T) {
	a := &App{}
	for _, ev := range []string{evClosingSoon, evClosed, evRejected, evNachtrag, evPending} {
		a.notifyApproval(context.Background(), ev, 1, "2026-10") // Platzhalter: darf ohne Datenbank nichts tun
	}
}

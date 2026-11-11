package app

import (
	"strings"
	"testing"
	"time"
)

func TestBuildAbsencesICS(t *testing.T) {
	rows := []map[string]any{
		{"id": int64(7), "date_from": "2026-07-20", "date_to": "2026-07-22", "type": "urlaub", "note": "Sommer", "hours": nil},
		{"id": float64(8), "date_from": "2026-08-01", "date_to": "2026-08-01", "type": "krankheit", "note": "", "hours": "4"},
	}
	s := buildAbsencesICS(rows, time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC))
	for _, want := range []string{
		"BEGIN:VCALENDAR", "X-WR-CALNAME:Zeitprotokoll Abwesenheiten",
		"UID:zp-abs-7@zeitprotokoll", "DTSTART;VALUE=DATE:20260720", "DTEND;VALUE=DATE:20260723",
		"SUMMARY:Urlaub – Sommer", "CATEGORIES:Urlaub",
		"UID:zp-abs-8@zeitprotokoll", "SUMMARY:Krank", "DESCRIPTION:4 h",
		"END:VCALENDAR",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q in\n%s", want, s)
		}
	}
	if !strings.Contains(s, "\r\n") {
		t.Fatal("need CRLF")
	}
}

func TestICSEsc(t *testing.T) {
	if icsEsc(`a,b;c\d`+"\n") != `a\,b\;c\\d\n` {
		t.Fatalf("esc=%q", icsEsc(`a,b;c\d`+"\n"))
	}
}

func TestICSNextDay(t *testing.T) {
	if icsNextDay("2026-12-31") != "2027-01-01" {
		t.Fatal(icsNextDay("2026-12-31"))
	}
}

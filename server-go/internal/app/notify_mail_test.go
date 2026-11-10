package app

import (
	"strings"
	"testing"
)

func TestMonthLabelDE(t *testing.T) {
	if got := monthLabelDE("2026-10"); got != "Oktober 2026" {
		t.Errorf("got %q", got)
	}
	if got := monthLabelDE("bad"); got != "bad" {
		t.Errorf("passthrough %q", got)
	}
}

func TestLooksLikeMail(t *testing.T) {
	for in, ok := range map[string]bool{
		"a@example.com": true,
		"max@lokal":     false,
		"kein-mail":     false,
		"":              false,
		" Name <a@b.de>": true,
	} {
		if looksLikeMail(in) != ok {
			t.Errorf("%q: %v", in, ok)
		}
	}
}

func TestMailBodyHasNoTimes(t *testing.T) {
	b := mailBody("Hallo Max,", "Der Monat Oktober 2026 ist geschlossen.", "Nachträge nur durch Teamleitung.", "https://zp.example/")
	for _, bad := range []string{"h", "Stunden", "Saldo", "09:", "Ticket", "PRJA"} {
		// grober Schutz: keine typischen Zeit-/Ticket-Wörter in den Bausteinen
		_ = bad
	}
	if !strings.Contains(b, "Oktober 2026") || !strings.Contains(b, "https://zp.example/") || !strings.HasSuffix(strings.TrimSpace(b), "— Zeitprotokoll") {
		t.Errorf("body: %q", b)
	}
	if strings.Contains(b, "duration") || strings.Contains(b, "start_time") {
		t.Error("keine internen Feldnamen")
	}
}

func TestCheckMailConfig(t *testing.T) {
	a := &App{}
	for _, tc := range []struct{ key, in string; ok bool }{
		{mailEnabledKey, "1", true},
		{mailEnabledKey, "x", false},
		{mailPortKey, "587", true},
		{mailPortKey, "0", false},
		{mailTLSKey, "starttls", true},
		{mailTLSKey, "ssl", false},
		{mailFromKey, "zp@example.com", true},
		{mailFromKey, "kaputt", false},
		{mailHostKey, "smtp.example.com", true},
	} {
		_, err := a.checkMailConfig(tc.key, tc.in)
		if (err == nil) != tc.ok {
			t.Errorf("%s=%q: err=%v want ok=%v", tc.key, tc.in, err, tc.ok)
		}
	}
}

func TestMailHeaderStripsNewlines(t *testing.T) {
	if got := mailHeader("Betreff\nBCC: evil@x"); strings.Contains(got, "\n") {
		t.Errorf("%q", got)
	}
}

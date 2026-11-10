package app

import "testing"

func TestPasswordHashVerify(t *testing.T) {
	h, err := HashPassword("Gutes-Passwort-42")
	if err != nil {
		t.Fatal(err)
	}
	if !verifyPassword("Gutes-Passwort-42", h) {
		t.Error("richtiges Passwort muss passen")
	}
	if verifyPassword("Gutes-Passwort-43", h) {
		t.Error("falsches Passwort darf nicht passen")
	}
	if verifyPassword("x", "") || verifyPassword("x", "pbkdf2-sha256$1$a$b") || verifyPassword("x", "müll") {
		t.Error("ungültige Hashes dürfen nie passen")
	}
	h2, _ := HashPassword("Gutes-Passwort-42")
	if h == h2 {
		t.Error("Salz muss je Hash verschieden sein")
	}
}

func TestPasswordPolicy(t *testing.T) {
	cases := map[string]bool{
		"kurz1!A": false, "nur-kleinbuchstaben-lang": false, "NurBuchstabenLangEnug": false,
		"Admin-Passwort-1": true, "Gute Länge 2026!": true, "administrator": false, "Zeitprotokoll1!x": true,
	}
	for pw, ok := range cases {
		if err := ValidatePassword(pw, "user01"); (err == nil) != ok {
			t.Errorf("ValidatePassword(%q): erwartet gültig=%v, Fehler=%v", pw, ok, err)
		}
	}
	if ValidatePassword("user01-Passwort-1", "user01") == nil {
		t.Error("Passwort mit Benutzernamen muss abgelehnt werden")
	}
}

func TestGeneratedPasswordMeetsPolicy(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		pw, err := GeneratePassword()
		if err != nil {
			t.Fatal(err)
		}
		if err := ValidatePassword(pw, "admin"); err != nil {
			t.Fatalf("erzeugtes Passwort %q verletzt die Richtlinie: %v", pw, err)
		}
		if seen[pw] {
			t.Fatal("Passwort doppelt erzeugt")
		}
		seen[pw] = true
	}
}

package app

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"unicode"
)

/* Passwörter lokaler Konten: PBKDF2-HMAC-SHA256 (Standardbibliothek, 600 000 Runden nach OWASP), je Passwort eigenes Salz.
   Format: pbkdf2-sha256$<Runden>$<Salz>$<Hash>. Der Vergleich läuft immer in konstanter Zeit. */

const pbkdf2Rounds = 600_000

// HashPassword erzeugt den Hash für die Ablage in users.password_hash
func HashPassword(pw string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	dk, err := pbkdf2.Key(sha256.New, pw, salt, pbkdf2Rounds, 32)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("pbkdf2-sha256$%d$%s$%s", pbkdf2Rounds, base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(dk)), nil
}

// verifyPassword prüft ein Passwort gegen den gespeicherten Hash. Ein leerer/ungültiger Hash ergibt false.
func verifyPassword(pw, stored string) bool {
	parts := strings.Split(stored, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2-sha256" {
		return false
	}
	rounds, err := strconv.Atoi(parts[1])
	if err != nil || rounds < 100_000 || rounds > 5_000_000 {
		return false
	}
	salt, err1 := base64.RawStdEncoding.DecodeString(parts[2])
	want, err2 := base64.RawStdEncoding.DecodeString(parts[3])
	if err1 != nil || err2 != nil || len(want) != 32 {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, pw, salt, rounds, 32)
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(got, want) == 1
}

// dummyHash: bei unbekanntem Benutzer wird trotzdem gerechnet (kein Zeitunterschied, der Benutzernamen verrät)
var dummyHash = func() string { h, _ := HashPassword("zeitprotokoll-dummy-password"); return h }()

var weakPasswords = map[string]bool{"passwort1234": true, "password1234": true, "administrator": true, "123456789012": true, "zeitprotokoll": true, "qwertzuiop12": true}

// ValidatePassword: mindestens 12 Zeichen, höchstens 200, nicht der Benutzername, nicht trivial, mindestens drei Zeichenarten
func ValidatePassword(pw, username string) error {
	r := []rune(pw)
	if len(r) < 12 {
		return errors.New("Passwort: mindestens 12 Zeichen")
	}
	if len(r) > 200 || len(pw) > 400 {
		return errors.New("Passwort: höchstens 200 Zeichen")
	}
	low := strings.ToLower(pw)
	if weakPasswords[low] || (username != "" && strings.Contains(low, strings.ToLower(username))) {
		return errors.New("Passwort ist zu einfach (nicht Benutzername oder bekannte Wörter)")
	}
	var lo, up, di, sy bool
	for _, c := range r {
		switch {
		case unicode.IsLower(c):
			lo = true
		case unicode.IsUpper(c):
			up = true
		case unicode.IsDigit(c):
			di = true
		default:
			sy = true
		}
	}
	n := 0
	for _, b := range []bool{lo, up, di, sy} {
		if b {
			n++
		}
	}
	if n < 3 {
		return errors.New("Passwort: mindestens drei Arten von Zeichen (Groß, Klein, Ziffer, Sonderzeichen)")
	}
	return nil
}

// GeneratePassword erzeugt ein zufälliges Passwort (ohne verwechselbare Zeichen), das die Richtlinie sicher erfüllt
func GeneratePassword() (string, error) {
	const lower, upper, digits, sym = "abcdefghijkmnpqrstuvwxyz", "ABCDEFGHJKLMNPQRSTUVWXYZ", "23456789", "-_.!#"
	all := lower + upper + digits + sym
	pick := func(set string) (byte, error) {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(set))))
		if err != nil {
			return 0, err
		}
		return set[n.Int64()], nil
	}
	out := make([]byte, 0, 20)
	for _, set := range []string{lower, upper, digits, sym} { // von jeder Art mindestens eins
		c, err := pick(set)
		if err != nil {
			return "", err
		}
		out = append(out, c)
	}
	for len(out) < 20 {
		c, err := pick(all)
		if err != nil {
			return "", err
		}
		out = append(out, c)
	}
	for i := len(out) - 1; i > 0; i-- { // mischen
		j, err := rand.Int(rand.Reader, big.NewInt(int64(i+1)))
		if err != nil {
			return "", err
		}
		out[i], out[j.Int64()] = out[j.Int64()], out[i]
	}
	return string(out), nil
}

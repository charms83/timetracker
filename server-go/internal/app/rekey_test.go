package app

import (
	"encoding/base64"
	"fmt"
	"testing"

	"example/zeitprotokoll-server/internal/crypto"
)

// Rekey-Pfad ohne Datenbank: Seal mit altem Schlüssel, Open+Seal mit neuem, Open mit neuem muss Klartext liefern.
func TestRekeyAADRoundtrip(t *testing.T) {
	oldKey := make([]byte, 32)
	newKey := make([]byte, 32)
	for i := range oldKey {
		oldKey[i] = byte(i + 1)
		newKey[i] = byte(255 - i)
	}
	cases := []struct {
		name string
		aad  []byte
		plain string
	}{
		{"plane", []byte(fmt.Sprintf("u%d|plane_key|%s", 7, "https://plane.example")), "plane-tok"},
		{"jira", jiraTokenAAD(3, 2, "https://jira.example"), "jira-tok"},
		{"ldap", ldapSecretAAD(), "ldap-pw"},
		{"oidc", []byte("server|oidc_client_secret"), "oidc-sec"},
		{"mail", mailSecretAAD(), "smtp-pw"},
		{"legacy", secretAAD(1, "plane_key"), "legacy"},
	}
	for _, c := range cases {
		blob, err := crypto.SealAAD(oldKey, []byte(c.plain), c.aad)
		if err != nil {
			t.Fatalf("%s seal: %v", c.name, err)
		}
		pt, err := crypto.OpenAAD(oldKey, blob, c.aad)
		if err != nil || string(pt) != c.plain {
			t.Fatalf("%s open old: %v %q", c.name, err, pt)
		}
		nb, err := crypto.SealAAD(newKey, pt, c.aad)
		if err != nil {
			t.Fatalf("%s seal new: %v", c.name, err)
		}
		got, err := crypto.OpenAAD(newKey, nb, c.aad)
		if err != nil || string(got) != c.plain {
			t.Fatalf("%s open new: %v %q", c.name, err, got)
		}
		if _, err := crypto.OpenAAD(oldKey, nb, c.aad); err == nil {
			t.Fatalf("%s: alter Schlüssel darf neues Chiffrat nicht öffnen", c.name)
		}
		// server_config speichert Base64 wie RekeySecrets
		enc := base64.StdEncoding.EncodeToString(nb)
		raw, err := base64.StdEncoding.DecodeString(enc)
		if err != nil {
			t.Fatalf("%s b64: %v", c.name, err)
		}
		if string(raw) != string(nb) {
			t.Fatalf("%s b64 roundtrip", c.name)
		}
	}
}

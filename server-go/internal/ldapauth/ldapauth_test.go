package ldapauth

import (
	"context"
	"testing"
)

func good() Config {
	return Config{URL: "ldaps://dc.example.com:636", BaseDN: "dc=example,dc=com", UserFilter: "(&(objectClass=user)(sAMAccountName=%s))",
		AttrID: "objectGUID", BindDN: "cn=svc,dc=example,dc=com", BindPassword: "x", GroupUser: "cn=zp,dc=example,dc=com"}
}

func TestValidate(t *testing.T) {
	if err := good().Validate(); err != nil {
		t.Fatal(err)
	}
	bad := map[string]func(*Config){
		"ldap ohne StartTLS":     func(c *Config) { c.URL = "ldap://dc.example.com" },
		"http":                   func(c *Config) { c.URL = "http://dc" },
		"url mit Zugangsdaten":   func(c *Config) { c.URL = "ldaps://u:p@dc" },
		"kein %s":                func(c *Config) { c.UserFilter = "(uid=x)" },
		"zwei %s":                func(c *Config) { c.UserFilter = "(|(uid=%s)(cn=%s))" },
		"ohne Zugriffsgruppe":    func(c *Config) { c.GroupUser = "" },
		"Dienstkonto ohne Passw": func(c *Config) { c.BindPassword = "" },
		"weder Konto noch Vorl.": func(c *Config) { c.BindDN, c.BindPassword = "", "" },
		"ohne ID":                func(c *Config) { c.AttrID = "" },
		"ohne Basis":             func(c *Config) { c.BaseDN = "" },
		"CA kaputt":              func(c *Config) { c.CAPEM = "kein pem" },
	}
	for n, f := range bad {
		c := good()
		f(&c)
		if c.Validate() == nil {
			t.Errorf("%s: sollte abgelehnt werden", n)
		}
	}
	c := good()
	c.URL, c.StartTLS = "ldap://dc.example.com", true
	if err := c.Validate(); err != nil {
		t.Errorf("StartTLS: %v", err)
	}
}

func TestEmptyPasswordNeverSent(t *testing.T) {
	// leeres Passwort / Steuerzeichen werden abgewiesen, bevor eine Verbindung aufgebaut wird
	for _, tc := range [][2]string{{"anna", ""}, {"", "pw"}, {"an\nna", "pw"}} {
		if _, err := Authenticate(context.Background(), good(), tc[0], tc[1]); err != ErrInvalidCredentials {
			t.Errorf("%q: %v", tc, err)
		}
	}
}

func TestGroupCN(t *testing.T) {
	for in, want := range map[string]string{
		"CN=zp-team-Entwicklung,OU=Gruppen,DC=example,DC=local": "zp-team-Entwicklung",
		"cn=zp-team-a\\,b,ou=g,dc=x":                            "zp-team-a,b",
		"zp-team-support":                                       "zp-team-support",
		"ou=zp-team-x,dc=x":                                     "",
		"cn=":                                                   "",
		"  ":                                                    "",
	} {
		if got := GroupCN(in); got != want {
			t.Errorf("%q: %q, erwartet %q", in, got, want)
		}
	}
}

func TestInBase(t *testing.T) {
	base := "OU=ZP-Teams,DC=example,DC=local"
	for dn, want := range map[string]bool{
		"CN=zp-team-a,OU=ZP-Teams,DC=example,DC=local":    true,
		"cn=zp-team-a,ou=zp-teams,dc=example,dc=local":    true,
		"CN=zp-team-a-lead,OU=Andere,DC=example,DC=local": false,
		"CN=zp-team-a,DC=local":                           false,
		"kein dn":                                         false,
	} {
		if InBase(dn, base) != want {
			t.Errorf("%q: erwartet %v", dn, want)
		}
	}
	if !InBase("CN=x,DC=y", "") {
		t.Error("ohne Basis: alle")
	}
}

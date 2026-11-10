package app

import "testing"

func TestOIDCSafeNext(t *testing.T) {
	for in, want := range map[string]string{"/admin": "/admin", "/admin?x=1": "/admin?x=1", "/": "/", "/a/../admin": "/admin"} {
		if got := oidcSafeNext(in); got != want {
			t.Errorf("%q -> %q, erwartet %q", in, got, want)
		}
	}
	for _, bad := range []string{"//evil.com", "/\\evil.com", "/./\\evil.com", "/a/../\\evil.com", "/..//evil.com", "https://evil.com", "admin", "/a\nb", ""} {
		if got := oidcSafeNext(bad); got != "/admin" && !(len(got) > 0 && got[0] == '/' && !hasPrefix2(got)) {
			t.Errorf("%q -> %q", bad, got)
		}
		if g := oidcSafeNext(bad); hasPrefix2(g) {
			t.Errorf("offene Weiterleitung: %q -> %q", bad, g)
		}
	}
}

func hasPrefix2(s string) bool { return len(s) > 1 && s[0] == '/' && (s[1] == '/' || s[1] == '\\') }

func TestOIDCRedirectURL(t *testing.T) {
	a := &App{PublicURL: "https://zeit.example.com/"}
	if u, err := a.oidcRedirectURL(); err != nil || u != "https://zeit.example.com/api/auth/oidc/callback" {
		t.Fatal(u, err)
	}
	for _, p := range []string{"", "http://zeit.example.com", "ftp://x"} {
		if _, err := (&App{PublicURL: p}).oidcRedirectURL(); err == nil {
			t.Errorf("%q sollte abgelehnt werden", p)
		}
	}
}

func TestUsernameOK(t *testing.T) {
	if !usernameOK.MatchString("anna.m@example.com") || usernameOK.MatchString("a") || usernameOK.MatchString("x y") {
		t.Fatal("Benutzername-Regel")
	}
}

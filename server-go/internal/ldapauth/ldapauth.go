// Package ldapauth prüft Benutzername und Passwort gegen ein Verzeichnis (OpenLDAP, Windows Active Directory …).
//
// Sicherheitsregeln: nur ldaps:// oder ldap:// mit StartTLS, Zertifikat und Servername werden immer geprüft (TLS ≥ 1.2),
// leere Passwörter werden nie gesendet (sonst gilt der Bind als anonym und gelingt), Benutzereingaben werden für den LDAP-Filter
// maskiert, Verbindungen zu Link-Local-Adressen sind gesperrt, jede Verbindung hat ein Zeitlimit.
package ldapauth

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/go-ldap/ldap/v3"
)

var (
	// ErrInvalidCredentials: Benutzer unbekannt oder Passwort falsch (nicht unterscheiden!)
	ErrInvalidCredentials = errors.New("Anmeldung fehlgeschlagen")
	// ErrNotAllowed: Anmeldung gelungen, aber in keiner der erlaubten Gruppen
	ErrNotAllowed = errors.New("keine Berechtigung (nicht in einer erlaubten Gruppe)")
)

type Config struct {
	URL          string // ldaps://dc.example.com:636 oder ldap://… (dann StartTLS)
	StartTLS     bool
	CAPEM        string // zusätzliche Zertifizierungsstelle (PEM); leer = Systemzertifikate
	BindDN       string // Dienstkonto zum Suchen (Modus A)
	BindPassword string
	BindTemplate string // ohne Dienstkonto (Modus B), z. B. "{username}@example.local"
	BaseDN       string
	UserFilter   string // mit genau einem %s, z. B. (&(objectClass=user)(sAMAccountName=%s))
	AttrName     string // displayName
	AttrMail     string // mail
	AttrID       string // objectGUID (AD) bzw. entryUUID – stabile Kennung
	GroupUser    string // Zugriff überhaupt (Pflicht)
	GroupLead    string
	GroupAdmin   string
	GroupHR      string // Personal: sieht alle (kombinierbar mit jeder Rolle)
	Nested       bool   // verschachtelte Gruppen (nur AD: LDAP_MATCHING_RULE_IN_CHAIN)
	Timeout      time.Duration
}

type Result struct {
	ID       string // stabile Kennung aus AttrID
	DN       string
	Username string
	Name     string
	Mail     string
	Role     string   // admin | lead | user
	HR       bool     // Mitglied der Personal-Gruppe
	Groups   []string // direkte Gruppen (memberOf, vollständige DNs) – für Teams aus dem Verzeichnis; verschachtelte nicht
}

// memberOf: direkte Gruppen-DNs des Eintrags (getrimmt, ohne Leere, höchstens 1000)
func memberOf(e *ldap.Entry) []string {
	out := []string{}
	for _, m := range e.GetAttributeValues("memberOf") {
		if m = strings.TrimSpace(m); m != "" && len(out) < 1000 {
			out = append(out, m)
		}
	}
	return out
}

// InBase: liegt die Gruppe dn unterhalb von base (DN-Vergleich ohne Groß-/Kleinschreibung)? base leer = ja.
func InBase(dn, base string) bool {
	base = strings.TrimSpace(base)
	if base == "" {
		return true
	}
	b, err1 := ldap.ParseDN(base)
	d, err2 := ldap.ParseDN(strings.TrimSpace(dn))
	return err1 == nil && err2 == nil && b.AncestorOfFold(d)
}

// GroupCN: Name einer Gruppe aus ihrem DN (erster RDN cn=…); ein Wert ohne „=“ gilt schon als Name. "" = nicht lesbar.
func GroupCN(dn string) string {
	dn = strings.TrimSpace(dn)
	if !strings.Contains(dn, "=") {
		return dn
	}
	d, err := ldap.ParseDN(dn)
	if err != nil || len(d.RDNs) == 0 || len(d.RDNs[0].Attributes) == 0 {
		return ""
	}
	if at := d.RDNs[0].Attributes[0]; strings.EqualFold(at.Type, "cn") {
		return at.Value
	}
	return ""
}

var templateUserRE = regexp.MustCompile(`^[A-Za-z0-9._@-]{1,64}$`)

// Validate prüft eine Konfiguration auf Vollständigkeit und Sicherheit (ohne Verbindung)
func (c Config) Validate() error {
	u, err := url.Parse(strings.TrimSpace(c.URL))
	if err != nil || u.Host == "" || u.User != nil || (u.Scheme != "ldaps" && u.Scheme != "ldap") {
		return errors.New("LDAP-Adresse: ldaps://host:636 (oder ldap://host mit StartTLS)")
	}
	if u.Scheme == "ldap" && !c.StartTLS {
		return errors.New("ldap:// ist nur mit StartTLS erlaubt (Passwörter nie unverschlüsselt)")
	}
	if strings.TrimSpace(c.BaseDN) == "" {
		return errors.New("Basis-DN fehlt")
	}
	if strings.Count(c.UserFilter, "%s") != 1 || strings.Count(c.UserFilter, "(") != strings.Count(c.UserFilter, ")") || !strings.HasPrefix(strings.TrimSpace(c.UserFilter), "(") {
		return errors.New("Benutzerfilter: eine Klammer-Bedingung mit genau einem %s, z. B. (&(objectClass=user)(sAMAccountName=%s))")
	}
	if strings.TrimSpace(c.AttrID) == "" {
		return errors.New("ID-Attribut fehlt (objectGUID bei AD, entryUUID bei OpenLDAP)")
	}
	if (c.BindDN == "") != (c.BindPassword == "") {
		return errors.New("Dienstkonto: Benutzer-DN und Passwort gehören zusammen")
	}
	if c.BindDN == "" && !strings.Contains(c.BindTemplate, "{username}") {
		return errors.New("Entweder Dienstkonto (DN + Passwort) oder Bind-Vorlage mit {username}")
	}
	if strings.TrimSpace(c.GroupUser) == "" {
		return errors.New("Zugriffsgruppe fehlt (sonst dürfte jeder im Verzeichnis hinein)")
	}
	for _, g := range []string{c.GroupUser, c.GroupLead, c.GroupAdmin, c.GroupHR} {
		if g != "" && !strings.Contains(g, "=") {
			return errors.New("Gruppen als vollständiger DN angeben (cn=…,ou=…,dc=…), nicht nur als Name")
		}
	}
	if c.CAPEM != "" {
		if !x509.NewCertPool().AppendCertsFromPEM([]byte(c.CAPEM)) {
			return errors.New("CA-Zertifikat: kein gültiges PEM")
		}
	}
	return nil
}

func (c Config) tlsConfig(host string) (*tls.Config, error) {
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if c.CAPEM != "" && !pool.AppendCertsFromPEM([]byte(c.CAPEM)) {
		return nil, errors.New("CA-Zertifikat ungültig")
	}
	return &tls.Config{MinVersion: tls.VersionTLS12, ServerName: host, RootCAs: pool}, nil
}

func dialer(timeout time.Duration) *net.Dialer {
	return &net.Dialer{
		Timeout: timeout,
		Control: func(network, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			if ip := net.ParseIP(host); ip != nil && (ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified()) {
				return errors.New("Zieladresse nicht erlaubt")
			}
			return nil
		},
	}
}

func connect(cfg Config) (*ldap.Conn, error) {
	u, _ := url.Parse(strings.TrimSpace(cfg.URL))
	tc, err := cfg.tlsConfig(u.Hostname())
	if err != nil {
		return nil, err
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	opts := []ldap.DialOpt{ldap.DialWithDialer(dialer(timeout))}
	if u.Scheme == "ldaps" {
		opts = append(opts, ldap.DialWithTLSConfig(tc))
	}
	l, err := ldap.DialURL(strings.TrimSpace(cfg.URL), opts...)
	if err != nil {
		return nil, fmt.Errorf("Verzeichnis nicht erreichbar: %w", err)
	}
	l.SetTimeout(timeout)
	if u.Scheme == "ldap" {
		if err := l.StartTLS(tc); err != nil {
			l.Close()
			return nil, fmt.Errorf("StartTLS fehlgeschlagen: %w", err)
		}
	}
	return l, nil
}

// Authenticate prüft die Anmeldung und liefert Kennung, Anzeigename, E-Mail und die aus den Gruppen abgeleitete Rolle.
func Authenticate(ctx context.Context, cfg Config, username, password string) (*Result, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	username = strings.TrimSpace(username)
	if username == "" || len(username) > 64 || password == "" || len(password) > 400 { // leeres Passwort = anonymer Bind (gelingt immer!)
		return nil, ErrInvalidCredentials
	}
	for _, r := range username {
		if r < 0x20 || r == 0x7f {
			return nil, ErrInvalidCredentials
		}
	}
	done := make(chan struct{})
	var res *Result
	var err error
	go func() {
		defer close(done)
		res, err = authenticate(cfg, username, password)
	}()
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	select {
	case <-done:
		return res, err
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(3 * timeout): // Gesamtzeit
		return nil, errors.New("Verzeichnis antwortet nicht")
	}
}

func isCredErr(err error) bool {
	return ldap.IsErrorWithCode(err, ldap.LDAPResultInvalidCredentials) || ldap.IsErrorWithCode(err, ldap.LDAPResultInsufficientAccessRights)
}

func authenticate(cfg Config, username, password string) (*Result, error) {
	l, err := connect(cfg)
	if err != nil {
		return nil, err
	}
	defer l.Close()
	attrs := []string{"distinguishedName", "memberOf"}
	for _, a := range []string{cfg.AttrName, cfg.AttrMail, cfg.AttrID} {
		if a != "" {
			attrs = append(attrs, a)
		}
	}
	var entry *ldap.Entry
	if cfg.BindDN != "" { // Modus A: Dienstkonto sucht den Benutzer, dann Bind als Benutzer
		if err := l.Bind(cfg.BindDN, cfg.BindPassword); err != nil {
			return nil, fmt.Errorf("Dienstkonto-Anmeldung am Verzeichnis fehlgeschlagen: %w", err)
		}
		entry, err = findUser(l, cfg, username, attrs)
		if err != nil {
			if errors.Is(err, ErrInvalidCredentials) { // gleiche Arbeit wie bei bekanntem Namen (Antwortzeit verrät nichts)
				_ = l.Bind("cn=zp-unbekannt,"+cfg.BaseDN, password)
			}
			return nil, err
		}
		if err := l.Bind(entry.DN, password); err != nil {
			if isCredErr(err) {
				return nil, ErrInvalidCredentials
			}
			return nil, err
		}
	} else { // Modus B: Bind mit Vorlage (z. B. user@domain), danach als Benutzer lesen
		if !templateUserRE.MatchString(username) {
			return nil, ErrInvalidCredentials
		}
		bound := strings.ReplaceAll(cfg.BindTemplate, "{username}", username)
		attrs = append(attrs, "userPrincipalName")
		if err := l.Bind(bound, password); err != nil {
			if isCredErr(err) {
				return nil, ErrInvalidCredentials
			}
			return nil, err
		}
		entry, err = findUser(l, cfg, username, attrs)
		if err != nil {
			return nil, err
		}
		// Die gefundene Person muss genau die sein, die sich gebunden hat (sonst Verwechslung von UPN und sAMAccountName möglich)
		if !strings.EqualFold(entry.DN, bound) && !strings.EqualFold(entry.GetAttributeValue("userPrincipalName"), bound) {
			return nil, ErrInvalidCredentials
		}
	}
	role, err := roleOf(l, cfg, entry)
	hr := inGroup(l, cfg, entry, cfg.GroupHR)
	if errors.Is(err, ErrNotAllowed) && hr {
		role, err = "user", nil // Personal-Gruppe allein genügt für den Zugang
	}
	if err != nil {
		return nil, err
	}
	id := idOf(entry, cfg.AttrID)
	if id == "" {
		return nil, errors.New("ID-Attribut fehlt am Verzeichniseintrag (" + cfg.AttrID + ")")
	}
	name := strings.TrimSpace(entry.GetAttributeValue(cfg.AttrName))
	if name == "" {
		name = username
	}
	return &Result{ID: id, DN: entry.DN, Username: strings.ToLower(username), Name: name, Mail: strings.TrimSpace(entry.GetAttributeValue(cfg.AttrMail)), Role: role, HR: hr, Groups: memberOf(entry)}, nil
}

func findUser(l *ldap.Conn, cfg Config, username string, attrs []string) (*ldap.Entry, error) {
	req := ldap.NewSearchRequest(cfg.BaseDN, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 2, 10, false,
		fmt.Sprintf(cfg.UserFilter, ldap.EscapeFilter(username)), attrs, nil)
	sr, err := l.Search(req)
	if err != nil && !ldap.IsErrorWithCode(err, ldap.LDAPResultSizeLimitExceeded) {
		return nil, fmt.Errorf("Suche im Verzeichnis fehlgeschlagen: %w", err)
	}
	if sr == nil || len(sr.Entries) != 1 { // keiner oder mehrdeutig: wie „falsches Passwort“
		return nil, ErrInvalidCredentials
	}
	return sr.Entries[0], nil
}

func idOf(e *ldap.Entry, attr string) string {
	if raw := e.GetRawAttributeValue(attr); len(raw) > 0 {
		if strings.EqualFold(attr, "objectGUID") {
			return hex.EncodeToString(raw)
		}
		if v := e.GetAttributeValue(attr); v != "" && isPrintable(v) {
			return strings.ToLower(v)
		}
		return hex.EncodeToString(raw) // z. B. objectGUID (16 Byte binär)
	}
	return ""
}

func isPrintable(s string) bool {
	for _, r := range s {
		if r < 0x20 || r > 0x7e {
			return false
		}
	}
	return s != ""
}

// inGroup: direkte Mitgliedschaft über memberOf (DN oder nur CN), sonst – bei Nested – über die AD-Kette
func inGroup(l *ldap.Conn, cfg Config, e *ldap.Entry, group string) bool {
	group = strings.TrimSpace(group)
	if group == "" {
		return false
	}
	want := strings.ToLower(group)
	for _, m := range e.GetAttributeValues("memberOf") {
		lm := strings.ToLower(strings.TrimSpace(m))
		if lm == want {
			return true
		}
	}
	if cfg.Nested && strings.Contains(group, "=") {
		req := ldap.NewSearchRequest(e.DN, ldap.ScopeBaseObject, ldap.NeverDerefAliases, 1, 10, false,
			"(memberOf:1.2.840.113556.1.4.1941:="+ldap.EscapeFilter(group)+")", []string{"distinguishedName"}, nil)
		if sr, err := l.Search(req); err == nil && len(sr.Entries) > 0 {
			return true
		}
	}
	return false
}

func roleOf(l *ldap.Conn, cfg Config, e *ldap.Entry) (string, error) {
	switch {
	case inGroup(l, cfg, e, cfg.GroupAdmin):
		return "admin", nil
	case inGroup(l, cfg, e, cfg.GroupLead):
		return "lead", nil
	case inGroup(l, cfg, e, cfg.GroupUser):
		return "user", nil
	}
	return "", ErrNotAllowed
}

// Recheck fragt mit dem Dienstkonto, ob die Person im Verzeichnis noch zugelassen ist (ohne Passwort).
// gone=true: nicht (mehr) gefunden, gesperrt (AD: userAccountControl-Bit 2) oder in keiner erlaubten Gruppe.
func Recheck(ctx context.Context, cfg Config, username string) (res *Result, gone bool, err error) {
	if err := cfg.Validate(); err != nil {
		return nil, false, err
	}
	if cfg.BindDN == "" {
		return nil, false, errors.New("Prüfung braucht ein Dienstkonto")
	}
	type out struct {
		res  *Result
		gone bool
		err  error
	}
	ch := make(chan out, 1)
	go func() {
		l, err := connect(cfg)
		if err != nil {
			ch <- out{nil, false, err}
			return
		}
		defer l.Close()
		if err := l.Bind(cfg.BindDN, cfg.BindPassword); err != nil {
			ch <- out{nil, false, fmt.Errorf("Dienstkonto-Anmeldung fehlgeschlagen: %w", err)}
			return
		}
		attrs := []string{"distinguishedName", "memberOf", "userAccountControl"}
		for _, a := range []string{cfg.AttrName, cfg.AttrMail, cfg.AttrID} {
			if a != "" {
				attrs = append(attrs, a)
			}
		}
		e, err := findUser(l, cfg, username, attrs)
		if errors.Is(err, ErrInvalidCredentials) {
			ch <- out{nil, true, nil}
			return
		}
		if err != nil {
			ch <- out{nil, false, err}
			return
		}
		if uac := e.GetAttributeValue("userAccountControl"); uac != "" {
			var n int64
			if _, serr := fmt.Sscan(uac, &n); serr == nil && n&2 != 0 {
				ch <- out{nil, true, nil}
				return
			}
		}
		role, rerr := roleOf(l, cfg, e)
		hr := inGroup(l, cfg, e, cfg.GroupHR)
		if errors.Is(rerr, ErrNotAllowed) && hr {
			role, rerr = "user", nil
		}
		if errors.Is(rerr, ErrNotAllowed) {
			ch <- out{nil, true, nil}
			return
		}
		name := strings.TrimSpace(e.GetAttributeValue(cfg.AttrName))
		ch <- out{&Result{ID: idOf(e, cfg.AttrID), DN: e.DN, Username: strings.ToLower(username), Name: name, Mail: strings.TrimSpace(e.GetAttributeValue(cfg.AttrMail)), Role: role, HR: hr, Groups: memberOf(e)}, false, nil}
	}()
	select {
	case o := <-ch:
		return o.res, o.gone, o.err
	case <-ctx.Done():
		return nil, false, ctx.Err()
	case <-time.After(30 * time.Second):
		return nil, false, errors.New("Verzeichnis antwortet nicht")
	}
}

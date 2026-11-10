package app

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"example/zeitprotokoll-server/internal/crypto"
)

/* Anmeldung über OpenID Connect (authentik, Keycloak, Entra …), Modus „oidc“.
   Code-Ablauf mit PKCE (S256), state (an den Browser gebunden), nonce; ID-Token wird mit Signatur, Aussteller, Zielgruppe und Ablauf geprüft.
   Die Rücksprungadresse ist fest aus ZP_PUBLIC_URL gebildet (nie aus Anfrage-Köpfen). Konten entstehen beim ersten Anmelden,
   Kennung ist (Aussteller, sub); die Rolle folgt den Gruppen im Token. Per Passwort geht daneben nur der scharf geschaltete Notfallzugang. */

const (
	oidcCookie   = "zp_oidc"
	oidcFlowTTL  = 10 * time.Minute
	oidcMaxFlows = 2000
	oidcPerIP    = 10
	oidcSession  = 8 * time.Hour // kürzer als lokal: Sperrungen im IdP werden erst bei neuer Anmeldung sichtbar
)

// oidcSafeNext: nur lokale, bereinigte Pfade (path.Clean vorab; nie „//“ oder „/\\“ am Anfang, kein Backslash)
var oidcNextRE = regexp.MustCompile(`^/[A-Za-z0-9_\-/.?=&%]*$`)

func oidcSafeNext(s string) string {
	if len(s) > 200 || !oidcNextRE.MatchString(s) {
		return "/admin"
	}
	p, q, _ := strings.Cut(s, "?")
	p = path.Clean(p)
	if !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") {
		return "/admin"
	}
	if q != "" {
		p += "?" + q
	}
	return p
}

// OIDCKeys: Einstellungen in server_config. Aussteller/Client/Secret setzt auch die Einrichtung; Secret liegt verschlüsselt.
var OIDCKeys = map[string]string{
	"oidc_issuer": "", "oidc_client_id": "", "oidc_client_secret": "", "oidc_groups_claim": "groups",
	"oidc_group_user": "", "oidc_group_lead": "", "oidc_group_admin": "", "oidc_group_hr": "",
	"oidc_team_prefix": defaultTeamPrefix, // Teams aus Gruppen <präfix><name> / <präfix><name>-lead (teamsync.go); „-“ = aus
}

type oidcProv struct {
	issuer   string
	provider *oidc.Provider
	at       time.Time
}

var (
	oidcMu    sync.Mutex
	oidcCache *oidcProv
)

func (a *App) oidcRedirectURL() (string, error) {
	u, err := url.Parse(a.PublicURL)
	if err != nil || u.Host == "" || (u.Scheme != "https" && !(u.Scheme == "http" && (hostLoopback(u.Host) || a.AllowHTTP))) {
		return "", errors.New("ZP_PUBLIC_URL (https) ist nicht gesetzt – nötig für die Anmeldung über authentik")
	}
	return strings.TrimRight(a.PublicURL, "/") + "/api/auth/oidc/callback", nil
}

func (a *App) oidcClientSecret(ctx context.Context) (string, error) {
	enc := a.conf(ctx, "oidc_client_secret")
	if enc == "" {
		return "", errors.New("Client-Secret fehlt")
	}
	blob, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		return "", errors.New("Client-Secret nicht lesbar (neu setzen)")
	}
	pt, err := crypto.OpenAAD(a.Key, blob, []byte("server|oidc_client_secret"))
	if err != nil {
		return "", errors.New("Client-Secret nicht entschlüsselbar (neu setzen)")
	}
	return string(pt), nil
}

// oidcConfig liefert OAuth-Konfiguration und Token-Prüfer (Anbieterdaten 10 min zwischengespeichert)
func (a *App) oidcConfig(ctx context.Context) (*oauth2.Config, *oidc.IDTokenVerifier, error) {
	issuer, clientID := a.conf(ctx, "oidc_issuer"), a.conf(ctx, "oidc_client_id")
	if issuer == "" || clientID == "" {
		return nil, nil, errors.New("OIDC ist nicht eingerichtet")
	}
	redirect, err := a.oidcRedirectURL()
	if err != nil {
		return nil, nil, err
	}
	secret, err := a.oidcClientSecret(ctx)
	if err != nil {
		return nil, nil, err
	}
	hctx := oidc.ClientContext(ctx, planeHTTP) // ohne Proxy, ohne Umleitungen, Link-Local gesperrt, mit Zeitlimit
	oidcMu.Lock()
	p := oidcCache
	if p == nil || p.issuer != issuer || time.Since(p.at) > 10*time.Minute {
		prov, perr := oidc.NewProvider(hctx, issuer)
		if perr != nil {
			oidcMu.Unlock()
			return nil, nil, fmt.Errorf("Anbieter nicht erreichbar: %w", perr)
		}
		p = &oidcProv{issuer: issuer, provider: prov, at: time.Now()}
		oidcCache = p
	}
	oidcMu.Unlock()
	conf := &oauth2.Config{ClientID: clientID, ClientSecret: secret, Endpoint: p.provider.Endpoint(), RedirectURL: redirect, Scopes: []string{oidc.ScopeOpenID, "profile", "email"}}
	return conf, p.provider.Verifier(&oidc.Config{ClientID: clientID}), nil
}

// flowIPKey: IPv6 auf das /64-Netz zusammenfassen (ein Angreifer hat sonst Milliarden Adressen)
func flowIPKey(ip string) string {
	if p := net.ParseIP(ip); p != nil && p.To4() == nil {
		return p.Mask(net.CIDRMask(64, 128)).String() + "/64"
	}
	return ip
}

func randB64(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func (a *App) routesOIDC(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/auth/oidc/start", a.oidcStart)
	mux.HandleFunc("GET /api/auth/oidc/callback", a.oidcCallback)
}

func (a *App) oidcFail(w http.ResponseWriter, r *http.Request, msg string) {
	http.Redirect(w, r, "/login?e="+url.QueryEscape(msg), http.StatusSeeOther)
}

func (a *App) oidcStart(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if a.conf(ctx, "auth_mode") != "oidc" {
		a.oidcFail(w, r, "off")
		return
	}
	a.initLimits()
	ip := a.clientIP(r)
	ipKey := flowIPKey(ip)
	var mine int
	_ = a.DB.QueryRow(ctx, `SELECT COUNT(*) FROM oidc_flows WHERE ip=$1 AND expires_at > now()`, ipKey).Scan(&mine)
	if mine >= oidcPerIP {
		a.oidcFail(w, r, "busy")
		return
	}
	conf, _, err := a.oidcConfig(ctx)
	if err != nil {
		a.Log.Error("OIDC-Start nicht möglich", "err", err)
		a.oidcFail(w, r, "unreach")
		return
	}
	next := oidcSafeNext(r.URL.Query().Get("next"))
	var n int
	_ = a.DB.QueryRow(ctx, `SELECT COUNT(*) FROM oidc_flows WHERE expires_at > now()`).Scan(&n)
	if n >= oidcMaxFlows {
		a.oidcFail(w, r, "busy")
		return
	}
	cookie, state, nonce, verifier := randB64(32), randB64(24), randB64(24), oauth2.GenerateVerifier()
	if _, err := a.DB.Exec(ctx, `INSERT INTO oidc_flows(id,state,nonce,verifier,next_path,expires_at,ip) VALUES($1,$2,$3,$4,$5,$6,$7)`,
		sessionID(cookie), state, nonce, verifier, next, time.Now().Add(oidcFlowTTL), ipKey); err != nil {
		a.fail(w, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: oidcCookie, Value: cookie, Path: "/api/auth/oidc/", HttpOnly: true, SameSite: http.SameSiteLaxMode,
		Secure: a.secureCookie(r), MaxAge: int(oidcFlowTTL.Seconds())})
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, conf.AuthCodeURL(state, oidc.Nonce(nonce), oauth2.S256ChallengeOption(verifier)), http.StatusFound)
}

func (a *App) oidcCallback(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	w.Header().Set("Cache-Control", "no-store")
	ip := a.clientIP(r)
	http.SetCookie(w, &http.Cookie{Name: oidcCookie, Value: "", Path: "/api/auth/oidc/", HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: a.secureCookie(r), MaxAge: -1})
	if a.conf(ctx, "auth_mode") != "oidc" {
		a.oidcFail(w, r, "off")
		return
	}
	deny := func(why string) {
		a.auditNil(ctx, "login.failed", "OIDC von "+ip+": "+why)
		a.oidcFail(w, r, "fail")
	}
	c, err := r.Cookie(oidcCookie)
	if err != nil || c.Value == "" || len(c.Value) > 100 {
		deny("kein Anmelde-Vorgang im Browser")
		return
	}
	// einmalig: Vorgang wird beim Lesen gelöscht
	var state, nonce, verifier, next string
	err = a.DB.QueryRow(ctx, `DELETE FROM oidc_flows WHERE id=$1 AND expires_at > now() RETURNING state, nonce, verifier, next_path`, sessionID(c.Value)).
		Scan(&state, &nonce, &verifier, &next)
	if err != nil {
		deny("Vorgang unbekannt oder abgelaufen")
		return
	}
	q := r.URL.Query()
	if subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(state)) != 1 {
		deny("state stimmt nicht")
		return
	}
	if e := q.Get("error"); e != "" {
		deny("Anbieter meldet: " + trim(e, 60))
		return
	}
	code := q.Get("code")
	if code == "" || len(code) > 2000 {
		deny("kein Code")
		return
	}
	conf, verifierTok, err := a.oidcConfig(ctx)
	if err != nil {
		a.Log.Error("OIDC-Rücksprung nicht möglich", "err", err)
		a.oidcFail(w, r, "unreach")
		return
	}
	hctx := context.WithValue(ctx, oauth2.HTTPClient, planeHTTP)
	tok, err := conf.Exchange(hctx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		deny("Code-Tausch fehlgeschlagen")
		return
	}
	raw, _ := tok.Extra("id_token").(string)
	if raw == "" {
		deny("kein ID-Token")
		return
	}
	idt, err := verifierTok.Verify(oidc.ClientContext(ctx, planeHTTP), raw)
	if err != nil {
		deny("ID-Token ungültig")
		return
	}
	if subtle.ConstantTimeCompare([]byte(idt.Nonce), []byte(nonce)) != 1 {
		deny("nonce stimmt nicht")
		return
	}
	var cl map[string]any
	if err := idt.Claims(&cl); err != nil {
		deny("Claims nicht lesbar")
		return
	}
	str := func(k string) string { v, _ := cl[k].(string); return strings.TrimSpace(v) }
	if idt.Subject == "" || len(idt.Subject) > 255 {
		deny("sub fehlt")
		return
	}
	groups := map[string]bool{}
	var groupList []string // nil = Claim fehlt (Teams dann unverändert lassen)
	switch g := cl[strings.TrimSpace(a.confDefault(ctx, "oidc_groups_claim"))].(type) {
	case []any:
		groupList = []string{}
		for _, x := range g {
			if s, ok := x.(string); ok {
				groups[s] = true
				if len(groupList) < 1000 {
					groupList = append(groupList, s)
				}
			}
		}
	case string:
		groups[g] = true
		groupList = []string{g}
	}
	if groupList != nil {
		groupList = groupNames(groupList)
	}
	role := ""
	switch {
	case a.conf(ctx, "oidc_group_admin") != "" && groups[a.conf(ctx, "oidc_group_admin")]:
		role = "admin"
	case a.conf(ctx, "oidc_group_lead") != "" && groups[a.conf(ctx, "oidc_group_lead")]:
		role = "lead"
	case a.conf(ctx, "oidc_group_user") != "" && groups[a.conf(ctx, "oidc_group_user")]:
		role = "user"
	}
	hr := a.conf(ctx, "oidc_group_hr") != "" && groups[a.conf(ctx, "oidc_group_hr")]
	if role == "" && hr {
		role = "user" // Personal-Gruppe allein genügt für den Zugang
	}
	uname := strings.ToLower(str("preferred_username"))
	if !usernameOK.MatchString(uname) {
		uname = ""
	}
	if role == "" {
		a.auditNil(ctx, "login.denied", fmt.Sprintf("OIDC „%s“ von %s: nicht in einer erlaubten Gruppe", trim(uname, 64), ip))
		a.oidcFail(w, r, "denied")
		return
	}
	name := trim(str("name"), 120)
	if name == "" {
		name = uname
	}
	subject := "oidc:" + idt.Issuer + "|" + idt.Subject
	uid, _, _, err := a.provisionExternal(ctx, "oidc", subject, uname, name, trim(str("email"), 200), role, hr, groupList, teamPrefix(a.confDefault(ctx, "oidc_team_prefix")))
	if err != nil {
		if errors.Is(err, errLDAPAccount) {
			deny("Konto gesperrt oder Name schon vergeben")
			return
		}
		a.Log.Error("OIDC-Konto", "err", err)
		a.oidcFail(w, r, "fail")
		return
	}
	ttl := oidcSession
	if role == "admin" {
		ttl = 4 * time.Hour
	}
	token, err := a.newSession(ctx, uid, ip, r.UserAgent(), ttl) // jede Anmeldung: neue Sitzung
	if err != nil {
		a.oidcFail(w, r, "fail")
		return
	}
	a.setSessionCookie(w, r, token, ttl)
	a.audit(ctx, uid, "login", "von "+ip+" (authentik)")
	http.Redirect(w, r, oidcSafeNext(next), http.StatusSeeOther)
}

var usernameOK = regexp.MustCompile(`^[a-z0-9][a-z0-9._@-]{2,63}$`)

func (a *App) confDefault(ctx context.Context, k string) string {
	if v := a.conf(ctx, k); v != "" {
		return v
	}
	return OIDCKeys[k]
}

// SetOIDCConfig speichert eine OIDC-Einstellung (Secret verschlüsselt, Aussteller geprüft). Leer löscht. Auch für den Befehl „oidc set“.
func SetOIDCConfig(ctx context.Context, x ConfExecer, key []byte, allowHTTP bool, k, v string) error {
	if _, ok := OIDCKeys[k]; !ok {
		return fmt.Errorf("unbekannte Einstellung %q", k)
	}
	v = strings.TrimSpace(v)
	if len(v) > 2000 {
		return errors.New("Wert zu lang")
	}
	switch k {
	case "oidc_issuer":
		u, err := url.Parse(v) // genau so speichern, wie der Anbieter ihn nennt (authentik: mit „/“ am Ende)
		if v != "" && (err != nil || u.Host == "" || u.User != nil || (u.Scheme != "https" && !(u.Scheme == "http" && (hostLoopback(u.Host) || allowHTTP)))) {
			return errors.New("Issuer: https://… (Adresse des Anbieters, bei authentik …/application/o/<name>/)")
		}
	case "oidc_team_prefix":
		if err := checkTeamPrefix(v); err != nil {
			return err
		}
	case "oidc_client_secret":
		if v != "" {
			blob, err := crypto.SealAAD(key, []byte(v), []byte("server|oidc_client_secret"))
			if err != nil {
				return err
			}
			v = base64.StdEncoding.EncodeToString(blob)
		}
	}
	if v == "" {
		_, err := x.Exec(ctx, `DELETE FROM server_config WHERE key=$1`, k)
		return err
	}
	_, err := x.Exec(ctx, `INSERT INTO server_config(key,value) VALUES($1,$2) ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value, updated_at=now()`, k, v)
	return err
}

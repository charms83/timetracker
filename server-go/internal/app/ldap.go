package app

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"example/zeitprotokoll-server/internal/crypto"
	"example/zeitprotokoll-server/internal/ldapauth"
)

/* Anmeldung gegen LDAP / Windows Active Directory (Modus „ldap“). Das Passwort wird nur zum Verzeichnis gesendet und nie gespeichert.
   Konten entstehen beim ersten Anmelden (kind='ldap', oidc_subject='ldap:<stabile Kennung>'); die Rolle folgt bei jeder Anmeldung den Gruppen.
   Per Passwort geht daneben nur der scharf geschaltete Notfallzugang. */

// LDAPKeys: Einstellungen in server_config (Wert → Standard). ldap_bind_password liegt verschlüsselt.
var LDAPKeys = map[string]string{
	"ldap_url": "", "ldap_starttls": "0", "ldap_ca": "", "ldap_bind_dn": "", "ldap_bind_password": "", "ldap_bind_template": "",
	"ldap_base_dn": "", "ldap_filter": "(&(objectClass=user)(sAMAccountName=%s))",
	"ldap_attr_name": "displayName", "ldap_attr_mail": "mail", "ldap_attr_id": "objectGUID",
	"ldap_group_user": "", "ldap_group_lead": "", "ldap_group_admin": "", "ldap_group_hr": "", "ldap_nested": "0", "ldap_link_user": "", "ldap_link_subject": "",
	"ldap_team_prefix": defaultTeamPrefix, // Teams aus Gruppen <präfix><name> / <präfix><name>-lead (teamsync.go); „-“ = aus
	"ldap_team_base":   "",                // nur Team-Gruppen unterhalb dieser DN (empfohlen: OU, die nur Berechtigte pflegen)
}

type ConfQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}
type ConfExecer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

func ldapSecretAAD() []byte { return []byte("server|ldap_bind_password") }

func ldapConf(ctx context.Context, q ConfQuerier, k string) string {
	var v string
	if err := q.QueryRow(ctx, `SELECT value FROM server_config WHERE key=$1`, k).Scan(&v); err != nil || v == "" {
		return LDAPKeys[k]
	}
	return v
}

// LoadLDAPConfig liest die Einstellungen (inkl. Dienstkonto-Passwort, entschlüsselt)
func LoadLDAPConfig(ctx context.Context, q ConfQuerier, key []byte) (ldapauth.Config, error) {
	g := func(k string) string { return strings.TrimSpace(ldapConf(ctx, q, k)) }
	c := ldapauth.Config{
		URL: g("ldap_url"), StartTLS: g("ldap_starttls") == "1", CAPEM: ldapConf(ctx, q, "ldap_ca"), BindDN: g("ldap_bind_dn"),
		BindTemplate: g("ldap_bind_template"), BaseDN: g("ldap_base_dn"), UserFilter: g("ldap_filter"),
		AttrName: g("ldap_attr_name"), AttrMail: g("ldap_attr_mail"), AttrID: g("ldap_attr_id"),
		GroupUser: g("ldap_group_user"), GroupLead: g("ldap_group_lead"), GroupAdmin: g("ldap_group_admin"), GroupHR: g("ldap_group_hr"), Nested: g("ldap_nested") == "1",
		Timeout: 10 * time.Second,
	}
	if enc := ldapConf(ctx, q, "ldap_bind_password"); enc != "" {
		blob, err := base64.StdEncoding.DecodeString(enc)
		if err != nil {
			return c, errors.New("Dienstkonto-Passwort nicht lesbar (neu setzen)")
		}
		pt, err := crypto.OpenAAD(key, blob, ldapSecretAAD())
		if err != nil {
			return c, errors.New("Dienstkonto-Passwort nicht entschlüsselbar (neu setzen)")
		}
		c.BindPassword = string(pt)
	}
	return c, nil
}

// SetLDAPConfig speichert eine Einstellung (Dienstkonto-Passwort verschlüsselt). Leerer Wert löscht.
func SetLDAPConfig(ctx context.Context, x ConfExecer, key []byte, k, v string) error {
	if _, ok := LDAPKeys[k]; !ok {
		return fmt.Errorf("unbekannte Einstellung %q", k)
	}
	v = strings.TrimSpace(v)
	if len(v) > 4000 {
		return errors.New("Wert zu lang")
	}
	switch k {
	case "ldap_starttls", "ldap_nested":
		if v != "0" && v != "1" {
			return errors.New("nur 0 oder 1")
		}
	case "ldap_url":
		if v != "" && !strings.HasPrefix(v, "ldaps://") && !strings.HasPrefix(v, "ldap://") {
			return errors.New("ldaps://host:636 oder ldap://host (mit ldap_starttls=1)")
		}
	case "ldap_filter":
		if v != "" && strings.Count(v, "%s") != 1 {
			return errors.New("Filter braucht genau ein %s")
		}
	case "ldap_team_base":
		if v != "" && !strings.Contains(v, "=") {
			return errors.New("Basis für Team-Gruppen als DN angeben (ou=…,dc=…)")
		}
	case "ldap_team_prefix":
		if err := checkTeamPrefix(v); err != nil {
			return err
		}
	case "ldap_bind_password":
		if v != "" {
			blob, err := crypto.SealAAD(key, []byte(v), ldapSecretAAD())
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

var errLDAPAccount = errors.New("Konto gesperrt oder nicht zulässig")

// ProvisionLDAPUser legt das Konto zur Verzeichnis-Identität an oder aktualisiert es (Name, Mail, Rolle aus Gruppen).
// Ein lokales Konto wird nur übernommen, wenn der Administrator es mit „ldap_link_user“ ausdrücklich freigegeben hat (einmalig).
func (a *App) ProvisionLDAPUser(ctx context.Context, res *ldapauth.Result) (uid int64, name, role string, err error) {
	return a.provisionExternal(ctx, "ldap", "ldap:"+res.ID, res.Username, res.Name, res.Mail, res.Role, res.HR,
		groupNames(teamGroups(res.Groups, ldapConf(ctx, a.DB, "ldap_team_base"))), teamPrefix(ldapConf(ctx, a.DB, "ldap_team_prefix")))
}

// provisionExternal: gemeinsam für LDAP und OIDC (kind 'ldap' | 'oidc'); subject ist die stabile Kennung im Verzeichnis.
// groups (Namen, nil = unbekannt) und prefix steuern die Teams aus dem Verzeichnis (teamsync.go), in derselben Transaktion.
func (a *App) provisionExternal(ctx context.Context, kind, subject, username, dname, dmail, drole string, hr bool, groups []string, prefix string) (uid int64, name, role string, err error) {
	tx, err := a.DB.Begin(ctx)
	if err != nil {
		return 0, "", "", err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(727002)`); err != nil {
		return 0, "", "", err
	}
	mail := dmail
	if mail == "" {
		mail = "kein-mail@" + kind + ".invalid"
	}
	var disabled, oldHR bool
	err = tx.QueryRow(ctx, `SELECT id, disabled, is_hr FROM users WHERE oidc_subject=$1 AND kind=$2`, subject, kind).Scan(&uid, &disabled, &oldHR)
	switch {
	case err == nil:
		if disabled {
			return 0, "", "", errLDAPAccount
		}
		if oldHR != hr {
			_, _ = tx.Exec(ctx, `INSERT INTO audit_log(user_id,action,detail) VALUES($1,'user.hr_synced',$2)`, uid, fmt.Sprintf("Personal-Recht %v → %v (Verzeichnisgruppe)", oldHR, hr))
		}
		if kind == "ldap" { // Verzeichnisname ist geprüft; bei OIDC ist preferred_username änderbar und wird nur beim Anlegen übernommen
			_, err = tx.Exec(ctx, `UPDATE users SET name=$2, email=$3, role=$4, username=NULLIF($5,''), is_hr=$6, last_login=now() WHERE id=$1`, uid, dname, mail, drole, username, hr)
		} else {
			_, err = tx.Exec(ctx, `UPDATE users SET name=$2, email=$3, role=$4, is_hr=$5, last_login=now() WHERE id=$1`, uid, dname, mail, drole, hr)
		}
	case errors.Is(err, pgx.ErrNoRows):
		link := ""
		_ = tx.QueryRow(ctx, `SELECT lower(value) FROM server_config WHERE key='ldap_link_user' AND updated_at > now() - interval '24 hours'`).Scan(&link) // Freigabe gilt 24 h
		var lid int64
		linkSub := ""
		_ = tx.QueryRow(ctx, `SELECT value FROM server_config WHERE key='ldap_link_subject' AND updated_at > now() - interval '24 hours'`).Scan(&linkSub)
		// OIDC: Freigabe nur für genau diese Kennung (sub), nie nach Anzeigename; LDAP: geprüfter Verzeichnisname
		if link != "" && username != "" && link == username && (kind == "ldap" || (linkSub != "" && strings.HasSuffix(subject, "|"+linkSub))) {
			e := tx.QueryRow(ctx, `SELECT id FROM users WHERE kind='local' AND role<>'breakglass' AND productive AND NOT disabled AND lower(username)=$1`, link).Scan(&lid)
			if e != nil && !errors.Is(e, pgx.ErrNoRows) {
				return 0, "", "", e
			}
		}
		if lid != 0 { // bestehendes lokales Konto übernehmen: Daten bleiben, Passwort entfällt, alte Sitzungen enden
			uid = lid
			if _, err = tx.Exec(ctx, `UPDATE users SET kind=$7, oidc_subject=$2, password_hash=NULL, must_change_password=false, name=$3, email=$4, role=$5, username=NULLIF($6,''), is_hr=$8, last_login=now() WHERE id=$1`,
				uid, subject, dname, mail, drole, username, kind, hr); err == nil {
				_, err = tx.Exec(ctx, `DELETE FROM sessions WHERE user_id=$1`, uid)
			}
			if err == nil {
				_, err = tx.Exec(ctx, `DELETE FROM server_config WHERE key IN ('ldap_link_user','ldap_link_subject')`)
			}
			if err == nil {
				_, err = tx.Exec(ctx, `INSERT INTO audit_log(user_id,action,detail) VALUES($1,'user.linked_'||$3::text,$2)`, uid, "Lokales Konto mit Verzeichnis-Konto "+username+" verknüpft", kind)
			}
		} else {
			if kind == "oidc" && username != "" { // Name schon vergeben (Besetzung durch Dritte): Konto ohne Benutzernamen anlegen statt ablehnen
				var taken bool
				_ = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE lower(username)=$1)`, username).Scan(&taken)
				if taken {
					username = ""
				}
			}
			err = tx.QueryRow(ctx, `INSERT INTO users(oidc_subject,email,name,role,kind,username,productive,is_hr) VALUES($1,$2,$3,$4,$6,NULLIF($5,''),true,$7) RETURNING id`,
				subject, mail, dname, drole, username, kind, hr).Scan(&uid)
			if err == nil {
				_, err = tx.Exec(ctx, `INSERT INTO audit_log(user_id,action,detail) VALUES($1,'user.created_'||$3::text,$2)`, uid, "Beim ersten Anmelden angelegt: "+username+" ("+drole+")", kind)
			}
		}
	}
	var pe *pgconn.PgError
	if errors.As(err, &pe) && pe.Code == "23505" { // Name oder Kennung schon vergeben: ablehnen statt Fehler 500
		return 0, "", "", errLDAPAccount
	}
	if err != nil {
		return 0, "", "", err
	}
	skipped, err := syncDirectoryTeams(ctx, tx, uid, groups, prefix)
	if err != nil {
		return 0, "", "", err
	}
	if err = tx.Commit(ctx); err != nil {
		return 0, "", "", err
	}
	a.logSkippedTeams(ctx, uid, skipped)
	return uid, dname, drole, nil
}

// ldapLogin: Anmeldung im Modus „ldap“ (gleiche Sperren wie bei lokalen Konten)
func (a *App) ldapLogin(w http.ResponseWriter, r *http.Request, username, password, ip string) {
	ctx := r.Context()
	tooMany := func() {
		writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": "Zu viele Fehlversuche – bitte später erneut versuchen"})
	}
	if !a.loginReserve("login_ip", ip, a.loginFailsIP) {
		tooMany()
		return
	}
	key := username + "|" + ip
	if !a.loginReserve("login_user", key, a.loginFailsUser) {
		a.loginRelease("login_ip", ip, a.loginFailsIP)
		tooMany()
		return
	}
	release := func() { a.loginRelease("login_ip", ip, a.loginFailsIP); a.loginRelease("login_user", key, a.loginFailsUser) }
	cfg, err := LoadLDAPConfig(ctx, a.DB, a.Key)
	if err == nil {
		err = cfg.Validate()
	}
	if err != nil {
		release()
		a.Log.Error("LDAP nicht (richtig) eingerichtet", "err", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "Anmeldung über das Verzeichnis ist nicht eingerichtet"})
		return
	}
	res, err := ldapauth.Authenticate(ctx, cfg, username, password)
	switch {
	case err == nil:
	case errors.Is(err, ldapauth.ErrInvalidCredentials):
		a.auditNil(ctx, "login.failed", fmt.Sprintf("Benutzer „%s“ von %s (Verzeichnis)", trim(username, 64), ip))
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "Anmeldung fehlgeschlagen"})
		return
	case errors.Is(err, ldapauth.ErrNotAllowed):
		a.auditNil(ctx, "login.denied", fmt.Sprintf("Benutzer „%s“ von %s: nicht in einer erlaubten Gruppe", trim(username, 64), ip))
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "Anmeldung fehlgeschlagen"})
		return
	default: // Verzeichnis nicht erreichbar o. Ä.: kein Fehlversuch der Person; Abbruch/Zeitüberschreitung des Clients zählt aber als Versuch
		if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			release()
		}
		a.Log.Error("LDAP-Anmeldung nicht möglich", "err", err)
		a.sysLog(ctx, 0, "error", "betrieb", "LDAP-Anmeldung nicht möglich: "+trim(err.Error(), 200))
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "Das Verzeichnis ist gerade nicht erreichbar"})
		return
	}
	uid, name, role, err := a.ProvisionLDAPUser(ctx, res)
	if err != nil {
		release()
		if errors.Is(err, errLDAPAccount) {
			a.auditNil(ctx, "login.failed", fmt.Sprintf("Benutzer „%s“ von %s: Konto gesperrt", trim(username, 64), ip))
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "Anmeldung fehlgeschlagen"})
			return
		}
		a.fail(w, err)
		return
	}
	release()
	token, err := a.newSession(ctx, uid, ip, r.UserAgent(), sessionAbsolute)
	if err != nil {
		a.fail(w, err)
		return
	}
	a.setSessionCookie(w, r, token, sessionAbsolute)
	a.audit(ctx, uid, "login", "von "+ip+" (Verzeichnis)")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "user": User{ID: uid, Name: name, Role: role, Kind: "ldap"}, "must_change_password": false})
}

// ldapRecheckTick: prüft alle LDAP-Konten gegen das Verzeichnis (nur mit Dienstkonto). Gesperrt/entfernt/ohne Gruppe → Sitzungen enden sofort;
// geänderte Rolle wird übernommen. Ist das Verzeichnis nicht erreichbar, bleibt alles unverändert (kein Aussperren bei Ausfall).
func (a *App) ldapRecheckTick(ctx context.Context) {
	if a.conf(ctx, "auth_mode") != "ldap" {
		return
	}
	cfg, err := LoadLDAPConfig(ctx, a.DB, a.Key)
	if err != nil || cfg.Validate() != nil || cfg.BindDN == "" {
		return
	}
	prefix, base := teamPrefix(ldapConf(ctx, a.DB, "ldap_team_prefix")), ldapConf(ctx, a.DB, "ldap_team_base")
	rows, err := a.DB.Query(ctx, `SELECT id, username, role, oidc_subject, is_hr FROM users WHERE kind='ldap' AND NOT disabled AND username IS NOT NULL`)
	if err != nil {
		return
	}
	type row struct {
		id             int64
		user, role, sb string
		hr             bool
	}
	var list []row
	for rows.Next() {
		var x row
		if rows.Scan(&x.id, &x.user, &x.role, &x.sb, &x.hr) == nil {
			list = append(list, x)
		}
	}
	rows.Close()
	for _, x := range list {
		res, gone, err := ldapauth.Recheck(ctx, cfg, x.user)
		if err != nil {
			a.Log.Warn("LDAP-Nachprüfung nicht möglich", "err", err)
			return // Verzeichnis gestört: nichts ändern
		}
		switch {
		case gone:
			n, _ := a.DB.Exec(ctx, `DELETE FROM sessions WHERE user_id=$1`, x.id)
			if n.RowsAffected() > 0 {
				a.audit(ctx, x.id, "login.revoked", "Im Verzeichnis gesperrt, entfernt oder ohne Gruppe – Sitzungen beendet")
			}
		case res.ID != "" && "ldap:"+res.ID != x.sb:
			a.DB.Exec(ctx, `DELETE FROM sessions WHERE user_id=$1`, x.id) //nolint:errcheck // anderer Mensch mit gleichem Namen
		case res.Role != x.role || res.HR != x.hr:
			a.DB.Exec(ctx, `UPDATE users SET role=$2, is_hr=$3 WHERE id=$1`, x.id, res.Role, res.HR) //nolint:errcheck
			a.audit(ctx, x.id, "user.role_synced", x.role+" → "+res.Role+" (Verzeichnis)")
			if res.Role != "admin" {
				a.DB.Exec(ctx, `DELETE FROM sessions WHERE user_id=$1`, x.id) //nolint:errcheck
			}
		}
		// Teams aus dem Verzeichnis: gesperrt/entfernt → Verzeichnis-Mitgliedschaften enden; andere Person mit gleichem Namen → nichts ändern
		var groups []string
		switch {
		case gone:
			groups = []string{}
		case res != nil && (res.ID == "" || "ldap:"+res.ID == x.sb):
			groups = groupNames(teamGroups(res.Groups, base))
		}
		if groups != nil {
			a.recheckTeams(ctx, x.id, groups, prefix)
		}
	}
}

// recheckTeams: Teamabgleich aus der Nachprüfung (gleiche Sperre wie beim Login, damit beide sich nicht überholen)
func (a *App) recheckTeams(ctx context.Context, uid int64, groups []string, prefix string) {
	var skipped []string
	err := a.inTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(727002)`); err != nil {
			return err
		}
		var err error
		skipped, err = syncDirectoryTeams(ctx, tx, uid, groups, prefix)
		return err
	})
	if err != nil {
		a.Log.Warn("Teamabgleich (Verzeichnis) fehlgeschlagen", "person", uid, "err", err)
		return
	}
	a.logSkippedTeams(ctx, uid, skipped)
}

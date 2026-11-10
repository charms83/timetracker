package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"example/zeitprotokoll-server/internal/app"
	"example/zeitprotokoll-server/internal/config"
	"example/zeitprotokoll-server/internal/crypto"
)

var usernameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{2,31}$`)

func flagValue(args []string, name string) string {
	for i, a := range args {
		if a == name && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func authMode(ctx context.Context, pool *pgxpool.Pool) string {
	var v string
	_ = pool.QueryRow(ctx, `SELECT value FROM server_config WHERE key='auth_mode'`).Scan(&v)
	return v
}

func printPassword(who, pw string) {
	fmt.Println()
	fmt.Println("  ┌─────────────────────────────────────────────────────────────")
	fmt.Printf("  │ Benutzer:  %s\n", who)
	fmt.Printf("  │ Passwort:  %s\n", pw)
	fmt.Println("  │ Wird nur jetzt angezeigt. Nicht in Chat, E-Mail oder Ticket kopieren.")
	fmt.Println("  └─────────────────────────────────────────────────────────────")
	fmt.Println()
}

// cmdAccounts: user add-local | reset-password | remove, breakglass …, auth-mode. Gibt (Exitcode, behandelt) zurück.
func cmdAccounts(ctx context.Context, pool *pgxpool.Pool, cfg config.Config, cmd string, args []string) (int, bool) {
	switch cmd {
	case "user":
		if len(args) == 0 {
			return 0, false
		}
		switch args[0] {
		case "add-local":
			return userAddLocal(ctx, pool, args[1:]), true
		case "reset-password":
			return userResetPassword(ctx, pool, args[1:]), true
		case "remove":
			return userRemove(ctx, pool, args[1:]), true
		case "set-hr":
			return userSetHR(ctx, pool, args[1:]), true
		case "set-suggest":
			return userSetSuggest(ctx, pool, args[1:]), true
		case "logout":
			return userLogout(ctx, pool, args[1:]), true
		}
	case "team":
		return cmdTeam(ctx, pool, args), true
	case "oidc":
		return cmdOIDC(ctx, pool, cfg, args), true
	case "ldap":
		return cmdLDAP(ctx, pool, cfg, args), true
	case "breakglass":
		return cmdBreakGlass(ctx, pool, args), true
	case "auth-mode":
		return cmdAuthMode(ctx, pool, cfg, args), true
	}
	return 0, false
}

func userAddLocal(ctx context.Context, pool *pgxpool.Pool, args []string) int {
	if len(args) < 3 {
		fmt.Fprintln(os.Stderr, "Aufruf: user add-local <benutzername> <E-Mail> <user|lead|admin> [--test]")
		return 2
	}
	username, email, role := strings.ToLower(args[0]), args[1], args[2]
	if !usernameRE.MatchString(username) {
		fmt.Fprintln(os.Stderr, "Benutzername: 3–32 Zeichen aus a–z, 0–9, Punkt, Bindestrich, Unterstrich")
		return 2
	}
	if role != "user" && role != "lead" && role != "admin" {
		fmt.Fprintln(os.Stderr, "Rolle: user, lead oder admin")
		return 2
	}
	productive := role != "admin" && !has(args, "--test")
	pw, err := app.GeneratePassword()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Fehler:", err)
		return 1
	}
	hash, err := app.HashPassword(pw)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Fehler:", err)
		return 1
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Fehler:", err)
		return 1
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(727002)`); err != nil {
		fmt.Fprintln(os.Stderr, "Fehler:", err)
		return 1
	}
	if !productive && role != "admin" {
		fmt.Fprintln(os.Stderr, "Ein Testkonto (--test) muss Administrator sein.")
		return 2
	}
	if _, err := tx.Exec(ctx, `INSERT INTO users(oidc_subject,email,name,role,kind,username,password_hash,productive,must_change_password)
		VALUES('local:'||$1,$2,$1,$3,'local',$1,$4,$5,true)`, username, email, role, hash, productive); err != nil {
		fmt.Fprintln(os.Stderr, "Fehler (Benutzername schon vergeben?):", err)
		return 1
	}
	if authMode(ctx, pool) == "local" {
		if err := app.CheckLocalLimits(ctx, tx); err != nil {
			fmt.Fprintln(os.Stderr, "Abgelehnt:", err)
			return 1
		}
	}
	if err := tx.Commit(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "Fehler:", err)
		return 1
	}
	audit(ctx, pool, "cli.user", fmt.Sprintf("add-local %s (%s, produktiv %v)", username, role, productive))
	fmt.Println("Konto angelegt (Passwort muss bei der ersten Anmeldung geändert werden).")
	printPassword(username, pw)
	return 0
}

func userResetPassword(ctx context.Context, pool *pgxpool.Pool, args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Aufruf: user reset-password <benutzername>")
		return 2
	}
	username := strings.ToLower(args[0])
	pw, err := app.GeneratePassword()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Fehler:", err)
		return 1
	}
	hash, err := app.HashPassword(pw)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Fehler:", err)
		return 1
	}
	var id int64
	var role string
	if username == "admin" { // Testkonto aus dem Testmodus (ohne Benutzername) wird zum Konto „admin“
		_, _ = pool.Exec(ctx, `UPDATE users SET username='admin' WHERE oidc_subject='local' AND username IS NULL AND NOT EXISTS (SELECT 1 FROM users WHERE lower(username)='admin')`)
	}
	if err := pool.QueryRow(ctx, `SELECT id, role FROM users WHERE lower(username)=$1 AND kind='local'`, username).Scan(&id, &role); err != nil {
		fmt.Fprintln(os.Stderr, "Kein lokales Konto mit diesem Benutzernamen:", username)
		return 1
	}
	if role == "breakglass" {
		fmt.Fprintln(os.Stderr, "Für den Notfallzugang: server breakglass reset")
		return 2
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET password_hash=$2, must_change_password=true WHERE id=$1`, id, hash); err != nil {
		fmt.Fprintln(os.Stderr, "Fehler:", err)
		return 1
	}
	_, _ = pool.Exec(ctx, `DELETE FROM sessions WHERE user_id=$1`, id)
	audit(ctx, pool, "cli.user", "reset-password "+username)
	printPassword(username, pw)
	return 0
}

func userRemove(ctx context.Context, pool *pgxpool.Pool, args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Aufruf: user remove <benutzername> [--delete-data]")
		return 2
	}
	username := strings.ToLower(args[0])
	var id int64
	var role string
	if err := pool.QueryRow(ctx, `SELECT id, role FROM users WHERE lower(username)=$1 AND kind='local'`, username).Scan(&id, &role); err != nil {
		fmt.Fprintln(os.Stderr, "Kein lokales Konto mit diesem Benutzernamen:", username)
		return 1
	}
	if role == "breakglass" {
		fmt.Fprintln(os.Stderr, "Der Notfallzugang wird nicht entfernt. Sperren: server breakglass disarm")
		return 2
	}
	var entries, projects int64
	_ = pool.QueryRow(ctx, `SELECT (SELECT COUNT(*) FROM time_entries WHERE user_id=$1), (SELECT COUNT(*) FROM projects WHERE user_id=$1)`, id).Scan(&entries, &projects)
	if (entries > 0 || projects > 0) && !has(args, "--delete-data") {
		fmt.Fprintf(os.Stderr, "Das Konto hat Daten (%d Einträge, %d Projekte). Mit --delete-data wird ALLES dieser Person gelöscht – oder stattdessen sperren: user disable %s\n", entries, projects, username)
		return 1
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Fehler:", err)
		return 1
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	_, _ = tx.Exec(ctx, `SELECT id FROM users WHERE role='admin' AND NOT disabled FOR UPDATE`)
	if _, err := tx.Exec(ctx, `DELETE FROM users WHERE id=$1`, id); err != nil {
		fmt.Fprintln(os.Stderr, "Fehler:", err)
		return 1
	}
	var admins int
	_ = tx.QueryRow(ctx, `SELECT COUNT(*) FROM users WHERE role='admin' AND NOT disabled`).Scan(&admins)
	if admins == 0 {
		fmt.Fprintln(os.Stderr, "Abgebrochen: Es muss mindestens ein aktiver Administrator bleiben.")
		return 1
	}
	if err := tx.Commit(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "Fehler:", err)
		return 1
	}
	audit(ctx, pool, "cli.user", fmt.Sprintf("remove %s (%d Einträge, %d Projekte gelöscht)", username, entries, projects))
	fmt.Println("OK")
	return 0
}

/* ── Notfallzugang ─────────────────────────────────────────────── */

// bgUser: Name des Notfallzugangs. Beim Anlegen mit Zufallsanteil (nicht erratbar), danach aus der Datenbank gelesen.
func newBGUser() string {
	b := make([]byte, 3)
	_, _ = rand.Read(b)
	return "notfall-" + hex.EncodeToString(b)
}

func bgName(ctx context.Context, pool *pgxpool.Pool) string {
	var u string
	_ = pool.QueryRow(ctx, `SELECT COALESCE(username,'notfall') FROM users WHERE role='breakglass'`).Scan(&u)
	return u
}

func cmdBreakGlass(ctx context.Context, pool *pgxpool.Pool, args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Aufruf: breakglass create | reset | arm [--minutes N] --reason \"…\" | disarm | status")
		return 2
	}
	switch args[0] {
	case "create", "reset":
		pw, err := app.GeneratePassword()
		if err != nil {
			fmt.Fprintln(os.Stderr, "Fehler:", err)
			return 1
		}
		hash, err := app.HashPassword(pw)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Fehler:", err)
			return 1
		}
		var id int64
		exists := pool.QueryRow(ctx, `SELECT id FROM users WHERE role='breakglass'`).Scan(&id) == nil
		if args[0] == "create" {
			if exists {
				fmt.Fprintln(os.Stderr, "Der Notfallzugang existiert schon (Passwort neu setzen: breakglass reset)")
				return 1
			}
			name := newBGUser()
			if _, err := pool.Exec(ctx, `INSERT INTO users(oidc_subject,email,name,role,kind,username,password_hash,productive)
				VALUES('local:'||$1,'notfall@lokal','Notfallzugang','breakglass','local',$1,$2,false)`, name, hash); err != nil {
				fmt.Fprintln(os.Stderr, "Fehler:", err)
				return 1
			}
			audit(ctx, pool, "cli.breakglass", "Notfallzugang angelegt")
		} else {
			if !exists {
				fmt.Fprintln(os.Stderr, "Es gibt keinen Notfallzugang (breakglass create)")
				return 1
			}
			if _, err := pool.Exec(ctx, `UPDATE users SET password_hash=$2, armed_until=NULL, armed_reason=NULL WHERE id=$1`, id, hash); err != nil {
				fmt.Fprintln(os.Stderr, "Fehler:", err)
				return 1
			}
			_, _ = pool.Exec(ctx, `DELETE FROM sessions WHERE user_id=$1`, id)
			audit(ctx, pool, "cli.breakglass", "Passwort des Notfallzugangs neu gesetzt")
		}
		fmt.Println("Notfallzugang bereit (gesperrt, bis er scharf geschaltet wird). Das Passwort gehört in den Tresor / einen versiegelten Umschlag.")
		printPassword(bgName(ctx, pool), pw)
		return 0
	case "arm":
		minutes := 60
		if v := flagValue(args, "--minutes"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 5 || n > 240 {
				fmt.Fprintln(os.Stderr, "--minutes: 5 bis 240")
				return 2
			}
			minutes = n
		}
		reason := strings.TrimSpace(flagValue(args, "--reason"))
		if len([]rune(reason)) < 10 {
			fmt.Fprintln(os.Stderr, "--reason \"…\" ist Pflicht (mindestens 10 Zeichen), z. B. \"authentik ausgefallen, Ticket PRJA-123\"")
			return 2
		}
		until := time.Now().Add(time.Duration(minutes) * time.Minute)
		tag, err := pool.Exec(ctx, `UPDATE users SET armed_until=$1, armed_reason=$2 WHERE role='breakglass'`, until, reason)
		if err != nil || tag.RowsAffected() == 0 {
			fmt.Fprintln(os.Stderr, "Kein Notfallzugang vorhanden (breakglass create)", err)
			return 1
		}
		audit(ctx, pool, "cli.breakglass", fmt.Sprintf("SCHARF geschaltet für %d Minuten: %s", minutes, reason))
		fmt.Printf("NOTFALLZUGANG SCHARF bis %s (%d Minuten). Anmelden: /login mit Benutzer „%s“.\nDer Zugang erlaubt nur Betrieb (Status, Selbsttest, Logs, Einstellungen, Monat abschließen), keine Zeitdaten. Alles wird protokolliert.\n",
			until.Format("02.01.2006 15:04"), minutes, bgName(ctx, pool))
		return 0
	case "disarm":
		_, _ = pool.Exec(ctx, `UPDATE users SET armed_until=NULL, armed_reason=NULL WHERE role='breakglass'`)
		_, _ = pool.Exec(ctx, `DELETE FROM sessions WHERE user_id IN (SELECT id FROM users WHERE role='breakglass')`)
		audit(ctx, pool, "cli.breakglass", "gesperrt (Freigabe zurückgenommen)")
		fmt.Println("Notfallzugang gesperrt, Sitzungen beendet.")
		return 0
	case "status":
		var until *time.Time
		var reason *string
		var id int64
		if err := pool.QueryRow(ctx, `SELECT id, armed_until, armed_reason FROM users WHERE role='breakglass'`).Scan(&id, &until, &reason); err != nil {
			fmt.Println("Kein Notfallzugang angelegt (breakglass create).")
			return 0
		}
		if until != nil && until.After(time.Now()) {
			fmt.Printf("SCHARF bis %s – Grund: %s\n", until.Format("02.01.2006 15:04"), *reason)
		} else {
			fmt.Println("vorhanden, gesperrt")
		}
		return 0
	}
	fmt.Fprintln(os.Stderr, "Aufruf: breakglass create | reset | arm [--minutes N] --reason \"…\" | disarm | status")
	return 2
}

/* ── Anmeldeart ────────────────────────────────────────────────── */

func cmdAuthMode(ctx context.Context, pool *pgxpool.Pool, cfg config.Config, args []string) int {
	if len(args) != 1 || (args[0] != "local" && args[0] != "oidc" && args[0] != "ldap" && args[0] != "none") {
		fmt.Fprintln(os.Stderr, "Aufruf: auth-mode <local|oidc|ldap|none>")
		return 2
	}
	mode := args[0]
	switch mode {
	case "none":
		if cfg.Production {
			fmt.Fprintln(os.Stderr, "Abgelehnt: Der Testmodus ohne Anmeldung ist im Betrieb nicht erlaubt.")
			return 1
		}
	case "local":
		var n int
		_ = pool.QueryRow(ctx, `SELECT COUNT(*) FROM users WHERE kind='local' AND role='admin' AND NOT disabled AND password_hash IS NOT NULL`).Scan(&n)
		tx, err := pool.Begin(ctx)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Fehler:", err)
			return 1
		}
		defer tx.Rollback(ctx) //nolint:errcheck
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(727002)`); err != nil {
			fmt.Fprintln(os.Stderr, "Fehler:", err)
			return 1
		}
		if lerr := app.CheckLocalLimits(ctx, tx); lerr != nil {
			fmt.Fprintln(os.Stderr, "Abgelehnt:", lerr)
			return 1
		}
		if n == 0 {
			fmt.Fprintln(os.Stderr, "Abgelehnt: Es gibt keinen lokalen Administrator mit Passwort (user add-local <name> <mail> admin  oder  user reset-password admin).")
			return 1
		}
	case "ldap":
		key, _, kerr := crypto.ReadKey(cfg.SecretKey, cfg.DataDir)
		if kerr != nil {
			fmt.Fprintln(os.Stderr, "Fehler:", kerr)
			return 1
		}
		lc, lerr := app.LoadLDAPConfig(ctx, pool, key)
		if lerr == nil {
			lerr = lc.Validate()
		}
		if lerr != nil {
			fmt.Fprintln(os.Stderr, "Abgelehnt: LDAP ist nicht vollständig eingerichtet:", lerr, "(server ldap show / ldap set …)")
			return 1
		}
	case "oidc":
		for _, k := range []string{"oidc_issuer", "oidc_client_id", "oidc_client_secret", "oidc_group_user"} {
			var v string
			_ = pool.QueryRow(ctx, `SELECT value FROM server_config WHERE key=$1`, k).Scan(&v)
			if v == "" {
				fmt.Fprintln(os.Stderr, "Abgelehnt: OIDC ist nicht vollständig eingerichtet ("+k+" fehlt; server oidc show).")
				return 1
			}
		}
		if !strings.HasPrefix(cfg.PublicURL, "https://") && !cfg.AllowHTTP && !strings.HasPrefix(cfg.PublicURL, "http://localhost") {
			fmt.Fprintln(os.Stderr, "Abgelehnt: ZP_PUBLIC_URL (https) ist nicht gesetzt.")
			return 1
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO server_config(key,value) VALUES('auth_mode',$1) ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value, updated_at=now()`, mode); err != nil {
		fmt.Fprintln(os.Stderr, "Fehler:", err)
		return 1
	}
	_, _ = pool.Exec(ctx, `DELETE FROM sessions`) // alle müssen sich neu anmelden
	audit(ctx, pool, "cli.config", "Anmeldeart auf "+mode+" gestellt (alle Sitzungen beendet)")
	fmt.Println("Anmeldeart:", mode, "(alle Sitzungen beendet)")
	return 0
}

// userLogout beendet alle Sitzungen einer Person sofort (z. B. nach Sperrung im Verzeichnis, wenn die Nachprüfung noch nicht lief)
func userLogout(ctx context.Context, pool *pgxpool.Pool, args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "Aufruf: user logout <benutzername>")
		return 2
	}
	tag, err := pool.Exec(ctx, `DELETE FROM sessions WHERE user_id IN (SELECT id FROM users WHERE lower(username)=$1)`, strings.ToLower(args[0]))
	if err != nil {
		fmt.Fprintln(os.Stderr, "Fehler:", err)
		return 1
	}
	audit(ctx, pool, "cli.user", "Sitzungen von "+strings.ToLower(args[0])+" beendet")
	fmt.Printf("%d Sitzung(en) beendet.\n", tag.RowsAffected())
	return 0
}

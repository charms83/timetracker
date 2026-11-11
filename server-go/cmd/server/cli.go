package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"example/zeitprotokoll-server/internal/app"
	"example/zeitprotokoll-server/internal/config"
	"example/zeitprotokoll-server/internal/db"
)

const usage = `Zeitprotokoll-Server

Aufruf:  server [Befehl]
  (ohne Befehl) / serve              Server starten
  team list|add|member|…             Teams und Teamleiter; user set-hr: Personal-Recht (siehe: server team)
  oidc show|set|set-secret|link …    authentik/OIDC-Anmeldung einrichten (siehe: server oidc)
  ldap show|set|test|link …          LDAP/AD-Anmeldung einrichten (siehe: server ldap)
  status                             Zustand: Version, Datenbank, Anmeldung, Zähler
  version                            Programmversion (ohne Datenbank)
  check [--fix] [--force]            Selbsttest; --fix zieht Migrationen nach und räumt auf,
                                     --force löscht außerdem nicht lesbare Zugangsdaten
  logs [-n 100] [--level warn|error|info] [--category plane] [-q Text] [-f]
                                     Anwendungs-Log lesen; -f liest mit
  users                              Benutzer auflisten
  user set-role <E-Mail> <user|lead|admin>
  user disable <E-Mail> | user enable <E-Mail>
  user add-local <name> <E-Mail> <user|lead|admin> [--test]   lokales Konto (Passwort wird einmal angezeigt)
  user reset-password <name> | user remove <name> [--delete-data]
  breakglass create | reset | arm [--minutes N] --reason "…" | disarm | status   Notfallzugang
  auth-mode <local|oidc|none>        Anmeldeart umstellen (beendet alle Sitzungen)
  config list                        Servereinstellungen zeigen
  config set <plane_base_url|plane_workspace|module_tempo|module_suggest|approval_…> <Wert>
                                     module_tempo 1/0 schaltet Jira/Tempo ein/aus, module_suggest 1/0 die Ticket-Vorschläge
  user set-suggest <benutzer> <on|off>   Ticket-Vorschläge für eine Person erlauben/sperren
  jira list|add|set|remove …         Jira-Instanzen verwalten (server jira für Hilfe)
  rekey --from-env|--from-file --to-env|--to-file [--yes]
                                     verschlüsselte Werte auf einen neuen Schlüssel umstellen
  migrate                            Datenbank-Migrationen ausführen
  help                               diese Hilfe

Umgebung: DATABASE_URL (Pflicht), ZP_SECRET_KEY, ZP_TZ. Auf OpenShift:  oc exec deploy/zeitprotokoll -- /server status
`

func runCLI(args []string) int {
	if args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		fmt.Print(usage)
		return 0
	}
	if args[0] == "version" || args[0] == "--version" || args[0] == "-V" {
		fmt.Println(app.ServerVersion())
		if app.Commit != "" {
			fmt.Println("commit:", app.Commit)
		}
		if app.Built != "" {
			fmt.Println("built:", app.Built)
		}
		return 0
	}
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Fehler:", err)
		return 2
	}
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	pool, err := openDB(ctx, log, cfg.DatabaseURL, 10*time.Second)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Datenbank nicht erreichbar:", err)
		return 1
	}
	defer pool.Close()

	if code, handled := cmdAccounts(ctx, pool, cfg, args[0], args[1:]); handled {
		return code
	}
	switch args[0] {
	case "migrate":
		done, err := db.Migrate(ctx, pool)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Fehler:", err)
			return 1
		}
		if len(done) == 0 {
			fmt.Println("Datenbank ist aktuell.")
		}
		for _, n := range done {
			fmt.Println("angewendet:", n)
		}
		return 0
	case "check":
		a, err := newApp(log, cfg, pool, "", true)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Fehler:", err)
			return 1
		}
		force := has(args, "--force")
		if force && a.KeyFromFile {
			fmt.Fprintln(os.Stderr, "--force abgelehnt: ZP_SECRET_KEY ist nicht gesetzt (Schlüssel aus Datei). Mit einem falschen Schlüssel würden ALLE Zugangsdaten als „nicht lesbar“ gelöscht. Setze ZP_SECRET_KEY wie beim Server.")
			return 1
		}
		if force || has(args, "--fix") {
			audit(ctx, pool, "cli.check", map[bool]string{true: "check --force", false: "check --fix"}[force])
		}
		rep := a.SelfCheck(ctx, app.CheckOpts{Fix: has(args, "--fix") || force, Force: force})
		tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		for _, i := range rep.Items {
			fmt.Fprintf(tw, "%s\t%s\t%s\n", strings.ToUpper(i.Status), i.Name, i.Detail)
		}
		tw.Flush()
		fmt.Println("Gesamt:", rep.Worst)
		if rep.Worst == "error" {
			return 1
		}
		return 0
	case "status":
		return cmdStatus(ctx, pool, cfg)
	case "logs":
		return cmdLogs(ctx, pool, args[1:])
	case "users":
		return cmdUsers(ctx, pool)
	case "user":
		return cmdUser(ctx, pool, args[1:])
	case "jira":
		a, err := newApp(log, cfg, pool, "", false)
		if err != nil { // Instanzen brauchen den Schlüssel nicht (es wird nichts verschlüsselt)
			a = &app.App{DB: pool, Log: log, Key: make([]byte, 32), AllowHTTP: cfg.AllowHTTP}
		}
		return cmdJira(ctx, a, pool, args[1:])
	case "config":
		a, err := newApp(log, cfg, pool, "", false)
		if err != nil { // Einstellungen brauchen den Schlüssel nicht: ohne ihn mit leerem Schlüssel weiter (es wird nichts verschlüsselt)
			a = &app.App{DB: pool, Log: log, Key: make([]byte, 32)}
		}
		return cmdConfig(ctx, a, pool, args[1:])
	case "rekey":
		return cmdRekey(ctx, pool, cfg, args[1:])
	}
	fmt.Fprintf(os.Stderr, "Unbekannter Befehl %q\n\n%s", args[0], usage)
	return 2
}

// audit schreibt einen Eintrag ins Änderungsprotokoll (Befehle laufen ohne angemeldete Person)
func audit(ctx context.Context, pool *pgxpool.Pool, action, detail string) {
	_, _ = pool.Exec(ctx, `INSERT INTO audit_log(user_id,action,detail) VALUES(NULL,$1,$2)`, action, "Befehl: "+detail)
}

func clip19(s string) string {
	if len(s) > 19 {
		return s[:19]
	}
	return s
}

func has(args []string, f string) bool {
	for _, a := range args {
		if a == f {
			return true
		}
	}
	return false
}

func one(ctx context.Context, pool *pgxpool.Pool, sql string, args ...any) string {
	var v any
	if err := pool.QueryRow(ctx, sql, args...).Scan(&v); err != nil {
		return "?"
	}
	return fmt.Sprint(v)
}

func cmdStatus(ctx context.Context, pool *pgxpool.Pool, cfg config.Config) int {
	prog := app.ServerVersion()
	if app.Commit != "" {
		prog += " (" + app.Commit + ")"
	}
	fmt.Println("Programm:       ", prog)
	if app.Built != "" {
		fmt.Println("Gebaut:         ", app.Built)
	}
	fmt.Println("Datenbank:      ", strings.SplitN(one(ctx, pool, `SELECT version()`), " on ", 2)[0])
	fmt.Println("Migrationen:    ", one(ctx, pool, `SELECT COUNT(*) FROM schema_migrations`), "angewendet,", len(db.MigrationNames()), "im Programm")
	fmt.Println("Eingerichtet:   ", one(ctx, pool, `SELECT COALESCE((SELECT value FROM server_config WHERE key='setup_done'),'nein')`))
	fmt.Println("Anmeldung:      ", one(ctx, pool, `SELECT COALESCE((SELECT value FROM server_config WHERE key='auth_mode'),'-')`))
	fmt.Println("Plane:          ", one(ctx, pool, `SELECT COALESCE((SELECT value FROM server_config WHERE key='plane_base_url'),'-')`), "/", one(ctx, pool, `SELECT COALESCE((SELECT value FROM server_config WHERE key='plane_workspace'),'-')`),
		"(Workspaces:", one(ctx, pool, `SELECT COUNT(*) FROM plane_workspaces`), "ein:", one(ctx, pool, `SELECT COUNT(*) FROM plane_workspaces WHERE enabled`)+")")
	fmt.Println("Benutzer:       ", one(ctx, pool, `SELECT COUNT(*) FROM users`), "(gesperrt:", one(ctx, pool, `SELECT COUNT(*) FROM users WHERE disabled`)+")")
	fmt.Println("Projekte:       ", one(ctx, pool, `SELECT COUNT(*) FROM projects`))
	fmt.Println("Zeiteinträge:   ", one(ctx, pool, `SELECT COUNT(*) FROM time_entries`))
	since := time.Now().Add(-24 * time.Hour).UTC().Format("2006-01-02T15:04:05.000Z")
	fmt.Println("Fehler (24 h):  ", one(ctx, pool, `SELECT COUNT(*) FROM system_logs WHERE level='error' AND timestamp >= $1`, since), " Warnungen:", one(ctx, pool, `SELECT COUNT(*) FROM system_logs WHERE level='warn' AND timestamp >= $1`, since))
	fmt.Println("Zeitzone:       ", cfg.TZ)
	return 0
}

func cmdLogs(ctx context.Context, pool *pgxpool.Pool, args []string) int {
	n, level, category, q, follow := 100, "", "", "", false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-n":
			i++
			if i < len(args) {
				n, _ = strconv.Atoi(args[i])
			}
		case "--level":
			i++
			if i < len(args) {
				level = args[i]
			}
		case "--category":
			i++
			if i < len(args) {
				category = args[i]
			}
		case "-q":
			i++
			if i < len(args) {
				q = args[i]
			}
		case "-f":
			follow = true
		}
	}
	if n < 1 || n > 5000 {
		n = 100
	}
	query := func(after int64, limit int, asc bool) ([]string, int64, error) {
		sql := `SELECT l.id, l.timestamp, l.level, l.category, COALESCE(u.email,''), l.message FROM system_logs l LEFT JOIN users u ON u.id=l.user_id WHERE l.id > $1`
		args := []any{after}
		if level == "warn" {
			sql += " AND l.level IN ('warn','error')"
		} else if level != "" {
			args = append(args, level)
			sql += " AND l.level=$" + strconv.Itoa(len(args))
		}
		if category != "" {
			args = append(args, category)
			sql += " AND l.category=$" + strconv.Itoa(len(args))
		}
		if q != "" {
			args = append(args, q)
			sql += " AND l.message ILIKE '%' || $" + strconv.Itoa(len(args)) + " || '%'"
		}
		if asc { // beim Mitlesen von „zuletzt gesehen“ aufwärts, damit bei vielen neuen Zeilen keine übersprungen werden
			sql += " ORDER BY l.id ASC LIMIT " + strconv.Itoa(limit)
		} else {
			sql += " ORDER BY l.id DESC LIMIT " + strconv.Itoa(limit)
		}
		rows, err := pool.Query(ctx, sql, args...)
		if err != nil {
			return nil, after, err
		}
		defer rows.Close()
		var lines []string
		maxID := after
		for rows.Next() {
			var id int64
			var ts, lvl, cat, user, msg string
			if err := rows.Scan(&id, &ts, &lvl, &cat, &user, &msg); err != nil {
				return nil, after, err
			}
			if id > maxID {
				maxID = id
			}
			who := ""
			if user != "" {
				who = " [" + user + "]"
			}
			lines = append(lines, fmt.Sprintf("%s %-7s %-10s%s %s", clip19(strings.Replace(ts, "T", " ", 1)), lvl, cat, who, msg))
		}
		if !asc { // älteste zuerst ausgeben
			for i, j := 0, len(lines)-1; i < j; i, j = i+1, j-1 {
				lines[i], lines[j] = lines[j], lines[i]
			}
		}
		return lines, maxID, rows.Err()
	}
	lines, last, err := query(0, n, false)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Fehler:", err)
		return 1
	}
	for _, l := range lines {
		fmt.Println(l)
	}
	for follow {
		time.Sleep(2 * time.Second)
		lines, last, err = query(last, 500, true)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Fehler:", err)
			return 1
		}
		for _, l := range lines {
			fmt.Println(l)
		}
	}
	return 0
}

func cmdUsers(ctx context.Context, pool *pgxpool.Pool) int {
	rows, err := pool.Query(ctx, `SELECT id, email, name, role, disabled, COALESCE(to_char(last_login,'YYYY-MM-DD HH24:MI'),'-'),
		EXISTS(SELECT 1 FROM user_secrets s WHERE s.user_id=users.id AND s.kind='plane_key'), EXISTS(SELECT 1 FROM user_secrets s WHERE s.user_id=users.id AND s.kind='jira_token')
		FROM users ORDER BY id`)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Fehler:", err)
		return 1
	}
	defer rows.Close()
	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tE-Mail\tName\tRolle\tGesperrt\tLetzte Anmeldung\tPlane\tJira")
	for rows.Next() {
		var id int64
		var email, name, role, last string
		var dis, pk, jt bool
		if err := rows.Scan(&id, &email, &name, &role, &dis, &last, &pk, &jt); err != nil {
			fmt.Fprintln(os.Stderr, "Fehler:", err)
			return 1
		}
		yn := func(b bool) string { return map[bool]string{true: "ja", false: "-"}[b] }
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", id, email, name, role, yn(dis), last, yn(pk), yn(jt))
	}
	tw.Flush()
	return 0
}

func cmdUser(ctx context.Context, pool *pgxpool.Pool, args []string) int {
	if len(args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	email := args[1]
	set := func(sql string, a ...any) int {
		tx, err := pool.Begin(ctx)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Fehler:", err)
			return 1
		}
		defer tx.Rollback(ctx) //nolint:errcheck
		// dieselbe Sperre wie im Web (Einzelbetrieb-Grenze), dann alle aktiven Administratoren sperren
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(727002)`); err != nil {
			fmt.Fprintln(os.Stderr, "Fehler:", err)
			return 1
		}
		if _, err := tx.Exec(ctx, `SELECT id FROM users WHERE role='admin' AND NOT disabled FOR UPDATE`); err != nil {
			fmt.Fprintln(os.Stderr, "Fehler:", err)
			return 1
		}
		tag, err := tx.Exec(ctx, sql, a...)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Fehler:", err)
			return 1
		}
		if tag.RowsAffected() == 0 {
			fmt.Fprintln(os.Stderr, "Kein Benutzer mit dieser E-Mail:", email)
			return 1
		}
		var admins int
		if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM users WHERE role='admin' AND NOT disabled`).Scan(&admins); err != nil || admins == 0 {
			fmt.Fprintln(os.Stderr, "Abgebrochen: Es muss mindestens ein aktiver Administrator bleiben.")
			return 1
		}
		if authMode(ctx, pool) == "local" {
			if err := app.CheckLocalLimits(ctx, tx); err != nil {
				fmt.Fprintln(os.Stderr, "Abgebrochen:", err)
				return 1
			}
		}
		if err := tx.Commit(ctx); err != nil {
			fmt.Fprintln(os.Stderr, "Fehler:", err)
			return 1
		}
		audit(ctx, pool, "cli.user", strings.Join(args, " "))
		fmt.Println("OK")
		return 0
	}
	switch args[0] {
	case "set-role":
		if len(args) < 3 || (args[2] != "user" && args[2] != "lead" && args[2] != "admin") {
			fmt.Fprintln(os.Stderr, "Rolle: user, lead oder admin")
			return 2
		}
		return set(`UPDATE users SET role=$2 WHERE lower(email)=lower($1) AND role <> 'breakglass' AND $2 <> 'breakglass'`, email, args[2])
	case "disable":
		return set(`UPDATE users SET disabled=true WHERE lower(email)=lower($1) AND role <> 'breakglass'`, email)
	case "enable":
		return set(`UPDATE users SET disabled=false WHERE lower(email)=lower($1) AND role <> 'breakglass'`, email)
	}
	fmt.Fprint(os.Stderr, usage)
	return 2
}

func cmdConfig(ctx context.Context, a *app.App, pool *pgxpool.Pool, args []string) int {
	if len(args) == 0 || args[0] == "list" {
		rows, err := pool.Query(ctx, `SELECT key, value FROM server_config WHERE key IN ('auth_mode','setup_done','plane_base_url','plane_workspace','oidc_issuer','oidc_client_id','module_tempo','module_suggest') ORDER BY key`)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Fehler:", err)
			return 1
		}
		defer rows.Close()
		for rows.Next() {
			var k, v string
			if rows.Scan(&k, &v) == nil {
				fmt.Printf("%s = %s\n", k, v)
			}
		}
		return 0
	}
	if args[0] == "set" && len(args) >= 3 {
		if err := a.SetAdminConfig(ctx, args[1], args[2]); err != nil {
			fmt.Fprintln(os.Stderr, "Fehler:", err)
			return 1
		}
		audit(ctx, pool, "cli.config", args[1]+" geändert")
		fmt.Println("OK")
		return 0
	}
	fmt.Fprint(os.Stderr, usage)
	return 2
}

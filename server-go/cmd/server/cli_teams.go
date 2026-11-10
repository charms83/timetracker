package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

const teamUsage = `Aufruf:
  team list                                  Teams mit Mitgliedern
  team add <name>                            Team anlegen
  team remove <name>                         Team löschen
  team member <team> <benutzer> [--lead]     Person ins Team (mit --lead als Teamleiter)
  team unmember <team> <benutzer>            Person aus dem Team nehmen
  user set-hr <benutzer> <on|off>            Personal-Recht (sieht alle) – bei Verzeichnis-Konten setzt die Gruppe es beim Login neu
Benutzer = Benutzername oder E-Mail.
`

func findUserID(ctx context.Context, pool *pgxpool.Pool, who string) (int64, bool) {
	var id int64
	err := pool.QueryRow(ctx, `SELECT id FROM users WHERE role<>'breakglass' AND (lower(username)=lower($1) OR lower(email)=lower($1))`, who).Scan(&id)
	return id, err == nil
}

func cmdTeam(ctx context.Context, pool *pgxpool.Pool, args []string) int {
	if len(args) == 0 {
		fmt.Print(teamUsage)
		return 2
	}
	switch args[0] {
	case "list":
		rows, err := pool.Query(ctx, `SELECT t.name, COALESCE(string_agg(x.name || CASE WHEN m.is_lead THEN ' (Leitung)' ELSE '' END, ', ' ORDER BY x.name), '–')
			FROM teams t LEFT JOIN team_members m ON m.team_id=t.id LEFT JOIN users x ON x.id=m.user_id GROUP BY t.id ORDER BY lower(t.name)`)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Fehler:", err)
			return 1
		}
		defer rows.Close()
		for rows.Next() {
			var n, m string
			if rows.Scan(&n, &m) == nil {
				fmt.Printf("%-24s %s\n", n, m)
			}
		}
		return 0
	case "add":
		if len(args) != 2 || len([]rune(strings.TrimSpace(args[1]))) == 0 || len(args[1]) > 80 {
			fmt.Print(teamUsage)
			return 2
		}
		if _, err := pool.Exec(ctx, `INSERT INTO teams(name) VALUES($1)`, strings.TrimSpace(args[1])); err != nil {
			fmt.Fprintln(os.Stderr, "Abgelehnt (gibt es das Team schon?):", err)
			return 1
		}
		audit(ctx, pool, "cli.team", "Team „"+args[1]+"“ angelegt")
		fmt.Println("Team angelegt.")
		return 0
	case "remove":
		if len(args) != 2 {
			fmt.Print(teamUsage)
			return 2
		}
		tag, err := pool.Exec(ctx, `DELETE FROM teams WHERE lower(name)=lower($1)`, args[1])
		if err != nil || tag.RowsAffected() == 0 {
			fmt.Fprintln(os.Stderr, "Team nicht gefunden.")
			return 1
		}
		audit(ctx, pool, "cli.team", "Team „"+args[1]+"“ gelöscht")
		fmt.Println("Team gelöscht.")
		return 0
	case "member", "unmember":
		if len(args) < 3 {
			fmt.Print(teamUsage)
			return 2
		}
		var tid int64
		if err := pool.QueryRow(ctx, `SELECT id FROM teams WHERE lower(name)=lower($1)`, args[1]).Scan(&tid); err != nil {
			fmt.Fprintln(os.Stderr, "Team nicht gefunden.")
			return 1
		}
		uid, ok := findUserID(ctx, pool, args[2])
		if !ok {
			fmt.Fprintln(os.Stderr, "Benutzer nicht gefunden.")
			return 1
		}
		if args[0] == "unmember" {
			_, _ = pool.Exec(ctx, `DELETE FROM team_members WHERE team_id=$1 AND user_id=$2`, tid, uid)
			audit(ctx, pool, "cli.team", fmt.Sprintf("Team %d: Person %d entfernt", tid, uid))
			fmt.Println("Entfernt.")
			return 0
		}
		lead := len(args) > 3 && args[3] == "--lead"
		if _, err := pool.Exec(ctx, `INSERT INTO team_members(team_id,user_id,is_lead) VALUES($1,$2,$3) ON CONFLICT (team_id,user_id) DO UPDATE SET is_lead=EXCLUDED.is_lead`, tid, uid, lead); err != nil {
			fmt.Fprintln(os.Stderr, "Fehler:", err)
			return 1
		}
		audit(ctx, pool, "cli.team", fmt.Sprintf("Team %d: Person %d, Teamleiter %v", tid, uid, lead))
		fmt.Println("Gespeichert.")
		return 0
	}
	fmt.Print(teamUsage)
	return 2
}

func userSetHR(ctx context.Context, pool *pgxpool.Pool, args []string) int {
	if len(args) != 2 || (args[1] != "on" && args[1] != "off") {
		fmt.Print(teamUsage)
		return 2
	}
	uid, ok := findUserID(ctx, pool, args[0])
	if !ok {
		fmt.Fprintln(os.Stderr, "Benutzer nicht gefunden.")
		return 1
	}
	_, _ = pool.Exec(ctx, `UPDATE users SET is_hr=$2 WHERE id=$1`, uid, args[1] == "on")
	audit(ctx, pool, "cli.user", fmt.Sprintf("Person %d: Personal %s", uid, args[1]))
	fmt.Println("Gespeichert.")
	return 0
}

// userSetSuggest: Ticket-Vorschläge für eine Person erlauben (on) oder sperren (off) – auditiert
func userSetSuggest(ctx context.Context, pool *pgxpool.Pool, args []string) int {
	if len(args) != 2 || (args[1] != "on" && args[1] != "off") {
		fmt.Println("Aufruf: user set-suggest <benutzer> <on|off>")
		return 2
	}
	uid, ok := findUserID(ctx, pool, args[0])
	if !ok {
		fmt.Fprintln(os.Stderr, "Benutzer nicht gefunden.")
		return 1
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET suggest_blocked=$2 WHERE id=$1`, uid, args[1] == "off"); err != nil {
		fmt.Fprintln(os.Stderr, "Fehler:", err)
		return 1
	}
	audit(ctx, pool, "cli.user", fmt.Sprintf("Person %d: Ticket-Vorschläge %s", uid, args[1]))
	fmt.Println("Gespeichert.")
	return 0
}

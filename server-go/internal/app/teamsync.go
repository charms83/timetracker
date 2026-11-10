package app

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"

	"example/zeitprotokoll-server/internal/ldapauth"
)

/* Teams aus dem Verzeichnis (Regel 3): Gruppe „<präfix><name>“ = Mitglied im Team <name>, „<präfix><name>-lead“ = Teamleitung.
   Präfix: ldap_team_prefix bzw. oidc_team_prefix (Standard „zp-team-“, „-“ schaltet den Abgleich aus). Abgleich beim Login
   (LDAP und OIDC) und in der LDAP-Nachprüfung. Fehlende Teams entstehen mit source='directory'; gesetzt/entfernt werden nur
   Mitgliedschaften mit source='directory'. Lokale Teams und lokale Mitgliedschaften fasst der Abgleich nie an: Gibt es schon ein
   lokales Team gleichen Namens, wird die Gruppe übergangen (Hinweis im Systemlog) – sonst könnte eine Verzeichnisgruppe Leitung
   in einem von Hand gepflegten Team verschaffen. Gruppenvergleich exakt (Groß-/Kleinschreibung zählt), nur direkte Gruppen.
   Sicherheit: Teamleitung gibt Einsicht und Nachtragsrecht. LDAP: ldap_team_base auf eine OU setzen, in der nur Berechtigte Gruppen
   pflegen (sonst zählt jede Gruppe passenden Namens im Verzeichnis). OIDC: der Anbieter muss Gruppennamen ebenso schützen.
   OIDC gleicht nur beim Login ab (keine Nachprüfung): entzogene Leitung wirkt erst mit der nächsten Anmeldung. */

const defaultTeamPrefix = "zp-team-"

// dirTeam: gewünschte Mitgliedschaft aus den Gruppen
type dirTeam struct {
	Name string
	Lead bool
}

// validTeamName: 1–80 Zeichen, keine Steuerzeichen, ohne Leerraum am Rand
func validTeamName(s string) bool {
	n := len([]rune(s))
	if n < 1 || n > 80 || strings.TrimSpace(s) != s {
		return false
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) {
			return false
		}
	}
	return true
}

// dirTeams: Teams aus Gruppennamen (schon als Name, nicht DN). Schlüssel: Kleinschreibung (wie der eindeutige Index teams_name_idx).
// Zweiter Wert: übergangene Gruppen (ungültiger Teamname).
func dirTeams(groups []string, prefix string) (map[string]dirTeam, []string) {
	out := map[string]dirTeam{}
	var bad []string
	if prefix == "" || prefix == "-" {
		return out, nil
	}
	for _, g := range groups {
		if !strings.HasPrefix(g, prefix) {
			continue
		}
		name, lead := strings.TrimPrefix(g, prefix), false
		if strings.HasSuffix(name, "-lead") {
			name, lead = strings.TrimSuffix(name, "-lead"), true
		}
		if !validTeamName(name) {
			bad = append(bad, g)
			continue
		}
		k := strings.ToLower(name)
		cur, seen := out[k]
		if !seen {
			cur = dirTeam{Name: name}
		}
		cur.Lead = cur.Lead || lead
		out[k] = cur
	}
	return out, bad
}

// teamDiff: was zu setzen (neu oder Leitung geändert) und was zu entfernen ist; have/want: Team-Id → Leitung
func teamDiff(have, want map[int64]bool) (set map[int64]bool, remove []int64) {
	set = map[int64]bool{}
	for id, lead := range want {
		if old, ok := have[id]; !ok || old != lead {
			set[id] = lead
		}
	}
	for id := range have {
		if _, ok := want[id]; !ok {
			remove = append(remove, id)
		}
	}
	sort.Slice(remove, func(i, j int) bool { return remove[i] < remove[j] })
	return set, remove
}

// teamGroups: nur Gruppen-DNs unterhalb von base (LDAP, ldap_team_base); base leer = alle
func teamGroups(dns []string, base string) []string {
	out := make([]string, 0, len(dns))
	for _, g := range dns {
		if ldapauth.InBase(g, base) {
			out = append(out, g)
		}
	}
	return out
}

// groupNames: Gruppen als Namen (DN → CN); leere/unlesbare fallen weg
func groupNames(raw []string) []string {
	out := make([]string, 0, len(raw))
	for _, g := range raw {
		if n := ldapauth.GroupCN(g); n != "" {
			out = append(out, n)
		}
	}
	return out
}

// teamPrefix: Präfix aus der Einstellung (leer = Standard)
func teamPrefix(v string) string {
	if v = strings.TrimSpace(v); v == "" {
		return defaultTeamPrefix
	}
	return v
}

// checkTeamPrefix: Wert für ldap_team_prefix/oidc_team_prefix ("" = Standard, "-" = aus)
func checkTeamPrefix(v string) error {
	if v == "" || v == "-" {
		return nil
	}
	if len([]rune(v)) > 60 || strings.ContainsAny(v, "=,") || !validTeamName(v) {
		return fmt.Errorf("Team-Präfix: bis 60 Zeichen, ohne „=“, „,“ und Steuerzeichen (oder „-“ = aus)")
	}
	return nil
}

// syncDirectoryTeams gleicht die Verzeichnis-Mitgliedschaften einer Person ab (in der Transaktion des Logins bzw. der Nachprüfung).
// groups nil = nichts bekannt (nichts ändern). Liefert übergangene Gruppen (lokales Team gleichen Namens, ungültiger Name).
func syncDirectoryTeams(ctx context.Context, tx pgx.Tx, uid int64, groups []string, prefix string) ([]string, error) {
	if groups == nil || prefix == "" || prefix == "-" {
		return nil, nil
	}
	want, skipped := dirTeams(groups, prefix)
	wantIDs := map[int64]bool{}
	names := map[int64]string{}
	for _, t := range want {
		if _, err := tx.Exec(ctx, `INSERT INTO teams(name,source) VALUES($1,'directory') ON CONFLICT DO NOTHING`, t.Name); err != nil {
			return nil, err
		}
		var id int64
		var src string
		if err := tx.QueryRow(ctx, `SELECT id, source FROM teams WHERE lower(name)=lower($1)`, t.Name).Scan(&id, &src); err != nil {
			return nil, err
		}
		if src != "directory" {
			skipped = append(skipped, prefix+t.Name)
			continue
		}
		wantIDs[id], names[id] = t.Lead, t.Name
	}
	have := map[int64]bool{}
	rows, err := tx.Query(ctx, `SELECT team_id, is_lead FROM team_members WHERE user_id=$1 AND source='directory'`, uid)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id int64
		var lead bool
		if err := rows.Scan(&id, &lead); err != nil {
			rows.Close()
			return nil, err
		}
		have[id] = lead
	}
	rows.Close()
	set, remove := teamDiff(have, wantIDs)
	for id, lead := range set {
		tag, err := tx.Exec(ctx, `INSERT INTO team_members(team_id,user_id,is_lead,source) VALUES($1,$2,$3,'directory')
			ON CONFLICT (team_id,user_id) DO UPDATE SET is_lead=EXCLUDED.is_lead WHERE team_members.source='directory'`, id, uid, lead)
		if err != nil {
			return nil, err
		}
		if tag.RowsAffected() > 0 {
			if _, err := tx.Exec(ctx, `INSERT INTO audit_log(user_id,action,detail) VALUES($1,'team.synced',$2)`, uid,
				fmt.Sprintf("Team „%s“: Mitglied, Teamleitung %v (Verzeichnis)", names[id], lead)); err != nil {
				return nil, err
			}
		}
	}
	for _, id := range remove {
		tag, err := tx.Exec(ctx, `DELETE FROM team_members WHERE team_id=$1 AND user_id=$2 AND source='directory'`, id, uid)
		if err != nil {
			return nil, err
		}
		if tag.RowsAffected() > 0 {
			if _, err := tx.Exec(ctx, `INSERT INTO audit_log(user_id,action,detail) VALUES($1,'team.synced',$2)`, uid,
				fmt.Sprintf("Team %d: Mitgliedschaft entfernt (Verzeichnis)", id)); err != nil {
				return nil, err
			}
		}
	}
	return skipped, nil
}

// logSkippedTeams: übergangene Gruppen ins Systemlog (ohne Abbruch der Anmeldung)
func (a *App) logSkippedTeams(ctx context.Context, uid int64, skipped []string) {
	if len(skipped) > 0 {
		a.sysLog(ctx, uid, "warn", "betrieb", "Teams aus dem Verzeichnis übergangen (lokales Team gleichen Namens oder ungültiger Name): "+trim(strings.Join(skipped, ", "), 300))
	}
}

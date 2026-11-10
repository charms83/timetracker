package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"example/zeitprotokoll-server/internal/app"
	"example/zeitprotokoll-server/internal/config"
	"example/zeitprotokoll-server/internal/crypto"
	"example/zeitprotokoll-server/internal/ldapauth"
)

const ldapUsage = `Aufruf:
  ldap show                          Einstellungen anzeigen (Passwort verdeckt)
  ldap set <name> <wert>             Einstellung setzen (leer = löschen), z. B. ldap set url ldaps://dc.example.local:636
  ldap set-bind-password             Passwort des Dienstkontos setzen (verdeckte Eingabe)
  ldap test <benutzername>           Anmeldung und Rollen-Zuordnung prüfen (ändert nichts)
  ldap link <benutzername>           bestehendes lokales Konto beim nächsten Anmelden mit diesem Verzeichnis-Konto verknüpfen (einmalig)
Namen: url starttls ca bind_dn bind_template base_dn filter attr_name attr_mail attr_id group_user group_lead group_admin group_hr nested
`

func readHidden(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	off := exec.Command("stty", "-echo")
	off.Stdin = os.Stdin
	hidden := off.Run() == nil
	defer func() {
		if hidden {
			on := exec.Command("stty", "echo")
			on.Stdin = os.Stdin
			_ = on.Run()
		}
		fmt.Fprintln(os.Stderr)
	}()
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func cmdLDAP(ctx context.Context, pool *pgxpool.Pool, cfg config.Config, args []string) int {
	if len(args) == 0 {
		fmt.Print(ldapUsage)
		return 2
	}
	key, _, err := crypto.ReadKey(cfg.SecretKey, cfg.DataDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Fehler:", err)
		return 1
	}
	norm := func(n string) string { return "ldap_" + strings.TrimPrefix(strings.ToLower(n), "ldap_") }
	switch args[0] {
	case "show":
		names := make([]string, 0, len(app.LDAPKeys))
		for k := range app.LDAPKeys {
			names = append(names, k)
		}
		sort.Strings(names)
		for _, k := range names {
			var v string
			_ = pool.QueryRow(ctx, `SELECT value FROM server_config WHERE key=$1`, k).Scan(&v)
			switch {
			case k == "ldap_bind_password" && v != "":
				v = "(gesetzt, verschlüsselt)"
			case k == "ldap_ca" && v != "":
				v = fmt.Sprintf("(%d Zeichen PEM)", len(v))
			case v == "":
				v = "– (Standard: " + app.LDAPKeys[k] + ")"
			}
			fmt.Printf("%-20s %s\n", k, v)
		}
		c, _ := app.LoadLDAPConfig(ctx, pool, key)
		if verr := c.Validate(); verr != nil {
			fmt.Println("\nNoch nicht vollständig:", verr)
		} else {
			fmt.Println("\nKonfiguration vollständig. Prüfen mit: ldap test <benutzername>")
		}
		return 0
	case "set":
		if len(args) < 2 {
			fmt.Print(ldapUsage)
			return 2
		}
		k := norm(args[1])
		v := strings.Join(args[2:], " ")
		if k == "ldap_bind_password" {
			fmt.Fprintln(os.Stderr, "Das Passwort bitte mit „ldap set-bind-password“ setzen (nicht in der Befehlszeile).")
			return 2
		}
		if k == "ldap_ca" && strings.HasPrefix(v, "@") { // ldap set ca @/pfad/zur/ca.pem
			b, rerr := os.ReadFile(strings.TrimPrefix(v, "@"))
			if rerr != nil {
				fmt.Fprintln(os.Stderr, "Fehler:", rerr)
				return 1
			}
			v = string(b)
		}
		if err := app.SetLDAPConfig(ctx, pool, key, k, v); err != nil {
			fmt.Fprintln(os.Stderr, "Abgelehnt:", err)
			return 1
		}
		audit(ctx, pool, "cli.config", "LDAP-Einstellung "+k+" geändert")
		fmt.Println("Gespeichert:", k)
		return 0
	case "set-bind-password":
		pw, err := readHidden("Passwort des Dienstkontos: ")
		if err != nil || pw == "" {
			fmt.Fprintln(os.Stderr, "Abgebrochen.")
			return 1
		}
		if err := app.SetLDAPConfig(ctx, pool, key, "ldap_bind_password", pw); err != nil {
			fmt.Fprintln(os.Stderr, "Fehler:", err)
			return 1
		}
		audit(ctx, pool, "cli.config", "LDAP-Dienstkonto-Passwort gesetzt")
		fmt.Println("Gespeichert (verschlüsselt).")
		return 0
	case "test":
		if len(args) != 2 {
			fmt.Print(ldapUsage)
			return 2
		}
		c, err := app.LoadLDAPConfig(ctx, pool, key)
		if err == nil {
			err = c.Validate()
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "Konfiguration:", err)
			return 1
		}
		pw, err := readHidden("Passwort von " + args[1] + ": ")
		if err != nil {
			return 1
		}
		tctx, cancel := context.WithTimeout(ctx, 40*time.Second)
		defer cancel()
		res, err := ldapauth.Authenticate(tctx, c, args[1], pw)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Ergebnis:", err)
			return 1
		}
		fmt.Printf("Anmeldung ok\n  Benutzer: %s\n  Name:     %s\n  E-Mail:   %s\n  Kennung:  %s\n  Rolle:    %s\n", res.Username, res.Name, res.Mail, res.ID, res.Role)
		return 0
	case "link":
		if len(args) != 2 {
			fmt.Print(ldapUsage)
			return 2
		}
		u := strings.ToLower(args[1])
		var n int
		_ = pool.QueryRow(ctx, `SELECT COUNT(*) FROM users WHERE kind='local' AND role<>'breakglass' AND productive AND NOT disabled AND lower(username)=$1`, u).Scan(&n)
		if n != 1 {
			fmt.Fprintln(os.Stderr, "Abgelehnt: Es gibt kein aktives produktives lokales Konto „"+u+"“.")
			return 1
		}
		if err := app.SetLDAPConfig(ctx, pool, key, "ldap_link_user", u); err != nil {
			fmt.Fprintln(os.Stderr, "Fehler:", err)
			return 1
		}
		audit(ctx, pool, "cli.config", "Verknüpfung des lokalen Kontos "+u+" mit dem Verzeichnis freigegeben")
		fmt.Println("Beim nächsten erfolgreichen Verzeichnis-Login von „" + u + "“ wird das lokale Konto übernommen (Daten bleiben, Passwort entfällt).")
		return 0
	}
	fmt.Print(ldapUsage)
	return 2
}

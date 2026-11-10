package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"example/zeitprotokoll-server/internal/app"
	"example/zeitprotokoll-server/internal/config"
	"example/zeitprotokoll-server/internal/crypto"
)

const oidcUsage = `Aufruf:
  oidc show                          Einstellungen anzeigen (Secret verdeckt)
  oidc set <name> <wert>             Einstellung setzen (leer = löschen), z. B. oidc set issuer https://auth.example.com/application/o/zeitprotokoll
  oidc set-secret                    Client-Secret setzen (verdeckte Eingabe)
  oidc link <benutzername> <sub>     bestehendes lokales Konto beim nächsten Login genau dieser authentik-Kennung (sub) übernehmen (24 h gültig)
Namen: issuer client_id groups_claim group_user group_lead group_admin group_hr
Rücksprung-Adresse im Anbieter eintragen:  <ZP_PUBLIC_URL>/api/auth/oidc/callback
`

func cmdOIDC(ctx context.Context, pool *pgxpool.Pool, cfg config.Config, args []string) int {
	if len(args) == 0 {
		fmt.Print(oidcUsage)
		return 2
	}
	key, _, err := crypto.ReadKey(cfg.SecretKey, cfg.DataDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Fehler:", err)
		return 1
	}
	norm := func(n string) string { return "oidc_" + strings.TrimPrefix(strings.ToLower(n), "oidc_") }
	switch args[0] {
	case "show":
		names := make([]string, 0, len(app.OIDCKeys))
		for k := range app.OIDCKeys {
			names = append(names, k)
		}
		sort.Strings(names)
		for _, k := range names {
			var v string
			_ = pool.QueryRow(ctx, `SELECT value FROM server_config WHERE key=$1`, k).Scan(&v)
			switch {
			case k == "oidc_client_secret" && v != "":
				v = "(gesetzt, verschlüsselt)"
			case v == "":
				v = "– (Standard: " + app.OIDCKeys[k] + ")"
			}
			fmt.Printf("%-20s %s\n", k, v)
		}
		fmt.Printf("%-20s %s\n", "Rücksprung-Adresse", strings.TrimRight(cfg.PublicURL, "/")+"/api/auth/oidc/callback")
		return 0
	case "set":
		if len(args) < 2 {
			fmt.Print(oidcUsage)
			return 2
		}
		k := norm(args[1])
		if k == "oidc_client_secret" {
			fmt.Fprintln(os.Stderr, "Das Secret bitte mit „oidc set-secret“ setzen (nicht in der Befehlszeile).")
			return 2
		}
		if err := app.SetOIDCConfig(ctx, pool, key, cfg.AllowHTTP, k, strings.Join(args[2:], " ")); err != nil {
			fmt.Fprintln(os.Stderr, "Abgelehnt:", err)
			return 1
		}
		audit(ctx, pool, "cli.config", "OIDC-Einstellung "+k+" geändert")
		fmt.Println("Gespeichert:", k)
		return 0
	case "set-secret":
		pw, err := readHidden("Client-Secret: ")
		if err != nil || pw == "" {
			fmt.Fprintln(os.Stderr, "Abgebrochen.")
			return 1
		}
		if err := app.SetOIDCConfig(ctx, pool, key, cfg.AllowHTTP, "oidc_client_secret", pw); err != nil {
			fmt.Fprintln(os.Stderr, "Fehler:", err)
			return 1
		}
		audit(ctx, pool, "cli.config", "OIDC-Client-Secret gesetzt")
		fmt.Println("Gespeichert (verschlüsselt).")
		return 0
	case "link":
		if len(args) != 3 {
			fmt.Fprintln(os.Stderr, "Aufruf: oidc link <lokaler-benutzername> <sub>   (sub = feste Benutzer-Kennung aus authentik, nicht der Anzeigename)")
			return 2
		}
		if len(args[2]) < 4 || len(args[2]) > 255 || strings.ContainsAny(args[2], "|\n ") {
			fmt.Fprintln(os.Stderr, "Abgelehnt: sub ungültig.")
			return 1
		}
		if code := cmdLDAP(ctx, pool, cfg, []string{"link", args[1]}); code != 0 {
			return code
		}
		if err := app.SetLDAPConfig(ctx, pool, key, "ldap_link_subject", args[2]); err != nil {
			fmt.Fprintln(os.Stderr, "Fehler:", err)
			return 1
		}
		audit(ctx, pool, "cli.config", "Verknüpfung nur für OIDC-Kennung (sub) freigegeben")
		return 0
	}
	fmt.Print(oidcUsage)
	return 2
}

package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"example/zeitprotokoll-server/internal/app"
	"example/zeitprotokoll-server/internal/config"
	"example/zeitprotokoll-server/internal/crypto"
)

const rekeyUsage = `Aufruf:
  rekey --from-env|--from-file  --to-env|--to-file [--yes]
    Alle verschlüsselten Werte (user_secrets, LDAP-Bind-Passwort, OIDC-Secret, SMTP-Passwort)
    von einem Schlüssel auf einen anderen umstellen.

  --from-env / --to-env     Schlüssel aus ZP_SECRET_KEY (bzw. ZP_SECRET_KEY_NEW für --to-env)
  --from-file / --to-file   Schlüsseldatei data/secret.key (bzw. ZP_DATA_DIR)
  --yes                     ohne Rückfrage ausführen

Beispiel (Test-Datei → Produktion):
  ZP_SECRET_KEY_NEW='…mind. 32 Zeichen…' server rekey --from-file --to-env --yes
`

func cmdRekey(ctx context.Context, pool *pgxpool.Pool, cfg config.Config, args []string) int {
	if len(args) == 0 || containsStr(args, "-h") || containsStr(args, "--help") {
		fmt.Print(rekeyUsage)
		return 2
	}
	fromEnv, toEnv, fromFile, toFile, yes := false, false, false, false, false
	for _, a := range args {
		switch a {
		case "--from-env":
			fromEnv = true
		case "--to-env":
			toEnv = true
		case "--from-file":
			fromFile = true
		case "--to-file":
			toFile = true
		case "--yes", "-y":
			yes = true
		default:
			fmt.Fprintln(os.Stderr, "Unbekannt:", a)
			fmt.Print(rekeyUsage)
			return 2
		}
	}
	if fromEnv == fromFile || toEnv == toFile {
		fmt.Fprintln(os.Stderr, "Genau eine Quelle (--from-env oder --from-file) und ein Ziel (--to-env oder --to-file).")
		return 2
	}
	oldKey, err := loadRekey(cfg, fromEnv, fromFile, false)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Alter Schlüssel:", err)
		return 1
	}
	newKey, err := loadRekey(cfg, toEnv, toFile, true)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Neuer Schlüssel:", err)
		return 1
	}
	if string(oldKey) == string(newKey) {
		fmt.Fprintln(os.Stderr, "Alter und neuer Schlüssel sind gleich – nichts zu tun.")
		return 1
	}
	if !yes {
		fmt.Fprintln(os.Stderr, "Schreibe alle Chiffrate um. Mit --yes bestätigen.")
		return 2
	}
	n, err := app.RekeySecrets(ctx, pool, oldKey, newKey)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Fehler:", err)
		return 1
	}
	audit(ctx, pool, "cli.rekey", fmt.Sprintf("%d Werte umgeschlüsselt", n))
	fmt.Printf("Fertig: %d Werte umgeschlüsselt.\n", n)
	if toEnv {
		fmt.Println("Hinweis: ZP_SECRET_KEY dauerhaft auf den neuen Wert setzen und die alte Schlüsseldatei entfernen.")
	}
	return 0
}

func loadRekey(cfg config.Config, fromEnv, fromFile, isNew bool) ([]byte, error) {
	if fromEnv {
		env := "ZP_SECRET_KEY"
		if isNew {
			env = "ZP_SECRET_KEY_NEW"
			if os.Getenv(env) == "" {
				env = "ZP_SECRET_KEY"
			}
		}
		s := strings.TrimSpace(os.Getenv(env))
		if s == "" {
			return nil, fmt.Errorf("%s fehlt", env)
		}
		k, _, err := crypto.LoadKey(s, "")
		return k, err
	}
	k, _, err := crypto.ReadKey("", cfg.DataDir)
	return k, err
}

func containsStr(a []string, s string) bool {
	for _, x := range a {
		if x == s {
			return true
		}
	}
	return false
}

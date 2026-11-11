package app

import (
	"context"
	"encoding/base64"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"example/zeitprotokoll-server/internal/crypto"
)

/* RekeySecrets: alle verschlüsselten Server-/Benutzerwerte von oldKey auf newKey umstellen.
   Nutzer-Geheimnisse (plane_key, jira_token), LDAP-Bind, OIDC-Secret, SMTP-Passwort. */

func RekeySecrets(ctx context.Context, pool *pgxpool.Pool, oldKey, newKey []byte) (int, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	n := 0

	rows, err := tx.Query(ctx, `SELECT user_id, kind, scope, ciphertext FROM user_secrets`)
	if err != nil {
		return 0, err
	}
	type sec struct {
		uid, scope int64
		kind       string
		blob       []byte
	}
	var list []sec
	for rows.Next() {
		var s sec
		if err := rows.Scan(&s.uid, &s.kind, &s.scope, &s.blob); err != nil {
			rows.Close()
			return 0, err
		}
		list = append(list, s)
	}
	rows.Close()

	// Plane-Hosts für AAD
	wsHost := map[int64]string{}
	if rs, err := tx.Query(ctx, `SELECT id, base_url FROM plane_workspaces`); err == nil {
		for rs.Next() {
			var id int64
			var base string
			if rs.Scan(&id, &base) == nil {
				wsHost[id] = base
			}
		}
		rs.Close()
	}
	var primBase string
	_ = tx.QueryRow(ctx, `SELECT base_url FROM plane_workspaces ORDER BY id LIMIT 1`).Scan(&primBase)
	if primBase == "" {
		_ = tx.QueryRow(ctx, `SELECT value FROM server_config WHERE key='plane_base_url'`).Scan(&primBase)
	}
	jiraBase := map[int64]string{}
	if rs, err := tx.Query(ctx, `SELECT id, base_url FROM jira_instances`); err == nil {
		for rs.Next() {
			var id int64
			var base string
			if rs.Scan(&id, &base) == nil {
				jiraBase[id] = base
			}
		}
		rs.Close()
	}

	for _, s := range list {
		var aad []byte
		switch s.kind {
		case "plane_key":
			base := primBase
			if s.scope > 0 {
				if h, ok := wsHost[s.scope]; ok {
					base = h
				}
			}
			aad = []byte(fmt.Sprintf("u%d|plane_key|%s", s.uid, planeScope(base)))
		case "jira_token":
			aad = jiraTokenAAD(s.uid, s.scope, jiraBase[s.scope])
		default:
			aad = secretAAD(s.uid, s.kind)
		}
		plain, err := crypto.OpenAAD(oldKey, s.blob, aad)
		if err != nil {
			// Legacy ohne Host-AAD
			plain, err = crypto.OpenAAD(oldKey, s.blob, secretAAD(s.uid, s.kind))
			if err != nil {
				return 0, fmt.Errorf("user_secrets u=%d %s scope=%d: %w", s.uid, s.kind, s.scope, err)
			}
			aad = secretAAD(s.uid, s.kind)
		}
		blob, err := crypto.SealAAD(newKey, plain, aad)
		if err != nil {
			return 0, err
		}
		if _, err := tx.Exec(ctx, `UPDATE user_secrets SET ciphertext=$4, updated_at=now() WHERE user_id=$1 AND kind=$2 AND scope=$3`, s.uid, s.kind, s.scope, blob); err != nil {
			return 0, err
		}
		n++
	}

	rekeyConf := func(key string, aad []byte) error {
		var enc string
		err := tx.QueryRow(ctx, `SELECT value FROM server_config WHERE key=$1`, key).Scan(&enc)
		if err != nil || enc == "" {
			return nil
		}
		blob, err := base64.StdEncoding.DecodeString(enc)
		if err != nil {
			return fmt.Errorf("%s: kein Base64", key)
		}
		plain, err := crypto.OpenAAD(oldKey, blob, aad)
		if err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
		nb, err := crypto.SealAAD(newKey, plain, aad)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE server_config SET value=$2, updated_at=now() WHERE key=$1`, key, base64.StdEncoding.EncodeToString(nb))
		if err == nil {
			n++
		}
		return err
	}
	if err := rekeyConf("ldap_bind_password", ldapSecretAAD()); err != nil {
		return 0, err
	}
	if err := rekeyConf("oidc_client_secret", []byte("server|oidc_client_secret")); err != nil {
		return 0, err
	}
	if err := rekeyConf(mailPassKey, mailSecretAAD()); err != nil {
		return 0, err
	}

	return n, tx.Commit(ctx)
}

-- Servereinstellungen aus dem Einrichtungsassistenten (auth_mode, oidc_*, setup_done).
-- Geheimnisse (oidc_client_secret) stehen nur verschlüsselt (base64) darin.
CREATE TABLE server_config (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

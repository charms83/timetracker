-- Integrations-Token für Abrechnungsprogramme (Projektstunden/Lohn) – nicht für Mitarbeiter-Clients.
CREATE TABLE IF NOT EXISTS integration_tokens (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    token_hash TEXT NOT NULL UNIQUE,
    label      TEXT NOT NULL DEFAULT '',
    scopes     TEXT[] NOT NULL DEFAULT ARRAY['projects','payroll']::text[],
    created_by BIGINT REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used  TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS integration_tokens_active_idx ON integration_tokens (id) WHERE revoked_at IS NULL;

-- Benutzer, Sitzungen, Geräte-Token und verschlüsselte Zugangsdaten je Person.
CREATE TABLE users (
    id           BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    oidc_subject TEXT NOT NULL UNIQUE,
    email        TEXT NOT NULL,
    name         TEXT NOT NULL DEFAULT '',
    role         TEXT NOT NULL DEFAULT 'user' CHECK (role IN ('user','lead','admin')),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_login   TIMESTAMPTZ
);

CREATE TABLE sessions (
    id         TEXT PRIMARY KEY,               -- Hash des Cookie-Werts
    user_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX sessions_user_idx ON sessions(user_id);

-- Widerrufbare Geräte-Token für Mac-App und Browser-Add-on.
CREATE TABLE device_tokens (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash TEXT NOT NULL UNIQUE,
    label      TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used  TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ
);

-- Jira-Token (gilt auch für Tempo) und Plane-Schlüssel, nur verschlüsselt gespeichert.
CREATE TABLE user_secrets (
    user_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind       TEXT NOT NULL CHECK (kind IN ('jira_token','plane_key')),
    ciphertext BYTEA NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, kind)
);

CREATE TABLE user_settings (
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    key     TEXT NOT NULL,
    value   TEXT,
    PRIMARY KEY (user_id, key)
);

CREATE TABLE audit_log (
    id      BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    user_id BIGINT REFERENCES users(id) ON DELETE SET NULL,
    action  TEXT NOT NULL,
    detail  TEXT
);

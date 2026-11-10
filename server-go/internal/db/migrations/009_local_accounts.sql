-- Lokale Konten (Einzelbetrieb ohne authentik): Benutzername + Passwort, Sitzungen mit Leerlauf-Ablauf.
-- Das bisherige Testkonto (ohne Anmeldung) wird zum lokalen Administrator „admin“ (nicht produktiv, behält die Testdaten).
-- Rolle „breakglass“: Notfallzugang. Nur Betrieb (Status, Selbsttest, Logs, Einstellungen, Monat abschließen), nie Zeitdaten.
ALTER TABLE users DROP CONSTRAINT users_role_check;
ALTER TABLE users ADD CONSTRAINT users_role_check CHECK (role IN ('user','lead','admin','breakglass'));

ALTER TABLE users
    ADD COLUMN armed_until TIMESTAMPTZ,       -- Notfallzugang: nur bis dahin anmeldbar (scharf geschaltet per Befehl am Server)
    ADD COLUMN armed_reason TEXT,
    ADD COLUMN kind TEXT NOT NULL DEFAULT 'oidc' CHECK (kind IN ('oidc','local')),
    ADD COLUMN username TEXT,
    ADD COLUMN password_hash TEXT,
    ADD COLUMN productive BOOLEAN NOT NULL DEFAULT true,
    ADD COLUMN must_change_password BOOLEAN NOT NULL DEFAULT false;
CREATE UNIQUE INDEX users_username_idx ON users(lower(username)) WHERE username IS NOT NULL;

UPDATE users SET kind='local', username='admin', productive=false, name='Lokaler Administrator', must_change_password=true
    WHERE oidc_subject='local';

ALTER TABLE sessions
    ADD COLUMN last_seen TIMESTAMPTZ NOT NULL DEFAULT now(),
    ADD COLUMN ip TEXT,
    ADD COLUMN user_agent TEXT;

-- Gemeinsame Zähler für Mehr-Pod-Betrieb (Anmeldesperre, Plane-/Jira-Bremse).
-- Ereignisse und Sperren liegen in PostgreSQL, damit alle Pods dieselbe Grenze sehen.

CREATE TABLE IF NOT EXISTS rate_events (
    kind  TEXT NOT NULL,                 -- login_ip | login_user | login_bg | plane | setup
    key   TEXT NOT NULL,
    at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS rate_events_lookup_idx ON rate_events(kind, key, at DESC);

CREATE TABLE IF NOT EXISTS rate_blocks (
    kind     TEXT NOT NULL,              -- plane | jira
    key      TEXT NOT NULL,
    until_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (kind, key)
);
CREATE INDEX IF NOT EXISTS rate_blocks_until_idx ON rate_blocks(until_at);

-- Urlaubsantrag (Genehmigung Teamleitung, Vertretung Personal) + Client-Hinweise (Geräte-Token).
-- device_tokens existiert seit 001; hier Annahme-Schnittstelle für Vorschläge vom Mac-Client/Add-on.

CREATE TABLE leave_requests (
    id           BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id      BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    date_from    DATE NOT NULL,
    date_to      DATE NOT NULL,
    days         NUMERIC(5,2),                    -- optional, sonst Werktage
    note         TEXT NOT NULL DEFAULT '',
    status       TEXT NOT NULL DEFAULT 'pending'
                   CHECK (status IN ('pending','approved','rejected','cancelled')),
    decided_by   BIGINT REFERENCES users(id) ON DELETE SET NULL,
    decided_at   TIMESTAMPTZ,
    decision_note TEXT NOT NULL DEFAULT '',
    absence_id   BIGINT REFERENCES absences(id) ON DELETE SET NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (date_to >= date_from)
);
CREATE INDEX leave_requests_user_idx ON leave_requests(user_id, status);
CREATE INDEX leave_requests_pending_idx ON leave_requests(status) WHERE status = 'pending';

-- Vom Client gemeldete Hinweise (SSH/Meeting/Kalender/Presence-Blöcke) – nur Vorschläge, keine Buchungen.
CREATE TABLE client_hints (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    device_id  BIGINT REFERENCES device_tokens(id) ON DELETE SET NULL,
    kind       TEXT NOT NULL CHECK (kind IN ('ssh','meeting','calendar','presence','other')),
    day        DATE NOT NULL,
    start_time TIMESTAMPTZ,
    end_time   TIMESTAMPTZ,
    title      TEXT NOT NULL DEFAULT '',
    payload    JSONB NOT NULL DEFAULT '{}',
    dedupe_key TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    consumed_at TIMESTAMPTZ
);
CREATE UNIQUE INDEX client_hints_dedupe_idx ON client_hints(user_id, kind, dedupe_key) WHERE dedupe_key <> '';
CREATE INDEX client_hints_open_idx ON client_hints(user_id, day) WHERE consumed_at IS NULL;

-- Geräte-Token: Art (mac/addon/minimal) für Stufen
ALTER TABLE device_tokens ADD COLUMN IF NOT EXISTS kind TEXT NOT NULL DEFAULT 'mac'
  CHECK (kind IN ('mac','addon','minimal','other'));

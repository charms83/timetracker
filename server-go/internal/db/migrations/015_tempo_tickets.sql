-- Jira/Tempo Stufe 2: Ticket-Zuordnung mit gewählter Instanz, Anlegen nur auf Bestätigung (idempotent),
-- Übergangs-Tickets je Person/Instanz/Monat mit Wiederöffnen.

-- von der Person gewählte Jira-Instanz für das jeweilige Ziel (bei mehrdeutigem Projektkürzel)
ALTER TABLE time_entries ADD COLUMN tempo_target_instance BIGINT REFERENCES jira_instances(id) ON DELETE SET NULL;
ALTER TABLE tasks ADD COLUMN jira_instance BIGINT REFERENCES jira_instances(id) ON DELETE SET NULL;
ALTER TABLE projects ADD COLUMN tempo_issue_instance BIGINT REFERENCES jira_instances(id) ON DELETE SET NULL;

-- „Ticket anlegen“: ein Auftrag je Person und Schlüssel – ein zweiter Klick legt kein zweites Ticket an
CREATE TABLE tempo_ticket_requests (
    user_id     BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    request_key TEXT NOT NULL CHECK (length(request_key) BETWEEN 1 AND 100),
    instance_id BIGINT REFERENCES jira_instances(id) ON DELETE SET NULL,
    summary     TEXT NOT NULL,
    issue_key   TEXT,
    status      TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','done','failed')),
    error       TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, request_key)
);

ALTER TABLE tempo_month_tickets ADD COLUMN reopened_at TEXT;

-- Die gewählte Instanz (tempo_target_instance) steht bewusst NICHT in der Sperrliste von time_entries_lock (013):
-- die App schreibt sie nur zusammen mit tempo_target (gesperrt) und prüft vorher die Monatssperre unter der gemeinsamen Sperre;
-- so bleibt ON DELETE SET NULL beim Löschen einer Instanz auch in freigegebenen Monaten möglich.

-- Ticket-Vorschläge je Person vom Administrator sperrbar (Merkmal des Kontos, nicht von der Person änderbar wie user_settings)
ALTER TABLE users ADD COLUMN suggest_blocked BOOLEAN NOT NULL DEFAULT false;

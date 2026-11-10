-- Anbindungen (Plane, Tempo/Jira), Quellen, Meetings, To-dos, Anwesenheit, Änderungsprotokoll.
-- Quelle: backend/{plane,tempo-push,sources,meetings,monthend,todo,presence,absence-sync,audit}.js, je Benutzer.

-- Plane
CREATE TABLE plane_deletions (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    worklog_id TEXT NOT NULL, work_item_id TEXT, project_id TEXT, label TEXT, error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE plane_ticket_sync (
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    ticket TEXT NOT NULL, work_item_id TEXT, project_id TEXT, worklog_id TEXT,
    synced_minutes INTEGER, synced_at TEXT, error TEXT, title TEXT, desc_hash TEXT,
    PRIMARY KEY (user_id, ticket)
);
CREATE TABLE ticket_map (
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    jira_key TEXT NOT NULL, status TEXT NOT NULL, plane_key TEXT, plane_work_item_id TEXT,
    plane_project_id TEXT, title TEXT, mapped_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, jira_key)
);
CREATE INDEX time_entries_plane_wl_idx ON time_entries(plane_worklog_id);

-- Tempo
CREATE TABLE tempo_push_deletions (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    worklog_id TEXT NOT NULL, label TEXT, error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE tempo_month_tickets (
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    month TEXT NOT NULL, worker TEXT NOT NULL, issue_key TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(), closed_at TEXT, close_error TEXT, desc_hash TEXT,
    PRIMARY KEY (user_id, month, worker)
);
CREATE TABLE tempo_months (
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    month TEXT NOT NULL, locked INTEGER DEFAULT 0, reason TEXT, remote_count INTEGER, checked_at TEXT,
    PRIMARY KEY (user_id, month)
);

-- Quellen (Projekt-Zuordnung aus Plane/Jira)
CREATE TABLE project_sources (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    system TEXT NOT NULL, external_id TEXT NOT NULL, key TEXT, name TEXT,
    project_id BIGINT REFERENCES projects(id) ON DELETE SET NULL,
    status TEXT NOT NULL DEFAULT 'new', gone INTEGER DEFAULT 0,
    first_seen TIMESTAMPTZ NOT NULL DEFAULT now(), last_seen TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (user_id, system, external_id)
);

-- Abwesenheits-Hinweise aus dem Kalender
CREATE TABLE absence_hints (
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    key TEXT NOT NULL, title TEXT, date_from TEXT, date_to TEXT, type TEXT, calendar TEXT,
    status TEXT DEFAULT 'new', created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, key)
);

-- Meetings / Kalender (werden von der Mac-App geliefert)
CREATE TABLE calendar_events (
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    uid TEXT NOT NULL, date TEXT NOT NULL, start_time TEXT NOT NULL, end_time TEXT NOT NULL,
    title TEXT, calendar TEXT, location TEXT, updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, uid)
);
CREATE INDEX calendar_events_date_idx ON calendar_events(user_id, date);
CREATE TABLE meeting_events (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    app TEXT, start_time TEXT NOT NULL, end_time TEXT, last_seen TEXT NOT NULL
);
CREATE TABLE meeting_state (
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    key TEXT NOT NULL, status TEXT NOT NULL, entry_id BIGINT REFERENCES time_entries(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, key)                       -- status: booked | dismissed
);
CREATE TABLE meeting_rules (
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    pattern TEXT NOT NULL, project_id BIGINT, task_id BIGINT, activity TEXT,
    hits INTEGER DEFAULT 1, updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, pattern)
);

-- Anwesenheit (Minutenraster) und Vorschlags-Lernen
CREATE TABLE presence_min (
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    ts TEXT NOT NULL, date TEXT NOT NULL,
    PRIMARY KEY (user_id, ts)
);
CREATE INDEX presence_min_date_idx ON presence_min(user_id, date);
CREATE TABLE suggest_feedback (
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    key TEXT NOT NULL, task_id BIGINT NOT NULL DEFAULT 0, project_id BIGINT NOT NULL,
    n INTEGER DEFAULT 0, last TIMESTAMPTZ,
    PRIMARY KEY (user_id, key, task_id, project_id)
);

-- To-do-Vorlagen
CREATE TABLE todo_templates (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    title TEXT NOT NULL, rule TEXT NOT NULL, project_id BIGINT, jira_ticket TEXT,
    estimated_minutes INTEGER DEFAULT 30, priority INTEGER DEFAULT 2, active INTEGER DEFAULT 1,
    last_for TEXT, created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE todo_dismissed (
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    key TEXT NOT NULL, until TEXT,
    PRIMARY KEY (user_id, key)
);

-- Änderungsprotokoll für Zeiteinträge: nur anhängen, mit Hash-Kette.
-- (Das allgemeine audit_log aus 001 ist für Anmeldung/Profil/Rechte.)
CREATE TABLE entry_audit (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    ts TIMESTAMPTZ NOT NULL DEFAULT now(),
    user_id BIGINT NOT NULL,                         -- bewusst ohne FK: Protokoll überlebt Benutzer
    row_id BIGINT, action TEXT NOT NULL, before JSONB, after JSONB,
    actor TEXT, origin TEXT, prev_hash TEXT, hash TEXT
);
CREATE INDEX entry_audit_row_idx ON entry_audit(row_id);

CREATE FUNCTION entry_audit_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'Änderungsprotokoll: Löschen nicht erlaubt';
    END IF;
    -- Ändern nur zum Versiegeln: hash von NULL auf Wert, sonst nichts
    IF OLD.hash IS NOT NULL OR NEW.hash IS NULL
       OR (NEW.id, NEW.ts, NEW.user_id, NEW.row_id, NEW.action, NEW.before, NEW.after, NEW.actor, NEW.origin, NEW.prev_hash)
          IS DISTINCT FROM
          (OLD.id, OLD.ts, OLD.user_id, OLD.row_id, OLD.action, OLD.before, OLD.after, OLD.actor, OLD.origin, OLD.prev_hash) THEN
        RAISE EXCEPTION 'Änderungsprotokoll: Ändern nicht erlaubt';
    END IF;
    RETURN NEW;
END $$;
CREATE TRIGGER entry_audit_guard_trg BEFORE UPDATE OR DELETE ON entry_audit
    FOR EACH ROW EXECUTE FUNCTION entry_audit_guard();

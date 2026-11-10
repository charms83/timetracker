-- Kerndaten, 1:1 aus dem Node-System (backend/server.js), jetzt je Benutzer.
-- Datums-/Zeitwerte bleiben TEXT im bisherigen Format, damit API und Client unverändert bleiben
-- und der Import aus SQLite verlustfrei ist. Umstellung auf DATE/TIMESTAMPTZ: offen.
-- Nummern (id) sind je Tabelle global; ids aus SQLite werden beim Import nicht übernommen (id_legacy).

CREATE TABLE customers (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    legacy_id BIGINT,
    name TEXT NOT NULL, contact_name TEXT, email TEXT, notes TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE projects (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    legacy_id BIGINT,
    customer_id BIGINT REFERENCES customers(id) ON DELETE SET NULL,
    name TEXT NOT NULL, jira_key TEXT, status TEXT DEFAULT 'active',
    budget_hours DOUBLE PRECISION DEFAULT 0, hourly_rate DOUBLE PRECISION DEFAULT 0,
    description TEXT, closed_at TEXT, gone_ack INTEGER DEFAULT 0, jira_project TEXT,
    merged_into BIGINT, muted INTEGER DEFAULT 0, output_rule TEXT,
    plane_identifier TEXT, plane_project_id TEXT, successor_id BIGINT,
    tempo_account TEXT, tempo_issue TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX projects_user_idx ON projects(user_id);

CREATE TABLE tasks (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    legacy_id BIGINT,
    project_id BIGINT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    title TEXT NOT NULL, jira_ticket TEXT, status TEXT DEFAULT 'open',
    estimated_hours DOUBLE PRECISION DEFAULT 0, description TEXT, sort_order INTEGER DEFAULT 0,
    jira_link TEXT, jira_mine INTEGER DEFAULT 0, jira_origin TEXT,
    plane_project_id TEXT, plane_work_item_id TEXT, ticket_system TEXT,
    local_only BOOLEAN NOT NULL DEFAULT false,   -- Entwurfs-Ticket, wird bei Freigabe in Plane/Tempo angelegt
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX tasks_user_idx ON tasks(user_id, project_id);

CREATE TABLE plan_tasks (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    legacy_id BIGINT,
    title TEXT NOT NULL,
    project_id BIGINT REFERENCES projects(id) ON DELETE SET NULL,
    jira_ticket TEXT, estimated_minutes INTEGER DEFAULT 60, priority INTEGER DEFAULT 2,
    status TEXT DEFAULT 'open', scheduled_date TEXT, scheduled_week TEXT, recurrence TEXT,
    notes TEXT, sort_order INTEGER DEFAULT 0, completed_at TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE time_entries (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    legacy_id BIGINT,
    project_id BIGINT REFERENCES projects(id) ON DELETE SET NULL,
    task_id BIGINT REFERENCES tasks(id) ON DELETE SET NULL,
    plan_task_id BIGINT REFERENCES plan_tasks(id) ON DELETE SET NULL,
    start_time TEXT NOT NULL, end_time TEXT, duration_seconds INTEGER, comment TEXT,
    billable INTEGER DEFAULT 1, date TEXT NOT NULL, source TEXT DEFAULT 'manual',
    activity TEXT, is_absence INTEGER DEFAULT 0,
    tempo_worklog_id BIGINT, tempo_account TEXT, tempo_dup TEXT, tempo_skip INTEGER DEFAULT 0,
    tempo_target TEXT, tempo_push_id TEXT, tempo_push_hash TEXT, tempo_push_target TEXT,
    tempo_push_error TEXT, tempo_push_synced_at TEXT,
    plane_include INTEGER DEFAULT 0, plane_skip INTEGER DEFAULT 0, plane_hash TEXT, plane_error TEXT,
    plane_project_id TEXT, plane_work_item_id TEXT, plane_worklog_id TEXT,
    plane_sum_key TEXT, plane_sum_secs INTEGER, plane_synced_at TEXT,
    version BIGINT NOT NULL DEFAULT 1,               -- für den Abgleich mit der App (Phase 3)
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (user_id, tempo_worklog_id)
);
CREATE INDEX time_entries_user_date_idx ON time_entries(user_id, date);

CREATE TABLE work_days (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    date TEXT NOT NULL, start_time TEXT NOT NULL, end_time TEXT, notes TEXT,
    UNIQUE (user_id, date)
);

CREATE TABLE daily_logs (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    work_day_id BIGINT REFERENCES work_days(id) ON DELETE CASCADE,
    timestamp TEXT NOT NULL, text TEXT NOT NULL, date TEXT NOT NULL
);

CREATE TABLE pauses (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    work_day_id BIGINT REFERENCES work_days(id) ON DELETE CASCADE,
    start_time TEXT NOT NULL, end_time TEXT, date TEXT NOT NULL
);

-- Laufender Timer / Pause: genau eine Zeile je Benutzer.
CREATE TABLE timer_state (
    user_id BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    project_id BIGINT, task_id BIGINT, plan_task_id BIGINT, comment TEXT, start_time TEXT, activity TEXT
);
CREATE TABLE pause_state (
    user_id BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    start_time TEXT
);

CREATE TABLE absences (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    date_from TEXT NOT NULL, date_to TEXT NOT NULL, type TEXT NOT NULL,
    hours DOUBLE PRECISION, note TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE month_closings (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    year INTEGER NOT NULL, month INTEGER NOT NULL,
    soll_hours DOUBLE PRECISION, ist_hours DOUBLE PRECISION, delta_hours DOUBLE PRECISION,
    carry_over_hours DOUBLE PRECISION, notes TEXT, closed_at TEXT,
    UNIQUE (user_id, year, month)
);

CREATE TABLE balance_corrections (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    date TEXT NOT NULL, balance_hours DOUBLE PRECISION NOT NULL, note TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE system_logs (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id BIGINT REFERENCES users(id) ON DELETE CASCADE,
    timestamp TEXT NOT NULL, level TEXT NOT NULL DEFAULT 'info',
    category TEXT NOT NULL DEFAULT 'system', message TEXT NOT NULL
);

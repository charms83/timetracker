-- SSH-Sitzungen vom Mac-Client (Parität zu Node ssh_sessions).
CREATE TABLE ssh_sessions (
    id                BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id           BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    session_uid       TEXT NOT NULL,
    date              TEXT NOT NULL,
    start_time        TEXT NOT NULL,
    end_time          TEXT,
    duration_seconds  INTEGER,
    active_seconds    INTEGER,
    samples           INTEGER NOT NULL DEFAULT 0,
    last_seen         TEXT,
    last_active       TEXT,
    end_reason        TEXT,
    target            TEXT,
    hostname          TEXT,
    username          TEXT,
    port              INTEGER,
    remote_cmd        TEXT,
    exit_code         INTEGER,
    note              TEXT,
    project_id        BIGINT REFERENCES projects(id) ON DELETE SET NULL,
    task_id           BIGINT REFERENCES tasks(id) ON DELETE SET NULL,
    assign_source     TEXT,
    time_entry_id     BIGINT REFERENCES time_entries(id) ON DELETE SET NULL,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (user_id, session_uid)
);
CREATE INDEX ssh_sessions_date_idx ON ssh_sessions(user_id, date);

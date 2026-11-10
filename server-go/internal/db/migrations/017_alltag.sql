-- Arbeitsalltag (Parität Gruppe D): To-dos mit Ticket/Fälligkeit/Zeitblock, Herkunft von Abwesenheiten.
-- Quelle: backend/todo.js (ensure: Spalten an plan_tasks) und backend/absence-sync.js (absences.source).

ALTER TABLE plan_tasks
    ADD COLUMN due_date      TEXT,
    ADD COLUMN task_id       BIGINT REFERENCES tasks(id) ON DELETE SET NULL,
    ADD COLUMN source        TEXT,
    ADD COLUMN moved_count   INTEGER DEFAULT 0,
    ADD COLUMN block_start   TEXT,
    ADD COLUMN tempo_plan_id TEXT,
    ADD COLUMN sync_hash     TEXT,
    ADD COLUMN sync_error    TEXT,
    ADD COLUMN template_id   BIGINT REFERENCES todo_templates(id) ON DELETE SET NULL,
    ADD COLUMN event_uid     TEXT,
    ADD COLUMN ticket_title  TEXT;
CREATE INDEX plan_tasks_user_status_idx ON plan_tasks(user_id, status);
CREATE INDEX plan_tasks_user_ticket_idx ON plan_tasks(user_id, upper(jira_ticket)) WHERE jira_ticket IS NOT NULL;
CREATE INDEX time_entries_plan_task_idx ON time_entries(plan_task_id) WHERE plan_task_id IS NOT NULL;
CREATE INDEX todo_templates_user_idx ON todo_templates(user_id);

-- Herkunft einer Abwesenheit (z. B. 'kalender' aus einem übernommenen Hinweis); NULL = von Hand.
-- Die Sperre (absences_lock, 013) prüft weiter jede Zeile, unabhängig von der Herkunft.
ALTER TABLE absences ADD COLUMN source TEXT, ADD COLUMN ext_key TEXT;

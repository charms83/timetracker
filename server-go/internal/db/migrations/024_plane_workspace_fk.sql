-- Plane-Workspace an Projekten, Aufgaben und Buchungen (analog jira_instance / tempo_*_instance).
-- Bestehende Verknüpfungen aus project_sources.workspace_id nachziehen.

ALTER TABLE projects ADD COLUMN IF NOT EXISTS plane_workspace_id BIGINT REFERENCES plane_workspaces(id) ON DELETE SET NULL;
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS plane_workspace_id BIGINT REFERENCES plane_workspaces(id) ON DELETE SET NULL;
ALTER TABLE time_entries ADD COLUMN IF NOT EXISTS plane_workspace_id BIGINT REFERENCES plane_workspaces(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS projects_plane_ws_idx ON projects (user_id, plane_workspace_id) WHERE plane_workspace_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS tasks_plane_ws_idx ON tasks (user_id, plane_workspace_id) WHERE plane_workspace_id IS NOT NULL;

-- Linked Plane-Quellen → Projekt
UPDATE projects p SET plane_workspace_id = s.workspace_id
FROM project_sources s
WHERE s.user_id = p.user_id AND s.system = 'plane' AND s.status = 'linked'
  AND s.project_id = p.id AND s.workspace_id IS NOT NULL
  AND p.plane_workspace_id IS NULL;

-- Aufgaben/Buchungen vom Projekt erben
UPDATE tasks t SET plane_workspace_id = p.plane_workspace_id
FROM projects p
WHERE t.project_id = p.id AND p.plane_workspace_id IS NOT NULL AND t.plane_workspace_id IS NULL
  AND t.plane_work_item_id IS NOT NULL;

UPDATE time_entries e SET plane_workspace_id = COALESCE(t.plane_workspace_id, p.plane_workspace_id)
FROM tasks t
LEFT JOIN projects p ON p.id = COALESCE(e.project_id, t.project_id)
WHERE e.task_id = t.id AND e.plane_workspace_id IS NULL
  AND (t.plane_workspace_id IS NOT NULL OR p.plane_workspace_id IS NOT NULL)
  AND (e.plane_worklog_id IS NOT NULL OR e.plane_work_item_id IS NOT NULL);

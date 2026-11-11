-- Mehrere Plane-Workspaces (Admin legt fest), analog zu jira_instances.
-- API-Schlüssel je Person und Plane-Adresse (Schema+Host); Workspaces auf demselben Host teilen ihn.
-- Bestehende server_config.plane_* → erste Workspace-Zeile; Schlüssel bleibt scope=0 (Primär).

CREATE TABLE plane_workspaces (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name       TEXT NOT NULL CHECK (length(btrim(name)) BETWEEN 1 AND 60),
    base_url   TEXT NOT NULL,
    workspace  TEXT NOT NULL CHECK (length(btrim(workspace)) BETWEEN 1 AND 120),
    enabled    BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX plane_workspaces_name_idx ON plane_workspaces (lower(btrim(name)));
CREATE UNIQUE INDEX plane_workspaces_pair_idx ON plane_workspaces (lower(btrim(base_url)), lower(btrim(workspace)));

INSERT INTO plane_workspaces(name, base_url, workspace, enabled)
SELECT 'Plane',
       rtrim(btrim((SELECT value FROM server_config WHERE key='plane_base_url')), '/'),
       btrim((SELECT value FROM server_config WHERE key='plane_workspace')),
       true
WHERE EXISTS (SELECT 1 FROM server_config WHERE key='plane_base_url' AND btrim(value)<>'')
  AND EXISTS (SELECT 1 FROM server_config WHERE key='plane_workspace' AND btrim(value)<>'')
  AND NOT EXISTS (SELECT 1 FROM plane_workspaces);

-- Quellen je Workspace (Plane-Projekt-UUIDs können über Workspaces kollidieren)
ALTER TABLE project_sources ADD COLUMN IF NOT EXISTS workspace_id BIGINT REFERENCES plane_workspaces(id) ON DELETE SET NULL;
ALTER TABLE project_sources DROP CONSTRAINT IF EXISTS project_sources_user_id_system_external_id_key;
CREATE UNIQUE INDEX IF NOT EXISTS project_sources_user_sys_ext_ws_idx
  ON project_sources (user_id, system, external_id, COALESCE(workspace_id, 0));

-- Bestehende Plane-Quellen dem Primär-Workspace zuordnen
UPDATE project_sources s SET workspace_id = w.id
FROM (SELECT id FROM plane_workspaces ORDER BY id LIMIT 1) w
WHERE s.system = 'plane' AND s.workspace_id IS NULL;

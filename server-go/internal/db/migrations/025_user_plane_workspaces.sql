-- Welche Plane-Workspaces eine Person nutzt (nach API-Schlüssel).
-- Administrator legt die Workspace-Liste fest; die Person wählt bei vorhandenem Token,
-- welche davon für Sync/Tickets/Quellen aktiv sind. Primärschlüssel (user, workspace).

CREATE TABLE user_plane_workspaces (
    user_id      BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    workspace_id BIGINT NOT NULL REFERENCES plane_workspaces(id) ON DELETE CASCADE,
    selected     BOOLEAN NOT NULL DEFAULT true,
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, workspace_id)
);
CREATE INDEX user_plane_workspaces_user_idx ON user_plane_workspaces (user_id) WHERE selected;

-- +goose Up
-- +goose NO TRANSACTION

PRAGMA foreign_keys = OFF;

DROP TRIGGER IF EXISTS tasks_source_workspace_project_insert;
DROP TRIGGER IF EXISTS tasks_source_workspace_project_update;

CREATE TABLE workspaces_new (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    canonical_root_path TEXT NOT NULL,
    display_name TEXT NOT NULL,
    availability TEXT NOT NULL,
    is_primary INTEGER NOT NULL,
    git_metadata_json TEXT NOT NULL DEFAULT '{}',
    created_at_unix_ms INTEGER NOT NULL,
    updated_at_unix_ms INTEGER NOT NULL
);

INSERT INTO workspaces_new (
    id,
    project_id,
    canonical_root_path,
    display_name,
    availability,
    is_primary,
    git_metadata_json,
    created_at_unix_ms,
    updated_at_unix_ms
)
SELECT
    id,
    project_id,
    canonical_root_path,
    display_name,
    availability,
    is_primary,
    git_metadata_json,
    created_at_unix_ms,
    updated_at_unix_ms
FROM workspaces;

DROP TABLE workspaces;
ALTER TABLE workspaces_new RENAME TO workspaces;

CREATE INDEX workspaces_project_idx ON workspaces(project_id);
CREATE UNIQUE INDEX workspaces_project_canonical_root_idx ON workspaces(project_id, canonical_root_path);

-- +goose StatementBegin
CREATE TRIGGER tasks_source_workspace_project_insert
BEFORE INSERT ON tasks
FOR EACH ROW
WHEN NEW.source_workspace_id IS NOT NULL
 AND NOT EXISTS (
    SELECT 1
    FROM workspaces w
    WHERE w.id = NEW.source_workspace_id
      AND w.project_id = NEW.project_id
 )
BEGIN
    SELECT RAISE(ABORT, 'source workspace must belong to task project');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER tasks_source_workspace_project_update
BEFORE UPDATE OF project_id, source_workspace_id ON tasks
FOR EACH ROW
WHEN NEW.source_workspace_id IS NOT NULL
 AND NOT EXISTS (
    SELECT 1
    FROM workspaces w
    WHERE w.id = NEW.source_workspace_id
      AND w.project_id = NEW.project_id
 )
BEGIN
    SELECT RAISE(ABORT, 'source workspace must belong to task project');
END;
-- +goose StatementEnd

PRAGMA foreign_keys = ON;

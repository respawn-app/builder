-- +goose Up
-- +goose NO TRANSACTION

PRAGMA foreign_keys = OFF;

DROP TRIGGER IF EXISTS tasks_source_workspace_project_insert;
DROP TRIGGER IF EXISTS tasks_source_workspace_project_update;
DROP TRIGGER IF EXISTS projects_primary_workspace_insert;
DROP TRIGGER IF EXISTS projects_primary_workspace_update;
DROP TRIGGER IF EXISTS workspaces_child_refs_delete_cleanup;
DROP TRIGGER IF EXISTS workspaces_primary_workspace_delete;
DROP TRIGGER IF EXISTS workspaces_primary_workspace_update;
DROP TRIGGER IF EXISTS workspaces_session_project_update;
DROP TRIGGER IF EXISTS workspaces_task_source_project_update;
DROP TRIGGER IF EXISTS worktrees_session_workspace_update;
DROP TRIGGER IF EXISTS worktrees_managed_task_workspace_update;
DROP TRIGGER IF EXISTS sessions_workspace_project_insert;
DROP TRIGGER IF EXISTS sessions_workspace_project_update;
DROP TRIGGER IF EXISTS sessions_worktree_workspace_insert;
DROP TRIGGER IF EXISTS sessions_worktree_workspace_update;
DROP TRIGGER IF EXISTS tasks_managed_worktree_context_insert;
DROP TRIGGER IF EXISTS tasks_managed_worktree_context_update;

CREATE TABLE workspaces_new (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    canonical_root_path TEXT NOT NULL,
    git_metadata_json TEXT NOT NULL DEFAULT '{}',
    created_at_unix_ms INTEGER NOT NULL,
    updated_at_unix_ms INTEGER NOT NULL
);

INSERT INTO workspaces_new (
    id,
    project_id,
    canonical_root_path,
    git_metadata_json,
    created_at_unix_ms,
    updated_at_unix_ms
)
SELECT
    id,
    project_id,
    canonical_root_path,
    git_metadata_json,
    created_at_unix_ms,
    updated_at_unix_ms
FROM workspaces
ORDER BY rowid ASC;

DROP TABLE workspaces;
ALTER TABLE workspaces_new RENAME TO workspaces;

CREATE INDEX workspaces_project_idx ON workspaces(project_id);
CREATE UNIQUE INDEX workspaces_project_canonical_root_idx ON workspaces(project_id, canonical_root_path);

CREATE TABLE worktrees_new (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    canonical_root_path TEXT NOT NULL UNIQUE,
    builder_managed INTEGER NOT NULL DEFAULT 0,
    created_branch INTEGER NOT NULL DEFAULT 0,
    origin_session_id TEXT NOT NULL DEFAULT '',
    git_metadata_json TEXT NOT NULL DEFAULT '{}',
    created_at_unix_ms INTEGER NOT NULL,
    updated_at_unix_ms INTEGER NOT NULL
);

INSERT INTO worktrees_new (
    id,
    workspace_id,
    canonical_root_path,
    builder_managed,
    created_branch,
    origin_session_id,
    git_metadata_json,
    created_at_unix_ms,
    updated_at_unix_ms
)
SELECT
    id,
    workspace_id,
    canonical_root_path,
    builder_managed,
    created_branch,
    origin_session_id,
    git_metadata_json,
    created_at_unix_ms,
    updated_at_unix_ms
FROM worktrees
ORDER BY rowid ASC;

DROP TABLE worktrees;
ALTER TABLE worktrees_new RENAME TO worktrees;

CREATE INDEX worktrees_workspace_idx ON worktrees(workspace_id);

-- +goose StatementBegin
CREATE TRIGGER tasks_source_workspace_project_insert
BEFORE INSERT ON tasks
FOR EACH ROW
WHEN NEW.source_workspace_id IS NOT NULL
 AND NOT EXISTS (
    SELECT 1
    FROM workspaces w
    JOIN project_workflow_links pwl ON pwl.id = NEW.project_workflow_link_id
    WHERE w.id = NEW.source_workspace_id
      AND w.project_id = pwl.project_id
 )
BEGIN
    SELECT RAISE(ABORT, 'source workspace must belong to task project');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER tasks_source_workspace_project_update
BEFORE UPDATE OF project_workflow_link_id, source_workspace_id ON tasks
FOR EACH ROW
WHEN NEW.source_workspace_id IS NOT NULL
 AND NOT EXISTS (
    SELECT 1
    FROM workspaces w
    JOIN project_workflow_links pwl ON pwl.id = NEW.project_workflow_link_id
    WHERE w.id = NEW.source_workspace_id
      AND w.project_id = pwl.project_id
 )
BEGIN
    SELECT RAISE(ABORT, 'source workspace must belong to task project');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER projects_primary_workspace_insert
BEFORE INSERT ON projects
FOR EACH ROW
WHEN NEW.primary_workspace_id != ''
 AND NOT EXISTS (
    SELECT 1
    FROM workspaces w
    WHERE w.id = NEW.primary_workspace_id
      AND w.project_id = NEW.id
 )
BEGIN
    SELECT RAISE(ABORT, 'primary workspace must belong to project');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER projects_primary_workspace_update
BEFORE UPDATE OF id, primary_workspace_id ON projects
FOR EACH ROW
WHEN NEW.primary_workspace_id != ''
 AND NOT EXISTS (
    SELECT 1
    FROM workspaces w
    WHERE w.id = NEW.primary_workspace_id
      AND w.project_id = NEW.id
 )
BEGIN
    SELECT RAISE(ABORT, 'primary workspace must belong to project');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER workspaces_child_refs_delete_cleanup
BEFORE DELETE ON workspaces
FOR EACH ROW
BEGIN
    UPDATE sessions
    SET worktree_id = NULL
    WHERE workspace_id = OLD.id
      AND worktree_id IN (
          SELECT wt.id
          FROM worktrees wt
          WHERE wt.workspace_id = OLD.id
      );

    UPDATE tasks
    SET managed_worktree_id = NULL
    WHERE source_workspace_id = OLD.id
      AND managed_worktree_id IN (
          SELECT wt.id
          FROM worktrees wt
          WHERE wt.workspace_id = OLD.id
      );
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER workspaces_primary_workspace_delete
AFTER DELETE ON workspaces
FOR EACH ROW
BEGIN
    UPDATE projects
    SET primary_workspace_id = ''
    WHERE id = OLD.project_id
      AND primary_workspace_id = OLD.id;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER workspaces_primary_workspace_update
BEFORE UPDATE OF id, project_id ON workspaces
FOR EACH ROW
WHEN EXISTS (
    SELECT 1
    FROM projects p
    WHERE p.primary_workspace_id = OLD.id
      AND (
          p.id != NEW.project_id
          OR OLD.id != NEW.id
      )
)
BEGIN
    SELECT RAISE(ABORT, 'primary workspace must belong to project');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER workspaces_session_project_update
BEFORE UPDATE OF id, project_id ON workspaces
FOR EACH ROW
WHEN EXISTS (
    SELECT 1
    FROM sessions s
    WHERE s.workspace_id = OLD.id
      AND (
          OLD.id != NEW.id
          OR s.project_id != NEW.project_id
      )
)
BEGIN
    SELECT RAISE(ABORT, 'session workspace must belong to project');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER workspaces_task_source_project_update
BEFORE UPDATE OF id, project_id ON workspaces
FOR EACH ROW
WHEN EXISTS (
    SELECT 1
    FROM tasks t
    JOIN project_workflow_links pwl ON pwl.id = t.project_workflow_link_id
    WHERE t.source_workspace_id = OLD.id
      AND (
          OLD.id != NEW.id
          OR pwl.project_id != NEW.project_id
      )
)
BEGIN
    SELECT RAISE(ABORT, 'source workspace must belong to task project');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER worktrees_session_workspace_update
BEFORE UPDATE OF id, workspace_id ON worktrees
FOR EACH ROW
WHEN EXISTS (
    SELECT 1
    FROM sessions s
    WHERE s.worktree_id = OLD.id
      AND (
          OLD.id != NEW.id
          OR s.workspace_id IS NULL
          OR s.workspace_id != NEW.workspace_id
      )
)
BEGIN
    SELECT RAISE(ABORT, 'session worktree must belong to session workspace');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER worktrees_managed_task_workspace_update
BEFORE UPDATE OF id, workspace_id ON worktrees
FOR EACH ROW
WHEN EXISTS (
    SELECT 1
    FROM tasks t
    WHERE t.managed_worktree_id = OLD.id
      AND (
          OLD.id != NEW.id
          OR t.source_workspace_id IS NULL
          OR t.source_workspace_id != NEW.workspace_id
      )
)
BEGIN
    SELECT RAISE(ABORT, 'managed worktree must belong to task source workspace');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER sessions_workspace_project_insert
BEFORE INSERT ON sessions
FOR EACH ROW
WHEN NEW.workspace_id IS NOT NULL
 AND NOT EXISTS (
    SELECT 1
    FROM workspaces w
    WHERE w.id = NEW.workspace_id
      AND w.project_id = NEW.project_id
 )
BEGIN
    SELECT RAISE(ABORT, 'session workspace must belong to project');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER sessions_workspace_project_update
BEFORE UPDATE OF project_id, workspace_id ON sessions
FOR EACH ROW
WHEN NEW.workspace_id IS NOT NULL
 AND NOT EXISTS (
    SELECT 1
    FROM workspaces w
    WHERE w.id = NEW.workspace_id
      AND w.project_id = NEW.project_id
 )
BEGIN
    SELECT RAISE(ABORT, 'session workspace must belong to project');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER sessions_worktree_workspace_insert
BEFORE INSERT ON sessions
FOR EACH ROW
WHEN NEW.worktree_id IS NOT NULL
 AND (
    NEW.workspace_id IS NULL
    OR NOT EXISTS (
        SELECT 1
        FROM worktrees wt
        WHERE wt.id = NEW.worktree_id
          AND wt.workspace_id = NEW.workspace_id
    )
 )
BEGIN
    SELECT RAISE(ABORT, 'session worktree must belong to session workspace');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER sessions_worktree_workspace_update
BEFORE UPDATE OF workspace_id, worktree_id ON sessions
FOR EACH ROW
WHEN NEW.worktree_id IS NOT NULL
 AND (
    NEW.workspace_id IS NULL
    OR NOT EXISTS (
        SELECT 1
        FROM worktrees wt
        WHERE wt.id = NEW.worktree_id
          AND wt.workspace_id = NEW.workspace_id
    )
 )
BEGIN
    SELECT RAISE(ABORT, 'session worktree must belong to session workspace');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER tasks_managed_worktree_context_insert
BEFORE INSERT ON tasks
FOR EACH ROW
WHEN NEW.managed_worktree_id IS NOT NULL
 AND NOT EXISTS (
    SELECT 1
    FROM worktrees wt
    JOIN workspaces source_workspace ON source_workspace.id = NEW.source_workspace_id
    JOIN project_workflow_links pwl ON pwl.id = NEW.project_workflow_link_id
    WHERE wt.id = NEW.managed_worktree_id
      AND wt.workspace_id = NEW.source_workspace_id
      AND source_workspace.project_id = pwl.project_id
 )
BEGIN
    SELECT RAISE(ABORT, 'managed worktree must belong to task source workspace');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER tasks_managed_worktree_context_update
BEFORE UPDATE OF project_workflow_link_id, source_workspace_id, managed_worktree_id ON tasks
FOR EACH ROW
WHEN NEW.managed_worktree_id IS NOT NULL
 AND NOT EXISTS (
    SELECT 1
    FROM worktrees wt
    JOIN workspaces source_workspace ON source_workspace.id = NEW.source_workspace_id
    JOIN project_workflow_links pwl ON pwl.id = NEW.project_workflow_link_id
    WHERE wt.id = NEW.managed_worktree_id
      AND wt.workspace_id = NEW.source_workspace_id
      AND source_workspace.project_id = pwl.project_id
 )
BEGIN
    SELECT RAISE(ABORT, 'managed worktree must belong to task source workspace');
END;
-- +goose StatementEnd

CREATE TEMP TABLE migration_workspace_storage_check_zero(value INTEGER NOT NULL CHECK (value = 0));

INSERT INTO migration_workspace_storage_check_zero(value)
SELECT 1
WHERE EXISTS (
    SELECT 1
    FROM pragma_foreign_key_check
);

DROP TABLE migration_workspace_storage_check_zero;

PRAGMA foreign_keys = ON;

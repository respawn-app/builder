-- +goose Down

DROP INDEX IF EXISTS runtime_leases_session_idx;
DROP TABLE IF EXISTS runtime_leases;

DROP INDEX IF EXISTS sessions_artifact_relpath_idx;
DROP INDEX IF EXISTS sessions_workspace_idx;
DROP INDEX IF EXISTS sessions_project_idx;
DROP TABLE IF EXISTS sessions;

DROP INDEX IF EXISTS worktrees_workspace_idx;
DROP TABLE IF EXISTS worktrees;

DROP INDEX IF EXISTS workspaces_project_idx;
DROP TABLE IF EXISTS workspaces;

DROP TABLE IF EXISTS projects;

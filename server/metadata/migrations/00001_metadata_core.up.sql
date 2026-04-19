-- +goose Up

CREATE TABLE projects (
    id TEXT PRIMARY KEY,
    display_name TEXT NOT NULL,
    created_at_unix_ms INTEGER NOT NULL,
    updated_at_unix_ms INTEGER NOT NULL,
    metadata_json TEXT NOT NULL DEFAULT '{}'
);

CREATE TABLE workspaces (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    canonical_root_path TEXT NOT NULL UNIQUE,
    display_name TEXT NOT NULL,
    availability TEXT NOT NULL,
    is_primary INTEGER NOT NULL,
    git_metadata_json TEXT NOT NULL DEFAULT '{}',
    created_at_unix_ms INTEGER NOT NULL,
    updated_at_unix_ms INTEGER NOT NULL
);

CREATE INDEX workspaces_project_idx ON workspaces(project_id);

CREATE TABLE worktrees (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    canonical_root_path TEXT NOT NULL UNIQUE,
    display_name TEXT NOT NULL,
    availability TEXT NOT NULL,
    is_main INTEGER NOT NULL,
    git_metadata_json TEXT NOT NULL DEFAULT '{}',
    created_at_unix_ms INTEGER NOT NULL,
    updated_at_unix_ms INTEGER NOT NULL
);

CREATE INDEX worktrees_workspace_idx ON worktrees(workspace_id);

CREATE TABLE sessions (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    worktree_id TEXT REFERENCES worktrees(id) ON DELETE SET NULL,
    artifact_relpath TEXT NOT NULL,
    name TEXT NOT NULL DEFAULT '',
    first_prompt_preview TEXT NOT NULL DEFAULT '',
    input_draft TEXT NOT NULL DEFAULT '',
    parent_session_id TEXT NOT NULL DEFAULT '',
    created_at_unix_ms INTEGER NOT NULL,
    updated_at_unix_ms INTEGER NOT NULL,
    last_sequence INTEGER NOT NULL DEFAULT 0,
    model_request_count INTEGER NOT NULL DEFAULT 0,
    in_flight_step INTEGER NOT NULL DEFAULT 0,
    agents_injected INTEGER NOT NULL DEFAULT 0,
    cwd_relpath TEXT NOT NULL DEFAULT '.',
    continuation_json TEXT NOT NULL DEFAULT '{}',
    locked_json TEXT NOT NULL DEFAULT '{}',
    usage_state_json TEXT NOT NULL DEFAULT '{}',
    metadata_json TEXT NOT NULL DEFAULT '{}'
);

CREATE INDEX sessions_project_idx ON sessions(project_id, updated_at_unix_ms DESC);
CREATE INDEX sessions_workspace_idx ON sessions(workspace_id, updated_at_unix_ms DESC);
CREATE UNIQUE INDEX sessions_artifact_relpath_idx ON sessions(artifact_relpath);

CREATE TABLE runtime_leases (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    client_id TEXT NOT NULL DEFAULT '',
    request_id TEXT NOT NULL DEFAULT '',
    state TEXT NOT NULL,
    created_at_unix_ms INTEGER NOT NULL,
    acquired_at_unix_ms INTEGER NOT NULL,
    released_at_unix_ms INTEGER NOT NULL DEFAULT 0,
    expires_at_unix_ms INTEGER NOT NULL DEFAULT 0,
    metadata_json TEXT NOT NULL DEFAULT '{}'
);

CREATE INDEX runtime_leases_session_idx ON runtime_leases(session_id, created_at_unix_ms DESC);

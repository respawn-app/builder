-- name: GetWorkspaceBindingByCanonicalRoot :one
SELECT
    p.id AS project_id,
    p.display_name AS project_display_name,
    w.id AS workspace_id,
    w.canonical_root_path AS workspace_root
FROM workspaces w
JOIN projects p ON p.id = w.project_id
WHERE w.canonical_root_path = sqlc.arg(canonical_root_path)
LIMIT 1;

-- name: UpsertProject :exec
INSERT INTO projects (
    id,
    display_name,
    created_at_unix_ms,
    updated_at_unix_ms,
    metadata_json
) VALUES (
    sqlc.arg(id),
    sqlc.arg(display_name),
    sqlc.arg(created_at_unix_ms),
    sqlc.arg(updated_at_unix_ms),
    sqlc.arg(metadata_json)
)
ON CONFLICT(id) DO UPDATE SET
    display_name = excluded.display_name,
    updated_at_unix_ms = excluded.updated_at_unix_ms,
    metadata_json = excluded.metadata_json;

-- name: UpsertWorkspace :exec
INSERT INTO workspaces (
    id,
    project_id,
    canonical_root_path,
    display_name,
    availability,
    is_primary,
    git_metadata_json,
    created_at_unix_ms,
    updated_at_unix_ms
) VALUES (
    sqlc.arg(id),
    sqlc.arg(project_id),
    sqlc.arg(canonical_root_path),
    sqlc.arg(display_name),
    sqlc.arg(availability),
    sqlc.arg(is_primary),
    sqlc.arg(git_metadata_json),
    sqlc.arg(created_at_unix_ms),
    sqlc.arg(updated_at_unix_ms)
)
ON CONFLICT(id) DO UPDATE SET
    project_id = excluded.project_id,
    canonical_root_path = excluded.canonical_root_path,
    display_name = excluded.display_name,
    availability = excluded.availability,
    is_primary = excluded.is_primary,
    git_metadata_json = excluded.git_metadata_json,
    updated_at_unix_ms = excluded.updated_at_unix_ms;

-- name: UpsertSession :exec
INSERT INTO sessions (
    id,
    project_id,
    workspace_id,
    worktree_id,
    artifact_relpath,
    name,
    first_prompt_preview,
    input_draft,
    parent_session_id,
    created_at_unix_ms,
    updated_at_unix_ms,
    last_sequence,
    model_request_count,
    in_flight_step,
    agents_injected,
    launch_visible,
    cwd_relpath,
    continuation_json,
    locked_json,
    usage_state_json,
    metadata_json
) VALUES (
    sqlc.arg(id),
    sqlc.arg(project_id),
    sqlc.arg(workspace_id),
    sqlc.narg(worktree_id),
    sqlc.arg(artifact_relpath),
    sqlc.arg(name),
    sqlc.arg(first_prompt_preview),
    sqlc.arg(input_draft),
    sqlc.arg(parent_session_id),
    sqlc.arg(created_at_unix_ms),
    sqlc.arg(updated_at_unix_ms),
    sqlc.arg(last_sequence),
    sqlc.arg(model_request_count),
    sqlc.arg(in_flight_step),
    sqlc.arg(agents_injected),
    sqlc.arg(launch_visible),
    sqlc.arg(cwd_relpath),
    sqlc.arg(continuation_json),
    sqlc.arg(locked_json),
    sqlc.arg(usage_state_json),
    sqlc.arg(metadata_json)
)
ON CONFLICT(id) DO UPDATE SET
    project_id = excluded.project_id,
    workspace_id = excluded.workspace_id,
    worktree_id = excluded.worktree_id,
    artifact_relpath = excluded.artifact_relpath,
    name = excluded.name,
    first_prompt_preview = excluded.first_prompt_preview,
    input_draft = excluded.input_draft,
    parent_session_id = excluded.parent_session_id,
    updated_at_unix_ms = excluded.updated_at_unix_ms,
    last_sequence = excluded.last_sequence,
    model_request_count = excluded.model_request_count,
    in_flight_step = excluded.in_flight_step,
    agents_injected = excluded.agents_injected,
    launch_visible = CASE
        WHEN sessions.launch_visible <> 0 OR excluded.launch_visible <> 0 THEN 1
        ELSE 0
    END,
    cwd_relpath = excluded.cwd_relpath,
    continuation_json = excluded.continuation_json,
    locked_json = excluded.locked_json,
    usage_state_json = excluded.usage_state_json,
    metadata_json = excluded.metadata_json;

-- name: ListProjects :many
SELECT
    p.id,
    p.display_name,
    w.canonical_root_path AS root_path,
    CAST(COALESCE(COUNT(s.id), 0) AS INTEGER) AS session_count,
    COALESCE(MAX(s.updated_at_unix_ms), p.updated_at_unix_ms) AS latest_activity_unix_ms
FROM projects p
JOIN workspaces w ON w.project_id = p.id AND w.is_primary = 1
LEFT JOIN sessions s ON s.project_id = p.id AND s.launch_visible <> 0
GROUP BY p.id, p.display_name, w.canonical_root_path, p.updated_at_unix_ms
ORDER BY latest_activity_unix_ms DESC;

-- name: GetProjectSummary :one
SELECT
    p.id,
    p.display_name,
    w.canonical_root_path AS root_path,
    CAST(COALESCE(COUNT(s.id), 0) AS INTEGER) AS session_count,
    COALESCE(MAX(s.updated_at_unix_ms), p.updated_at_unix_ms) AS latest_activity_unix_ms
FROM projects p
JOIN workspaces w ON w.project_id = p.id AND w.is_primary = 1
LEFT JOIN sessions s ON s.project_id = p.id AND s.launch_visible <> 0
WHERE p.id = sqlc.arg(project_id)
GROUP BY p.id, p.display_name, w.canonical_root_path, p.updated_at_unix_ms
LIMIT 1;

-- name: ListSessionsByProject :many
SELECT
    id,
    name,
    first_prompt_preview,
    updated_at_unix_ms
FROM sessions
WHERE project_id = sqlc.arg(project_id)
  AND launch_visible <> 0
ORDER BY updated_at_unix_ms DESC, rowid DESC;

-- name: GetSessionRecordByID :one
SELECT
    s.id,
    s.artifact_relpath,
    s.name,
    s.first_prompt_preview,
    s.input_draft,
    s.parent_session_id,
    s.created_at_unix_ms,
    s.updated_at_unix_ms,
    s.last_sequence,
    s.model_request_count,
    s.in_flight_step,
    s.agents_injected,
    s.continuation_json,
    s.locked_json,
    s.usage_state_json,
    s.metadata_json,
    w.canonical_root_path AS workspace_root
FROM sessions s
JOIN workspaces w ON w.id = s.workspace_id
WHERE s.id = sqlc.arg(session_id)
LIMIT 1;

-- name: GetSessionExecutionTargetByID :one
SELECT
    s.id AS session_id,
    s.project_id,
    s.workspace_id,
    w.display_name AS workspace_name,
    w.canonical_root_path AS workspace_root,
    w.availability AS workspace_availability,
    s.worktree_id,
    COALESCE(wt.display_name, '') AS worktree_name,
    COALESCE(wt.canonical_root_path, '') AS worktree_root,
    COALESCE(wt.availability, '') AS worktree_availability,
    s.cwd_relpath
FROM sessions s
JOIN workspaces w ON w.id = s.workspace_id
LEFT JOIN worktrees wt ON wt.id = s.worktree_id
WHERE s.id = sqlc.arg(session_id)
LIMIT 1;

-- name: InsertRuntimeLease :exec
INSERT INTO runtime_leases (
    id,
    session_id,
    client_id,
    request_id,
    state,
    created_at_unix_ms,
    acquired_at_unix_ms,
    released_at_unix_ms,
    expires_at_unix_ms,
    metadata_json
) VALUES (
    sqlc.arg(id),
    sqlc.arg(session_id),
    sqlc.arg(client_id),
    sqlc.arg(request_id),
    sqlc.arg(state),
    sqlc.arg(created_at_unix_ms),
    sqlc.arg(acquired_at_unix_ms),
    sqlc.arg(released_at_unix_ms),
    sqlc.arg(expires_at_unix_ms),
    sqlc.arg(metadata_json)
);

-- name: GetRuntimeLeaseByID :one
SELECT
    id,
    session_id,
    client_id,
    request_id,
    state,
    created_at_unix_ms,
    acquired_at_unix_ms,
    released_at_unix_ms,
    expires_at_unix_ms,
    metadata_json
FROM runtime_leases
WHERE id = sqlc.arg(lease_id)
LIMIT 1;

-- name: ReleaseRuntimeLeaseByID :execrows
UPDATE runtime_leases
SET
    state = 'released',
    released_at_unix_ms = sqlc.arg(released_at_unix_ms)
WHERE id = sqlc.arg(lease_id)
  AND session_id = sqlc.arg(session_id)
  AND state <> 'released';

-- name: ReleaseActiveRuntimeLeasesBySession :exec
UPDATE runtime_leases
SET
    state = 'released',
    released_at_unix_ms = sqlc.arg(released_at_unix_ms)
WHERE session_id = sqlc.arg(session_id)
  AND state = 'active';

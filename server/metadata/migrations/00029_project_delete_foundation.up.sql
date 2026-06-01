-- +goose Up

CREATE TEMP TABLE migration_project_delete_check_zero(value INTEGER NOT NULL CHECK (value = 0));

INSERT INTO migration_project_delete_check_zero(value)
SELECT 1
WHERE EXISTS (
    SELECT 1
    FROM task_runs r
    JOIN task_node_placements p ON p.id = r.placement_id
    JOIN task_records t ON t.id = p.task_id
    JOIN sessions s ON s.id = r.session_id
    WHERE r.session_id IS NOT NULL
      AND trim(r.session_id) != ''
      AND s.project_id != t.project_id
);

DROP TABLE migration_project_delete_check_zero;

CREATE TABLE project_delete_jobs (
    project_id TEXT PRIMARY KEY,
    impact_token TEXT NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('active', 'finalizing', 'completed')),
    expected_workspace_count INTEGER NOT NULL CHECK (expected_workspace_count >= 0),
    expected_workflow_link_count INTEGER NOT NULL CHECK (expected_workflow_link_count >= 0),
    expected_task_count INTEGER NOT NULL CHECK (expected_task_count >= 0),
    expected_terminal_task_count INTEGER NOT NULL DEFAULT 0 CHECK (expected_terminal_task_count >= 0),
    expected_non_terminal_task_count INTEGER NOT NULL DEFAULT 0 CHECK (expected_non_terminal_task_count >= 0),
    expected_session_count INTEGER NOT NULL CHECK (expected_session_count >= 0),
    expected_session_artifact_count INTEGER NOT NULL CHECK (expected_session_artifact_count >= 0),
    created_at_unix_ms INTEGER NOT NULL CHECK (created_at_unix_ms >= 0),
    updated_at_unix_ms INTEGER NOT NULL CHECK (updated_at_unix_ms >= 0),
    completed_at_unix_ms INTEGER NOT NULL DEFAULT 0 CHECK (completed_at_unix_ms >= 0)
);

CREATE TABLE project_delete_session_artifacts (
    project_id TEXT NOT NULL,
    session_id TEXT NOT NULL,
    artifact_relpath TEXT NOT NULL,
    expected_relpath TEXT NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('pending', 'cleaned', 'missing', 'failed', 'skipped_not_builder_owned')),
    last_error TEXT NOT NULL DEFAULT '',
    updated_at_unix_ms INTEGER NOT NULL CHECK (updated_at_unix_ms >= 0),
    PRIMARY KEY (project_id, session_id)
);

CREATE TABLE project_delete_finalizer_bypass (
    project_id TEXT PRIMARY KEY,
    token TEXT NOT NULL
);

CREATE INDEX project_delete_session_artifacts_state_idx
    ON project_delete_session_artifacts(project_id, state);

-- +goose StatementBegin
CREATE TRIGGER projects_project_delete_insert
BEFORE INSERT ON projects
FOR EACH ROW
WHEN EXISTS (
    SELECT 1
    FROM project_delete_jobs j
    WHERE j.project_id = NEW.id
      AND j.state IN ('active', 'finalizing')
)
AND NOT EXISTS (
    SELECT 1
    FROM project_delete_finalizer_bypass b
    WHERE b.project_id = NEW.id
)
BEGIN
    SELECT RAISE(ABORT, 'project_delete_in_progress');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER projects_project_delete_update
BEFORE UPDATE ON projects
FOR EACH ROW
WHEN (
    EXISTS (
        SELECT 1
        FROM project_delete_jobs j
        WHERE j.project_id = OLD.id
          AND j.state IN ('active', 'finalizing')
    )
    AND NOT EXISTS (
        SELECT 1
        FROM project_delete_finalizer_bypass b
        WHERE b.project_id = OLD.id
    )
)
OR (
    EXISTS (
        SELECT 1
        FROM project_delete_jobs j
        WHERE j.project_id = NEW.id
          AND j.state IN ('active', 'finalizing')
    )
    AND NOT EXISTS (
        SELECT 1
        FROM project_delete_finalizer_bypass b
        WHERE b.project_id = NEW.id
    )
)
BEGIN
    SELECT RAISE(ABORT, 'project_delete_in_progress');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER projects_project_delete_delete
BEFORE DELETE ON projects
FOR EACH ROW
WHEN EXISTS (
    SELECT 1
    FROM project_delete_jobs j
    WHERE j.project_id = OLD.id
      AND j.state IN ('active', 'finalizing')
)
AND NOT EXISTS (
    SELECT 1
    FROM project_delete_finalizer_bypass b
    WHERE b.project_id = OLD.id
)
BEGIN
    SELECT RAISE(ABORT, 'project_delete_in_progress');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER workspaces_project_delete_insert
BEFORE INSERT ON workspaces
FOR EACH ROW
WHEN EXISTS (
    SELECT 1
    FROM project_delete_jobs j
    WHERE j.project_id = NEW.project_id
      AND j.state IN ('active', 'finalizing')
)
AND NOT EXISTS (
    SELECT 1
    FROM project_delete_finalizer_bypass b
    WHERE b.project_id = NEW.project_id
)
BEGIN
    SELECT RAISE(ABORT, 'project_delete_in_progress');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER workspaces_project_delete_update
BEFORE UPDATE ON workspaces
FOR EACH ROW
WHEN (
    EXISTS (
        SELECT 1
        FROM project_delete_jobs j
        WHERE j.project_id = OLD.project_id
          AND j.state IN ('active', 'finalizing')
    )
    AND NOT EXISTS (
        SELECT 1
        FROM project_delete_finalizer_bypass b
        WHERE b.project_id = OLD.project_id
    )
)
OR (
    EXISTS (
        SELECT 1
        FROM project_delete_jobs j
        WHERE j.project_id = NEW.project_id
          AND j.state IN ('active', 'finalizing')
    )
    AND NOT EXISTS (
        SELECT 1
        FROM project_delete_finalizer_bypass b
        WHERE b.project_id = NEW.project_id
    )
)
BEGIN
    SELECT RAISE(ABORT, 'project_delete_in_progress');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER workspaces_project_delete_delete
BEFORE DELETE ON workspaces
FOR EACH ROW
WHEN EXISTS (
    SELECT 1
    FROM project_delete_jobs j
    WHERE j.project_id = OLD.project_id
      AND j.state IN ('active', 'finalizing')
)
AND NOT EXISTS (
    SELECT 1
    FROM project_delete_finalizer_bypass b
    WHERE b.project_id = OLD.project_id
)
BEGIN
    SELECT RAISE(ABORT, 'project_delete_in_progress');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER project_workflow_links_project_delete_insert
BEFORE INSERT ON project_workflow_links
FOR EACH ROW
WHEN EXISTS (
    SELECT 1
    FROM project_delete_jobs j
    WHERE j.project_id = NEW.project_id
      AND j.state IN ('active', 'finalizing')
)
AND NOT EXISTS (
    SELECT 1
    FROM project_delete_finalizer_bypass b
    WHERE b.project_id = NEW.project_id
)
BEGIN
    SELECT RAISE(ABORT, 'project_delete_in_progress');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER project_workflow_links_project_delete_update
BEFORE UPDATE ON project_workflow_links
FOR EACH ROW
WHEN (
    EXISTS (
        SELECT 1
        FROM project_delete_jobs j
        WHERE j.project_id = OLD.project_id
          AND j.state IN ('active', 'finalizing')
    )
    AND NOT EXISTS (
        SELECT 1
        FROM project_delete_finalizer_bypass b
        WHERE b.project_id = OLD.project_id
    )
)
OR (
    EXISTS (
        SELECT 1
        FROM project_delete_jobs j
        WHERE j.project_id = NEW.project_id
          AND j.state IN ('active', 'finalizing')
    )
    AND NOT EXISTS (
        SELECT 1
        FROM project_delete_finalizer_bypass b
        WHERE b.project_id = NEW.project_id
    )
)
BEGIN
    SELECT RAISE(ABORT, 'project_delete_in_progress');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER project_workflow_links_project_delete_delete
BEFORE DELETE ON project_workflow_links
FOR EACH ROW
WHEN EXISTS (
    SELECT 1
    FROM project_delete_jobs j
    WHERE j.project_id = OLD.project_id
      AND j.state IN ('active', 'finalizing')
)
AND NOT EXISTS (
    SELECT 1
    FROM project_delete_finalizer_bypass b
    WHERE b.project_id = OLD.project_id
)
BEGIN
    SELECT RAISE(ABORT, 'project_delete_in_progress');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER worktrees_project_delete_insert
BEFORE INSERT ON worktrees
FOR EACH ROW
WHEN EXISTS (
    SELECT 1
    FROM workspaces w
    JOIN project_delete_jobs j ON j.project_id = w.project_id
    WHERE w.id = NEW.workspace_id
      AND j.state IN ('active', 'finalizing')
)
AND NOT EXISTS (
    SELECT 1
    FROM workspaces w
    JOIN project_delete_finalizer_bypass b ON b.project_id = w.project_id
    WHERE w.id = NEW.workspace_id
)
BEGIN
    SELECT RAISE(ABORT, 'project_delete_in_progress');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER worktrees_project_delete_update
BEFORE UPDATE ON worktrees
FOR EACH ROW
WHEN (
    EXISTS (
        SELECT 1
        FROM workspaces w
        JOIN project_delete_jobs j ON j.project_id = w.project_id
        WHERE w.id = OLD.workspace_id
          AND j.state IN ('active', 'finalizing')
    )
    AND NOT EXISTS (
        SELECT 1
        FROM workspaces w
        JOIN project_delete_finalizer_bypass b ON b.project_id = w.project_id
        WHERE w.id = OLD.workspace_id
    )
)
OR (
    EXISTS (
        SELECT 1
        FROM workspaces w
        JOIN project_delete_jobs j ON j.project_id = w.project_id
        WHERE w.id = NEW.workspace_id
          AND j.state IN ('active', 'finalizing')
    )
    AND NOT EXISTS (
        SELECT 1
        FROM workspaces w
        JOIN project_delete_finalizer_bypass b ON b.project_id = w.project_id
        WHERE w.id = NEW.workspace_id
    )
)
BEGIN
    SELECT RAISE(ABORT, 'project_delete_in_progress');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER worktrees_project_delete_delete
BEFORE DELETE ON worktrees
FOR EACH ROW
WHEN EXISTS (
    SELECT 1
    FROM workspaces w
    JOIN project_delete_jobs j ON j.project_id = w.project_id
    WHERE w.id = OLD.workspace_id
      AND j.state IN ('active', 'finalizing')
)
AND NOT EXISTS (
    SELECT 1
    FROM workspaces w
    JOIN project_delete_finalizer_bypass b ON b.project_id = w.project_id
    WHERE w.id = OLD.workspace_id
)
BEGIN
    SELECT RAISE(ABORT, 'project_delete_in_progress');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER sessions_project_delete_insert
BEFORE INSERT ON sessions
FOR EACH ROW
WHEN EXISTS (
    SELECT 1
    FROM project_delete_jobs j
    WHERE j.project_id = NEW.project_id
      AND j.state IN ('active', 'finalizing')
)
AND NOT EXISTS (
    SELECT 1
    FROM project_delete_finalizer_bypass b
    WHERE b.project_id = NEW.project_id
)
BEGIN
    SELECT RAISE(ABORT, 'project_delete_in_progress');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER sessions_project_delete_update
BEFORE UPDATE OF project_id, workspace_id, worktree_id, artifact_relpath ON sessions
FOR EACH ROW
WHEN (
    EXISTS (
        SELECT 1
        FROM project_delete_jobs j
        WHERE j.project_id = OLD.project_id
          AND j.state IN ('active', 'finalizing')
    )
    AND NOT EXISTS (
        SELECT 1
        FROM project_delete_finalizer_bypass b
        WHERE b.project_id = OLD.project_id
    )
)
OR (
    EXISTS (
        SELECT 1
        FROM project_delete_jobs j
        WHERE j.project_id = NEW.project_id
          AND j.state IN ('active', 'finalizing')
    )
    AND NOT EXISTS (
        SELECT 1
        FROM project_delete_finalizer_bypass b
        WHERE b.project_id = NEW.project_id
    )
)
BEGIN
    SELECT RAISE(ABORT, 'project_delete_in_progress');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER sessions_project_delete_delete
BEFORE DELETE ON sessions
FOR EACH ROW
WHEN EXISTS (
    SELECT 1
    FROM project_delete_jobs j
    WHERE j.project_id = OLD.project_id
      AND j.state IN ('active', 'finalizing')
)
AND NOT EXISTS (
    SELECT 1
    FROM project_delete_finalizer_bypass b
    WHERE b.project_id = OLD.project_id
)
BEGIN
    SELECT RAISE(ABORT, 'project_delete_in_progress');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER tasks_project_delete_insert
BEFORE INSERT ON tasks
FOR EACH ROW
WHEN EXISTS (
    SELECT 1
    FROM project_workflow_links pwl
    JOIN project_delete_jobs j ON j.project_id = pwl.project_id
    WHERE pwl.id = NEW.project_workflow_link_id
      AND j.state IN ('active', 'finalizing')
)
AND NOT EXISTS (
    SELECT 1
    FROM project_workflow_links pwl
    JOIN project_delete_finalizer_bypass b ON b.project_id = pwl.project_id
    WHERE pwl.id = NEW.project_workflow_link_id
)
BEGIN
    SELECT RAISE(ABORT, 'project_delete_in_progress');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER tasks_project_delete_update
BEFORE UPDATE ON tasks
FOR EACH ROW
WHEN (
    EXISTS (
        SELECT 1
        FROM project_workflow_links pwl
        JOIN project_delete_jobs j ON j.project_id = pwl.project_id
        WHERE pwl.id = OLD.project_workflow_link_id
          AND j.state IN ('active', 'finalizing')
    )
    AND NOT EXISTS (
        SELECT 1
        FROM project_workflow_links pwl
        JOIN project_delete_finalizer_bypass b ON b.project_id = pwl.project_id
        WHERE pwl.id = OLD.project_workflow_link_id
    )
)
OR (
    EXISTS (
        SELECT 1
        FROM project_workflow_links pwl
        JOIN project_delete_jobs j ON j.project_id = pwl.project_id
        WHERE pwl.id = NEW.project_workflow_link_id
          AND j.state IN ('active', 'finalizing')
    )
    AND NOT EXISTS (
        SELECT 1
        FROM project_workflow_links pwl
        JOIN project_delete_finalizer_bypass b ON b.project_id = pwl.project_id
        WHERE pwl.id = NEW.project_workflow_link_id
    )
)
BEGIN
    SELECT RAISE(ABORT, 'project_delete_in_progress');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER tasks_project_delete_delete
BEFORE DELETE ON tasks
FOR EACH ROW
WHEN EXISTS (
    SELECT 1
    FROM project_workflow_links pwl
    JOIN project_delete_jobs j ON j.project_id = pwl.project_id
    WHERE pwl.id = OLD.project_workflow_link_id
      AND j.state IN ('active', 'finalizing')
)
AND NOT EXISTS (
    SELECT 1
    FROM project_workflow_links pwl
    JOIN project_delete_finalizer_bypass b ON b.project_id = pwl.project_id
    WHERE pwl.id = OLD.project_workflow_link_id
)
BEGIN
    SELECT RAISE(ABORT, 'project_delete_in_progress');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER task_node_placements_project_delete_insert
BEFORE INSERT ON task_node_placements
FOR EACH ROW
WHEN EXISTS (
    SELECT 1
    FROM task_records t
    JOIN project_delete_jobs j ON j.project_id = t.project_id
    WHERE t.id = NEW.task_id
      AND j.state IN ('active', 'finalizing')
)
AND NOT EXISTS (
    SELECT 1
    FROM task_records t
    JOIN project_delete_finalizer_bypass b ON b.project_id = t.project_id
    WHERE t.id = NEW.task_id
)
BEGIN
    SELECT RAISE(ABORT, 'project_delete_in_progress');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER task_node_placements_project_delete_update
BEFORE UPDATE ON task_node_placements
FOR EACH ROW
WHEN (
    EXISTS (
        SELECT 1
        FROM task_records t
        JOIN project_delete_jobs j ON j.project_id = t.project_id
        WHERE t.id = OLD.task_id
          AND j.state IN ('active', 'finalizing')
    )
    AND NOT EXISTS (
        SELECT 1
        FROM task_records t
        JOIN project_delete_finalizer_bypass b ON b.project_id = t.project_id
        WHERE t.id = OLD.task_id
    )
)
OR (
    EXISTS (
        SELECT 1
        FROM task_records t
        JOIN project_delete_jobs j ON j.project_id = t.project_id
        WHERE t.id = NEW.task_id
          AND j.state IN ('active', 'finalizing')
    )
    AND NOT EXISTS (
        SELECT 1
        FROM task_records t
        JOIN project_delete_finalizer_bypass b ON b.project_id = t.project_id
        WHERE t.id = NEW.task_id
    )
)
BEGIN
    SELECT RAISE(ABORT, 'project_delete_in_progress');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER task_node_placements_project_delete_delete
BEFORE DELETE ON task_node_placements
FOR EACH ROW
WHEN EXISTS (
    SELECT 1
    FROM task_records t
    JOIN project_delete_jobs j ON j.project_id = t.project_id
    WHERE t.id = OLD.task_id
      AND j.state IN ('active', 'finalizing')
)
AND NOT EXISTS (
    SELECT 1
    FROM task_records t
    JOIN project_delete_finalizer_bypass b ON b.project_id = t.project_id
    WHERE t.id = OLD.task_id
)
BEGIN
    SELECT RAISE(ABORT, 'project_delete_in_progress');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER task_runs_project_delete_insert
BEFORE INSERT ON task_runs
FOR EACH ROW
WHEN EXISTS (
    SELECT 1
    FROM task_node_placements p
    JOIN task_records t ON t.id = p.task_id
    JOIN project_delete_jobs j ON j.project_id = t.project_id
    WHERE p.id = NEW.placement_id
      AND j.state IN ('active', 'finalizing')
)
AND NOT EXISTS (
    SELECT 1
    FROM task_node_placements p
    JOIN task_records t ON t.id = p.task_id
    JOIN project_delete_finalizer_bypass b ON b.project_id = t.project_id
    WHERE p.id = NEW.placement_id
)
BEGIN
    SELECT RAISE(ABORT, 'project_delete_in_progress');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER task_runs_project_delete_update
BEFORE UPDATE ON task_runs
FOR EACH ROW
WHEN (
    EXISTS (
        SELECT 1
        FROM task_node_placements p
        JOIN task_records t ON t.id = p.task_id
        JOIN project_delete_jobs j ON j.project_id = t.project_id
        WHERE p.id = OLD.placement_id
          AND j.state IN ('active', 'finalizing')
    )
    AND NOT EXISTS (
        SELECT 1
        FROM task_node_placements p
        JOIN task_records t ON t.id = p.task_id
        JOIN project_delete_finalizer_bypass b ON b.project_id = t.project_id
        WHERE p.id = OLD.placement_id
    )
)
OR (
    EXISTS (
        SELECT 1
        FROM task_node_placements p
        JOIN task_records t ON t.id = p.task_id
        JOIN project_delete_jobs j ON j.project_id = t.project_id
        WHERE p.id = NEW.placement_id
          AND j.state IN ('active', 'finalizing')
    )
    AND NOT EXISTS (
        SELECT 1
        FROM task_node_placements p
        JOIN task_records t ON t.id = p.task_id
        JOIN project_delete_finalizer_bypass b ON b.project_id = t.project_id
        WHERE p.id = NEW.placement_id
    )
)
BEGIN
    SELECT RAISE(ABORT, 'project_delete_in_progress');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER task_runs_project_delete_delete
BEFORE DELETE ON task_runs
FOR EACH ROW
WHEN EXISTS (
    SELECT 1
    FROM task_node_placements p
    JOIN task_records t ON t.id = p.task_id
    JOIN project_delete_jobs j ON j.project_id = t.project_id
    WHERE p.id = OLD.placement_id
      AND j.state IN ('active', 'finalizing')
)
AND NOT EXISTS (
    SELECT 1
    FROM task_node_placements p
    JOIN task_records t ON t.id = p.task_id
    JOIN project_delete_finalizer_bypass b ON b.project_id = t.project_id
    WHERE p.id = OLD.placement_id
)
BEGIN
    SELECT RAISE(ABORT, 'project_delete_in_progress');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER task_runs_session_project_insert
BEFORE INSERT ON task_runs
FOR EACH ROW
WHEN NEW.session_id IS NOT NULL
 AND trim(NEW.session_id) != ''
 AND NOT EXISTS (
    SELECT 1
    FROM task_node_placements p
    JOIN task_records t ON t.id = p.task_id
    JOIN sessions s ON s.id = NEW.session_id
    WHERE p.id = NEW.placement_id
      AND s.project_id = t.project_id
)
BEGIN
    SELECT RAISE(ABORT, 'task run session must belong to the task project');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER task_runs_session_project_update
BEFORE UPDATE OF placement_id, session_id ON task_runs
FOR EACH ROW
WHEN NEW.session_id IS NOT NULL
 AND trim(NEW.session_id) != ''
 AND NOT EXISTS (
    SELECT 1
    FROM task_node_placements p
    JOIN task_records t ON t.id = p.task_id
    JOIN sessions s ON s.id = NEW.session_id
    WHERE p.id = NEW.placement_id
      AND s.project_id = t.project_id
)
BEGIN
    SELECT RAISE(ABORT, 'task run session must belong to the task project');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER task_transitions_project_delete_insert
BEFORE INSERT ON task_transitions
FOR EACH ROW
WHEN EXISTS (
    SELECT 1
    FROM task_records t
    JOIN project_delete_jobs j ON j.project_id = t.project_id
    WHERE t.id = NEW.task_id
      AND j.state IN ('active', 'finalizing')
)
AND NOT EXISTS (
    SELECT 1
    FROM task_records t
    JOIN project_delete_finalizer_bypass b ON b.project_id = t.project_id
    WHERE t.id = NEW.task_id
)
BEGIN
    SELECT RAISE(ABORT, 'project_delete_in_progress');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER task_transitions_project_delete_update
BEFORE UPDATE ON task_transitions
FOR EACH ROW
WHEN (
    EXISTS (
        SELECT 1
        FROM task_records t
        JOIN project_delete_jobs j ON j.project_id = t.project_id
        WHERE t.id = OLD.task_id
          AND j.state IN ('active', 'finalizing')
    )
    AND NOT EXISTS (
        SELECT 1
        FROM task_records t
        JOIN project_delete_finalizer_bypass b ON b.project_id = t.project_id
        WHERE t.id = OLD.task_id
    )
)
OR (
    EXISTS (
        SELECT 1
        FROM task_records t
        JOIN project_delete_jobs j ON j.project_id = t.project_id
        WHERE t.id = NEW.task_id
          AND j.state IN ('active', 'finalizing')
    )
    AND NOT EXISTS (
        SELECT 1
        FROM task_records t
        JOIN project_delete_finalizer_bypass b ON b.project_id = t.project_id
        WHERE t.id = NEW.task_id
    )
)
BEGIN
    SELECT RAISE(ABORT, 'project_delete_in_progress');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER task_transitions_project_delete_delete
BEFORE DELETE ON task_transitions
FOR EACH ROW
WHEN EXISTS (
    SELECT 1
    FROM task_records t
    JOIN project_delete_jobs j ON j.project_id = t.project_id
    WHERE t.id = OLD.task_id
      AND j.state IN ('active', 'finalizing')
)
AND NOT EXISTS (
    SELECT 1
    FROM task_records t
    JOIN project_delete_finalizer_bypass b ON b.project_id = t.project_id
    WHERE t.id = OLD.task_id
)
BEGIN
    SELECT RAISE(ABORT, 'project_delete_in_progress');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER task_transition_edges_project_delete_insert
BEFORE INSERT ON task_transition_edges
FOR EACH ROW
WHEN EXISTS (
    SELECT 1
    FROM task_transitions tt
    JOIN task_records t ON t.id = tt.task_id
    JOIN project_delete_jobs j ON j.project_id = t.project_id
    WHERE tt.id = NEW.task_transition_id
      AND j.state IN ('active', 'finalizing')
)
AND NOT EXISTS (
    SELECT 1
    FROM task_transitions tt
    JOIN task_records t ON t.id = tt.task_id
    JOIN project_delete_finalizer_bypass b ON b.project_id = t.project_id
    WHERE tt.id = NEW.task_transition_id
)
BEGIN
    SELECT RAISE(ABORT, 'project_delete_in_progress');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER task_transition_edges_project_delete_update
BEFORE UPDATE ON task_transition_edges
FOR EACH ROW
WHEN (
    EXISTS (
        SELECT 1
        FROM task_transitions tt
        JOIN task_records t ON t.id = tt.task_id
        JOIN project_delete_jobs j ON j.project_id = t.project_id
        WHERE tt.id = OLD.task_transition_id
          AND j.state IN ('active', 'finalizing')
    )
    AND NOT EXISTS (
        SELECT 1
        FROM task_transitions tt
        JOIN task_records t ON t.id = tt.task_id
        JOIN project_delete_finalizer_bypass b ON b.project_id = t.project_id
        WHERE tt.id = OLD.task_transition_id
    )
)
OR (
    EXISTS (
        SELECT 1
        FROM task_transitions tt
        JOIN task_records t ON t.id = tt.task_id
        JOIN project_delete_jobs j ON j.project_id = t.project_id
        WHERE tt.id = NEW.task_transition_id
          AND j.state IN ('active', 'finalizing')
    )
    AND NOT EXISTS (
        SELECT 1
        FROM task_transitions tt
        JOIN task_records t ON t.id = tt.task_id
        JOIN project_delete_finalizer_bypass b ON b.project_id = t.project_id
        WHERE tt.id = NEW.task_transition_id
    )
)
BEGIN
    SELECT RAISE(ABORT, 'project_delete_in_progress');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER task_transition_edges_project_delete_delete
BEFORE DELETE ON task_transition_edges
FOR EACH ROW
WHEN EXISTS (
    SELECT 1
    FROM task_transitions tt
    JOIN task_records t ON t.id = tt.task_id
    JOIN project_delete_jobs j ON j.project_id = t.project_id
    WHERE tt.id = OLD.task_transition_id
      AND j.state IN ('active', 'finalizing')
)
AND NOT EXISTS (
    SELECT 1
    FROM task_transitions tt
    JOIN task_records t ON t.id = tt.task_id
    JOIN project_delete_finalizer_bypass b ON b.project_id = t.project_id
    WHERE tt.id = OLD.task_transition_id
)
BEGIN
    SELECT RAISE(ABORT, 'project_delete_in_progress');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER task_comments_project_delete_insert
BEFORE INSERT ON task_comments
FOR EACH ROW
WHEN EXISTS (
    SELECT 1
    FROM task_records t
    JOIN project_delete_jobs j ON j.project_id = t.project_id
    WHERE t.id = NEW.task_id
      AND j.state IN ('active', 'finalizing')
)
AND NOT EXISTS (
    SELECT 1
    FROM task_records t
    JOIN project_delete_finalizer_bypass b ON b.project_id = t.project_id
    WHERE t.id = NEW.task_id
)
BEGIN
    SELECT RAISE(ABORT, 'project_delete_in_progress');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER task_comments_project_delete_update
BEFORE UPDATE ON task_comments
FOR EACH ROW
WHEN (
    EXISTS (
        SELECT 1
        FROM task_records t
        JOIN project_delete_jobs j ON j.project_id = t.project_id
        WHERE t.id = OLD.task_id
          AND j.state IN ('active', 'finalizing')
    )
    AND NOT EXISTS (
        SELECT 1
        FROM task_records t
        JOIN project_delete_finalizer_bypass b ON b.project_id = t.project_id
        WHERE t.id = OLD.task_id
    )
)
OR (
    EXISTS (
        SELECT 1
        FROM task_records t
        JOIN project_delete_jobs j ON j.project_id = t.project_id
        WHERE t.id = NEW.task_id
          AND j.state IN ('active', 'finalizing')
    )
    AND NOT EXISTS (
        SELECT 1
        FROM task_records t
        JOIN project_delete_finalizer_bypass b ON b.project_id = t.project_id
        WHERE t.id = NEW.task_id
    )
)
BEGIN
    SELECT RAISE(ABORT, 'project_delete_in_progress');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER task_comments_project_delete_delete
BEFORE DELETE ON task_comments
FOR EACH ROW
WHEN EXISTS (
    SELECT 1
    FROM task_records t
    JOIN project_delete_jobs j ON j.project_id = t.project_id
    WHERE t.id = OLD.task_id
      AND j.state IN ('active', 'finalizing')
)
AND NOT EXISTS (
    SELECT 1
    FROM task_records t
    JOIN project_delete_finalizer_bypass b ON b.project_id = t.project_id
    WHERE t.id = OLD.task_id
)
BEGIN
    SELECT RAISE(ABORT, 'project_delete_in_progress');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER runtime_leases_project_delete_insert
BEFORE INSERT ON runtime_leases
FOR EACH ROW
WHEN EXISTS (
    SELECT 1
    FROM sessions s
    JOIN project_delete_jobs j ON j.project_id = s.project_id
    WHERE s.id = NEW.session_id
      AND j.state IN ('active', 'finalizing')
)
AND NOT EXISTS (
    SELECT 1
    FROM sessions s
    JOIN project_delete_finalizer_bypass b ON b.project_id = s.project_id
    WHERE s.id = NEW.session_id
)
BEGIN
    SELECT RAISE(ABORT, 'project_delete_in_progress');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER runtime_leases_project_delete_update
BEFORE UPDATE ON runtime_leases
FOR EACH ROW
WHEN (
    EXISTS (
        SELECT 1
        FROM sessions s
        JOIN project_delete_jobs j ON j.project_id = s.project_id
        WHERE s.id = OLD.session_id
          AND j.state IN ('active', 'finalizing')
    )
    AND NOT EXISTS (
        SELECT 1
        FROM sessions s
        JOIN project_delete_finalizer_bypass b ON b.project_id = s.project_id
        WHERE s.id = OLD.session_id
    )
)
OR (
    EXISTS (
        SELECT 1
        FROM sessions s
        JOIN project_delete_jobs j ON j.project_id = s.project_id
        WHERE s.id = NEW.session_id
          AND j.state IN ('active', 'finalizing')
    )
    AND NOT EXISTS (
        SELECT 1
        FROM sessions s
        JOIN project_delete_finalizer_bypass b ON b.project_id = s.project_id
        WHERE s.id = NEW.session_id
    )
)
BEGIN
    SELECT RAISE(ABORT, 'project_delete_in_progress');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER runtime_leases_project_delete_delete
BEFORE DELETE ON runtime_leases
FOR EACH ROW
WHEN EXISTS (
    SELECT 1
    FROM sessions s
    JOIN project_delete_jobs j ON j.project_id = s.project_id
    WHERE s.id = OLD.session_id
      AND j.state IN ('active', 'finalizing')
)
AND NOT EXISTS (
    SELECT 1
    FROM sessions s
    JOIN project_delete_finalizer_bypass b ON b.project_id = s.project_id
    WHERE s.id = OLD.session_id
)
BEGIN
    SELECT RAISE(ABORT, 'project_delete_in_progress');
END;
-- +goose StatementEnd

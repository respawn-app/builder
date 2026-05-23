-- +goose Up
-- +goose NO TRANSACTION

PRAGMA foreign_keys = OFF;

DROP TRIGGER IF EXISTS task_runs_runtime_insert;
DROP TRIGGER IF EXISTS task_runs_runtime_update;
DROP TRIGGER IF EXISTS task_transitions_runtime_insert;
DROP TRIGGER IF EXISTS task_transitions_runtime_update;

DROP VIEW IF EXISTS task_run_records;

CREATE TEMP TABLE migration_task_run_check_zero(value INTEGER NOT NULL CHECK (value = 0));

INSERT INTO migration_task_run_check_zero(value)
SELECT 1
WHERE EXISTS (
    SELECT 1
    FROM task_runs r
    LEFT JOIN task_node_placements p ON p.id = r.placement_id
    WHERE p.id IS NULL
       OR r.task_id != p.task_id
       OR r.node_id != p.node_id
);

CREATE TABLE task_runs_new (
    id TEXT PRIMARY KEY,
    placement_id TEXT NOT NULL REFERENCES task_node_placements(id) ON DELETE CASCADE,
    session_id TEXT REFERENCES sessions(id) ON DELETE SET NULL,
    run_generation INTEGER NOT NULL DEFAULT 0 CHECK (run_generation >= 0),
    workflow_revision_seen INTEGER NOT NULL CHECK (workflow_revision_seen >= 1),
    automation_requested_at_unix_ms INTEGER NOT NULL DEFAULT 0 CHECK (automation_requested_at_unix_ms >= 0),
    created_at_unix_ms INTEGER NOT NULL CHECK (created_at_unix_ms >= 0),
    updated_at_unix_ms INTEGER NOT NULL CHECK (updated_at_unix_ms >= 0),
    started_at_unix_ms INTEGER NOT NULL DEFAULT 0 CHECK (started_at_unix_ms >= 0),
    completed_at_unix_ms INTEGER NOT NULL DEFAULT 0 CHECK (completed_at_unix_ms >= 0),
    interrupted_at_unix_ms INTEGER NOT NULL DEFAULT 0 CHECK (interrupted_at_unix_ms >= 0),
    interruption_reason TEXT NOT NULL DEFAULT '',
    interruption_detail_json TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(interruption_detail_json)),
    waiting_ask_id TEXT NOT NULL DEFAULT '',
    final_answer_violation_count INTEGER NOT NULL DEFAULT 0 CHECK (final_answer_violation_count >= 0),
    invalid_completion_count INTEGER NOT NULL DEFAULT 0 CHECK (invalid_completion_count >= 0),
    run_start_snapshot_json TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(run_start_snapshot_json)),
    metadata_json TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(metadata_json))
);

INSERT INTO task_runs_new (
    id,
    placement_id,
    session_id,
    run_generation,
    workflow_revision_seen,
    automation_requested_at_unix_ms,
    created_at_unix_ms,
    updated_at_unix_ms,
    started_at_unix_ms,
    completed_at_unix_ms,
    interrupted_at_unix_ms,
    interruption_reason,
    interruption_detail_json,
    waiting_ask_id,
    final_answer_violation_count,
    invalid_completion_count,
    run_start_snapshot_json,
    metadata_json
)
SELECT
    id,
    placement_id,
    session_id,
    run_generation,
    workflow_revision_seen,
    automation_requested_at_unix_ms,
    created_at_unix_ms,
    updated_at_unix_ms,
    started_at_unix_ms,
    completed_at_unix_ms,
    interrupted_at_unix_ms,
    interruption_reason,
    interruption_detail_json,
    waiting_ask_id,
    final_answer_violation_count,
    invalid_completion_count,
    run_start_snapshot_json,
    metadata_json
FROM task_runs;

DROP TABLE task_runs;
ALTER TABLE task_runs_new RENAME TO task_runs;

CREATE INDEX task_runs_placement_idx
    ON task_runs(placement_id);

CREATE INDEX task_runs_session_idx
    ON task_runs(session_id);

CREATE INDEX task_runs_runnable_idx
    ON task_runs(automation_requested_at_unix_ms, id)
    WHERE automation_requested_at_unix_ms > 0 AND completed_at_unix_ms = 0 AND interrupted_at_unix_ms = 0;

CREATE INDEX task_runs_outcome_idx
    ON task_runs(started_at_unix_ms, completed_at_unix_ms, interrupted_at_unix_ms);

CREATE INDEX task_runs_placement_created_idx
    ON task_runs(placement_id, created_at_unix_ms DESC);

CREATE VIEW task_run_records AS
SELECT
    r.id,
    p.task_id,
    r.placement_id,
    p.node_id,
    r.session_id,
    r.run_generation,
    r.workflow_revision_seen,
    r.automation_requested_at_unix_ms,
    r.created_at_unix_ms,
    r.updated_at_unix_ms,
    r.started_at_unix_ms,
    r.completed_at_unix_ms,
    r.interrupted_at_unix_ms,
    r.interruption_reason,
    r.interruption_detail_json,
    r.waiting_ask_id,
    r.final_answer_violation_count,
    r.invalid_completion_count,
    r.run_start_snapshot_json,
    r.metadata_json
FROM task_runs r
JOIN task_node_placements p ON p.id = r.placement_id;

-- +goose StatementBegin
CREATE TRIGGER task_transitions_runtime_insert
BEFORE INSERT ON task_transitions
FOR EACH ROW
WHEN (
    NEW.source_run_id IS NOT NULL
    AND trim(NEW.source_run_id) != ''
    AND NOT EXISTS (
        SELECT 1
        FROM task_run_records r
        WHERE r.id = NEW.source_run_id
          AND r.task_id = NEW.task_id
    )
)
OR (
    NEW.source_placement_id IS NOT NULL
    AND trim(NEW.source_placement_id) != ''
    AND NOT EXISTS (
        SELECT 1
        FROM task_node_placements p
        WHERE p.id = NEW.source_placement_id
          AND p.task_id = NEW.task_id
          AND (
              NEW.source_node_id IS NULL
              OR trim(NEW.source_node_id) = ''
              OR p.node_id = NEW.source_node_id
          )
    )
)
OR (
    NEW.source_node_id IS NOT NULL
    AND trim(NEW.source_node_id) != ''
    AND NOT EXISTS (
        SELECT 1
        FROM task_records t
        JOIN workflow_nodes n ON n.id = NEW.source_node_id
        WHERE t.id = NEW.task_id
          AND n.workflow_id = t.workflow_id
    )
)
OR (
    NEW.transition_group_id IS NOT NULL
    AND trim(NEW.transition_group_id) != ''
    AND NOT EXISTS (
        SELECT 1
        FROM task_records t
        JOIN workflow_transition_groups tg ON tg.id = NEW.transition_group_id
        JOIN workflow_nodes source_node ON source_node.id = tg.source_node_id
        WHERE t.id = NEW.task_id
          AND source_node.workflow_id = t.workflow_id
    )
)
BEGIN
    SELECT RAISE(ABORT, 'task transition references must stay within one task workflow');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER task_transitions_runtime_update
BEFORE UPDATE OF task_id, source_run_id, source_placement_id, source_node_id, transition_group_id, transition_id ON task_transitions
FOR EACH ROW
WHEN (
    NEW.source_run_id IS NOT NULL
    AND trim(NEW.source_run_id) != ''
    AND NOT EXISTS (
        SELECT 1
        FROM task_run_records r
        WHERE r.id = NEW.source_run_id
          AND r.task_id = NEW.task_id
    )
)
OR (
    NEW.source_placement_id IS NOT NULL
    AND trim(NEW.source_placement_id) != ''
    AND NOT EXISTS (
        SELECT 1
        FROM task_node_placements p
        WHERE p.id = NEW.source_placement_id
          AND p.task_id = NEW.task_id
          AND (
              NEW.source_node_id IS NULL
              OR trim(NEW.source_node_id) = ''
              OR p.node_id = NEW.source_node_id
          )
    )
)
OR (
    NEW.source_node_id IS NOT NULL
    AND trim(NEW.source_node_id) != ''
    AND NOT EXISTS (
        SELECT 1
        FROM task_records t
        JOIN workflow_nodes n ON n.id = NEW.source_node_id
        WHERE t.id = NEW.task_id
          AND n.workflow_id = t.workflow_id
    )
)
OR (
    NEW.transition_group_id IS NOT NULL
    AND trim(NEW.transition_group_id) != ''
    AND NOT EXISTS (
        SELECT 1
        FROM task_records t
        JOIN workflow_transition_groups tg ON tg.id = NEW.transition_group_id
        JOIN workflow_nodes source_node ON source_node.id = tg.source_node_id
        WHERE t.id = NEW.task_id
          AND source_node.workflow_id = t.workflow_id
    )
)
BEGIN
    SELECT RAISE(ABORT, 'task transition references must stay within one task workflow');
END;
-- +goose StatementEnd

CREATE TEMP TABLE migration_task_run_fk_check_zero(value INTEGER NOT NULL CHECK (value = 0));

INSERT INTO migration_task_run_fk_check_zero(value)
SELECT 1
WHERE EXISTS (
    SELECT 1
    FROM pragma_foreign_key_check
);

DROP TABLE migration_task_run_fk_check_zero;
DROP TABLE migration_task_run_check_zero;

PRAGMA foreign_keys = ON;

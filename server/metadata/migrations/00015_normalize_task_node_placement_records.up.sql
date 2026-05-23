-- +goose Up
-- +goose NO TRANSACTION

PRAGMA foreign_keys = OFF;

DROP TRIGGER IF EXISTS task_node_placements_runtime_insert;
DROP TRIGGER IF EXISTS task_node_placements_runtime_update;
DROP TRIGGER IF EXISTS task_transitions_runtime_insert;
DROP TRIGGER IF EXISTS task_transitions_runtime_update;
DROP TRIGGER IF EXISTS task_transition_edges_runtime_insert;
DROP TRIGGER IF EXISTS task_transition_edges_runtime_update;

DROP VIEW IF EXISTS task_run_records;
DROP VIEW IF EXISTS task_node_placement_records;

CREATE TEMP TABLE migration_placement_check_zero(value INTEGER NOT NULL CHECK (value = 0));

INSERT INTO migration_placement_check_zero(value)
SELECT 1
WHERE EXISTS (
    SELECT 1
    FROM (
        SELECT target_placement_id
        FROM task_transition_edges
        WHERE target_placement_id IS NOT NULL
          AND trim(target_placement_id) != ''
        GROUP BY target_placement_id
        HAVING COUNT(*) > 1
    )
);

INSERT INTO migration_placement_check_zero(value)
SELECT 1
WHERE EXISTS (
    SELECT 1
    FROM task_node_placements p
    WHERE p.created_by_transition_id IS NOT NULL
      AND trim(p.created_by_transition_id) != ''
      AND NOT EXISTS (
          SELECT 1
          FROM task_transition_edges te
          WHERE te.target_placement_id = p.id
            AND te.task_transition_id = p.created_by_transition_id
      )
);

CREATE TABLE task_node_placements_new (
    id TEXT PRIMARY KEY,
    task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    node_id TEXT NOT NULL REFERENCES workflow_nodes(id) ON DELETE RESTRICT,
    state TEXT NOT NULL CHECK (state IN ('active', 'waiting_approval', 'completed', 'superseded')),
    parallel_batch_transition_id TEXT,
    parallel_branch_edge_id TEXT REFERENCES workflow_edges(id) ON DELETE SET NULL,
    created_at_unix_ms INTEGER NOT NULL CHECK (created_at_unix_ms >= 0),
    updated_at_unix_ms INTEGER NOT NULL CHECK (updated_at_unix_ms >= 0),
    FOREIGN KEY (parallel_batch_transition_id) REFERENCES task_transitions(id) ON DELETE SET NULL
);

INSERT INTO task_node_placements_new (
    id,
    task_id,
    node_id,
    state,
    parallel_batch_transition_id,
    parallel_branch_edge_id,
    created_at_unix_ms,
    updated_at_unix_ms
)
SELECT
    id,
    task_id,
    node_id,
    state,
    parallel_batch_transition_id,
    parallel_branch_edge_id,
    created_at_unix_ms,
    updated_at_unix_ms
FROM task_node_placements;

DROP TABLE task_node_placements;
ALTER TABLE task_node_placements_new RENAME TO task_node_placements;

CREATE INDEX task_node_placements_task_state_idx
    ON task_node_placements(task_id, state);

CREATE INDEX task_node_placements_node_state_idx
    ON task_node_placements(node_id, state);

CREATE INDEX task_node_placements_parallel_batch_idx
    ON task_node_placements(parallel_batch_transition_id, parallel_branch_edge_id, state);

CREATE UNIQUE INDEX task_transition_edges_target_placement_unique_idx
    ON task_transition_edges(target_placement_id)
    WHERE target_placement_id IS NOT NULL
      AND trim(target_placement_id) != '';

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

-- +goose StatementBegin
CREATE TRIGGER task_transition_edges_runtime_insert
BEFORE INSERT ON task_transition_edges
FOR EACH ROW
WHEN NOT EXISTS (
    SELECT 1
    FROM task_transitions tt
    WHERE tt.id = NEW.task_transition_id
)
OR (
    NEW.target_placement_id IS NOT NULL
    AND trim(NEW.target_placement_id) != ''
    AND NOT EXISTS (
        SELECT 1
        FROM task_transitions tt
        JOIN task_node_placements p ON p.id = NEW.target_placement_id
        WHERE tt.id = NEW.task_transition_id
          AND p.task_id = tt.task_id
          AND (
              NEW.target_node_id IS NULL
              OR trim(NEW.target_node_id) = ''
              OR p.node_id = NEW.target_node_id
          )
    )
)
OR (
    NEW.target_node_id IS NOT NULL
    AND trim(NEW.target_node_id) != ''
    AND NOT EXISTS (
        SELECT 1
        FROM task_transitions tt
        JOIN task_records t ON t.id = tt.task_id
        JOIN workflow_nodes n ON n.id = NEW.target_node_id
        WHERE tt.id = NEW.task_transition_id
          AND n.workflow_id = t.workflow_id
    )
)
OR (
    NEW.workflow_edge_id IS NOT NULL
    AND trim(NEW.workflow_edge_id) != ''
    AND NOT EXISTS (
        SELECT 1
        FROM task_transitions tt
        JOIN task_records t ON t.id = tt.task_id
        JOIN workflow_edges e ON e.id = NEW.workflow_edge_id
        WHERE tt.id = NEW.task_transition_id
          AND e.workflow_id = t.workflow_id
    )
)
BEGIN
    SELECT RAISE(ABORT, 'task transition edge references must stay within one task workflow');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER task_transition_edges_runtime_update
BEFORE UPDATE OF task_transition_id, workflow_edge_id, target_node_id, target_placement_id ON task_transition_edges
FOR EACH ROW
WHEN NOT EXISTS (
    SELECT 1
    FROM task_transitions tt
    WHERE tt.id = NEW.task_transition_id
)
OR (
    NEW.target_placement_id IS NOT NULL
    AND trim(NEW.target_placement_id) != ''
    AND NOT EXISTS (
        SELECT 1
        FROM task_transitions tt
        JOIN task_node_placements p ON p.id = NEW.target_placement_id
        WHERE tt.id = NEW.task_transition_id
          AND p.task_id = tt.task_id
          AND (
              NEW.target_node_id IS NULL
              OR trim(NEW.target_node_id) = ''
              OR p.node_id = NEW.target_node_id
          )
    )
)
OR (
    NEW.target_node_id IS NOT NULL
    AND trim(NEW.target_node_id) != ''
    AND NOT EXISTS (
        SELECT 1
        FROM task_transitions tt
        JOIN task_records t ON t.id = tt.task_id
        JOIN workflow_nodes n ON n.id = NEW.target_node_id
        WHERE tt.id = NEW.task_transition_id
          AND n.workflow_id = t.workflow_id
    )
)
OR (
    NEW.workflow_edge_id IS NOT NULL
    AND trim(NEW.workflow_edge_id) != ''
    AND NOT EXISTS (
        SELECT 1
        FROM task_transitions tt
        JOIN task_records t ON t.id = tt.task_id
        JOIN workflow_edges e ON e.id = NEW.workflow_edge_id
        WHERE tt.id = NEW.task_transition_id
          AND e.workflow_id = t.workflow_id
    )
)
BEGIN
    SELECT RAISE(ABORT, 'task transition edge references must stay within one task workflow');
END;
-- +goose StatementEnd

CREATE VIEW task_node_placement_records AS
SELECT
    p.id,
    p.task_id,
    p.node_id,
    p.state,
    CAST(COALESCE((
        SELECT te.task_transition_id
        FROM task_transition_edges te
        WHERE te.target_placement_id = p.id
        ORDER BY te.rowid ASC
        LIMIT 1
    ), '') AS TEXT) AS created_by_transition_id,
    p.parallel_batch_transition_id,
    p.parallel_branch_edge_id,
    p.created_at_unix_ms,
    p.updated_at_unix_ms
FROM task_node_placements p;

-- +goose StatementBegin
CREATE TRIGGER task_node_placements_runtime_insert
BEFORE INSERT ON task_node_placements
FOR EACH ROW
WHEN NOT EXISTS (
    SELECT 1
    FROM task_records t
    JOIN workflow_nodes n ON n.id = NEW.node_id
    WHERE t.id = NEW.task_id
      AND n.workflow_id = t.workflow_id
)
OR (
    NEW.parallel_batch_transition_id IS NOT NULL
    AND trim(NEW.parallel_batch_transition_id) != ''
    AND NOT EXISTS (
        SELECT 1
        FROM task_transitions tt
        WHERE tt.id = NEW.parallel_batch_transition_id
          AND tt.task_id = NEW.task_id
    )
)
OR (
    NEW.parallel_branch_edge_id IS NOT NULL
    AND trim(NEW.parallel_branch_edge_id) != ''
    AND NOT EXISTS (
        SELECT 1
        FROM task_records t
        JOIN workflow_edges e ON e.id = NEW.parallel_branch_edge_id
        WHERE t.id = NEW.task_id
          AND e.workflow_id = t.workflow_id
    )
)
BEGIN
    SELECT RAISE(ABORT, 'task node placement references must stay within one task workflow');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER task_node_placements_runtime_update
BEFORE UPDATE OF task_id, node_id, parallel_batch_transition_id, parallel_branch_edge_id ON task_node_placements
FOR EACH ROW
WHEN NOT EXISTS (
    SELECT 1
    FROM task_records t
    JOIN workflow_nodes n ON n.id = NEW.node_id
    WHERE t.id = NEW.task_id
      AND n.workflow_id = t.workflow_id
)
OR (
    NEW.parallel_batch_transition_id IS NOT NULL
    AND trim(NEW.parallel_batch_transition_id) != ''
    AND NOT EXISTS (
        SELECT 1
        FROM task_transitions tt
        WHERE tt.id = NEW.parallel_batch_transition_id
          AND tt.task_id = NEW.task_id
    )
)
OR (
    NEW.parallel_branch_edge_id IS NOT NULL
    AND trim(NEW.parallel_branch_edge_id) != ''
    AND NOT EXISTS (
        SELECT 1
        FROM task_records t
        JOIN workflow_edges e ON e.id = NEW.parallel_branch_edge_id
        WHERE t.id = NEW.task_id
          AND e.workflow_id = t.workflow_id
    )
)
BEGIN
    SELECT RAISE(ABORT, 'task node placement references must stay within one task workflow');
END;
-- +goose StatementEnd

CREATE TEMP TABLE migration_placement_fk_check_zero(value INTEGER NOT NULL CHECK (value = 0));

INSERT INTO migration_placement_fk_check_zero(value)
SELECT 1
WHERE EXISTS (
    SELECT 1
    FROM pragma_foreign_key_check
);

DROP TABLE migration_placement_fk_check_zero;
DROP TABLE migration_placement_check_zero;

PRAGMA foreign_keys = ON;

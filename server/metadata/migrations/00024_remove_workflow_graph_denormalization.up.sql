-- +goose Up
-- +goose NO TRANSACTION

PRAGMA foreign_keys = OFF;

BEGIN IMMEDIATE;

CREATE TEMP TABLE migration_workflow_graph_denormalization_check_zero(value INTEGER NOT NULL CHECK (value = 0));

INSERT INTO migration_workflow_graph_denormalization_check_zero(value)
SELECT 1
WHERE EXISTS (
    SELECT 1
    FROM workflow_transition_groups tg
    LEFT JOIN workflow_nodes source ON source.id = tg.source_node_id
    WHERE source.id IS NULL
       OR source.workflow_id != tg.workflow_id
);

INSERT INTO migration_workflow_graph_denormalization_check_zero(value)
SELECT 1
WHERE EXISTS (
    SELECT 1
    FROM workflow_edges e
    LEFT JOIN workflow_transition_groups tg ON tg.id = e.transition_group_id
    LEFT JOIN workflow_nodes source ON source.id = tg.source_node_id
    WHERE tg.id IS NULL
       OR source.id IS NULL
       OR source.workflow_id != e.workflow_id
);

INSERT INTO migration_workflow_graph_denormalization_check_zero(value)
SELECT 1
WHERE EXISTS (
    SELECT 1
    FROM workflow_edges e
    LEFT JOIN workflow_transition_groups tg ON tg.id = e.transition_group_id
    LEFT JOIN workflow_nodes source ON source.id = tg.source_node_id
    LEFT JOIN workflow_nodes target ON target.id = e.target_node_id
    WHERE tg.id IS NULL
       OR source.id IS NULL
       OR target.id IS NULL
       OR target.workflow_id != source.workflow_id
);

DROP TABLE migration_workflow_graph_denormalization_check_zero;

DROP TRIGGER IF EXISTS task_transitions_runtime_insert;
DROP TRIGGER IF EXISTS task_transitions_runtime_update;
DROP TRIGGER IF EXISTS task_transition_edges_runtime_insert;
DROP TRIGGER IF EXISTS task_transition_edges_runtime_update;
DROP TRIGGER IF EXISTS task_node_placements_runtime_insert;
DROP TRIGGER IF EXISTS task_node_placements_runtime_update;
DROP TRIGGER IF EXISTS workflow_edges_target_workflow_insert;
DROP TRIGGER IF EXISTS workflow_edges_target_workflow_update;

DROP VIEW IF EXISTS task_transition_records;

CREATE TABLE workflow_transition_groups_new (
    id TEXT PRIMARY KEY,
    source_node_id TEXT NOT NULL REFERENCES workflow_nodes(id) ON DELETE CASCADE,
    transition_id TEXT NOT NULL CHECK (length(transition_id) BETWEEN 1 AND 64),
    display_name TEXT NOT NULL DEFAULT '' CHECK (length(display_name) <= 120),
    sort_order INTEGER NOT NULL DEFAULT 0 CHECK (sort_order >= 0),
    UNIQUE (source_node_id, transition_id)
);

INSERT INTO workflow_transition_groups_new (
    id,
    source_node_id,
    transition_id,
    display_name,
    sort_order
)
SELECT
    id,
    source_node_id,
    transition_id,
    display_name,
    sort_order
FROM workflow_transition_groups
ORDER BY rowid ASC;

DROP TABLE workflow_transition_groups;
ALTER TABLE workflow_transition_groups_new RENAME TO workflow_transition_groups;

CREATE TABLE workflow_edges_new (
    id TEXT PRIMARY KEY,
    transition_group_id TEXT NOT NULL REFERENCES workflow_transition_groups(id) ON DELETE CASCADE,
    edge_key TEXT NOT NULL CHECK (length(edge_key) BETWEEN 1 AND 64),
    target_node_id TEXT NOT NULL REFERENCES workflow_nodes(id) ON DELETE CASCADE,
    requires_approval INTEGER NOT NULL DEFAULT 0 CHECK (requires_approval IN (0, 1)),
    context_mode TEXT NOT NULL CHECK (context_mode IN ('new_session', 'continue_session', 'compact_and_continue_session')),
    input_bindings_json TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(input_bindings_json)),
    output_requirements_json TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(output_requirements_json)),
    sort_order INTEGER NOT NULL DEFAULT 0 CHECK (sort_order >= 0),
    UNIQUE (transition_group_id, edge_key)
);

INSERT INTO workflow_edges_new (
    id,
    transition_group_id,
    edge_key,
    target_node_id,
    requires_approval,
    context_mode,
    input_bindings_json,
    output_requirements_json,
    sort_order
)
SELECT
    id,
    transition_group_id,
    edge_key,
    target_node_id,
    requires_approval,
    context_mode,
    input_bindings_json,
    output_requirements_json,
    sort_order
FROM workflow_edges
ORDER BY rowid ASC;

DROP TABLE workflow_edges;
ALTER TABLE workflow_edges_new RENAME TO workflow_edges;

CREATE INDEX workflow_edges_transition_group_sort_idx
    ON workflow_edges(transition_group_id, sort_order);

CREATE INDEX workflow_edges_target_node_idx
    ON workflow_edges(target_node_id);

-- +goose StatementBegin
CREATE TRIGGER workflow_edges_target_workflow_insert
BEFORE INSERT ON workflow_edges
FOR EACH ROW
WHEN NOT EXISTS (
    SELECT 1
    FROM workflow_transition_groups tg
    JOIN workflow_nodes source ON source.id = tg.source_node_id
    JOIN workflow_nodes target ON target.id = NEW.target_node_id
    WHERE tg.id = NEW.transition_group_id
      AND target.workflow_id = source.workflow_id
)
BEGIN
    SELECT RAISE(ABORT, 'workflow edge target node must belong to transition group workflow');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER workflow_edges_target_workflow_update
BEFORE UPDATE OF transition_group_id, target_node_id ON workflow_edges
FOR EACH ROW
WHEN NOT EXISTS (
    SELECT 1
    FROM workflow_transition_groups tg
    JOIN workflow_nodes source ON source.id = tg.source_node_id
    JOIN workflow_nodes target ON target.id = NEW.target_node_id
    WHERE tg.id = NEW.transition_group_id
      AND target.workflow_id = source.workflow_id
)
BEGIN
    SELECT RAISE(ABORT, 'workflow edge target node must belong to transition group workflow');
END;
-- +goose StatementEnd

CREATE VIEW task_transition_records AS
SELECT
    tt.id,
    tt.task_id,
    tt.source_run_id,
    tt.source_placement_id,
    p.node_id AS source_node_id,
    tt.source_node_key,
    tt.source_node_display_name,
    derived_group_edge.transition_group_id,
    tt.transition_id,
    tt.transition_display_name,
    tt.workflow_revision_seen,
    tt.actor,
    tt.state,
    tt.commentary,
    tt.output_values_json,
    tt.created_at_unix_ms,
    tt.applied_at_unix_ms
FROM task_transitions tt
LEFT JOIN task_node_placements p ON p.id = tt.source_placement_id
LEFT JOIN task_transition_edges derived_transition_edge ON derived_transition_edge.id = (
    SELECT te.id
    FROM task_transition_edges te
    JOIN workflow_edges e ON e.id = te.workflow_edge_id
    WHERE te.task_transition_id = tt.id
      AND NOT EXISTS (
          SELECT 1
          FROM task_transition_edges other_te
          JOIN workflow_edges other_e ON other_e.id = other_te.workflow_edge_id
          WHERE other_te.task_transition_id = tt.id
            AND other_e.transition_group_id != e.transition_group_id
      )
    ORDER BY te.rowid ASC
    LIMIT 1
)
LEFT JOIN workflow_edges derived_group_edge ON derived_group_edge.id = derived_transition_edge.workflow_edge_id;

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
    )
)
BEGIN
    SELECT RAISE(ABORT, 'task transition references must stay within one task workflow');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER task_transitions_runtime_update
BEFORE UPDATE OF task_id, source_run_id, source_placement_id, transition_id ON task_transitions
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
        JOIN workflow_transition_groups tg ON tg.id = e.transition_group_id
        JOIN workflow_nodes source ON source.id = tg.source_node_id
        WHERE tt.id = NEW.task_transition_id
          AND source.workflow_id = t.workflow_id
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
        JOIN workflow_transition_groups tg ON tg.id = e.transition_group_id
        JOIN workflow_nodes source ON source.id = tg.source_node_id
        WHERE tt.id = NEW.task_transition_id
          AND source.workflow_id = t.workflow_id
    )
)
BEGIN
    SELECT RAISE(ABORT, 'task transition edge references must stay within one task workflow');
END;
-- +goose StatementEnd

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
        JOIN workflow_transition_groups tg ON tg.id = e.transition_group_id
        JOIN workflow_nodes source ON source.id = tg.source_node_id
        WHERE t.id = NEW.task_id
          AND source.workflow_id = t.workflow_id
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
        JOIN workflow_transition_groups tg ON tg.id = e.transition_group_id
        JOIN workflow_nodes source ON source.id = tg.source_node_id
        WHERE t.id = NEW.task_id
          AND source.workflow_id = t.workflow_id
    )
)
BEGIN
    SELECT RAISE(ABORT, 'task node placement references must stay within one task workflow');
END;
-- +goose StatementEnd

CREATE TEMP TABLE migration_workflow_graph_denormalization_fk_check_zero(value INTEGER NOT NULL CHECK (value = 0));

INSERT INTO migration_workflow_graph_denormalization_fk_check_zero(value)
SELECT 1
WHERE EXISTS (
    SELECT 1
    FROM pragma_foreign_key_check
);

DROP TABLE migration_workflow_graph_denormalization_fk_check_zero;

COMMIT;

PRAGMA foreign_keys = ON;

-- +goose Up

CREATE TEMP TABLE migration_runtime_check_zero(value INTEGER NOT NULL CHECK (value = 0));

INSERT INTO migration_runtime_check_zero(value)
SELECT 1
WHERE EXISTS (
    SELECT 1
    FROM task_node_placements p
    JOIN task_records t ON t.id = p.task_id
    JOIN workflow_nodes n ON n.id = p.node_id
    WHERE n.workflow_id != t.workflow_id
);

INSERT INTO migration_runtime_check_zero(value)
SELECT 1
WHERE EXISTS (
    SELECT 1
    FROM task_node_placements p
    JOIN task_transitions tt ON tt.id = p.created_by_transition_id
    WHERE tt.task_id != p.task_id
);

INSERT INTO migration_runtime_check_zero(value)
SELECT 1
WHERE EXISTS (
    SELECT 1
    FROM task_node_placements p
    JOIN task_transitions tt ON tt.id = p.parallel_batch_transition_id
    WHERE tt.task_id != p.task_id
);

INSERT INTO migration_runtime_check_zero(value)
SELECT 1
WHERE EXISTS (
    SELECT 1
    FROM task_node_placements p
    JOIN task_records t ON t.id = p.task_id
    JOIN workflow_edges e ON e.id = p.parallel_branch_edge_id
    WHERE e.workflow_id != t.workflow_id
);

INSERT INTO migration_runtime_check_zero(value)
SELECT 1
WHERE EXISTS (
    SELECT 1
    FROM task_runs r
    JOIN task_node_placements p ON p.id = r.placement_id
    WHERE r.task_id != p.task_id
       OR r.node_id != p.node_id
);

INSERT INTO migration_runtime_check_zero(value)
SELECT 1
WHERE EXISTS (
    SELECT 1
    FROM task_transitions tt
    JOIN task_runs r ON r.id = tt.source_run_id
    WHERE r.task_id != tt.task_id
);

INSERT INTO migration_runtime_check_zero(value)
SELECT 1
WHERE EXISTS (
    SELECT 1
    FROM task_transitions tt
    JOIN task_node_placements p ON p.id = tt.source_placement_id
    WHERE p.task_id != tt.task_id
       OR (tt.source_node_id IS NOT NULL AND trim(tt.source_node_id) != '' AND p.node_id != tt.source_node_id)
);

INSERT INTO migration_runtime_check_zero(value)
SELECT 1
WHERE EXISTS (
    SELECT 1
    FROM task_transitions tt
    JOIN task_records t ON t.id = tt.task_id
    JOIN workflow_nodes n ON n.id = tt.source_node_id
    WHERE n.workflow_id != t.workflow_id
);

INSERT INTO migration_runtime_check_zero(value)
SELECT 1
WHERE EXISTS (
    SELECT 1
    FROM task_transitions tt
    JOIN task_records t ON t.id = tt.task_id
    JOIN workflow_transition_groups tg ON tg.id = tt.transition_group_id
    JOIN workflow_nodes source_node ON source_node.id = tg.source_node_id
    WHERE source_node.workflow_id != t.workflow_id
);

INSERT INTO migration_runtime_check_zero(value)
SELECT 1
WHERE EXISTS (
    SELECT 1
    FROM task_transition_edges te
    JOIN task_transitions tt ON tt.id = te.task_transition_id
    JOIN task_node_placements p ON p.id = te.target_placement_id
    WHERE p.task_id != tt.task_id
       OR (te.target_node_id IS NOT NULL AND trim(te.target_node_id) != '' AND p.node_id != te.target_node_id)
);

INSERT INTO migration_runtime_check_zero(value)
SELECT 1
WHERE EXISTS (
    SELECT 1
    FROM task_transition_edges te
    JOIN task_transitions tt ON tt.id = te.task_transition_id
    JOIN task_records t ON t.id = tt.task_id
    JOIN workflow_nodes n ON n.id = te.target_node_id
    WHERE n.workflow_id != t.workflow_id
);

INSERT INTO migration_runtime_check_zero(value)
SELECT 1
WHERE EXISTS (
    SELECT 1
    FROM task_transition_edges te
    JOIN task_transitions tt ON tt.id = te.task_transition_id
    JOIN task_records t ON t.id = tt.task_id
    JOIN workflow_edges e ON e.id = te.workflow_edge_id
    WHERE e.workflow_id != t.workflow_id
);

DROP TABLE migration_runtime_check_zero;

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
    NEW.created_by_transition_id IS NOT NULL
    AND trim(NEW.created_by_transition_id) != ''
    AND NOT EXISTS (
        SELECT 1
        FROM task_transitions tt
        WHERE tt.id = NEW.created_by_transition_id
          AND tt.task_id = NEW.task_id
    )
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
BEFORE UPDATE OF task_id, node_id, created_by_transition_id, parallel_batch_transition_id, parallel_branch_edge_id ON task_node_placements
FOR EACH ROW
WHEN NOT EXISTS (
    SELECT 1
    FROM task_records t
    JOIN workflow_nodes n ON n.id = NEW.node_id
    WHERE t.id = NEW.task_id
      AND n.workflow_id = t.workflow_id
)
OR (
    NEW.created_by_transition_id IS NOT NULL
    AND trim(NEW.created_by_transition_id) != ''
    AND NOT EXISTS (
        SELECT 1
        FROM task_transitions tt
        WHERE tt.id = NEW.created_by_transition_id
          AND tt.task_id = NEW.task_id
    )
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
CREATE TRIGGER task_runs_runtime_insert
BEFORE INSERT ON task_runs
FOR EACH ROW
WHEN NOT EXISTS (
    SELECT 1
    FROM task_node_placements p
    WHERE p.id = NEW.placement_id
      AND p.task_id = NEW.task_id
      AND p.node_id = NEW.node_id
)
BEGIN
    SELECT RAISE(ABORT, 'task run task/node references must match placement');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER task_runs_runtime_update
BEFORE UPDATE OF task_id, placement_id, node_id ON task_runs
FOR EACH ROW
WHEN NOT EXISTS (
    SELECT 1
    FROM task_node_placements p
    WHERE p.id = NEW.placement_id
      AND p.task_id = NEW.task_id
      AND p.node_id = NEW.node_id
)
BEGIN
    SELECT RAISE(ABORT, 'task run task/node references must match placement');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER task_transitions_runtime_insert
BEFORE INSERT ON task_transitions
FOR EACH ROW
WHEN (
    NEW.source_run_id IS NOT NULL
    AND trim(NEW.source_run_id) != ''
    AND NOT EXISTS (
        SELECT 1
        FROM task_runs r
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
        FROM task_runs r
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

-- +goose Down

DROP INDEX IF EXISTS workflow_events_project_sequence_idx;
DROP TABLE IF EXISTS workflow_events;

DROP TRIGGER IF EXISTS workflow_nodes_group_workflow_update;
DROP TRIGGER IF EXISTS workflow_nodes_group_workflow_insert;

ALTER TABLE workflow_nodes DROP COLUMN group_id;

DROP INDEX IF EXISTS workflow_node_groups_workflow_sort_idx;
DROP TABLE IF EXISTS workflow_node_groups;

-- +goose Up

CREATE TABLE workflow_node_groups (
    id TEXT PRIMARY KEY,
    workflow_id TEXT NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,
    group_key TEXT NOT NULL CHECK (length(group_key) BETWEEN 1 AND 64),
    display_name TEXT NOT NULL CHECK (length(trim(display_name)) BETWEEN 1 AND 120),
    sort_order INTEGER NOT NULL DEFAULT 0 CHECK (sort_order >= 0),
    metadata_json TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(metadata_json)),
    UNIQUE (workflow_id, id),
    UNIQUE (workflow_id, group_key)
);

CREATE INDEX workflow_node_groups_workflow_sort_idx
    ON workflow_node_groups(workflow_id, sort_order);

ALTER TABLE workflow_nodes
    ADD COLUMN group_id TEXT REFERENCES workflow_node_groups(id) ON DELETE SET NULL;

-- +goose StatementBegin
CREATE TRIGGER workflow_nodes_group_workflow_insert
BEFORE INSERT ON workflow_nodes
FOR EACH ROW
WHEN NEW.group_id IS NOT NULL
  AND NOT EXISTS (
    SELECT 1
    FROM workflow_node_groups g
    WHERE g.id = NEW.group_id
      AND g.workflow_id = NEW.workflow_id
  )
BEGIN
    SELECT RAISE(ABORT, 'workflow_nodes.group_id must belong to node workflow');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER workflow_nodes_group_workflow_update
BEFORE UPDATE OF workflow_id, group_id ON workflow_nodes
FOR EACH ROW
WHEN NEW.group_id IS NOT NULL
  AND NOT EXISTS (
    SELECT 1
    FROM workflow_node_groups g
    WHERE g.id = NEW.group_id
      AND g.workflow_id = NEW.workflow_id
  )
BEGIN
    SELECT RAISE(ABORT, 'workflow_nodes.group_id must belong to node workflow');
END;
-- +goose StatementEnd

CREATE TABLE workflow_events (
    sequence INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id TEXT NOT NULL DEFAULT '',
    workflow_id TEXT NOT NULL DEFAULT '',
    resource TEXT NOT NULL CHECK (length(trim(resource)) > 0),
    action TEXT NOT NULL CHECK (length(trim(action)) > 0),
    changed_ids_json TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(changed_ids_json)),
    occurred_at_unix_ms INTEGER NOT NULL CHECK (occurred_at_unix_ms >= 0)
);

CREATE INDEX workflow_events_project_sequence_idx
    ON workflow_events(project_id, sequence);

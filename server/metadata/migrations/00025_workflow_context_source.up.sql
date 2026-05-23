-- +goose Up

ALTER TABLE workflow_edges
    ADD COLUMN context_source_kind TEXT NOT NULL DEFAULT 'immediate_source'
    CHECK (context_source_kind IN ('immediate_source', 'selected_node'));

ALTER TABLE workflow_edges
    ADD COLUMN context_source_node_key TEXT NOT NULL DEFAULT ''
    CHECK (length(context_source_node_key) <= 64);

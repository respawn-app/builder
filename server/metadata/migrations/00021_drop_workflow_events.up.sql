-- +goose Up
DROP INDEX IF EXISTS workflow_events_project_sequence_idx;
DROP TABLE IF EXISTS workflow_events;

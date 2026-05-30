-- +goose Up

ALTER TABLE runtime_leases
ADD COLUMN released_at_unix_ms INTEGER NOT NULL DEFAULT 0;

-- +goose Up

CREATE TABLE mutation_dedupe (
    method TEXT NOT NULL,
    resource_id TEXT NOT NULL,
    client_request_id TEXT NOT NULL,
    payload_fingerprint TEXT NOT NULL,
    response_json TEXT NOT NULL DEFAULT '',
    error_code TEXT NOT NULL DEFAULT '',
    error_message TEXT NOT NULL DEFAULT '',
    completed_at_unix_ms INTEGER NOT NULL,
    expires_at_unix_ms INTEGER NOT NULL,
    metadata_json TEXT NOT NULL DEFAULT '{}',
    PRIMARY KEY (method, resource_id, client_request_id)
);

CREATE INDEX mutation_dedupe_expires_idx ON mutation_dedupe(expires_at_unix_ms);

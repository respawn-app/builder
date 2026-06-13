-- +goose Up
-- +goose NO TRANSACTION

PRAGMA foreign_keys = OFF;

CREATE TABLE task_comments_new (
    id TEXT PRIMARY KEY,
    task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    body TEXT NOT NULL CHECK (length(body) <= 262144),
    author_kind TEXT NOT NULL CHECK (author_kind IN ('user', 'agent')),
    author_id TEXT NOT NULL DEFAULT '',
    created_at_unix_ms INTEGER NOT NULL CHECK (created_at_unix_ms >= 0),
    updated_at_unix_ms INTEGER NOT NULL CHECK (updated_at_unix_ms >= 0)
);

INSERT INTO task_comments_new (
    id,
    task_id,
    body,
    author_kind,
    author_id,
    created_at_unix_ms,
    updated_at_unix_ms
)
SELECT
    id,
    task_id,
    body,
    CASE WHEN author_kind = 'system' THEN 'agent' ELSE author_kind END,
    CASE WHEN author_kind = 'system' AND author_id = '' THEN 'system' ELSE author_id END,
    created_at_unix_ms,
    updated_at_unix_ms
FROM task_comments
WHERE deleted_at_unix_ms = 0
ORDER BY rowid ASC;

DROP TABLE task_comments;
ALTER TABLE task_comments_new RENAME TO task_comments;

CREATE INDEX task_comments_task_updated_idx
    ON task_comments(task_id, updated_at_unix_ms DESC);

CREATE TABLE runtime_leases_new (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    created_at_unix_ms INTEGER NOT NULL
);

INSERT INTO runtime_leases_new (
    id,
    session_id,
    created_at_unix_ms
)
SELECT
    id,
    session_id,
    created_at_unix_ms
FROM runtime_leases
ORDER BY rowid ASC;

DROP TABLE runtime_leases;
ALTER TABLE runtime_leases_new RENAME TO runtime_leases;

CREATE TEMP TABLE migration_comments_runtime_leases_check_zero(value INTEGER NOT NULL CHECK (value = 0));

INSERT INTO migration_comments_runtime_leases_check_zero(value)
SELECT 1
WHERE EXISTS (
    SELECT 1
    FROM pragma_foreign_key_check
);

DROP TABLE migration_comments_runtime_leases_check_zero;

PRAGMA foreign_keys = ON;

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
ORDER BY rowid ASC;

DROP TABLE task_comments;
ALTER TABLE task_comments_new RENAME TO task_comments;

-- Recreate the lookup index dropped together with the old task_comments table
-- (originally added by migration 00020) so comment list/activity queries keep
-- using it instead of scanning as histories grow.
CREATE INDEX task_comments_task_updated_idx
    ON task_comments(task_id, updated_at_unix_ms DESC);

CREATE TEMP TABLE migration_remove_system_task_comment_author_check_zero(value INTEGER NOT NULL CHECK (value = 0));

INSERT INTO migration_remove_system_task_comment_author_check_zero(value)
SELECT 1
WHERE EXISTS (
    SELECT 1
    FROM pragma_foreign_key_check
);

DROP TABLE migration_remove_system_task_comment_author_check_zero;

PRAGMA foreign_keys = ON;

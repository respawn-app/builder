-- +goose Up

ALTER TABLE worktrees ADD COLUMN builder_managed INTEGER NOT NULL DEFAULT 0;
ALTER TABLE worktrees ADD COLUMN created_branch INTEGER NOT NULL DEFAULT 0;
ALTER TABLE worktrees ADD COLUMN origin_session_id TEXT NOT NULL DEFAULT '';

-- +goose Down

ALTER TABLE worktrees DROP COLUMN origin_session_id;
ALTER TABLE worktrees DROP COLUMN created_branch;
ALTER TABLE worktrees DROP COLUMN builder_managed;

-- +goose Down

ALTER TABLE sessions DROP COLUMN launch_visible;

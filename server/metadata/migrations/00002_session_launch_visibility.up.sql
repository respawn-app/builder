-- +goose Up

ALTER TABLE sessions ADD COLUMN launch_visible INTEGER NOT NULL DEFAULT 0;

UPDATE sessions
SET launch_visible = CASE
    WHEN trim(name) <> ''
      OR trim(first_prompt_preview) <> ''
      OR trim(input_draft) <> ''
      OR trim(parent_session_id) <> ''
      OR last_sequence > 0
      OR model_request_count > 0
    THEN 1
    ELSE 0
END;

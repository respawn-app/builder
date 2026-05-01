-- +goose Down

ALTER TABLE runtime_leases ADD COLUMN state TEXT NOT NULL DEFAULT '';
ALTER TABLE runtime_leases ADD COLUMN released_at_unix_ms INTEGER NOT NULL DEFAULT 0;
ALTER TABLE runtime_leases ADD COLUMN expires_at_unix_ms INTEGER NOT NULL DEFAULT 0;

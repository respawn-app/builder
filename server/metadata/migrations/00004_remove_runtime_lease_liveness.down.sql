-- +goose Down

-- Runtime lease liveness was intentionally removed because active runtime
-- ownership is process-local. Reconstructing state/released_at_unix_ms/
-- expires_at_unix_ms would create invalid semantics for older binaries, so this
-- migration is irreversible.
SELECT builder_irreversible_migration('runtime lease liveness columns cannot be reconstructed safely');

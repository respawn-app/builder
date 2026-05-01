-- +goose Up

-- runtime_leases are durable controller-token records only. Runtime liveness is
-- process-local state owned by sessionruntime.Service/RuntimeRegistry and must
-- not be persisted here.
ALTER TABLE runtime_leases DROP COLUMN state;
ALTER TABLE runtime_leases DROP COLUMN released_at_unix_ms;
ALTER TABLE runtime_leases DROP COLUMN expires_at_unix_ms;

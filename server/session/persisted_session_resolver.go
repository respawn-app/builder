package session

import "context"

type PersistedSessionRecord struct {
	SessionDir string
	Meta       *Meta
}

type PersistedSessionResolver interface {
	ResolvePersistedSession(ctx context.Context, sessionID string) (PersistedSessionRecord, error)
}

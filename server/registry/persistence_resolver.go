package registry

import (
	"context"
	"strings"

	"builder/server/session"
	"builder/server/sessionpath"
)

type PersistenceSessionResolver struct {
	containerDir string
}

func NewPersistenceSessionResolver(containerDir string) PersistenceSessionResolver {
	return PersistenceSessionResolver{containerDir: strings.TrimSpace(containerDir)}
}

func (r PersistenceSessionResolver) ResolveSession(_ context.Context, sessionID string) (session.Snapshot, error) {
	realSessionDir, err := sessionpath.ResolveScopedSessionDir(r.containerDir, sessionID)
	if err != nil {
		return session.Snapshot{}, err
	}
	return session.SnapshotFromDir(realSessionDir)
}

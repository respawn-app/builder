package registry

import (
	"context"
	"path/filepath"
	"strings"

	"builder/server/session"
)

type PersistenceSessionResolver struct {
	containerDir string
}

func NewPersistenceSessionResolver(containerDir string) PersistenceSessionResolver {
	return PersistenceSessionResolver{containerDir: strings.TrimSpace(containerDir)}
}

func (r PersistenceSessionResolver) ResolveSession(_ context.Context, sessionID string) (session.Snapshot, error) {
	return session.SnapshotFromDir(filepath.Join(r.containerDir, strings.TrimSpace(sessionID)))
}

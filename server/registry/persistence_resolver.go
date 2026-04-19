package registry

import (
	"context"
	"strings"

	"builder/server/session"
	"builder/server/sessionpath"
)

type PersistenceSessionResolver struct {
	containerDir string
	storeOptions []session.StoreOption
}

func NewPersistenceSessionResolver(containerDir string, storeOptions ...session.StoreOption) PersistenceSessionResolver {
	return PersistenceSessionResolver{containerDir: strings.TrimSpace(containerDir), storeOptions: append([]session.StoreOption(nil), storeOptions...)}
}

func (r PersistenceSessionResolver) ResolveSession(_ context.Context, sessionID string) (session.Snapshot, error) {
	realSessionDir, err := sessionpath.ResolveScopedSessionDir(r.containerDir, sessionID)
	if err != nil {
		return session.Snapshot{}, err
	}
	return session.SnapshotFromDir(realSessionDir)
}

func (r PersistenceSessionResolver) ResolveSessionStore(_ context.Context, sessionID string) (*session.Store, error) {
	realSessionDir, err := sessionpath.ResolveScopedSessionDir(r.containerDir, sessionID)
	if err != nil {
		return nil, err
	}
	return session.Open(realSessionDir, r.storeOptions...)
}

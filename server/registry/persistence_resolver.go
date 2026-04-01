package registry

import (
	"context"

	"builder/server/session"
)

type PersistenceSessionResolver struct {
	root string
}

func NewPersistenceSessionResolver(root string) PersistenceSessionResolver {
	return PersistenceSessionResolver{root: root}
}

func (r PersistenceSessionResolver) ResolveSession(_ context.Context, sessionID string) (session.Snapshot, error) {
	return session.SnapshotByID(r.root, sessionID)
}

package registry

import (
	"context"
	"fmt"
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
	containerDir, resolvedSessionDir, err := r.resolveSessionDir(sessionID)
	if err != nil {
		return session.Snapshot{}, err
	}
	if !isDescendantPath(containerDir, resolvedSessionDir) {
		return session.Snapshot{}, fmt.Errorf("session %q is outside workspace container", strings.TrimSpace(sessionID))
	}
	return session.SnapshotFromDir(resolvedSessionDir)
}

func (r PersistenceSessionResolver) resolveSessionDir(sessionID string) (string, string, error) {
	containerDir := strings.TrimSpace(r.containerDir)
	trimmedSessionID := strings.TrimSpace(sessionID)
	if containerDir == "" {
		return "", "", fmt.Errorf("workspace container dir is required")
	}
	if trimmedSessionID == "" {
		return "", "", fmt.Errorf("session id is required")
	}
	if filepath.IsAbs(trimmedSessionID) || trimmedSessionID == "." || trimmedSessionID == ".." {
		return "", "", fmt.Errorf("session id %q is invalid", trimmedSessionID)
	}
	if strings.Contains(trimmedSessionID, "/") || strings.Contains(trimmedSessionID, "\\") {
		return "", "", fmt.Errorf("session id %q is invalid", trimmedSessionID)
	}
	if cleaned := filepath.Clean(trimmedSessionID); cleaned != trimmedSessionID {
		return "", "", fmt.Errorf("session id %q is invalid", trimmedSessionID)
	}
	absContainerDir, err := filepath.Abs(containerDir)
	if err != nil {
		return "", "", fmt.Errorf("resolve workspace container dir: %w", err)
	}
	return absContainerDir, filepath.Join(absContainerDir, trimmedSessionID), nil
}

func isDescendantPath(parent, child string) bool {
	cleanParent := filepath.Clean(strings.TrimSpace(parent))
	cleanChild := filepath.Clean(strings.TrimSpace(child))
	if cleanParent == "" || cleanChild == "" {
		return false
	}
	if cleanParent == cleanChild {
		return true
	}
	prefix := cleanParent + string(filepath.Separator)
	return strings.HasPrefix(cleanChild, prefix)
}

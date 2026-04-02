package registry

import (
	"context"
	"path/filepath"
	"testing"

	"builder/server/session"
)

func TestPersistenceSessionResolverScopesLookupsToWorkspaceContainer(t *testing.T) {
	root := t.TempDir()
	containerA := filepath.Join(root, "workspace-a")
	containerB := filepath.Join(root, "workspace-b")

	storeA, err := session.Create(containerA, "workspace-a", "/tmp/workspace-a")
	if err != nil {
		t.Fatalf("create session A: %v", err)
	}
	if err := storeA.SetName("session-a"); err != nil {
		t.Fatalf("persist session A meta: %v", err)
	}
	storeB, err := session.Create(containerB, "workspace-b", "/tmp/workspace-b")
	if err != nil {
		t.Fatalf("create session B: %v", err)
	}
	if err := storeB.SetName("session-b"); err != nil {
		t.Fatalf("persist session B meta: %v", err)
	}

	resolver := NewPersistenceSessionResolver(containerA)
	resolved, err := resolver.ResolveSession(context.Background(), storeA.Meta().SessionID)
	if err != nil {
		t.Fatalf("ResolveSession session A: %v", err)
	}
	if resolved.Meta.SessionID != storeA.Meta().SessionID {
		t.Fatalf("resolved session = %q, want %q", resolved.Meta.SessionID, storeA.Meta().SessionID)
	}
	if _, err := resolver.ResolveSession(context.Background(), storeB.Meta().SessionID); err == nil {
		t.Fatal("expected resolver to reject cross-container session lookup")
	}
}

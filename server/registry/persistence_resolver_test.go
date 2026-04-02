package registry

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

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

func TestPersistenceSessionResolverRejectsInvalidSessionIDs(t *testing.T) {
	containerDir := t.TempDir()
	legacyDir := filepath.Join(containerDir, "legacy-session")
	if err := os.MkdirAll(legacyDir, 0o755); err != nil {
		t.Fatalf("mkdir legacy session dir: %v", err)
	}
	metaData, err := json.Marshal(session.Meta{
		SessionID:          "legacy-session",
		WorkspaceContainer: "workspace-a",
		WorkspaceRoot:      "/tmp/workspace-a",
		CreatedAt:          time.Now().UTC(),
		UpdatedAt:          time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("marshal legacy session meta: %v", err)
	}
	if err := os.WriteFile(filepath.Join(legacyDir, "session.json"), metaData, 0o644); err != nil {
		t.Fatalf("write legacy session meta: %v", err)
	}
	if err := os.WriteFile(filepath.Join(legacyDir, "events.jsonl"), nil, 0o644); err != nil {
		t.Fatalf("write legacy session events: %v", err)
	}
	resolver := NewPersistenceSessionResolver(containerDir)
	if _, err := resolver.ResolveSession(context.Background(), "../other-container/session-id"); err == nil {
		t.Fatal("expected resolver to reject path-traversal session id")
	}
	if _, err := resolver.ResolveSession(context.Background(), "/tmp/escaped-session"); err == nil {
		t.Fatal("expected resolver to reject absolute-path session id")
	}
	if snapshot, err := resolver.ResolveSession(context.Background(), "legacy-session"); err != nil {
		t.Fatalf("expected plain legacy session id to remain allowed, got %v", err)
	} else if snapshot.Meta.SessionID != "legacy-session" {
		t.Fatalf("resolved legacy session = %q, want legacy-session", snapshot.Meta.SessionID)
	}
}

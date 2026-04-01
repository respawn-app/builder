package session_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"builder/server/llm"
	"builder/server/session"
)

func TestSnapshotByIDReturnsDurableSessionState(t *testing.T) {
	root := t.TempDir()
	containerDir := filepath.Join(root, "sessions", "workspace-x")
	if err := os.MkdirAll(containerDir, 0o755); err != nil {
		t.Fatalf("mkdir container dir: %v", err)
	}
	store, err := session.Create(containerDir, "workspace-x", "/tmp/work")
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	if err := store.SetName("incident triage"); err != nil {
		t.Fatalf("set name: %v", err)
	}
	if _, err := store.AppendEvent("step-1", "message", llm.Message{Role: llm.RoleUser, Content: "hello"}); err != nil {
		t.Fatalf("append user message: %v", err)
	}
	startedAt := time.Now().UTC().Add(-time.Minute)
	if _, err := store.AppendRunStarted(session.RunRecord{RunID: "run-1", StepID: "step-1", StartedAt: startedAt}); err != nil {
		t.Fatalf("append run start: %v", err)
	}

	snapshot, err := session.SnapshotByID(root, store.Meta().SessionID)
	if err != nil {
		t.Fatalf("snapshot by id: %v", err)
	}
	if snapshot.Meta.SessionID != store.Meta().SessionID || snapshot.Meta.Name != "incident triage" {
		t.Fatalf("unexpected snapshot meta: %+v", snapshot.Meta)
	}
	if snapshot.ConversationFreshness != session.ConversationFreshnessEstablished {
		t.Fatalf("unexpected conversation freshness: %v", snapshot.ConversationFreshness)
	}
	if len(snapshot.Events) != 2 || len(snapshot.Runs) != 1 {
		t.Fatalf("unexpected snapshot counts: events=%d runs=%d", len(snapshot.Events), len(snapshot.Runs))
	}
	if snapshot.Runs[0].RunID != "run-1" || snapshot.Runs[0].Status != session.RunStatusRunning {
		t.Fatalf("unexpected snapshot run: %+v", snapshot.Runs[0])
	}
}

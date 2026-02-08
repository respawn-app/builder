package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAppendEventMonotonicSequence(t *testing.T) {
	root := t.TempDir()
	store, err := Create(root, "workspace-x", "/tmp/work")
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	e1, err := store.AppendEvent("step1", "message", map[string]any{"a": 1})
	if err != nil {
		t.Fatalf("append event1: %v", err)
	}
	e2, err := store.AppendEvent("step1", "message", map[string]any{"b": 2})
	if err != nil {
		t.Fatalf("append event2: %v", err)
	}

	if e1.Seq != 1 || e2.Seq != 2 {
		t.Fatalf("unexpected sequence values: %d, %d", e1.Seq, e2.Seq)
	}

	events, err := store.ReadEvents()
	if err != nil {
		t.Fatalf("read events: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("events len = %d, want 2", len(events))
	}
	if events[0].Seq != 1 || events[1].Seq != 2 {
		t.Fatalf("persisted sequence mismatch: %+v", events)
	}
}

func TestListSessionsSortedByUpdatedAt(t *testing.T) {
	root := t.TempDir()
	s1, err := Create(root, "workspace-x", "/tmp/work")
	if err != nil {
		t.Fatalf("create session1: %v", err)
	}
	if _, err := s1.AppendEvent("step1", "message", map[string]any{"a": 1}); err != nil {
		t.Fatalf("append event1: %v", err)
	}

	s2, err := Create(root, "workspace-x", "/tmp/work")
	if err != nil {
		t.Fatalf("create session2: %v", err)
	}
	if _, err := s2.AppendEvent("step1", "message", map[string]any{"b": 2}); err != nil {
		t.Fatalf("append event2: %v", err)
	}

	items, err := ListSessions(root)
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(items))
	}
	if filepath.Base(items[0].Path) != s2.Meta().SessionID {
		t.Fatalf("latest session expected first")
	}
}

func TestLockedContractPersistenceDoesNotIncludePromptOrToolSchema(t *testing.T) {
	root := t.TempDir()
	store, err := Create(root, "workspace-x", "/tmp/work")
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	if err := store.MarkModelDispatchLocked(LockedContract{
		Model:          "gpt-5",
		Temperature:    1,
		MaxOutputToken: 0,
	}); err != nil {
		t.Fatalf("mark model dispatch locked: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(store.Dir(), sessionFile))
	if err != nil {
		t.Fatalf("read session file: %v", err)
	}
	text := string(data)
	if strings.Contains(text, "tools_json") {
		t.Fatalf("session metadata must not persist tools_json: %s", text)
	}
	if strings.Contains(text, "system_prompt") {
		t.Fatalf("session metadata must not persist system_prompt: %s", text)
	}
}

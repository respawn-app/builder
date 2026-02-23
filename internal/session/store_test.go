package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewLazyDoesNotPersistUntilFirstWrite(t *testing.T) {
	root := t.TempDir()
	store, err := NewLazy(root, "workspace-x", "/tmp/work")
	if err != nil {
		t.Fatalf("new lazy store: %v", err)
	}
	if _, err := os.Stat(store.Dir()); !os.IsNotExist(err) {
		t.Fatalf("expected no session dir before first write, stat err=%v", err)
	}

	if _, err := store.AppendEvent("step1", "message", map[string]any{"a": 1}); err != nil {
		t.Fatalf("append event: %v", err)
	}
	if _, err := os.Stat(filepath.Join(store.Dir(), sessionFile)); err != nil {
		t.Fatalf("expected session metadata after first write: %v", err)
	}
	if _, err := os.Stat(filepath.Join(store.Dir(), eventsFile)); err != nil {
		t.Fatalf("expected events file after first write: %v", err)
	}
}

func TestNewLazyReadEventsBeforePersistReturnsEmpty(t *testing.T) {
	root := t.TempDir()
	store, err := NewLazy(root, "workspace-x", "/tmp/work")
	if err != nil {
		t.Fatalf("new lazy store: %v", err)
	}
	events, err := store.ReadEvents()
	if err != nil {
		t.Fatalf("read events: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("events len = %d, want 0", len(events))
	}
}

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

func TestReadEventsHandlesLargeJSONLines(t *testing.T) {
	root := t.TempDir()
	store, err := Create(root, "workspace-x", "/tmp/work")
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	const payloadSize = 128 * 1024
	large := strings.Repeat("x", payloadSize)
	if _, err := store.AppendEvent("step1", "message", map[string]any{"blob": large}); err != nil {
		t.Fatalf("append large event: %v", err)
	}

	events, err := store.ReadEvents()
	if err != nil {
		t.Fatalf("read events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events len = %d, want 1", len(events))
	}

	var payload map[string]string
	if err := json.Unmarshal(events[0].Payload, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if got := len(payload["blob"]); got != payloadSize {
		t.Fatalf("payload blob size = %d, want %d", got, payloadSize)
	}
}

func TestSetNamePersistsAndAppearsInList(t *testing.T) {
	root := t.TempDir()
	store, err := Create(root, "workspace-x", "/tmp/work")
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	if err := store.SetName("Incident Triage"); err != nil {
		t.Fatalf("set name: %v", err)
	}
	items, err := ListSessions(root)
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected one session, got %d", len(items))
	}
	if items[0].Name != "Incident Triage" {
		t.Fatalf("expected list name to match, got %q", items[0].Name)
	}
}

func TestForkAtUserMessageCopiesPrefixBeforeSelectedMessage(t *testing.T) {
	root := t.TempDir()
	parent, err := Create(root, "workspace-x", "/tmp/work")
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}
	if _, err := parent.AppendEvent("s1", "message", map[string]any{"role": "user", "content": "u1"}); err != nil {
		t.Fatalf("append u1: %v", err)
	}
	if _, err := parent.AppendEvent("s1", "message", map[string]any{"role": "assistant", "content": "a1"}); err != nil {
		t.Fatalf("append a1: %v", err)
	}
	if _, err := parent.AppendEvent("s2", "message", map[string]any{"role": "user", "content": "u2"}); err != nil {
		t.Fatalf("append u2: %v", err)
	}
	if _, err := parent.AppendEvent("s2", "message", map[string]any{"role": "assistant", "content": "a2"}); err != nil {
		t.Fatalf("append a2: %v", err)
	}

	forked, err := ForkAtUserMessage(parent, 2, "Parent → edit u2")
	if err != nil {
		t.Fatalf("fork at user message: %v", err)
	}
	forkEvents, err := forked.ReadEvents()
	if err != nil {
		t.Fatalf("read fork events: %v", err)
	}
	if len(forkEvents) != 2 {
		t.Fatalf("expected two replayed events, got %d", len(forkEvents))
	}
	var first struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(forkEvents[0].Payload, &first); err != nil {
		t.Fatalf("decode first message: %v", err)
	}
	if first.Role != "user" || first.Content != "u1" {
		t.Fatalf("unexpected first message in fork: %+v", first)
	}
	meta := forked.Meta()
	if meta.ParentSessionID != parent.Meta().SessionID {
		t.Fatalf("expected fork parent session id, got %q", meta.ParentSessionID)
	}
	if meta.Name != "Parent → edit u2" {
		t.Fatalf("expected fork name, got %q", meta.Name)
	}
}

func TestSetParentSessionIDPersists(t *testing.T) {
	root := t.TempDir()
	store, err := Create(root, "workspace-x", "/tmp/work")
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	if err := store.SetParentSessionID("parent-session-1"); err != nil {
		t.Fatalf("set parent session id: %v", err)
	}
	opened, err := Open(store.Dir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if opened.Meta().ParentSessionID != "parent-session-1" {
		t.Fatalf("expected parent session id persisted, got %q", opened.Meta().ParentSessionID)
	}
}

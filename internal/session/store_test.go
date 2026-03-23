package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"builder/prompts"
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

func TestReadPromptHistoryFallsBackToVisibleUserMessages(t *testing.T) {
	root := t.TempDir()
	store, err := Create(root, "workspace-x", "/tmp/work")
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	if _, err := store.AppendEvent("s1", "message", map[string]any{"role": "user", "content": "first\nline"}); err != nil {
		t.Fatalf("append first user message: %v", err)
	}
	if _, err := store.AppendEvent("s1", "message", map[string]any{"role": "assistant", "content": "ignored"}); err != nil {
		t.Fatalf("append assistant message: %v", err)
	}
	if _, err := store.AppendEvent("s2", "message", map[string]any{"role": "user", "content": "second"}); err != nil {
		t.Fatalf("append second user message: %v", err)
	}

	history, err := store.ReadPromptHistory()
	if err != nil {
		t.Fatalf("read prompt history: %v", err)
	}
	if len(history) != 2 {
		t.Fatalf("expected 2 prompt history entries, got %d", len(history))
	}
	if history[0] != "first\nline" || history[1] != "second" {
		t.Fatalf("unexpected prompt history: %+v", history)
	}
}

func TestReadPromptHistoryUsesExplicitPromptHistoryEvents(t *testing.T) {
	root := t.TempDir()
	store, err := Create(root, "workspace-x", "/tmp/work")
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	if _, err := store.AppendEvent("", "prompt_history", map[string]any{"text": "/resume"}); err != nil {
		t.Fatalf("append slash command history: %v", err)
	}
	if _, err := store.AppendEvent("s1", "message", map[string]any{"role": "user", "content": "plain user message"}); err != nil {
		t.Fatalf("append user message: %v", err)
	}
	if _, err := store.AppendEvent("", "prompt_history", map[string]any{"text": "plain user message"}); err != nil {
		t.Fatalf("append explicit user history: %v", err)
	}

	history, err := store.ReadPromptHistory()
	if err != nil {
		t.Fatalf("read prompt history: %v", err)
	}
	if len(history) != 2 {
		t.Fatalf("expected 2 explicit prompt history entries, got %d", len(history))
	}
	if history[0] != "/resume" || history[1] != "plain user message" {
		t.Fatalf("unexpected prompt history: %+v", history)
	}
}

func TestReadPromptHistoryKeepsLegacyEntriesBeforeFirstExplicitEvent(t *testing.T) {
	root := t.TempDir()
	store, err := Create(root, "workspace-x", "/tmp/work")
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	if _, err := store.AppendEvent("s1", "message", map[string]any{"role": "user", "content": "legacy one"}); err != nil {
		t.Fatalf("append legacy one: %v", err)
	}
	if _, err := store.AppendEvent("s2", "message", map[string]any{"role": "user", "content": "legacy two"}); err != nil {
		t.Fatalf("append legacy two: %v", err)
	}
	if _, err := store.AppendEvent("", "prompt_history", map[string]any{"text": "/resume"}); err != nil {
		t.Fatalf("append explicit history: %v", err)
	}
	if _, err := store.AppendEvent("s3", "message", map[string]any{"role": "user", "content": "expanded later user message"}); err != nil {
		t.Fatalf("append post-upgrade user message: %v", err)
	}

	history, err := store.ReadPromptHistory()
	if err != nil {
		t.Fatalf("read prompt history: %v", err)
	}
	if len(history) != 3 {
		t.Fatalf("expected 3 history entries, got %d", len(history))
	}
	if history[0] != "legacy one" || history[1] != "legacy two" || history[2] != "/resume" {
		t.Fatalf("unexpected prompt history: %+v", history)
	}
}

func TestReadPromptHistoryPreservesExactStoredText(t *testing.T) {
	root := t.TempDir()
	store, err := Create(root, "workspace-x", "/tmp/work")
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	want := "  line one\nline two  "
	if _, err := store.AppendEvent("", "prompt_history", map[string]any{"text": want}); err != nil {
		t.Fatalf("append prompt history: %v", err)
	}

	history, err := store.ReadPromptHistory()
	if err != nil {
		t.Fatalf("read prompt history: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("expected 1 history entry, got %d", len(history))
	}
	if history[0] != want {
		t.Fatalf("expected exact stored prompt text, got %q want %q", history[0], want)
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

func TestAppendEventPersistsFirstPromptPreview(t *testing.T) {
	root := t.TempDir()
	store, err := Create(root, "workspace-x", "/tmp/work")
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	if _, err := store.AppendEvent("s1", "message", map[string]any{"role": "assistant", "content": "hello"}); err != nil {
		t.Fatalf("append assistant event: %v", err)
	}
	if got := store.Meta().FirstPromptPreview; got != "" {
		t.Fatalf("expected assistant event to leave preview empty, got %q", got)
	}
	if _, err := store.AppendEvent("s2", "message", map[string]any{"role": "user", "content": "Investigate config load failures\nsecond line"}); err != nil {
		t.Fatalf("append user event: %v", err)
	}
	if got := store.Meta().FirstPromptPreview; got != "Investigate config load failures" {
		t.Fatalf("preview = %q, want %q", got, "Investigate config load failures")
	}

	opened, err := Open(store.Dir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if got := opened.Meta().FirstPromptPreview; got != "Investigate config load failures" {
		t.Fatalf("reopened preview = %q, want %q", got, "Investigate config load failures")
	}

	items, err := ListSessions(root)
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected one session, got %d", len(items))
	}
	if items[0].FirstPromptPreview != "Investigate config load failures" {
		t.Fatalf("list preview = %q, want %q", items[0].FirstPromptPreview, "Investigate config load failures")
	}
}

func TestFirstPromptPreviewSkipsCompactionSummaryMessages(t *testing.T) {
	root := t.TempDir()
	store, err := Create(root, "workspace-x", "/tmp/work")
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	if _, err := store.AppendEvent("s1", "message", map[string]any{"role": "user", "content": prompts.CompactionSummaryPrefix + "\nsummary"}); err != nil {
		t.Fatalf("append compaction summary event: %v", err)
	}
	if got := store.Meta().FirstPromptPreview; got != "" {
		t.Fatalf("expected compaction summary to be ignored, got %q", got)
	}
	if _, err := store.AppendEvent("s2", "message", map[string]any{"role": "user", "content": "\n  Fix config registry boot path\nmore details"}); err != nil {
		t.Fatalf("append visible user event: %v", err)
	}
	if got := store.Meta().FirstPromptPreview; got != "Fix config registry boot path" {
		t.Fatalf("preview = %q, want %q", got, "Fix config registry boot path")
	}
}

func TestAppendTurnAtomicPersistsFirstPromptPreview(t *testing.T) {
	root := t.TempDir()
	store, err := Create(root, "workspace-x", "/tmp/work")
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	if _, err := store.AppendTurnAtomic("s1", []EventInput{{Kind: "message", Payload: map[string]any{"role": "assistant", "content": "hello"}}, {Kind: "message", Payload: map[string]any{"role": "user", "content": "Atomic preview source\nmore"}}}); err != nil {
		t.Fatalf("append turn: %v", err)
	}
	if got := store.Meta().FirstPromptPreview; got != "Atomic preview source" {
		t.Fatalf("preview = %q, want %q", got, "Atomic preview source")
	}
}

func TestListSessionsDoesNotDeriveFirstPromptPreviewFromLegacySessionMeta(t *testing.T) {
	root := t.TempDir()
	store, err := Create(root, "workspace-x", "/tmp/work")
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	if _, err := store.AppendEvent("s1", "message", map[string]any{"role": "user", "content": "Legacy preview source\nsecond line"}); err != nil {
		t.Fatalf("append user event: %v", err)
	}

	metaPath := filepath.Join(store.Dir(), sessionFile)
	data, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatalf("read session file: %v", err)
	}
	var meta Meta
	if err := json.Unmarshal(data, &meta); err != nil {
		t.Fatalf("decode session meta: %v", err)
	}
	meta.FirstPromptPreview = ""
	rewritten, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		t.Fatalf("encode session meta: %v", err)
	}
	if err := os.WriteFile(metaPath, rewritten, 0o644); err != nil {
		t.Fatalf("write session file: %v", err)
	}

	items, err := ListSessions(root)
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected one session, got %d", len(items))
	}
	if items[0].FirstPromptPreview != "" {
		t.Fatalf("expected legacy session preview to remain empty after hard cutover, got %q", items[0].FirstPromptPreview)
	}

	reloaded, err := Open(store.Dir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if reloaded.Meta().FirstPromptPreview != "" {
		t.Fatalf("expected legacy metadata preview to remain empty after list, got %q", reloaded.Meta().FirstPromptPreview)
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
	if meta.FirstPromptPreview != "u1" {
		t.Fatalf("expected fork preview to persist first user message, got %q", meta.FirstPromptPreview)
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

func TestSetContinuationContextStaysLazyUntilFirstWrite(t *testing.T) {
	root := t.TempDir()
	store, err := NewLazy(root, "workspace-x", "/tmp/work")
	if err != nil {
		t.Fatalf("new lazy store: %v", err)
	}
	if err := store.SetContinuationContext(ContinuationContext{OpenAIBaseURL: "http://example.local/v1"}); err != nil {
		t.Fatalf("set continuation context: %v", err)
	}
	if store.Meta().Continuation == nil || store.Meta().Continuation.OpenAIBaseURL != "http://example.local/v1" {
		t.Fatalf("expected in-memory continuation context, got %+v", store.Meta().Continuation)
	}
	if _, err := os.Stat(store.Dir()); !os.IsNotExist(err) {
		t.Fatalf("expected lazy session to remain unpersisted, stat err=%v", err)
	}
	if _, err := store.AppendEvent("step1", "message", map[string]any{"a": 1}); err != nil {
		t.Fatalf("append event: %v", err)
	}
	opened, err := Open(store.Dir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if opened.Meta().Continuation == nil || opened.Meta().Continuation.OpenAIBaseURL != "http://example.local/v1" {
		t.Fatalf("expected persisted continuation context, got %+v", opened.Meta().Continuation)
	}
}

func TestSessionMetadataDoesNotPersistModelVerbosityState(t *testing.T) {
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
	if err := store.SetContinuationContext(ContinuationContext{OpenAIBaseURL: "http://example.local/v1"}); err != nil {
		t.Fatalf("set continuation context: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(store.Dir(), sessionFile))
	if err != nil {
		t.Fatalf("read session file: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, "openai_base_url") {
		t.Fatalf("expected continuation openai_base_url to persist, got %q", text)
	}
	if strings.Contains(text, "model_verbosity") {
		t.Fatalf("session metadata must not persist model_verbosity: %s", text)
	}

	opened, err := Open(store.Dir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if opened.Meta().Continuation == nil || opened.Meta().Continuation.OpenAIBaseURL != "http://example.local/v1" {
		t.Fatalf("expected persisted continuation context, got %+v", opened.Meta().Continuation)
	}
	reopenedMetaJSON, err := json.Marshal(opened.Meta())
	if err != nil {
		t.Fatalf("marshal reopened meta: %v", err)
	}
	if strings.Contains(string(reopenedMetaJSON), "model_verbosity") {
		t.Fatal("expected reopened session metadata to remain free of model_verbosity")
	}
}

func TestOpenByIDFindsSessionAcrossContainers(t *testing.T) {
	root := t.TempDir()
	containerA := filepath.Join(root, sessionsDirName, "workspace-a")
	containerB := filepath.Join(root, sessionsDirName, "workspace-b")
	if err := os.MkdirAll(containerA, 0o755); err != nil {
		t.Fatalf("mkdir container a: %v", err)
	}
	if err := os.MkdirAll(containerB, 0o755); err != nil {
		t.Fatalf("mkdir container b: %v", err)
	}
	_, err := Create(containerA, "workspace-a", "/tmp/work-a")
	if err != nil {
		t.Fatalf("create session a: %v", err)
	}
	target, err := Create(containerB, "workspace-b", "/tmp/work-b")
	if err != nil {
		t.Fatalf("create session b: %v", err)
	}
	if err := target.SetContinuationContext(ContinuationContext{OpenAIBaseURL: "http://target.local/v1"}); err != nil {
		t.Fatalf("set continuation context: %v", err)
	}

	opened, err := OpenByID(root, target.Meta().SessionID)
	if err != nil {
		t.Fatalf("open by id: %v", err)
	}
	meta := opened.Meta()
	if meta.SessionID != target.Meta().SessionID {
		t.Fatalf("expected session id %q, got %q", target.Meta().SessionID, meta.SessionID)
	}
	if meta.WorkspaceRoot != "/tmp/work-b" {
		t.Fatalf("expected workspace root from target session, got %q", meta.WorkspaceRoot)
	}
	if meta.Continuation == nil || meta.Continuation.OpenAIBaseURL != "http://target.local/v1" {
		t.Fatalf("expected continuation context from target session, got %+v", meta.Continuation)
	}
}

func TestOpenByIDRejectsLegacyPersistenceRootLayout(t *testing.T) {
	root := t.TempDir()
	legacyContainer := filepath.Join(root, "workspace-legacy")
	if err := os.MkdirAll(legacyContainer, 0o755); err != nil {
		t.Fatalf("mkdir legacy container: %v", err)
	}
	store, err := Create(legacyContainer, "workspace-legacy", "/tmp/work-legacy")
	if err != nil {
		t.Fatalf("create legacy session: %v", err)
	}
	if _, err := store.AppendEvent("step1", "message", map[string]any{"role": "user", "content": "hello"}); err != nil {
		t.Fatalf("append event: %v", err)
	}

	if _, err := OpenByID(root, store.Meta().SessionID); err == nil {
		t.Fatal("expected legacy persistence root layout to be ignored")
	}
}

func TestReadEventsIgnoresTrailingTruncatedEOFLine(t *testing.T) {
	root := t.TempDir()
	store, err := Create(root, "workspace-x", "/tmp/work")
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	if _, err := store.AppendEvent("s1", "message", map[string]any{"role": "user", "content": "u1"}); err != nil {
		t.Fatalf("append event: %v", err)
	}

	fp, err := os.OpenFile(filepath.Join(store.Dir(), eventsFile), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open events for append: %v", err)
	}
	if _, err := fp.WriteString("{\"seq\":2"); err != nil {
		_ = fp.Close()
		t.Fatalf("append truncated line: %v", err)
	}
	if err := fp.Close(); err != nil {
		t.Fatalf("close events file: %v", err)
	}

	events, err := store.ReadEvents()
	if err != nil {
		t.Fatalf("read events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events len = %d, want 1", len(events))
	}
	if events[0].Seq != 1 {
		t.Fatalf("expected seq=1, got %d", events[0].Seq)
	}
}

func TestAppendEventRepairsTruncatedTailBeforeAppend(t *testing.T) {
	root := t.TempDir()
	store, err := Create(root, "workspace-x", "/tmp/work")
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	if _, err := store.AppendEvent("s1", "message", map[string]any{"role": "user", "content": "u1"}); err != nil {
		t.Fatalf("append event 1: %v", err)
	}

	fp, err := os.OpenFile(filepath.Join(store.Dir(), eventsFile), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open events for append: %v", err)
	}
	if _, err := fp.WriteString("{\"seq\":2"); err != nil {
		_ = fp.Close()
		t.Fatalf("append truncated tail: %v", err)
	}
	if err := fp.Close(); err != nil {
		t.Fatalf("close events file: %v", err)
	}

	e2, err := store.AppendEvent("s2", "message", map[string]any{"role": "assistant", "content": "a2"})
	if err != nil {
		t.Fatalf("append event 2: %v", err)
	}
	if e2.Seq != 2 {
		t.Fatalf("expected seq=2, got %d", e2.Seq)
	}

	events, err := store.ReadEvents()
	if err != nil {
		t.Fatalf("read events: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("events len = %d, want 2", len(events))
	}
	if events[0].Seq != 1 || events[1].Seq != 2 {
		t.Fatalf("unexpected event sequence: %+v", events)
	}
}

func TestOpenReconcilesMetaLastSequenceFromEventLog(t *testing.T) {
	root := t.TempDir()
	store, err := Create(root, "workspace-x", "/tmp/work")
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	if _, err := store.AppendEvent("s1", "message", map[string]any{"role": "user", "content": "u1"}); err != nil {
		t.Fatalf("append event 1: %v", err)
	}
	if _, err := store.AppendEvent("s2", "message", map[string]any{"role": "assistant", "content": "a1"}); err != nil {
		t.Fatalf("append event 2: %v", err)
	}

	sessionPath := filepath.Join(store.Dir(), sessionFile)
	data, err := os.ReadFile(sessionPath)
	if err != nil {
		t.Fatalf("read session file: %v", err)
	}
	var meta Meta
	if err := json.Unmarshal(data, &meta); err != nil {
		t.Fatalf("decode session meta: %v", err)
	}
	meta.LastSequence = 0
	rewritten, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		t.Fatalf("encode session meta: %v", err)
	}
	if err := os.WriteFile(sessionPath, rewritten, 0o644); err != nil {
		t.Fatalf("write session file: %v", err)
	}

	reopened, err := Open(store.Dir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if reopened.Meta().LastSequence != 2 {
		t.Fatalf("expected reconciled last sequence 2, got %d", reopened.Meta().LastSequence)
	}
	next, err := reopened.AppendEvent("s3", "message", map[string]any{"role": "user", "content": "u2"})
	if err != nil {
		t.Fatalf("append event after reconcile: %v", err)
	}
	if next.Seq != 3 {
		t.Fatalf("expected seq=3 after reopen reconciliation, got %d", next.Seq)
	}
}

func TestPeriodicCompactionRewritesCanonicalEventsLog(t *testing.T) {
	root := t.TempDir()
	store, err := Create(
		root,
		"workspace-x",
		"/tmp/work",
		WithEventLogCompaction(1, 1),
		WithEventLogFSyncPolicy(EventLogFSyncNever),
	)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	if _, err := store.AppendEvent("s1", "message", map[string]any{"role": "user", "content": "u1"}); err != nil {
		t.Fatalf("append event 1: %v", err)
	}

	fp, err := os.OpenFile(filepath.Join(store.Dir(), eventsFile), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open events file: %v", err)
	}
	if _, err := fp.WriteString("\n\n"); err != nil {
		_ = fp.Close()
		t.Fatalf("append padding lines: %v", err)
	}
	if err := fp.Close(); err != nil {
		t.Fatalf("close events file: %v", err)
	}

	if _, err := store.AppendEvent("s2", "message", map[string]any{"role": "assistant", "content": "a1"}); err != nil {
		t.Fatalf("append event 2: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(store.Dir(), eventsFile))
	if err != nil {
		t.Fatalf("read events file: %v", err)
	}
	if strings.Contains(string(raw), "\n\n") {
		t.Fatalf("expected compaction to remove blank lines from events log")
	}
}

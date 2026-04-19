package runtime

import (
	"testing"

	"builder/server/llm"
	"builder/server/session"
)

func TestResolvePersistedUserMessageIndexCountsHistoryReplacementEntries(t *testing.T) {
	store := createPersistedTranscriptIndexStore(t)
	if _, err := store.AppendEvent("step-1", "message", llm.Message{Role: llm.RoleUser, Content: "before compaction"}); err != nil {
		t.Fatalf("append first user message: %v", err)
	}
	if _, err := store.AppendEvent("step-1", "message", llm.Message{Role: llm.RoleAssistant, Content: "answer", Phase: llm.MessagePhaseFinal}); err != nil {
		t.Fatalf("append assistant message: %v", err)
	}
	if _, err := store.AppendEvent("step-compact", "history_replaced", historyReplacementPayload{Items: llm.ItemsFromMessages([]llm.Message{{Role: llm.RoleUser, MessageType: llm.MessageTypeCompactionSummary, Content: "summary"}})}); err != nil {
		t.Fatalf("append history_replaced: %v", err)
	}
	if _, err := store.AppendEvent("step-2", "message", llm.Message{Role: llm.RoleUser, Content: "after compaction"}); err != nil {
		t.Fatalf("append second user message: %v", err)
	}

	got, err := ResolvePersistedUserMessageIndex(walkPersistedTranscriptIndexEvents(store), 3)
	if err != nil {
		t.Fatalf("ResolvePersistedUserMessageIndex: %v", err)
	}
	if got != 2 {
		t.Fatalf("user message index = %d, want 2", got)
	}
}

func TestResolvePersistedUserMessageIndexSkipsNoopFinalEntries(t *testing.T) {
	store := createPersistedTranscriptIndexStore(t)
	if _, err := store.AppendEvent("step-1", "message", llm.Message{Role: llm.RoleUser, Content: "u1"}); err != nil {
		t.Fatalf("append first user message: %v", err)
	}
	if _, err := store.AppendEvent("step-1", "message", llm.Message{Role: llm.RoleAssistant, Content: reviewerNoopToken, Phase: llm.MessagePhaseFinal}); err != nil {
		t.Fatalf("append noop final message: %v", err)
	}
	if _, err := store.AppendEvent("step-2", "message", llm.Message{Role: llm.RoleUser, Content: "u2"}); err != nil {
		t.Fatalf("append second user message: %v", err)
	}

	got, err := ResolvePersistedUserMessageIndex(walkPersistedTranscriptIndexEvents(store), 1)
	if err != nil {
		t.Fatalf("ResolvePersistedUserMessageIndex: %v", err)
	}
	if got != 2 {
		t.Fatalf("user message index = %d, want 2", got)
	}
}

func createPersistedTranscriptIndexStore(t *testing.T) *session.Store {
	t.Helper()
	store, err := session.Create(t.TempDir(), "ws", t.TempDir())
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	return store
}

func walkPersistedTranscriptIndexEvents(store *session.Store) func(func(session.Event) error) error {
	return func(visit func(session.Event) error) error {
		return store.WalkEvents(func(evt session.Event) error {
			return visit(evt)
		})
	}
}

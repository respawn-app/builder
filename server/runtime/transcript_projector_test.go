package runtime

import (
	"encoding/json"
	"testing"

	"builder/server/llm"
	"builder/server/session"
	"builder/server/tools"
)

func TestTranscriptProjectorReconstructsPersistedTranscript(t *testing.T) {
	projector := NewTranscriptProjector()
	toolOutput, err := json.Marshal(map[string]any{"ok": true})
	if err != nil {
		t.Fatalf("marshal tool output: %v", err)
	}
	events := []session.Event{
		mustPersistedEvent(t, "message", llm.Message{Role: llm.RoleUser, Content: "hello"}),
		mustPersistedEvent(t, "message", llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "call-1", Name: string(tools.ToolShell), Input: json.RawMessage(`{"command":"pwd"}`)}}}),
		mustPersistedEvent(t, "tool_completed", map[string]any{"call_id": "call-1", "name": string(tools.ToolShell), "output": json.RawMessage(toolOutput)}),
		mustPersistedEvent(t, "local_entry", storedLocalEntry{Role: "system", Text: "persisted note"}),
		mustPersistedEvent(t, "message", llm.Message{Role: llm.RoleAssistant, Content: "final answer", Phase: llm.MessagePhaseFinal}),
	}
	for _, evt := range events {
		if err := projector.ApplyPersistedEvent(evt); err != nil {
			t.Fatalf("ApplyPersistedEvent(%q): %v", evt.Kind, err)
		}
	}

	snapshot := projector.ChatSnapshot()
	if len(snapshot.Entries) != 5 {
		t.Fatalf("entry count = %d, want 5", len(snapshot.Entries))
	}
	if snapshot.Entries[1].Role != "tool_call" {
		t.Fatalf("entry[1].Role = %q, want tool_call", snapshot.Entries[1].Role)
	}
	if snapshot.Entries[2].Role != "tool_result_ok" {
		t.Fatalf("entry[2].Role = %q, want tool_result_ok", snapshot.Entries[2].Role)
	}
	if snapshot.Entries[3].Role != "system" || snapshot.Entries[3].Text != "persisted note" {
		t.Fatalf("unexpected local entry: %+v", snapshot.Entries[3])
	}
	if got := projector.LastCommittedAssistantFinalAnswer(); got != "final answer" {
		t.Fatalf("LastCommittedAssistantFinalAnswer() = %q, want final answer", got)
	}
}

func mustPersistedEvent(t *testing.T, kind string, payload any) session.Event {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal %q payload: %v", kind, err)
	}
	return session.Event{Kind: kind, Payload: body}
}

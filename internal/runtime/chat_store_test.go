package runtime

import (
	"builder/internal/llm"
	"builder/internal/tools"
	"encoding/json"
	"strings"
	"testing"
)

func TestChatStoreSnapshotProjectsConversation(t *testing.T) {
	s := newChatStore()
	s.appendMessage(llm.Message{Role: llm.RoleUser, Content: "hello"})
	s.appendMessage(llm.Message{
		Role: llm.RoleAssistant,
		ToolCalls: []llm.ToolCall{
			{ID: "call_1", Name: "bash", Input: json.RawMessage(`{"command":"pwd"}`)},
		},
	})
	s.recordToolCompletion(tools.Result{
		CallID:  "call_1",
		Name:    tools.ToolBash,
		IsError: false,
		Output:  json.RawMessage(`{"stdout":"/tmp"}`),
	})
	s.appendMessage(llm.Message{
		Role:       llm.RoleTool,
		ToolCallID: "call_1",
		Name:       string(tools.ToolBash),
		Content:    `{"stdout":"/tmp"}`,
	})
	s.appendMessage(llm.Message{Role: llm.RoleAssistant, Content: "done"})

	s.appendOngoingDelta("stream")
	s.setOngoingError("failed")
	s.appendLocalEntry("system", "note")

	snap := s.snapshot()
	if len(snap.Entries) != 5 {
		t.Fatalf("expected 5 entries, got %d (%+v)", len(snap.Entries), snap.Entries)
	}
	if snap.Entries[0].Role != "user" || snap.Entries[0].Text != "hello" {
		t.Fatalf("unexpected first entry: %+v", snap.Entries[0])
	}
	if snap.Entries[1].Role != "tool_call" || !strings.Contains(snap.Entries[1].Text, "id=call_1") {
		t.Fatalf("unexpected tool_call entry: %+v", snap.Entries[1])
	}
	if snap.Entries[2].Role != "tool_result" || !strings.Contains(snap.Entries[2].Text, "id=call_1 name=bash error=false") {
		t.Fatalf("unexpected tool_result entry: %+v", snap.Entries[2])
	}
	if snap.Entries[3].Role != "assistant" || snap.Entries[3].Text != "done" {
		t.Fatalf("unexpected assistant entry: %+v", snap.Entries[3])
	}
	if snap.Entries[4].Role != "system" || snap.Entries[4].Text != "note" {
		t.Fatalf("unexpected local entry: %+v", snap.Entries[4])
	}
	if snap.Ongoing != "stream" {
		t.Fatalf("unexpected ongoing text: %q", snap.Ongoing)
	}
	if snap.OngoingError != "failed" {
		t.Fatalf("unexpected ongoing error: %q", snap.OngoingError)
	}
}

func TestChatStoreFiltersInjectedAgentsMessage(t *testing.T) {
	s := newChatStore()
	s.appendMessage(llm.Message{Role: llm.RoleUser, Content: agentsInjectedPrefix + "\nsource: /tmp/AGENTS.md"})
	s.appendMessage(llm.Message{Role: llm.RoleUser, Content: "real"})

	snap := s.snapshot()
	if len(snap.Entries) != 1 {
		t.Fatalf("expected 1 visible entry, got %d (%+v)", len(snap.Entries), snap.Entries)
	}
	if snap.Entries[0].Text != "real" {
		t.Fatalf("unexpected visible entry: %+v", snap.Entries[0])
	}
}

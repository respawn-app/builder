package runtime

import (
	"builder/server/llm"
	"encoding/json"
	"testing"
)

func TestTranscriptEntriesFromEventBuildsToolCallFallbackWithoutPresentation(t *testing.T) {
	entries := TranscriptEntriesFromEvent(Event{
		Kind: EventToolCallStarted,
		ToolCall: &llm.ToolCall{
			ID:    "call-1",
			Name:  "shell",
			Input: json.RawMessage(`{"command":"pwd"}`),
		},
	})
	if len(entries) != 1 {
		t.Fatalf("expected one transcript entry, got %+v", entries)
	}
	entry := entries[0]
	if entry.Role != "tool_call" {
		t.Fatalf("entry role = %q, want tool_call", entry.Role)
	}
	if entry.Text != "pwd" {
		t.Fatalf("entry text = %q, want pwd", entry.Text)
	}
	if entry.ToolCall == nil || !entry.ToolCall.IsShell {
		t.Fatalf("expected rebuilt shell tool metadata, got %+v", entry.ToolCall)
	}
	if entry.ToolCall.Command != "pwd" {
		t.Fatalf("tool metadata command = %q, want pwd", entry.ToolCall.Command)
	}
}

func TestNormalizeToolCallForTranscriptRepairsMalformedPresentation(t *testing.T) {
	normalized := normalizeToolCallForTranscript(llm.ToolCall{
		ID:           "call-1",
		Name:         "shell",
		Presentation: json.RawMessage(`{"broken":`),
		Input:        json.RawMessage(`{"command":"pwd"}`),
	}, "/tmp")
	meta := decodeToolCallMeta(normalized)
	if meta == nil {
		t.Fatal("expected rebuilt tool presentation metadata")
	}
	if !meta.IsShell {
		t.Fatalf("expected rebuilt shell metadata, got %+v", meta)
	}
	if meta.Command != "pwd" {
		t.Fatalf("rebuilt command = %q, want pwd", meta.Command)
	}
}

package runtime

import (
	"builder/server/llm"
	"builder/server/tools"
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

func TestTranscriptEntriesFromEventEmitsVisibleToolCompletionEntriesForOrdinaryAndTriggerHandoffTools(t *testing.T) {
	testCases := []struct {
		name   string
		result tools.Result
	}{
		{
			name: "ordinary shell result",
			result: tools.Result{
				CallID: "call-shell-1",
				Name:   tools.ToolShell,
				Output: json.RawMessage(`{"output":"/tmp","exit_code":0,"truncated":false}`),
			},
		},
		{
			name: "trigger handoff synthetic success result",
			result: tools.Result{
				CallID: "call-handoff-1",
				Name:   tools.ToolTriggerHandoff,
				Output: json.RawMessage(`""`),
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			entries := TranscriptEntriesFromEvent(Event{
				Kind:       EventToolCallCompleted,
				ToolResult: &tc.result,
			})
			if len(entries) != 1 {
				t.Fatalf("expected one visible transcript entry, got %+v", entries)
			}
			entry := entries[0]
			if entry.Role != "tool_result_ok" {
				t.Fatalf("entry role = %q, want tool_result_ok", entry.Role)
			}
			if entry.ToolCallID != tc.result.CallID {
				t.Fatalf("entry tool call id = %q, want %q", entry.ToolCallID, tc.result.CallID)
			}
		})
	}
}

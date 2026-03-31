package tui

import (
	"testing"

	"builder/shared/transcript"
)

func TestReduceAppendTranscriptMsgReportsMutationFlagsAndNormalizesEntry(t *testing.T) {
	m := NewModel()
	var result modelUpdateResult
	hint := &transcript.ToolCallMeta{ToolName: "shell", Command: "pwd"}

	m.reduceAppendTranscriptMsg(AppendTranscriptMsg{Role: "  ", Text: "hello", ToolCallID: " call_1 ", ToolCall: hint}, &result)

	if !result.autoFollowOngoing || !result.ongoingChanged || !result.detailChanged {
		t.Fatalf("expected append transcript reducer to mark transcript refresh flags, got %+v", result)
	}
	if len(m.transcript) != 1 {
		t.Fatalf("expected one transcript entry, got %d", len(m.transcript))
	}
	entry := m.transcript[0]
	if entry.Role != "unknown" {
		t.Fatalf("expected empty role to normalize to unknown, got %q", entry.Role)
	}
	if entry.ToolCallID != "call_1" {
		t.Fatalf("expected trimmed tool call id, got %q", entry.ToolCallID)
	}
	if entry.ToolCall == nil || entry.ToolCall == hint {
		t.Fatalf("expected cloned tool call metadata, got %#v", entry.ToolCall)
	}
}

func TestReduceSetConversationMsgNormalizesEntriesAndClearsInvalidSelection(t *testing.T) {
	m := NewModel()
	m.selectedTranscriptEntry = 5
	m.selectedTranscriptActive = true
	var result modelUpdateResult

	m.reduceSetConversationMsg(SetConversationMsg{
		Entries:      []TranscriptEntry{{Role: "assistant", Text: "a", ToolCallID: " call_a ", ToolCall: &transcript.ToolCallMeta{ToolName: "shell"}}},
		Ongoing:      "stream",
		OngoingError: "  err  ",
	}, &result)

	if !result.autoFollowOngoing || !result.ongoingChanged || !result.detailChanged {
		t.Fatalf("expected set conversation reducer to mark transcript refresh flags, got %+v", result)
	}
	if len(m.transcript) != 1 {
		t.Fatalf("expected conversation to replace transcript entries, got %d", len(m.transcript))
	}
	if m.transcript[0].ToolCallID != "call_a" {
		t.Fatalf("expected trimmed tool call id, got %q", m.transcript[0].ToolCallID)
	}
	if m.ongoing != "stream" {
		t.Fatalf("expected ongoing text replacement, got %q", m.ongoing)
	}
	if m.ongoingError != "err" {
		t.Fatalf("expected trimmed ongoing error, got %q", m.ongoingError)
	}
	if m.selectedTranscriptActive {
		t.Fatal("expected invalid transcript selection to be cleared")
	}
}

func TestApplyUpdateResultAutoFollowsOngoingAtBottom(t *testing.T) {
	m := NewModel(WithPreviewLines(2))
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "one"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "two"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "three"})
	if got, want := m.ongoingScroll, m.maxOngoingScroll(); got != want {
		t.Fatalf("expected setup at bottom, got %d want %d", got, want)
	}

	m.ongoing = ""
	m.applyUpdateResult(modelUpdateResult{autoFollowOngoing: true, ongoingChanged: true}, true)

	if got, want := m.ongoingScroll, m.maxOngoingScroll(); got != want {
		t.Fatalf("expected auto follow to keep ongoing at bottom, got %d want %d", got, want)
	}
}

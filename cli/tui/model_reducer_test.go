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

func TestReduceAppendTranscriptMsgAdvancesTotalEntriesFromTailWindow(t *testing.T) {
	m := NewModel()
	m.transcriptBaseOffset = 250
	m.transcriptTotalEntries = 252
	m.transcript = []TranscriptEntry{{Role: "assistant", Text: "existing"}, {Role: "assistant", Text: "tail"}}
	var result modelUpdateResult

	m.reduceAppendTranscriptMsg(AppendTranscriptMsg{Role: "assistant", Text: "new tail"}, &result)

	if got, want := m.transcriptTotalEntries, 253; got != want {
		t.Fatalf("transcriptTotalEntries = %d, want %d", got, want)
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

func TestReduceSetViewportSizeMsgNoopWhenSizeUnchanged(t *testing.T) {
	m := NewModel()
	m.viewportLines = 20
	m.viewportWidth = 80
	m.ongoingBaseDirty = false
	m.ongoingDirty = false
	m.detailDirty = false

	next, _ := m.Update(SetViewportSizeMsg{Lines: 20, Width: 80})
	updated := next.(Model)

	if updated.viewportLines != 20 || updated.viewportWidth != 80 {
		t.Fatalf("expected viewport to remain unchanged, got lines=%d width=%d", updated.viewportLines, updated.viewportWidth)
	}
	if updated.ongoingDirty || updated.detailDirty {
		t.Fatalf("expected unchanged viewport update to avoid dirtying snapshots, got ongoingDirty=%v detailDirty=%v", updated.ongoingDirty, updated.detailDirty)
	}
}

func TestReduceStreamAssistantMsgInvalidatesDetailOnlyOnceWhileDirty(t *testing.T) {
	m := NewModel()
	m.detailDirty = false

	var first modelUpdateResult
	m.reduceStreamAssistantMsg(StreamAssistantMsg{Delta: "a"}, &first)
	if !first.detailChanged {
		t.Fatal("expected first streaming delta to invalidate detail snapshot")
	}
	m.applyUpdateResult(first, true)
	if !m.detailDirty {
		t.Fatal("expected detail snapshot marked dirty after first streaming delta")
	}

	var second modelUpdateResult
	m.reduceStreamAssistantMsg(StreamAssistantMsg{Delta: "b"}, &second)
	if second.detailChanged {
		t.Fatal("expected repeated streaming deltas to avoid redundant detail invalidation while already dirty")
	}
}

package tui

import (
	"strings"
	"testing"

	"builder/internal/transcript"
)

func TestCommittedOngoingProjectionRenderAppendDeltaFromAppendedEntry(t *testing.T) {
	m := NewModel(WithPreviewLines(20))
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "seed"})
	previous := m.CommittedOngoingProjection()

	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "tail"})
	current := m.CommittedOngoingProjection()
	delta, ok := current.RenderAppendDeltaFrom(previous, TranscriptDivider)
	if !ok {
		t.Fatal("expected append-only committed projection delta")
	}
	if !strings.Contains(delta, "tail") {
		t.Fatalf("expected delta to include appended tail, got %q", delta)
	}
	if strings.Contains(delta, "seed") {
		t.Fatalf("expected delta to exclude already committed prefix, got %q", delta)
	}
}

func TestCommittedOngoingProjectionCommitFrontierWaitsForToolResult(t *testing.T) {
	m := NewModel(WithPreviewLines(20))
	m = updateModel(t, m, AppendTranscriptMsg{Role: "user", Text: "prompt"})
	base := m.CommittedOngoingProjection()

	m = updateModel(t, m, AppendTranscriptMsg{
		Role:       "tool_call",
		Text:       "pwd",
		ToolCallID: "call_1",
		ToolCall:   &transcript.ToolCallMeta{ToolName: "shell", IsShell: true, Command: "pwd"},
	})
	pending := m.CommittedOngoingProjection()
	if rendered := pending.Render(TranscriptDivider); strings.Contains(rendered, "pwd") {
		t.Fatalf("expected unresolved tool call to stay out of committed projection, got %q", rendered)
	}

	m = updateModel(t, m, AppendTranscriptMsg{Role: "tool_result_ok", Text: "/tmp", ToolCallID: "call_1"})
	current := m.CommittedOngoingProjection()
	delta, ok := current.RenderAppendDeltaFrom(base, TranscriptDivider)
	if !ok {
		t.Fatal("expected tool completion to extend committed projection")
	}
	if !strings.Contains(delta, "pwd") {
		t.Fatalf("expected committed delta to include finalized tool call, got %q", delta)
	}
	if strings.Contains(delta, "prompt") {
		t.Fatalf("expected committed delta to exclude previously emitted prompt, got %q", delta)
	}
}

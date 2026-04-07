package tui

import (
	"strings"
	"testing"

	"builder/shared/transcript"
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

func TestRenderAppendDeltaFromIgnoresHiddenSourceIndexShifts(t *testing.T) {
	previous := TranscriptProjection{Blocks: []TranscriptProjectionBlock{{
		Role:         "user",
		DividerGroup: "user",
		EntryIndex:   0,
		EntryEnd:     0,
		Lines:        []string{"❯ trigger"},
	}}}
	current := TranscriptProjection{Blocks: []TranscriptProjectionBlock{
		{
			Role:         "user",
			DividerGroup: "user",
			EntryIndex:   3,
			EntryEnd:     3,
			Lines:        []string{"❯ trigger"},
		},
		{
			Role:         "assistant",
			DividerGroup: "assistant",
			EntryIndex:   4,
			EntryEnd:     4,
			Lines:        []string{"❮ FINAL-CONTENT"},
		},
	}}

	delta, ok := current.RenderAppendDeltaFrom(previous, TranscriptDivider)
	if !ok {
		t.Fatal("expected append delta to survive hidden source index shifts")
	}
	if !strings.Contains(delta, "FINAL-CONTENT") {
		t.Fatalf("expected delta to include appended assistant content, got %q", delta)
	}
	if strings.Contains(delta, "trigger") {
		t.Fatalf("expected delta to exclude already rendered user content, got %q", delta)
	}
}

func TestTranscriptProjectionSharedPrefixBlockCountStopsAtFirstDivergence(t *testing.T) {
	previous := TranscriptProjection{Blocks: []TranscriptProjectionBlock{
		{Role: "user", DividerGroup: "user", Lines: []string{"❯ prompt"}},
		{Role: "assistant", DividerGroup: "assistant", Lines: []string{"❮ before"}},
		{Role: "user", DividerGroup: "user", Lines: []string{"❯ later"}},
	}}
	current := TranscriptProjection{Blocks: []TranscriptProjectionBlock{
		{Role: "user", DividerGroup: "user", Lines: []string{"❯ prompt"}},
		{Role: "assistant", DividerGroup: "assistant", Lines: []string{"❮ after"}},
		{Role: "user", DividerGroup: "user", Lines: []string{"❯ later"}},
	}}

	if got := current.SharedPrefixBlockCount(previous); got != 1 {
		t.Fatalf("expected shared prefix to stop before divergent assistant block, got %d", got)
	}
}

func TestTranscriptProjectionSharedPrefixBlockCountUsesShorterProjectionLength(t *testing.T) {
	previous := TranscriptProjection{Blocks: []TranscriptProjectionBlock{
		{Role: "assistant", DividerGroup: "assistant", Lines: []string{"❮ one"}},
		{Role: "assistant", DividerGroup: "assistant", Lines: []string{"❮ two"}},
	}}
	current := TranscriptProjection{Blocks: []TranscriptProjectionBlock{
		{Role: "assistant", DividerGroup: "assistant", Lines: []string{"❮ one"}},
		{Role: "assistant", DividerGroup: "assistant", Lines: []string{"❮ two"}},
		{Role: "assistant", DividerGroup: "assistant", Lines: []string{"❮ three"}},
	}}

	if got := current.SharedPrefixBlockCount(previous); got != 2 {
		t.Fatalf("expected shared prefix to include all shorter matching blocks, got %d", got)
	}
}

func TestCommittedOngoingEntriesDoNotTruncateAfterEmptyToolResult(t *testing.T) {
	entries := []TranscriptEntry{
		{Role: "user", Text: "prompt"},
		{Role: "tool_call", Text: "apply patch", ToolCallID: "call_patch", ToolCall: &transcript.ToolCallMeta{ToolName: "patch"}},
		{Role: "tool_result_ok", Text: "", ToolCallID: "call_patch"},
		{Role: "assistant", Text: "continued after empty result"},
	}

	committed := CommittedOngoingEntries(entries)
	if len(committed) != 3 {
		t.Fatalf("expected empty tool result to resolve committed frontier without rendering itself, got %#v", committed)
	}
	if committed[2].Role != "assistant" || committed[2].Text != "continued after empty result" {
		t.Fatalf("expected committed entries to include content after empty tool result, got %#v", committed)
	}

	pending := PendingOngoingEntries(entries)
	if len(pending) != 0 {
		t.Fatalf("expected no pending entries after empty tool result resolution, got %#v", pending)
	}
}

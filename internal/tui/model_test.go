package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestModeTogglePreservesOngoingScroll(t *testing.T) {
	m := NewModel(WithPreviewLines(2))
	m = updateModel(t, m, StreamAssistantMsg{Delta: "l1\nl2\nl3\nl4"})
	m = updateModel(t, m, ScrollOngoingMsg{Delta: 1})

	if got := m.OngoingScroll(); got != 1 {
		t.Fatalf("scroll before toggle = %d, want 1", got)
	}

	before := m.View()
	linesBefore := strings.Split(before, "\n")
	if len(linesBefore) != 2 {
		t.Fatalf("ongoing lines = %d, want 2", len(linesBefore))
	}
	if strings.TrimSpace(linesBefore[0]) != "l2" || strings.TrimSpace(linesBefore[1]) != "l3" {
		t.Fatalf("unexpected ongoing view before toggle: %q", before)
	}

	m = updateModel(t, m, ToggleModeMsg{})
	if got := m.Mode(); got != ModeDetail {
		t.Fatalf("mode after first toggle = %q, want %q", got, ModeDetail)
	}

	m = updateModel(t, m, ToggleModeMsg{})
	if got := m.Mode(); got != ModeOngoing {
		t.Fatalf("mode after second toggle = %q, want %q", got, ModeOngoing)
	}
	if got := m.OngoingScroll(); got != 1 {
		t.Fatalf("scroll after roundtrip toggle = %d, want 1", got)
	}

	after := m.View()
	if after != before {
		t.Fatalf("ongoing view changed after roundtrip toggle:\nbefore=%q\nafter=%q", before, after)
	}
}

func TestOngoingShowsFullConversationContext(t *testing.T) {
	m := NewModel(WithPreviewLines(20))
	m = updateModel(t, m, AppendTranscriptMsg{Role: "user", Text: "first question"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "first answer"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "user", Text: "second question"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "second answer"})

	view := m.View()
	if !strings.Contains(view, "❯ first question") {
		t.Fatalf("expected first user message in ongoing view, got %q", view)
	}
	if !strings.Contains(view, "❮ first answer") {
		t.Fatalf("expected first assistant message in ongoing view, got %q", view)
	}
	if !strings.Contains(view, "❯ second question") {
		t.Fatalf("expected second user message in ongoing view, got %q", view)
	}
	if !strings.Contains(view, "❮ second answer") {
		t.Fatalf("expected second assistant message in ongoing view, got %q", view)
	}
}

func TestDetailSnapshotIsStaticUntilRetoggle(t *testing.T) {
	m := NewModel()
	m = updateModel(t, m, AppendTranscriptMsg{Role: "user", Text: "question"})
	m = updateModel(t, m, StreamAssistantMsg{Delta: "alpha"})
	m = updateModel(t, m, ToggleModeMsg{})

	snapshot := m.View()
	if !strings.Contains(snapshot, "❮ alpha") {
		t.Fatalf("detail snapshot missing assistant stream: %q", snapshot)
	}

	m = updateModel(t, m, StreamAssistantMsg{Delta: " beta"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "tool", Text: "ran"})

	if got := m.View(); got != snapshot {
		t.Fatalf("detail snapshot changed while in detail mode:\ninitial=%q\ncurrent=%q", snapshot, got)
	}

	m = updateModel(t, m, ToggleModeMsg{})
	m = updateModel(t, m, ToggleModeMsg{})
	refreshed := m.View()

	if refreshed == snapshot {
		t.Fatalf("detail snapshot did not refresh after mode roundtrip")
	}
	if !strings.Contains(refreshed, "❮ alpha beta") {
		t.Fatalf("refreshed snapshot missing full assistant stream: %q", refreshed)
	}
	if !strings.Contains(refreshed, "• ran") {
		t.Fatalf("refreshed snapshot missing new transcript entry: %q", refreshed)
	}
}

func TestClearOngoingAssistantMsgDropsPartialStream(t *testing.T) {
	m := NewModel()
	m = updateModel(t, m, StreamAssistantMsg{Delta: "partial"})
	m = updateModel(t, m, ClearOngoingAssistantMsg{})
	m = updateModel(t, m, StreamAssistantMsg{Delta: "final"})
	m = updateModel(t, m, CommitAssistantMsg{})
	m = updateModel(t, m, ToggleModeMsg{})

	snapshot := m.View()
	if strings.Contains(snapshot, "❮ partial") {
		t.Fatalf("snapshot should not contain discarded attempt delta: %q", snapshot)
	}
	if !strings.Contains(snapshot, "❮ final") {
		t.Fatalf("snapshot missing committed final assistant output: %q", snapshot)
	}
}

func TestOngoingShowsCommittedAssistantAfterCommit(t *testing.T) {
	m := NewModel(WithPreviewLines(3))
	m = updateModel(t, m, StreamAssistantMsg{Delta: "line1\nline2"})
	m = updateModel(t, m, CommitAssistantMsg{})

	view := m.View()
	if !strings.Contains(view, "line1") || !strings.Contains(view, "line2") {
		t.Fatalf("ongoing view should keep committed assistant visible, got %q", view)
	}
}

func TestDetailUsesRequestedSymbolsAndDividers(t *testing.T) {
	m := NewModel()
	m = updateModel(t, m, AppendTranscriptMsg{Role: "user", Text: "hello"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "hi"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "tool_call", Text: "call"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "tool_result", Text: "result"})
	m = updateModel(t, m, ToggleModeMsg{})

	view := m.View()
	if !strings.Contains(view, "❯ hello") {
		t.Fatalf("expected user symbol, got %q", view)
	}
	if !strings.Contains(view, "❮ hi") {
		t.Fatalf("expected assistant symbol, got %q", view)
	}
	if !strings.Contains(view, "•") || !strings.Contains(view, "call") || !strings.Contains(view, "result") {
		t.Fatalf("expected tool call/result pair with tool symbol, got %q", view)
	}
	if got := strings.Count(view, strings.Repeat("─", 24)); got != 2 {
		t.Fatalf("expected 2 dividers for 3 blocks, got %d in %q", got, view)
	}
}

func TestOngoingCompactsToolCallAndHidesThinking(t *testing.T) {
	m := NewModel(WithPreviewLines(20))
	m = updateModel(t, m, AppendTranscriptMsg{Role: "user", Text: "run command"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "thinking", Text: "internal trace"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "tool_call", Text: "pwd" + toolInlineMetaSep + "timeout: 5m\nworkdir: /tmp"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "tool_result_ok", Text: "/tmp"})

	view := m.View()
	if strings.Contains(view, "internal trace") {
		t.Fatalf("expected thinking trace hidden in ongoing view, got %q", view)
	}
	if !strings.Contains(view, "pwd") {
		t.Fatalf("expected compact one-line tool input in ongoing view, got %q", view)
	}
	if strings.Contains(view, "workdir: /tmp") {
		t.Fatalf("expected tool input to stay one line in ongoing view, got %q", view)
	}
	if strings.Contains(view, "/tmp") {
		t.Fatalf("expected tool output to be omitted in ongoing view, got %q", view)
	}
}

func TestDetailToolFormattingShowsTimeoutAndInlineOutput(t *testing.T) {
	m := NewModel()
	m = updateModel(t, m, SetViewportSizeMsg{Lines: 20, Width: 80})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "tool_call", Text: "pwd" + toolInlineMetaSep + "timeout: 5m"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "tool_result_ok", Text: "alpha\nbeta"})
	m = updateModel(t, m, ToggleModeMsg{})

	view := m.View()
	lines := strings.Split(view, "\n")
	if len(lines) == 0 || !strings.Contains(lines[0], "pwd") || !strings.Contains(lines[0], "timeout: 5m") {
		t.Fatalf("expected first detail line to contain command and timeout: %q", view)
	}
	if strings.Contains(view, "output:") {
		t.Fatalf("expected no output prefix in detail view, got %q", view)
	}
	if len(lines) < 2 || strings.TrimSpace(lines[1]) != "alpha" {
		t.Fatalf("expected output to start immediately after command line, got %q", view)
	}
}

func TestToolBlockRoleFromResult(t *testing.T) {
	if got := toolBlockRoleFromResult("tool_result_ok"); got != "tool_success" {
		t.Fatalf("unexpected role for success result: %q", got)
	}
	if got := toolBlockRoleFromResult("tool_result_error"); got != "tool_error" {
		t.Fatalf("unexpected role for error result: %q", got)
	}
	if got := toolBlockRoleFromResult("tool_result"); got != "tool_success" {
		t.Fatalf("unexpected role for legacy result: %q", got)
	}
}

func TestFormatOngoingErrorIsNotTruncated(t *testing.T) {
	input := strings.Repeat("e", 300)
	formatted := FormatOngoingError(errString(input))
	if formatted != "error: "+input {
		t.Fatalf("unexpected formatted error: %q", formatted)
	}
}

func TestDetailRendersMarkdownForUserAndAssistant(t *testing.T) {
	m := NewModel()
	m = updateModel(t, m, SetViewportSizeMsg{Lines: 20, Width: 80})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "user", Text: "**bold** and `code`"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "- one\n- two"})
	m = updateModel(t, m, ToggleModeMsg{})

	view := m.View()
	if !strings.Contains(view, "❯") || !strings.Contains(view, "❮") {
		t.Fatalf("expected user/assistant prefixes in view: %q", view)
	}
	if strings.Contains(view, "**bold**") || strings.Contains(view, "`code`") {
		t.Fatalf("expected markdown formatting to be rendered, got raw markdown: %q", view)
	}
	if !strings.Contains(view, "bold") || !strings.Contains(view, "code") {
		t.Fatalf("expected rendered markdown text to remain visible: %q", view)
	}
}

func TestOngoingStreamingStaysPlainUntilCommit(t *testing.T) {
	m := NewModel()
	m = updateModel(t, m, SetViewportSizeMsg{Lines: 6, Width: 60})
	m = updateModel(t, m, StreamAssistantMsg{Delta: "**bold**"})

	streaming := m.View()
	if !strings.Contains(streaming, "**bold**") {
		t.Fatalf("expected plain markdown while streaming, got %q", streaming)
	}

	m = updateModel(t, m, CommitAssistantMsg{})
	committed := m.View()
	if strings.Contains(committed, "**bold**") {
		t.Fatalf("expected markdown rendering after commit, got %q", committed)
	}
	if !strings.Contains(committed, "bold") {
		t.Fatalf("expected committed rendered text to remain visible, got %q", committed)
	}
}

func TestViewportWidthChangeAffectsMarkdownRender(t *testing.T) {
	m := NewModel()
	m = updateModel(t, m, SetViewportSizeMsg{Lines: 10, Width: 24})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "This is **markdown** content that should wrap at different widths."})
	m = updateModel(t, m, ToggleModeMsg{})
	narrow := m.View()

	m = updateModel(t, m, ToggleModeMsg{})
	m = updateModel(t, m, SetViewportSizeMsg{Lines: 10, Width: 80})
	m = updateModel(t, m, ToggleModeMsg{})
	wide := m.View()

	if narrow == wide {
		t.Fatalf("expected markdown rendering to change with width; narrow and wide views are identical: %q", narrow)
	}
}

func TestNonMarkdownRolesStayPlain(t *testing.T) {
	m := NewModel()
	m = updateModel(t, m, SetViewportSizeMsg{Lines: 8, Width: 60})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "tool_result", Text: "**raw**"})
	m = updateModel(t, m, ToggleModeMsg{})

	view := m.View()
	if !strings.Contains(view, "**raw**") {
		t.Fatalf("expected tool text to remain plain, got %q", view)
	}
}

type errString string

func (e errString) Error() string {
	return string(e)
}

func updateModel(t *testing.T, m Model, msg tea.Msg) Model {
	t.Helper()

	next, _ := m.Update(msg)
	updated, ok := next.(Model)
	if !ok {
		t.Fatalf("unexpected model type %T", next)
	}
	return updated
}

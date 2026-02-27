package tui

import (
	"builder/internal/transcript"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"
)

func TestModeTogglePreservesOngoingScroll(t *testing.T) {
	m := NewModel(WithPreviewLines(2))
	m = updateModel(t, m, StreamAssistantMsg{Delta: "l1\nl2\nl3\nl4"})
	m = updateModel(t, m, ScrollOngoingMsg{Delta: -1})

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

func TestModeToggleReturnsToBottomWhenConversationGrewInDetail(t *testing.T) {
	m := NewModel(WithPreviewLines(2))
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "a1"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "a2"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "a3"})
	m = updateModel(t, m, ScrollOngoingMsg{Delta: -1})
	before := m.OngoingScroll()
	if before >= m.maxOngoingScroll() {
		t.Fatalf("expected to start above bottom, got %d", before)
	}

	m = updateModel(t, m, ToggleModeMsg{})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "a4"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "a5"})
	m = updateModel(t, m, ToggleModeMsg{})

	if got, want := m.OngoingScroll(), m.maxOngoingScroll(); got != want {
		t.Fatalf("expected ongoing to snap to bottom after growth in detail: got %d want %d", got, want)
	}
	view := plainTranscript(m.View())
	if !strings.Contains(view, "a5") {
		t.Fatalf("expected newest entry visible after returning from detail, got %q", view)
	}
}

func TestToggleToDetailStartsAtBottom(t *testing.T) {
	m := NewModel(WithPreviewLines(2))
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "a1"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "a2"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "a3"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "a4"})

	m = updateModel(t, m, ToggleModeMsg{})

	if got, want := m.detailScroll, m.maxDetailScroll(); got != want {
		t.Fatalf("detail scroll after toggle = %d, want bottom %d", got, want)
	}
	view := plainTranscript(m.View())
	if !strings.Contains(view, "a4") {
		t.Fatalf("expected detail toggle to show newest content, got %q", view)
	}
}

func TestOngoingShowsFullConversationContext(t *testing.T) {
	m := NewModel(WithPreviewLines(20))
	m = updateModel(t, m, AppendTranscriptMsg{Role: "user", Text: "first question"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "first answer"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "user", Text: "second question"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "second answer"})

	view := plainTranscript(m.View())
	if !containsInOrder(view, "❯", "first question", "❮", "first answer", "❯", "second question", "❮", "second answer") {
		t.Fatalf("expected first user message in ongoing view, got %q", view)
	}
}

func TestOngoingDoesNotPinOngoingErrorToBottomLine(t *testing.T) {
	m := NewModel(WithPreviewLines(4))
	m = updateModel(t, m, SetConversationMsg{
		Entries:      []TranscriptEntry{{Role: "assistant", Text: "line one"}},
		Ongoing:      "line two",
		OngoingError: "error: should not pin",
	})

	view := plainTranscript(m.View())
	if strings.Contains(view, "should not pin") {
		t.Fatalf("did not expect ongoing error to consume a fixed viewport line, got %q", view)
	}
	if !containsInOrder(view, "line one", "line two") {
		t.Fatalf("expected transcript content to remain visible, got %q", view)
	}
}

func TestErrorEntryIsRenderedWithPrefixAndErrorStyle(t *testing.T) {
	m := NewModel(WithPreviewLines(6))
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "ready"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "error", Text: "boom trace"})

	view := m.View()
	plain := plainTranscript(view)
	if !containsInOrder(plain, "❮", "ready", "!", "boom trace") {
		t.Fatalf("expected error entry in transcript history, got %q", plain)
	}
	renderedError := m.palette().error.Render("boom trace")
	if !strings.Contains(view, renderedError) {
		t.Fatalf("expected error text to use error style, got %q", view)
	}
}

func TestDetailSnapshotIsStaticUntilRetoggle(t *testing.T) {
	m := NewModel()
	m = updateModel(t, m, AppendTranscriptMsg{Role: "user", Text: "question"})
	m = updateModel(t, m, StreamAssistantMsg{Delta: "alpha"})
	m = updateModel(t, m, ToggleModeMsg{})

	snapshot := plainTranscript(m.View())
	if !containsInOrder(snapshot, "❮", "alpha") {
		t.Fatalf("detail snapshot missing assistant stream: %q", snapshot)
	}

	m = updateModel(t, m, StreamAssistantMsg{Delta: " beta"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "tool", Text: "ran"})

	if got := plainTranscript(m.View()); got != snapshot {
		t.Fatalf("detail snapshot changed while in detail mode:\ninitial=%q\ncurrent=%q", snapshot, got)
	}

	m = updateModel(t, m, ToggleModeMsg{})
	m = updateModel(t, m, ToggleModeMsg{})
	refreshed := plainTranscript(m.View())

	if refreshed == snapshot {
		t.Fatalf("detail snapshot did not refresh after mode roundtrip")
	}
	if !containsInOrder(refreshed, "❮", "alpha beta") {
		t.Fatalf("refreshed snapshot missing full assistant stream: %q", refreshed)
	}
	if !containsInOrder(refreshed, "•", "ran") {
		t.Fatalf("refreshed snapshot missing new transcript entry: %q", refreshed)
	}
}

func TestDetailDoesNotScrollOnIncomingMessages(t *testing.T) {
	m := NewModel(WithPreviewLines(2))
	m = updateModel(t, m, AppendTranscriptMsg{Role: "user", Text: "u1"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "a1"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "user", Text: "u2"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "a2"})
	m = updateModel(t, m, ToggleModeMsg{})
	m = updateModel(t, m, ScrollOngoingMsg{Delta: 1})

	before := plainTranscript(m.View())
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "a3"})
	after := plainTranscript(m.View())
	if after != before {
		t.Fatalf("detail view changed while new messages arrived:\nbefore=%q\nafter=%q", before, after)
	}
}

func TestClearOngoingAssistantMsgDropsPartialStream(t *testing.T) {
	m := NewModel()
	m = updateModel(t, m, StreamAssistantMsg{Delta: "partial"})
	m = updateModel(t, m, ClearOngoingAssistantMsg{})
	m = updateModel(t, m, StreamAssistantMsg{Delta: "final"})
	m = updateModel(t, m, CommitAssistantMsg{})
	m = updateModel(t, m, ToggleModeMsg{})

	snapshot := plainTranscript(m.View())
	if strings.Contains(snapshot, "partial") {
		t.Fatalf("snapshot should not contain discarded attempt delta: %q", snapshot)
	}
	if !strings.Contains(snapshot, "final") {
		t.Fatalf("snapshot missing committed final assistant output: %q", snapshot)
	}
}

func TestOngoingShowsCommittedAssistantAfterCommit(t *testing.T) {
	m := NewModel(WithPreviewLines(3))
	m = updateModel(t, m, StreamAssistantMsg{Delta: "line1\nline2"})
	m = updateModel(t, m, CommitAssistantMsg{})

	view := plainTranscript(m.View())
	if !strings.Contains(view, "line1") || !strings.Contains(view, "line2") {
		t.Fatalf("ongoing view should keep committed assistant visible, got %q", view)
	}
}

func TestOngoingAutoFollowsWhenUserIsAtBottom(t *testing.T) {
	m := NewModel(WithPreviewLines(2))
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "a1"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "a2"})
	if got, want := m.OngoingScroll(), m.maxOngoingScroll(); got != want {
		t.Fatalf("expected to start at bottom, got %d want %d", got, want)
	}

	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "a3"})
	if got, want := m.OngoingScroll(), m.maxOngoingScroll(); got != want {
		t.Fatalf("scroll after growth = %d, want bottom %d", got, want)
	}
	view := plainTranscript(m.View())
	if !strings.Contains(view, "a3") {
		t.Fatalf("expected latest line visible at bottom, got %q", view)
	}
}

func TestOngoingDoesNotAutoFollowWhenUserScrolledUp(t *testing.T) {
	m := NewModel(WithPreviewLines(2))
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "a1"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "a2"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "a3"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "a4"})
	if got, want := m.OngoingScroll(), m.maxOngoingScroll(); got != want {
		t.Fatalf("expected to start at bottom, got %d want %d", got, want)
	}

	m = updateModel(t, m, ScrollOngoingMsg{Delta: -1})
	pinned := m.OngoingScroll()
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "a5"})
	if got := m.OngoingScroll(); got != pinned {
		t.Fatalf("scroll should stay pinned when user scrolled up, got %d want %d", got, pinned)
	}
	if m.OngoingScroll() == m.maxOngoingScroll() {
		t.Fatalf("expected to remain above bottom after new message")
	}
}

func TestMouseWheelDoesNotAffectOngoingView(t *testing.T) {
	m := NewModel(WithPreviewLines(2))
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "a1"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "a2"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "a3"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "a4"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "a5"})

	start := m.OngoingScroll()
	m = updateModel(t, m, tea.MouseMsg{Button: tea.MouseButtonWheelUp, Type: tea.MouseWheelUp})
	m = updateModel(t, m, tea.MouseMsg{Button: tea.MouseButtonWheelDown, Type: tea.MouseWheelDown})
	if got := m.OngoingScroll(); got != start {
		t.Fatalf("expected mouse wheel to not change ongoing scroll, got %d want %d", got, start)
	}
}

func TestMouseWheelDoesNotAffectDetailView(t *testing.T) {
	m := NewModel(WithPreviewLines(2))
	m = updateModel(t, m, AppendTranscriptMsg{Role: "user", Text: "u1"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "a1"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "user", Text: "u2"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "a2"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "user", Text: "u3"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "a3"})
	m = updateModel(t, m, ToggleModeMsg{})
	start := m.detailScroll
	if start == 0 {
		t.Fatalf("expected detail mode to start at bottom, got detailScroll=%d", start)
	}

	m = updateModel(t, m, tea.MouseMsg{Button: tea.MouseButtonWheelUp, Type: tea.MouseWheelUp})
	m = updateModel(t, m, tea.MouseMsg{Button: tea.MouseButtonWheelDown, Type: tea.MouseWheelDown})
	if m.detailScroll != start {
		t.Fatalf("expected mouse wheel to not change detail scroll, got %d want %d", m.detailScroll, start)
	}
}

func TestPageKeysScrollActiveView(t *testing.T) {
	m := NewModel(WithPreviewLines(2))
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "a1"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "a2"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "a3"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "a4"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "a5"})

	start := m.OngoingScroll()
	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyPgUp})
	if got := m.OngoingScroll(); got >= start {
		t.Fatalf("expected pgup to scroll up ongoing view, got %d from %d", got, start)
	}

	m = updateModel(t, m, tea.KeyMsg{Type: tea.KeyPgDown})
	if got, want := m.OngoingScroll(), m.maxOngoingScroll(); got != want {
		t.Fatalf("expected pgdown to return to bottom, got %d want %d", got, want)
	}
}

func TestDetailUsesRequestedSymbolsAndDividers(t *testing.T) {
	m := NewModel()
	m = updateModel(t, m, AppendTranscriptMsg{Role: "user", Text: "hello"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "hi"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "tool_call", Text: "call"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "tool_result", Text: "result"})
	m = updateModel(t, m, ToggleModeMsg{})

	view := plainTranscript(m.View())
	if !containsInOrder(view, "❯", "hello") {
		t.Fatalf("expected user symbol, got %q", view)
	}
	if !containsInOrder(view, "❮", "hi") {
		t.Fatalf("expected assistant symbol, got %q", view)
	}
	if !strings.Contains(view, "•") || !strings.Contains(view, "call") || !strings.Contains(view, "result") {
		t.Fatalf("expected tool call/result pair with tool symbol, got %q", view)
	}
	if got := strings.Count(view, strings.Repeat("─", 24)); got != 2 {
		t.Fatalf("expected 2 dividers for 3 blocks, got %d in %q", got, view)
	}
}

func TestDetailShellToolUsesDollarPrefixAndKeepsSuccessColorRole(t *testing.T) {
	m := NewModel()
	m = updateModel(t, m, AppendTranscriptMsg{
		Role: "tool_call",
		Text: "pwd",
		ToolCall: &transcript.ToolCallMeta{
			IsShell:      true,
			Command:      "pwd",
			TimeoutLabel: "timeout: 5m",
		},
	})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "tool_result_ok", Text: "/tmp"})
	m = updateModel(t, m, ToggleModeMsg{})

	view := plainTranscript(m.View())
	if !containsInOrder(view, "$", "pwd") {
		t.Fatalf("expected shell tool to use $ prefix, got %q", view)
	}
	if strings.Contains(view, "• pwd") {
		t.Fatalf("expected no dot prefix for shell tool, got %q", view)
	}
}

func TestDetailMatchesParallelShellResultsByCallID(t *testing.T) {
	m := NewModel()
	m = updateModel(t, m, AppendTranscriptMsg{
		Role:       "tool_call",
		Text:       "echo a",
		ToolCallID: "call_a",
		ToolCall: &transcript.ToolCallMeta{
			IsShell: true,
			Command: "echo a",
		},
	})
	m = updateModel(t, m, AppendTranscriptMsg{
		Role:       "tool_call",
		Text:       "echo b",
		ToolCallID: "call_b",
		ToolCall: &transcript.ToolCallMeta{
			IsShell: true,
			Command: "echo b",
		},
	})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "tool_result_ok", ToolCallID: "call_a", Text: "out-a"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "tool_result_ok", ToolCallID: "call_b", Text: "out-b"})
	m = updateModel(t, m, ToggleModeMsg{})

	view := plainTranscript(m.View())
	idxCallA := strings.Index(view, "echo a")
	idxOutA := strings.Index(view, "out-a")
	idxCallB := strings.Index(view, "echo b")
	idxOutB := strings.Index(view, "out-b")
	if idxCallA < 0 || idxOutA < 0 || idxCallB < 0 || idxOutB < 0 {
		t.Fatalf("expected both calls and outputs in view, got %q", view)
	}
	if !(idxCallA < idxOutA && idxOutA < idxCallB && idxCallB < idxOutB) {
		t.Fatalf("expected each output to stay with matching call, got %q", view)
	}
	if strings.Contains(view, "• out-a") || strings.Contains(view, "• out-b") {
		t.Fatalf("expected no standalone tool result blocks for matched call IDs, got %q", view)
	}
}

func TestDetailDoesNotMatchAdjacentResultWhenCallIDMissing(t *testing.T) {
	m := NewModel()
	m = updateModel(t, m, AppendTranscriptMsg{
		Role: "tool_call",
		Text: "echo missing-id",
		ToolCall: &transcript.ToolCallMeta{
			IsShell: true,
			Command: "echo missing-id",
		},
	})
	m = updateModel(t, m, AppendTranscriptMsg{
		Role:       "tool_result_ok",
		ToolCallID: "call_other",
		Text:       "out-other",
	})
	m = updateModel(t, m, ToggleModeMsg{})

	view := plainTranscript(m.View())
	if !containsInOrder(view, "$", "echo missing-id", "•", "out-other") {
		t.Fatalf("expected unmatched result to remain standalone, got %q", view)
	}
}

func TestDetailAskQuestionRendersQuestionSuggestionsAndAnswer(t *testing.T) {
	m := NewModel(WithPreviewLines(20))
	m = updateModel(t, m, AppendTranscriptMsg{
		Role: "tool_call",
		Text: "question: Choose scope?\nsuggestions: - Recommended: flat scan\n  - Recursive scan",
		ToolCall: &transcript.ToolCallMeta{
			ToolName:    "ask_question",
			Question:    "Choose scope?",
			Suggestions: []string{"Recommended: flat scan", "Recursive scan"},
		},
	})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "tool_result_ok", Text: "Use flat scan."})
	m = updateModel(t, m, ToggleModeMsg{})

	plain := plainTranscript(m.View())
	if strings.Contains(plain, "question:") || strings.Contains(plain, "suggestions:") {
		t.Fatalf("expected ask_question labels removed from detail view, got %q", plain)
	}
	if !containsInOrder(plain, "?", "Choose scope?", "- Recommended: flat scan", "- Recursive scan", "Use flat scan.") {
		t.Fatalf("expected question, suggestions and answer in detail order, got %q", plain)
	}

	colored := m.View()
	if !strings.Contains(colored, m.palette().preview.Faint(true).Render("- Recommended: flat scan")) {
		t.Fatalf("expected suggestions to be muted in detail view, got %q", colored)
	}
	if !strings.Contains(colored, m.palette().user.Render("Use flat scan.")) {
		t.Fatalf("expected answer to use user color in detail view, got %q", colored)
	}
}

func TestOngoingAskQuestionRendersQuestionAndAnswerOnly(t *testing.T) {
	m := NewModel(WithPreviewLines(20))
	m = updateModel(t, m, AppendTranscriptMsg{
		Role: "tool_call",
		Text: "question: Choose scope?\nsuggestions: - Recommended: flat scan\n  - Recursive scan",
		ToolCall: &transcript.ToolCallMeta{
			ToolName:    "ask_question",
			Question:    "Choose scope?",
			Suggestions: []string{"Recommended: flat scan", "Recursive scan"},
		},
	})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "tool_result_ok", Text: "Use flat scan."})

	plain := plainTranscript(m.View())
	if strings.Contains(plain, "question:") || strings.Contains(plain, "suggestions:") {
		t.Fatalf("expected ask_question labels removed from ongoing view, got %q", plain)
	}
	if strings.Contains(plain, "- Recommended: flat scan") || strings.Contains(plain, "- Recursive scan") {
		t.Fatalf("expected ongoing view to omit ask_question suggestions, got %q", plain)
	}
	if !containsInOrder(plain, "?", "Choose scope?", "Use flat scan.") {
		t.Fatalf("expected question and answer in ongoing view, got %q", plain)
	}

	colored := m.View()
	if !strings.Contains(colored, m.palette().user.Render("Use flat scan.")) {
		t.Fatalf("expected answer to use user color in ongoing view, got %q", colored)
	}
}

func TestOngoingCompactsToolCallAndHidesThinking(t *testing.T) {
	m := NewModel(WithPreviewLines(20))
	m = updateModel(t, m, AppendTranscriptMsg{Role: "user", Text: "run command"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "thinking", Text: "internal trace"})
	m = updateModel(t, m, AppendTranscriptMsg{
		Role: "tool_call",
		Text: "pwd",
		ToolCall: &transcript.ToolCallMeta{
			IsShell:      true,
			Command:      "pwd",
			TimeoutLabel: "timeout: 5m",
		},
	})
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

func TestDetailShowsReasoningSummaryAsSeparateEntry(t *testing.T) {
	m := NewModel(WithPreviewLines(20))
	m = updateModel(t, m, AppendTranscriptMsg{Role: "user", Text: "u"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "reasoning", Text: "Plan summary"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "a"})

	ongoing := plainTranscript(m.View())
	if strings.Contains(ongoing, "Plan summary") {
		t.Fatalf("expected reasoning hidden in ongoing view, got %q", ongoing)
	}

	m = updateModel(t, m, ToggleModeMsg{})
	colored := m.View()
	detail := plainTranscript(m.View())
	if !strings.Contains(detail, "Plan summary") {
		t.Fatalf("expected reasoning summary entry in detail view, got %q", detail)
	}
	if strings.Contains(detail, "…") {
		t.Fatalf("expected reasoning entry without ellipsis prefix, got %q", detail)
	}
	if !strings.Contains(colored, "\x1b[38;5;252mPlan") {
		t.Fatalf("expected reasoning summary styled with muted/system color, got %q", colored)
	}
}

func TestDetailReordersTrailingReasoningBeforeAssistantResponse(t *testing.T) {
	m := NewModel(WithPreviewLines(20))
	m = updateModel(t, m, AppendTranscriptMsg{Role: "user", Text: "u"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "final answer"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "reasoning", Text: "hidden plan"})
	m = updateModel(t, m, ToggleModeMsg{})

	detail := plainTranscript(m.View())
	if !containsInOrder(detail, "hidden plan", "❮", "final answer") {
		t.Fatalf("expected trailing reasoning rendered before assistant response, got %q", detail)
	}
}

func TestDetailReordersTrailingReasoningBeforeToolCalls(t *testing.T) {
	m := NewModel(WithPreviewLines(20))
	m = updateModel(t, m, AppendTranscriptMsg{Role: "tool_call", Text: "run"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "reasoning", Text: "decide to call tool"})
	m = updateModel(t, m, ToggleModeMsg{})

	detail := plainTranscript(m.View())
	if !containsInOrder(detail, "decide to call tool", "•", "run") {
		t.Fatalf("expected trailing reasoning rendered before tool call, got %q", detail)
	}
}

func TestDetailGroupsReasoningEntriesAndRendersMarkdown(t *testing.T) {
	m := NewModel(WithPreviewLines(30))
	m = updateModel(t, m, AppendTranscriptMsg{Role: "user", Text: "u"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "reasoning", Text: "**First step**"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "reasoning", Text: "`second` details"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "reasoning", Text: "**third**"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "done"})
	m = updateModel(t, m, ToggleModeMsg{})

	view := plainTranscript(m.View())
	if strings.Contains(view, "**First step**") || strings.Contains(view, "`second` details") || strings.Contains(view, "**third**") {
		t.Fatalf("expected reasoning markdown to be rendered, got %q", view)
	}
	if !containsInOrder(view, "First step", "second", "details", "third") {
		t.Fatalf("expected grouped reasoning text in order, got %q", view)
	}
	if got := strings.Count(view, strings.Repeat("─", 24)); got != 2 {
		t.Fatalf("expected 2 dividers for user/reasoning/assistant groups, got %d in %q", got, view)
	}
}

func TestCompactionNoticeAndSummaryRenderingByMode(t *testing.T) {
	m := NewModel(WithPreviewLines(20))
	m = updateModel(t, m, AppendTranscriptMsg{Role: "compaction_notice", Text: "context compacted for the 1st time"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "compaction_summary", Text: "line one\nline two"})

	ongoing := plainTranscript(m.View())
	if !containsInOrder(ongoing, "@", "context compacted for the 1st time") {
		t.Fatalf("expected compaction notice in ongoing view, got %q", ongoing)
	}
	if strings.Contains(ongoing, "line one") || strings.Contains(ongoing, "line two") {
		t.Fatalf("expected compaction summary hidden in ongoing view, got %q", ongoing)
	}

	m = updateModel(t, m, ToggleModeMsg{})
	detail := plainTranscript(m.View())
	if !containsInOrder(detail, "@", "context compacted for the 1st time", "@", "line one", "line two") {
		t.Fatalf("expected compaction notice and full summary in detail view, got %q", detail)
	}
}

func TestReviewerStatusRendersShortInOngoingAndFullInDetail(t *testing.T) {
	m := NewModel(WithPreviewLines(20))
	m = updateModel(t, m, AppendTranscriptMsg{Role: "user", Text: "run task"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "reviewer_status", Text: "Supervisor ran: 2 suggestions, no changes applied.\n\nSupervisor suggestions:\n1. First\n2. Second"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "done"})

	ongoing := plainTranscript(m.View())
	if !strings.Contains(ongoing, "Supervisor ran: 2 suggestions, no changes applied.") {
		t.Fatalf("expected short reviewer status in ongoing view, got %q", ongoing)
	}
	if strings.Contains(ongoing, "Supervisor suggestions:") || strings.Contains(ongoing, "1. First") {
		t.Fatalf("expected full reviewer suggestions hidden in ongoing view, got %q", ongoing)
	}

	m = updateModel(t, m, ToggleModeMsg{})
	detail := plainTranscript(m.View())
	if !containsInOrder(detail, "❯", "run task", "@", "Supervisor ran: 2 suggestions, no changes applied.", "Supervisor suggestions:", "1. First", "2. Second", "❮", "done") {
		t.Fatalf("expected reviewer status visible in detail view, got %q", detail)
	}
}

func TestIsReviewerCacheHitLine(t *testing.T) {
	if !isReviewerCacheHitLine("85% cache hit") {
		t.Fatal("expected cache-hit line to be detected")
	}
	if isReviewerCacheHitLine("cache hit") {
		t.Fatal("expected invalid cache-hit line to be rejected")
	}
}

func TestOngoingDividersAreInsertedOnlyBetweenRoleGroups(t *testing.T) {
	m := NewModel(WithPreviewLines(30))
	m = updateModel(t, m, AppendTranscriptMsg{Role: "user", Text: "u1"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "user", Text: "u2"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "a1"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "assistant", Text: "a2"})
	m = updateModel(t, m, AppendTranscriptMsg{
		Role: "tool_call",
		Text: "pwd",
		ToolCall: &transcript.ToolCallMeta{
			IsShell:      true,
			Command:      "pwd",
			TimeoutLabel: "timeout: 5m",
		},
	})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "tool_result_ok", Text: "/tmp"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "tool_call", Text: "ls"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "tool_result_error", Text: "failed"})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "user", Text: "u3"})

	view := plainTranscript(m.View())
	if got := strings.Count(view, strings.Repeat("─", 24)); got != 3 {
		t.Fatalf("expected 3 dividers for 4 role groups, got %d in %q", got, view)
	}
	if !containsInOrder(view, "❯", "u1", "u2", "❮", "a1", "a2", "$", "pwd", "•", "ls", "❯", "u3") {
		t.Fatalf("expected grouped ongoing transcript order, got %q", view)
	}
}

func TestDetailToolFormattingShowsTimeoutAndInlineOutput(t *testing.T) {
	m := NewModel()
	m = updateModel(t, m, SetViewportSizeMsg{Lines: 20, Width: 80})
	m = updateModel(t, m, AppendTranscriptMsg{
		Role: "tool_call",
		Text: "pwd",
		ToolCall: &transcript.ToolCallMeta{
			IsShell:      true,
			Command:      "pwd",
			TimeoutLabel: "timeout: 5m",
		},
	})
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
	if got := toolBlockRoleFromResult("tool_result_ok", "tool"); got != "tool_success" {
		t.Fatalf("unexpected role for success result: %q", got)
	}
	if got := toolBlockRoleFromResult("tool_result_error", "tool"); got != "tool_error" {
		t.Fatalf("unexpected role for error result: %q", got)
	}
	if got := toolBlockRoleFromResult("tool_result", "tool"); got != "tool_success" {
		t.Fatalf("unexpected role for legacy result: %q", got)
	}
	if got := toolBlockRoleFromResult("tool_result_ok", "tool_shell"); got != "tool_shell_success" {
		t.Fatalf("unexpected role for shell success result: %q", got)
	}
	if got := toolBlockRoleFromResult("tool_result_error", "tool_shell"); got != "tool_shell_error" {
		t.Fatalf("unexpected role for shell error result: %q", got)
	}
}

func TestPatchPayloadRendersSummaryInOngoingAndDetailDiffInDetail(t *testing.T) {
	summary := "Edited:\n./path/to/file/1.go +13 -9\n./path/to/file/2.go +386"
	detail := "Edited:\n/abs/path/to/file/1.go\n+new line\n-old line\n/abs/path/to/file/2.go\n+another line"

	m := NewModel(WithPreviewLines(20))
	m = updateModel(t, m, AppendTranscriptMsg{
		Role: "tool_call",
		Text: summary,
		ToolCall: &transcript.ToolCallMeta{
			ToolName:     "patch",
			PatchSummary: summary,
			PatchDetail:  detail,
		},
	})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "tool_result_ok", Text: ""})

	ongoing := m.View()
	if !strings.Contains(ongoing, "Edited:") || !strings.Contains(ongoing, "./path/to/file/1.go") || !strings.Contains(ongoing, "./path/to/file/2.go") {
		t.Fatalf("expected patch summary in ongoing mode, got %q", ongoing)
	}
	if strings.Contains(ongoing, "/abs/path/to/file/1.go") || strings.Contains(ongoing, "+new line") {
		t.Fatalf("did not expect detail diff in ongoing mode, got %q", ongoing)
	}

	m = updateModel(t, m, ToggleModeMsg{})
	detailView := m.View()
	if !strings.Contains(detailView, "/abs/path/to/file/1.go") || !strings.Contains(detailView, "/abs/path/to/file/2.go") {
		t.Fatalf("expected absolute file paths in detail mode, got %q", detailView)
	}
	if !strings.Contains(detailView, "+new line") || !strings.Contains(detailView, "-old line") || !strings.Contains(detailView, "+another line") {
		t.Fatalf("expected full diff lines in detail mode, got %q", detailView)
	}
	if strings.Contains(detailView, "output:") {
		t.Fatalf("did not expect output prefix in detail mode, got %q", detailView)
	}
}

func TestStyleToolLineColorsPatchCountsAndDiff(t *testing.T) {
	m := NewModel()
	counts := m.styleToolLine("./file.go +13 -9")
	if !strings.Contains(counts, "+13") || !strings.Contains(counts, "-9") {
		t.Fatalf("expected patch counts preserved, got %q", counts)
	}
	added := m.styleToolLine("+added")
	if !strings.Contains(added, "+added") {
		t.Fatalf("expected addition line preserved, got %q", added)
	}
	removed := m.styleToolLine("-removed")
	if !strings.Contains(removed, "-removed") {
		t.Fatalf("expected removal line preserved, got %q", removed)
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

func TestDetailWrapsNonMarkdownRoles(t *testing.T) {
	m := NewModel()
	m = updateModel(t, m, SetViewportSizeMsg{Lines: 10, Width: 28})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "compaction_summary", Text: "This compaction summary line is intentionally long and should wrap in detail mode."})
	m = updateModel(t, m, ToggleModeMsg{})

	view := plainTranscript(m.View())
	for _, line := range strings.Split(view, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "─") {
			continue
		}
		if got := runewidth.StringWidth(line); got > 28 {
			t.Fatalf("expected wrapped line width <= 28, got %d for line %q", got, line)
		}
	}
}

func TestDetailReflowsNonMarkdownRolesOnViewportResize(t *testing.T) {
	text := "Compaction notice should reflow when viewport width changes."
	m := NewModel()
	m = updateModel(t, m, SetViewportSizeMsg{Lines: 10, Width: 24})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "compaction_notice", Text: text})
	m = updateModel(t, m, ToggleModeMsg{})
	narrow := plainTranscript(m.View())

	m = updateModel(t, m, ToggleModeMsg{})
	m = updateModel(t, m, SetViewportSizeMsg{Lines: 10, Width: 80})
	m = updateModel(t, m, ToggleModeMsg{})
	wide := plainTranscript(m.View())

	if strings.Contains(narrow, text) {
		t.Fatalf("expected narrow detail view to wrap non-markdown line, got %q", narrow)
	}
	if !strings.Contains(wide, text) {
		t.Fatalf("expected wide detail view to reflow and contain single-line text, got %q", wide)
	}
}

func TestRenderEntryTextHighlightsOnlyResultForShellSourceHint(t *testing.T) {
	m := NewModel()
	meta := &transcript.ToolCallMeta{
		RenderHint: &transcript.ToolRenderHint{
			Kind:       transcript.ToolRenderKindSource,
			Path:       "main.go",
			ResultOnly: true,
		},
	}

	out := m.renderEntryText("tool_shell_success", "cat main.go\npackage main\nfunc main() {}", 120, meta, false)
	lines := strings.Split(out, "\n")
	if len(lines) < 2 {
		t.Fatalf("expected command and highlighted output lines, got %q", out)
	}
	if lines[0] != "cat main.go" {
		t.Fatalf("expected command line to stay plain, got %q", lines[0])
	}
	if !strings.Contains(lines[1], "\x1b[") {
		t.Fatalf("expected highlighted result line, got %q", lines[1])
	}
	plain := ansi.Strip(out)
	if !strings.Contains(plain, "package main") || !strings.Contains(plain, "func main() {}") {
		t.Fatalf("expected result text preserved after highlighting, got %q", plain)
	}
}

func TestRenderEntryTextSkipsHighlightWhenMuted(t *testing.T) {
	m := NewModel()
	meta := &transcript.ToolCallMeta{
		RenderHint: &transcript.ToolRenderHint{
			Kind:       transcript.ToolRenderKindSource,
			Path:       "main.go",
			ResultOnly: true,
		},
	}

	out := m.renderEntryText("tool_shell_success", "cat main.go\npackage main\nfunc main() {}", 120, meta, true)
	if strings.Contains(out, "\x1b[") {
		t.Fatalf("expected muted tool text to skip syntax highlighting, got %q", out)
	}
}

func TestDetailShellUserInitiatedCallUsesUserRanLabel(t *testing.T) {
	m := NewModel(WithPreviewLines(20))
	m = updateModel(t, m, AppendTranscriptMsg{
		Role:       "tool_call",
		Text:       "pwd",
		ToolCallID: "call_1",
		ToolCall: &transcript.ToolCallMeta{
			ToolName:      "shell",
			IsShell:       true,
			UserInitiated: true,
			Command:       "pwd",
		},
	})
	m = updateModel(t, m, AppendTranscriptMsg{Role: "tool_result_ok", Text: "/tmp", ToolCallID: "call_1"})

	ongoing := plainTranscript(m.View())
	if strings.Contains(ongoing, "User ran:") {
		t.Fatalf("did not expect ongoing view label to change, got %q", ongoing)
	}

	m = updateModel(t, m, ToggleModeMsg{})
	detail := plainTranscript(m.View())
	if !strings.Contains(detail, "User ran: pwd") {
		t.Fatalf("expected detailed shell label to include user-ran text, got %q", detail)
	}
	if !strings.Contains(detail, "/tmp") {
		t.Fatalf("expected detailed shell block to include output, got %q", detail)
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

func plainTranscript(view string) string {
	stripped := ansi.Strip(view)
	lines := strings.Split(stripped, "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " ")
	}
	return strings.Join(lines, "\n")
}

func containsInOrder(text string, parts ...string) bool {
	offset := 0
	for _, part := range parts {
		idx := strings.Index(text[offset:], part)
		if idx < 0 {
			return false
		}
		offset += idx + len(part)
	}
	return true
}

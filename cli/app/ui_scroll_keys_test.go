package app

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"builder/cli/tui"
	"builder/server/runtime"
	"builder/shared/transcript"
	"builder/shared/uiglyphs"

	tea "github.com/charmbracelet/bubbletea"
)

func TestPageKeysScrollTranscriptWhileInputFocused(t *testing.T) {
	m := newProjectedStaticUIModel()
	m.termWidth = 80
	m.termHeight = 8

	for i := 0; i < 12; i++ {
		m.forwardToView(tui.AppendTranscriptMsg{Role: "assistant", Text: fmt.Sprintf("line %d", i)})
	}
	m.forwardToView(tui.ToggleModeMsg{}) // detail mode starts at scroll=0

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	updated := next.(*uiModel)
	if down := updated.view.View(); down == "" {
		t.Fatal("expected detail transcript to remain visible after pgdown")
	}

	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	updated = next.(*uiModel)
	if up := updated.view.View(); up == "" {
		t.Fatal("expected detail transcript to remain visible after pgup")
	}
}

func TestDetailModeUpDownScrollTranscript(t *testing.T) {
	m := newProjectedStaticUIModel()
	m.termWidth = 80
	m.termHeight = 8
	m.syncViewport()

	for i := 0; i < 16; i++ {
		m.forwardToView(tui.AppendTranscriptMsg{Role: "assistant", Text: fmt.Sprintf("line %d", i)})
	}
	m = updateUIModel(t, m, tea.KeyMsg{Type: tea.KeyShiftTab})

	initial := stripDetailSelectionRail(stripANSIAndTrimRight(m.view.View()))
	if initial == "" {
		t.Fatal("expected detail transcript visible before scrolling")
	}
	initialScroll := m.view.DetailScroll()

	m = updateUIModel(t, m, tea.KeyMsg{Type: tea.KeyUp})
	afterUp := stripDetailSelectionRail(stripANSIAndTrimRight(m.view.View()))
	if afterUp == initial {
		t.Fatal("expected detail transcript to change after up")
	}

	beforeDownScroll := m.view.DetailScroll()
	m = updateUIModel(t, m, tea.KeyMsg{Type: tea.KeyDown})
	if got := m.view.DetailScroll(); got >= beforeDownScroll {
		t.Fatalf("expected detail down after prior up to move toward bottom, got %d from %d", got, beforeDownScroll)
	}
	if got := m.view.DetailScroll(); got != initialScroll {
		t.Fatalf("expected detail scroll to round-trip after up/down, got %d want %d", got, initialScroll)
	}
	if afterDown := stripDetailSelectionRail(stripANSIAndTrimRight(m.view.View())); afterDown == afterUp {
		t.Fatal("expected detail transcript to change after down")
	}
}

func TestDetailModeLineScrollRoundTripsScrollAndSelectionState(t *testing.T) {
	m := newProjectedStaticUIModel()
	m.termWidth = 80
	m.termHeight = 8
	m.syncViewport()

	for idx := 0; idx < 20; idx++ {
		m.forwardToView(tui.AppendTranscriptMsg{Role: "assistant", Text: fmt.Sprintf("state entry %02d", idx)})
	}
	m = updateUIModel(t, m, tea.KeyMsg{Type: tea.KeyShiftTab})
	m = updateUIModel(t, m, tea.KeyMsg{Type: tea.KeyPgUp})
	startScroll := m.view.DetailScroll()
	startSelected, startSelectedOK := m.view.DetailSelectedEntry()
	if !startSelectedOK {
		t.Fatal("expected selected detail entry before state round-trip")
	}

	m = updateUIModel(t, m, tea.KeyMsg{Type: tea.KeyUp})
	if got := m.view.DetailScroll(); got != startScroll+1 {
		t.Fatalf("expected up to move detail scroll state by one line, got %d want %d", got, startScroll+1)
	}
	m = updateUIModel(t, m, tea.KeyMsg{Type: tea.KeyDown})
	if got := m.view.DetailScroll(); got != startScroll {
		t.Fatalf("expected up/down detail scroll state to round-trip, got %d want %d", got, startScroll)
	}
	selected, selectedOK := m.view.DetailSelectedEntry()
	if !selectedOK || selected != startSelected {
		t.Fatalf("expected selected entry state to round-trip, got %d ok=%v want %d", selected, selectedOK, startSelected)
	}
}

func TestDetailModeCompactExpansionRoutesThroughUIModel(t *testing.T) {
	m := newProjectedStaticUIModel()
	m.termWidth = 80
	m.termHeight = 12
	m.syncViewport()
	m.forwardToView(tui.AppendTranscriptMsg{
		Role:       "tool_call",
		Text:       "cat large.txt",
		ToolCallID: "call_1",
		ToolCall:   &transcript.ToolCallMeta{ToolName: "exec_command", IsShell: true, Command: "cat large.txt"},
	})
	m.forwardToView(tui.AppendTranscriptMsg{Role: "tool_result_ok", ToolCallID: "call_1", Text: "line 1\nline 2"})

	m = updateUIModel(t, m, tea.KeyMsg{Type: tea.KeyShiftTab})
	collapsed := stripANSIAndTrimRight(m.view.View())
	if !strings.Contains(collapsed, "▶ cat large.txt") {
		t.Fatalf("expected collapsed compact tool row, got %q", collapsed)
	}
	if strings.Contains(collapsed, "line 2") {
		t.Fatalf("expected collapsed detail to hide tool output, got %q", collapsed)
	}

	m = updateUIModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	expanded := stripANSIAndTrimRight(m.view.View())
	if !strings.Contains(expanded, "▼ cat large.txt") || !strings.Contains(expanded, "│ line 1") || !strings.Contains(expanded, "└ line 2") {
		t.Fatalf("expected UI-routed enter to expand tool output, got %q", expanded)
	}
}

func TestDetailModeStatusLineShowsSelectedExpansionAction(t *testing.T) {
	m := newProjectedStaticUIModel()
	m.termWidth = 80
	m.termHeight = 12
	m.syncViewport()
	m.forwardToView(tui.AppendTranscriptMsg{
		Role:       "tool_call",
		Text:       "cat large.txt",
		ToolCallID: "call_1",
		ToolCall:   &transcript.ToolCallMeta{ToolName: "exec_command", IsShell: true, Command: "cat large.txt"},
	})
	m.forwardToView(tui.AppendTranscriptMsg{Role: "tool_result_ok", ToolCallID: "call_1", Text: "line 1\nline 2"})

	m = updateUIModel(t, m, tea.KeyMsg{Type: tea.KeyShiftTab})
	status := stripANSIAndTrimRight(m.renderStatusLine(120, uiThemeStyles("dark")))
	if !strings.Contains(status, "Enter to expand") {
		t.Fatalf("expected detail status line expansion hint, got %q", status)
	}

	m = updateUIModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	status = stripANSIAndTrimRight(m.renderStatusLine(120, uiThemeStyles("dark")))
	if !strings.Contains(status, "Enter to collapse") {
		t.Fatalf("expected detail status line collapse hint, got %q", status)
	}
}

func TestDetailModeStatusLineFallsBackWhenSelectionIsNotExpandable(t *testing.T) {
	m := newProjectedStaticUIModel()
	m.termWidth = 80
	m.termHeight = 8
	m.syncViewport()
	errorLines := make([]string, 0, 16)
	for idx := 0; idx < 16; idx++ {
		errorLines = append(errorLines, fmt.Sprintf("non expandable error line %02d", idx))
	}
	m.forwardToView(tui.AppendTranscriptMsg{Role: "error", Text: strings.Join(errorLines, "\n")})
	for idx := 0; idx < 12; idx++ {
		callID := fmt.Sprintf("call_%d", idx)
		command := fmt.Sprintf("cmd %d", idx)
		m.forwardToView(tui.AppendTranscriptMsg{
			Role:       "tool_call",
			Text:       command,
			ToolCallID: callID,
			ToolCall:   &transcript.ToolCallMeta{ToolName: "exec_command", IsShell: true, Command: command},
		})
		m.forwardToView(tui.AppendTranscriptMsg{Role: "tool_result_ok", ToolCallID: callID, Text: "line 1\nline 2"})
	}

	m = updateUIModel(t, m, tea.KeyMsg{Type: tea.KeyShiftTab})
	status := stripANSIAndTrimRight(m.renderStatusLine(120, uiThemeStyles("dark")))
	if !strings.Contains(status, "Enter to expand") {
		t.Fatalf("expected expandable selection hint before scrolling, got %q", status)
	}

	for guard := 0; guard < 8; guard++ {
		m = updateUIModel(t, m, tea.KeyMsg{Type: tea.KeyPgUp})
		status = stripANSIAndTrimRight(m.renderStatusLine(120, uiThemeStyles("dark")))
		if !strings.Contains(status, "Enter to expand") && !strings.Contains(status, "Enter to collapse") {
			break
		}
	}
	if strings.Contains(status, "Enter to expand") || strings.Contains(status, "Enter to collapse") {
		t.Fatalf("did not expect expansion hint after scrolling to non-expandable selection, got %q", status)
	}
}

func TestDetailModeEnterOnShortSelectedMessageDoesNotShowExpansionHintOrMutateState(t *testing.T) {
	m := newProjectedStaticUIModel()
	m.termWidth = 80
	m.termHeight = 8
	m.syncViewport()
	m.forwardToView(tui.AppendTranscriptMsg{Role: "user", Text: "short user"})
	m.forwardToView(tui.AppendTranscriptMsg{Role: "assistant", Text: "short assistant"})
	m = updateUIModel(t, m, tea.KeyMsg{Type: tea.KeyShiftTab})

	beforeView := stripANSIAndTrimRight(m.view.View())
	status := stripANSIAndTrimRight(m.renderStatusLine(120, uiThemeStyles("dark")))
	if strings.Contains(beforeView, "▶") || strings.Contains(beforeView, "▼") || strings.Contains(status, "Enter to expand") || strings.Contains(status, "Enter to collapse") {
		t.Fatalf("did not expect expansion affordance for selected short message, view=%q status=%q", beforeView, status)
	}

	m = updateUIModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	afterView := stripANSIAndTrimRight(m.view.View())
	status = stripANSIAndTrimRight(m.renderStatusLine(120, uiThemeStyles("dark")))
	if afterView != beforeView || strings.Contains(afterView, "▶") || strings.Contains(afterView, "▼") || strings.Contains(status, "Enter to expand") || strings.Contains(status, "Enter to collapse") {
		t.Fatalf("expected enter on selected short message to be no-op with normal help, before=%q after=%q status=%q", beforeView, afterView, status)
	}
}

func TestDetailModeArrowScrollsDetailByLineAndTracksCenterSelection(t *testing.T) {
	m := newProjectedStaticUIModel()
	m.termWidth = 80
	m.termHeight = 8
	m.syncViewport()
	m.forwardToView(tui.AppendTranscriptMsg{
		Role:       "tool_call",
		Text:       "first-command",
		ToolCallID: "call_1",
		ToolCall:   &transcript.ToolCallMeta{ToolName: "exec_command", IsShell: true, Command: "first-command"},
	})
	firstOutput := make([]string, 0, 20)
	for idx := 0; idx < 20; idx++ {
		firstOutput = append(firstOutput, fmt.Sprintf("first output line %02d", idx))
	}
	m.forwardToView(tui.AppendTranscriptMsg{Role: "tool_result_ok", ToolCallID: "call_1", Text: strings.Join(firstOutput, "\n")})
	m.forwardToView(tui.AppendTranscriptMsg{
		Role:       "tool_call",
		Text:       "second-command",
		ToolCallID: "call_2",
		ToolCall:   &transcript.ToolCallMeta{ToolName: "exec_command", IsShell: true, Command: "second-command"},
	})
	m.forwardToView(tui.AppendTranscriptMsg{Role: "tool_result_ok", ToolCallID: "call_2", Text: "second output"})
	m.forwardToView(tui.AppendTranscriptMsg{
		Role:       "tool_call",
		Text:       "third-command",
		ToolCallID: "call_3",
		ToolCall:   &transcript.ToolCallMeta{ToolName: "exec_command", IsShell: true, Command: "third-command"},
	})
	m.forwardToView(tui.AppendTranscriptMsg{Role: "tool_result_ok", ToolCallID: "call_3", Text: "third output"})
	for idx := 0; idx < 10; idx++ {
		m.forwardToView(tui.AppendTranscriptMsg{Role: "assistant", Text: fmt.Sprintf("tail entry %02d", idx)})
	}

	m = updateUIModel(t, m, tea.KeyMsg{Type: tea.KeyShiftTab})
	topVisible := 0
	ok := false
	for guard := 0; guard < 20; guard++ {
		topVisible, _, ok = m.view.DetailVisibleEntryRange()
		if ok && topVisible == 0 {
			break
		}
		m = updateUIModel(t, m, tea.KeyMsg{Type: tea.KeyPgUp})
	}
	topVisible, _, ok = m.view.DetailVisibleEntryRange()
	if !ok || topVisible != 0 {
		t.Fatalf("expected top command visible before expansion, range=(%d, ok=%v) view=%q", topVisible, ok, stripANSIAndTrimRight(m.view.View()))
	}
	m = updateUIModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	beforeScroll := m.view.DetailScroll()

	for step := 1; step <= 5; step++ {
		m = updateUIModel(t, m, tea.KeyMsg{Type: tea.KeyDown})
		if got, want := m.view.DetailScroll(), beforeScroll+step; got != want {
			t.Fatalf("step %d: expected detail arrow scroll by one line, got %d want %d", step, got, want)
		}
		if selected := selectedDetailContentLine(t, m.view.View()); selected == "" {
			t.Fatalf("step %d: expected center selection to remain visible", step)
		}
	}

	if selected := selectedDetailContentLine(t, m.view.View()); selected == "" {
		t.Fatal("expected centered selection after line scrolling")
	}
	if spacer := selectedDetailSpacerLine(t, m.view.View()); spacer == "" {
		t.Fatal("expected selected card spacer rail after line scrolling")
	}
}

func TestDetailModeReviewerSuggestionsCollapseAndExpandThroughUIModel(t *testing.T) {
	m := newProjectedStaticUIModel()
	m.termWidth = 80
	m.termHeight = 12
	m.syncViewport()
	m.forwardToView(tui.AppendTranscriptMsg{
		Role:        "reviewer_suggestions",
		Text:        "Supervisor suggested:\n1. Add app-level coverage.\n2. Rebuild before final answer.",
		OngoingText: "Supervisor made 2 suggestions.",
	})

	m = updateUIModel(t, m, tea.KeyMsg{Type: tea.KeyShiftTab})
	collapsed := stripANSIAndTrimRight(m.view.View())
	if !strings.Contains(collapsed, "Supervisor made 2 suggestions.") {
		t.Fatalf("expected collapsed reviewer suggestions summary, got %q", collapsed)
	}
	if strings.Contains(collapsed, "Add app-level coverage") || strings.Contains(collapsed, "Rebuild before final answer") {
		t.Fatalf("expected collapsed reviewer suggestions to hide full text, got %q", collapsed)
	}

	m = updateUIModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	expanded := stripANSIAndTrimRight(m.view.View())
	if !strings.Contains(expanded, "Add app-level coverage") || !strings.Contains(expanded, "Rebuild before final answer") {
		t.Fatalf("expected UI-routed enter to expand reviewer suggestions, got %q", expanded)
	}
}

func TestDetailModeEnterRoutesThroughInputControllerWhenInputLocked(t *testing.T) {
	m := newProjectedStaticUIModel()
	m.termWidth = 80
	m.termHeight = 12
	m.syncViewport()
	m.input = "locked draft"
	m.inputSubmitLocked = true
	m.lockedInjectText = "locked draft"
	m.forwardToView(tui.AppendTranscriptMsg{
		Role:       "tool_call",
		Text:       "cat large.txt",
		ToolCallID: "call_1",
		ToolCall:   &transcript.ToolCallMeta{ToolName: "exec_command", IsShell: true, Command: "cat large.txt"},
	})
	m.forwardToView(tui.AppendTranscriptMsg{Role: "tool_result_ok", ToolCallID: "call_1", Text: "line 1\nline 2"})
	m = updateUIModel(t, m, tea.KeyMsg{Type: tea.KeyShiftTab})

	controller := uiInputController{model: m}
	next, cmd := controller.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatalf("expected detail enter to be handled without command, got %T", cmd)
	}
	updated := next.(*uiModel)
	expanded := stripANSIAndTrimRight(updated.view.View())
	if !strings.Contains(expanded, "▼ cat large.txt") || !strings.Contains(expanded, "│ line 1") || !strings.Contains(expanded, "└ line 2") {
		t.Fatalf("expected input-controller enter to expand detail even while input locked, got %q", expanded)
	}
	if updated.input != "locked draft" || !updated.inputSubmitLocked || updated.lockedInjectText != "locked draft" {
		t.Fatalf("expected locked input state preserved, input=%q locked=%t inject=%q", updated.input, updated.inputSubmitLocked, updated.lockedInjectText)
	}
}

func TestDetailModeMouseWheelScrollTranscript(t *testing.T) {
	m := newProjectedStaticUIModel()
	m.termWidth = 80
	m.termHeight = 8
	m.syncViewport()

	for i := 0; i < 16; i++ {
		m.forwardToView(tui.AppendTranscriptMsg{Role: "assistant", Text: fmt.Sprintf("line %d", i)})
	}
	m = updateUIModel(t, m, tea.KeyMsg{Type: tea.KeyShiftTab})

	initial := stripDetailSelectionRail(stripANSIAndTrimRight(m.view.View()))
	if initial == "" {
		t.Fatal("expected detail transcript visible before mouse scrolling")
	}

	m = updateUIModel(t, m, tea.MouseMsg{Button: tea.MouseButtonWheelUp, Type: tea.MouseWheelUp})
	afterWheelUp := stripDetailSelectionRail(stripANSIAndTrimRight(m.view.View()))
	if afterWheelUp == initial {
		t.Fatal("expected detail transcript to change after mouse wheel up")
	}

	beforeWheelDownScroll := m.view.DetailScroll()
	m = updateUIModel(t, m, tea.MouseMsg{Button: tea.MouseButtonWheelDown, Type: tea.MouseWheelDown})
	if got := m.view.DetailScroll(); got >= beforeWheelDownScroll {
		t.Fatalf("expected detail wheel down after prior wheel up to move toward bottom, got %d from %d", got, beforeWheelDownScroll)
	}
	if afterWheelDown := stripDetailSelectionRail(stripANSIAndTrimRight(m.view.View())); afterWheelDown == afterWheelUp {
		t.Fatal("expected detail transcript to change after mouse wheel down")
	}
}

func TestDetailModeScrollThenEnterExpandsCenterSelectedItem(t *testing.T) {
	tests := []struct {
		name   string
		scroll tea.Msg
	}{
		{name: "mouse wheel", scroll: tea.MouseMsg{Button: tea.MouseButtonWheelUp, Type: tea.MouseWheelUp}},
		{name: "page key", scroll: tea.KeyMsg{Type: tea.KeyPgUp}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newProjectedStaticUIModel()
			m.termWidth = 80
			m.termHeight = 8
			m.syncViewport()

			for idx := 0; idx < 8; idx++ {
				callID := fmt.Sprintf("call_%d", idx)
				command := fmt.Sprintf("cmd %d", idx)
				m.forwardToView(tui.AppendTranscriptMsg{
					Role:       "tool_call",
					Text:       command,
					ToolCallID: callID,
					ToolCall:   &transcript.ToolCallMeta{ToolName: "exec_command", IsShell: true, Command: command},
				})
				m.forwardToView(tui.AppendTranscriptMsg{
					Role:       "tool_result_ok",
					ToolCallID: callID,
					Text:       fmt.Sprintf("output %d line 1\noutput %d line 2", idx, idx),
				})
			}
			m = updateUIModel(t, m, tea.KeyMsg{Type: tea.KeyShiftTab})
			m = updateUIModel(t, m, tt.scroll)

			selected := selectedDetailCommandIndex(t, m.view.View())
			m = updateUIModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})

			expanded := stripANSIAndTrimRight(m.view.View())
			if !strings.Contains(expanded, fmt.Sprintf("▼ cmd %d", selected)) || !strings.Contains(expanded, fmt.Sprintf("└ output %d line 2", selected)) {
				t.Fatalf("expected enter after %s scroll to expand selected center command %d, got %q", tt.name, selected, expanded)
			}
		})
	}
}

func TestUpDownRouteByTranscriptMode(t *testing.T) {
	m := newProjectedStaticUIModel(WithUIPromptHistory([]string{"hello"}))
	m.termWidth = 80
	m.termHeight = 8
	m.syncViewport()
	for i := 0; i < 20; i++ {
		m.forwardToView(tui.AppendTranscriptMsg{Role: "assistant", Text: fmt.Sprintf("line %d", i)})
	}

	ongoingStart := m.view.OngoingScroll()
	if ongoingStart == 0 {
		t.Fatal("expected ongoing transcript to be scrollable")
	}

	m = updateUIModel(t, m, tea.KeyMsg{Type: tea.KeyUp})
	if m.input != "hello" {
		t.Fatalf("expected ongoing mode up to recall prompt history, got %q", m.input)
	}
	if got := m.view.OngoingScroll(); got != ongoingStart {
		t.Fatalf("expected ongoing mode up not to scroll transcript, got %d from %d", got, ongoingStart)
	}

	m = updateUIModel(t, m, tea.KeyMsg{Type: tea.KeyShiftTab})
	if m.view.Mode() != tui.ModeDetail {
		t.Fatalf("expected detail mode, got %q", m.view.Mode())
	}
	initialDetail := m.view.View()

	m = updateUIModel(t, m, tea.KeyMsg{Type: tea.KeyUp})
	afterDetailUp := m.view.View()
	if afterDetailUp == initialDetail {
		t.Fatal("expected detail mode up to scroll transcript")
	}
	if m.input != "hello" {
		t.Fatalf("expected detail mode scrolling not to mutate recalled input, got %q", m.input)
	}
}

func selectedDetailCommandIndex(t *testing.T, view string) int {
	t.Helper()

	line := strings.TrimPrefix(selectedDetailContentLine(t, view), uiglyphs.SelectionRailGlyph)
	line = strings.TrimSpace(line)
	_, suffix, ok := strings.Cut(line, " cmd ")
	if !ok {
		t.Fatalf("expected selected command line, got %q in %q", line, stripANSIAndTrimRight(view))
	}
	value, parseErr := strconv.Atoi(strings.TrimSpace(suffix))
	if parseErr != nil {
		t.Fatalf("failed to parse selected command index from %q: %v", line, parseErr)
	}
	return value
}

func selectedDetailContentLine(t *testing.T, view string) string {
	t.Helper()

	for _, line := range strings.Split(stripANSIAndTrimRight(view), "\n") {
		if strings.HasPrefix(line, uiglyphs.SelectionRailGlyph) && strings.TrimSpace(strings.TrimPrefix(line, uiglyphs.SelectionRailGlyph)) != "" {
			return line
		}
	}
	t.Fatalf("expected selected detail line in %q", stripANSIAndTrimRight(view))
	return ""
}

func selectedDetailSpacerLine(t *testing.T, view string) string {
	t.Helper()

	for _, line := range strings.Split(stripANSIAndTrimRight(view), "\n") {
		if strings.HasPrefix(line, uiglyphs.SelectionRailGlyph) && strings.TrimSpace(strings.TrimPrefix(line, uiglyphs.SelectionRailGlyph)) == "" {
			return line
		}
	}
	t.Fatalf("expected selected detail spacer line in %q", stripANSIAndTrimRight(view))
	return ""
}

func stripDetailSelectionRail(view string) string {
	lines := strings.Split(view, "\n")
	for idx, line := range lines {
		if strings.HasPrefix(line, uiglyphs.SelectionRailGlyph) {
			lines[idx] = uiglyphs.SelectionRailBlank + strings.TrimPrefix(line, uiglyphs.SelectionRailGlyph)
		}
	}
	return strings.Join(lines, "\n")
}

func TestMainInputUpDownAtBoundsStayInInput(t *testing.T) {
	m := newProjectedStaticUIModel()
	m.termWidth = 80
	m.termHeight = 8
	m.syncViewport()
	for i := 0; i < 20; i++ {
		m.forwardToView(tui.AppendTranscriptMsg{Role: "assistant", Text: fmt.Sprintf("line %d", i)})
	}
	m.input = "abcd"
	m.inputCursor = 2

	start := m.view.OngoingScroll()
	if start == 0 {
		t.Fatal("expected ongoing transcript to be scrollable")
	}

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyUp})
	updated := next.(*uiModel)
	if updated.inputCursor != 0 {
		t.Fatalf("expected first up to move cursor to start, got %d", updated.inputCursor)
	}
	if got := updated.view.OngoingScroll(); got != start {
		t.Fatalf("expected first up not to scroll transcript, got %d from %d", got, start)
	}

	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyUp})
	updated = next.(*uiModel)
	if updated.inputCursor != 0 {
		t.Fatalf("expected second up at top to stay at start, got %d", updated.inputCursor)
	}
	if got := updated.view.OngoingScroll(); got != start {
		t.Fatalf("expected second up at top not to scroll transcript, got %d from %d", got, start)
	}

	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated = next.(*uiModel)
	if updated.inputCursor != len([]rune(updated.input)) {
		t.Fatalf("expected first down to move cursor to end, got %d", updated.inputCursor)
	}
	if got := updated.view.OngoingScroll(); got != start {
		t.Fatalf("expected first down not to scroll transcript, got %d from %d", got, start)
	}

	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated = next.(*uiModel)
	if updated.inputCursor != len([]rune(updated.input)) {
		t.Fatalf("expected second down at end to stay at end, got %d", updated.inputCursor)
	}
	if got := updated.view.OngoingScroll(); got != start {
		t.Fatalf("expected second down at end not to scroll transcript, got %d from %d", got, start)
	}
}

func TestReviewerRunStillAllowsEditingWithoutTranscriptScroll(t *testing.T) {
	m := newProjectedStaticUIModel()
	m.termWidth = 80
	m.termHeight = 8
	m.syncViewport()
	for i := 0; i < 20; i++ {
		m.forwardToView(tui.AppendTranscriptMsg{Role: "assistant", Text: fmt.Sprintf("line %d", i)})
	}
	m.busy = true
	m.activity = uiActivityRunning
	m.input = "keep this draft"

	start := m.view.OngoingScroll()
	if start == 0 {
		t.Fatal("expected ongoing transcript to be scrollable")
	}

	next, _ := m.Update(projectedRuntimeEventMsg(runtime.Event{Kind: runtime.EventReviewerStarted}))
	locked := next.(*uiModel)
	if !locked.reviewerBlocking {
		t.Fatal("expected reviewer running state")
	}
	if locked.isInputLocked() {
		t.Fatal("did not expect reviewer to lock input")
	}

	next, _ = locked.Update(tea.KeyMsg{Type: tea.KeyUp})
	locked = next.(*uiModel)

	next, _ = locked.Update(tea.KeyMsg{Type: tea.KeyUp})
	locked = next.(*uiModel)
	if locked.inputCursor != 0 {
		t.Fatalf("expected up to move cursor to start while reviewer runs, got %d", locked.inputCursor)
	}
	if got := locked.view.OngoingScroll(); got != start {
		t.Fatalf("expected up not to scroll transcript while reviewer runs, got %d from %d", got, start)
	}
	if locked.input != "keep this draft" {
		t.Fatalf("expected input text preserved while reviewer runs, got %q", locked.input)
	}

	next, _ = locked.Update(tea.KeyMsg{Type: tea.KeyDown})
	locked = next.(*uiModel)

	next, _ = locked.Update(tea.KeyMsg{Type: tea.KeyDown})
	locked = next.(*uiModel)
	if locked.inputCursor != len([]rune(locked.input)) {
		t.Fatalf("expected down to move cursor to end while reviewer runs, got %d", locked.inputCursor)
	}
	if got := locked.view.OngoingScroll(); got != start {
		t.Fatalf("expected down not to scroll transcript while reviewer runs, got %d from %d", got, start)
	}

	next, _ = locked.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	locked = next.(*uiModel)
	if locked.input != "keep this draftx" {
		t.Fatalf("expected input editable while reviewer runs, got %q", locked.input)
	}
}

package app

import (
	"fmt"
	"strings"
	"testing"

	"builder/cli/tui"
	"builder/server/runtime"
	"builder/shared/transcript"

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

	initial := m.view.View()
	if initial == "" {
		t.Fatal("expected detail transcript visible before scrolling")
	}

	m = updateUIModel(t, m, tea.KeyMsg{Type: tea.KeyUp})
	afterUp := m.view.View()
	if afterUp == initial {
		t.Fatal("expected detail transcript to change after up")
	}

	m = updateUIModel(t, m, tea.KeyMsg{Type: tea.KeyDown})
	afterDown := m.view.View()
	if afterDown != initial {
		t.Fatalf("expected detail transcript to return after down, got %q want %q", afterDown, initial)
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
	if !strings.Contains(collapsed, "$ cat large.txt") || !strings.Contains(collapsed, "▶︎") {
		t.Fatalf("expected collapsed compact tool row, got %q", collapsed)
	}
	if strings.Contains(collapsed, "line 2") {
		t.Fatalf("expected collapsed detail to hide tool output, got %q", collapsed)
	}

	m = updateUIModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	expanded := stripANSIAndTrimRight(m.view.View())
	if !strings.Contains(expanded, "$ cat large.txt") || !strings.Contains(expanded, "▼") || !strings.Contains(expanded, "line 2") {
		t.Fatalf("expected UI-routed enter to expand tool output, got %q", expanded)
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
	if !strings.Contains(expanded, "$ cat large.txt") || !strings.Contains(expanded, "▼") || !strings.Contains(expanded, "line 2") {
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

	initial := stripANSIAndTrimRight(m.view.View())
	if initial == "" {
		t.Fatal("expected detail transcript visible before mouse scrolling")
	}

	m = updateUIModel(t, m, tea.MouseMsg{Button: tea.MouseButtonWheelUp, Type: tea.MouseWheelUp})
	afterWheelUp := stripANSIAndTrimRight(m.view.View())
	if afterWheelUp == initial {
		t.Fatal("expected detail transcript to change after mouse wheel up")
	}

	m = updateUIModel(t, m, tea.MouseMsg{Button: tea.MouseButtonWheelDown, Type: tea.MouseWheelDown})
	afterWheelDown := stripANSIAndTrimRight(m.view.View())
	if afterWheelDown != initial {
		t.Fatalf("expected detail transcript to return after mouse wheel down, got %q want %q", afterWheelDown, initial)
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

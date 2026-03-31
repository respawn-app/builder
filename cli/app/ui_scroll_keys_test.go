package app

import (
	"fmt"
	"testing"

	"builder/cli/tui"
	"builder/server/runtime"

	tea "github.com/charmbracelet/bubbletea"
)

func TestPageKeysScrollTranscriptWhileInputFocused(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
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
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.termWidth = 80
	m.termHeight = 8
	m.syncViewport()

	for i := 0; i < 16; i++ {
		m.forwardToView(tui.AppendTranscriptMsg{Role: "assistant", Text: fmt.Sprintf("line %d", i)})
	}
	m = updateUIModel(t, m, tea.KeyMsg{Type: tea.KeyShiftTab})

	initial := stripANSIAndTrimRight(m.view.View())
	if initial == "" {
		t.Fatal("expected detail transcript visible before scrolling")
	}

	m = updateUIModel(t, m, tea.KeyMsg{Type: tea.KeyUp})
	afterUp := stripANSIAndTrimRight(m.view.View())
	if afterUp == initial {
		t.Fatal("expected detail transcript to change after up")
	}

	m = updateUIModel(t, m, tea.KeyMsg{Type: tea.KeyDown})
	afterDown := stripANSIAndTrimRight(m.view.View())
	if afterDown != initial {
		t.Fatalf("expected detail transcript to return after down, got %q want %q", afterDown, initial)
	}
}

func TestDetailModeMouseWheelScrollTranscript(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
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
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent), WithUIPromptHistory([]string{"hello"})).(*uiModel)
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
	initialDetail := stripANSIAndTrimRight(m.view.View())

	m = updateUIModel(t, m, tea.KeyMsg{Type: tea.KeyUp})
	afterDetailUp := stripANSIAndTrimRight(m.view.View())
	if afterDetailUp == initialDetail {
		t.Fatal("expected detail mode up to scroll transcript")
	}
	if m.input != "hello" {
		t.Fatalf("expected detail mode scrolling not to mutate recalled input, got %q", m.input)
	}
}

func TestMainInputUpDownAtBoundsStayInInput(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
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
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
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

	next, _ := m.Update(runtimeEventMsg{event: runtime.Event{Kind: runtime.EventReviewerStarted}})
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

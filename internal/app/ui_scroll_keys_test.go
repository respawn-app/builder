package app

import (
	"fmt"
	"testing"

	"builder/internal/runtime"
	"builder/internal/tui"

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

func TestMainInputUpDownAtBoundsScrollsTranscript(t *testing.T) {
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
	afterUp := updated.view.OngoingScroll()
	if afterUp >= start {
		t.Fatalf("expected second up at top to scroll transcript up, got %d from %d", afterUp, start)
	}

	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated = next.(*uiModel)
	if updated.inputCursor != len([]rune(updated.input)) {
		t.Fatalf("expected first down to move cursor to end, got %d", updated.inputCursor)
	}
	if got := updated.view.OngoingScroll(); got != afterUp {
		t.Fatalf("expected first down not to scroll transcript, got %d from %d", got, afterUp)
	}

	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated = next.(*uiModel)
	if got := updated.view.OngoingScroll(); got <= afterUp {
		t.Fatalf("expected second down at end to scroll transcript down, got %d from %d", got, afterUp)
	}
}

func TestReviewerRunStillAllowsTranscriptScrollAndEditing(t *testing.T) {
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
	afterUp := locked.view.OngoingScroll()
	if afterUp >= start {
		t.Fatalf("expected up to scroll transcript while reviewer runs, got %d from %d", afterUp, start)
	}
	if locked.input != "keep this draft" {
		t.Fatalf("expected input text preserved while reviewer runs, got %q", locked.input)
	}

	next, _ = locked.Update(tea.KeyMsg{Type: tea.KeyDown})
	locked = next.(*uiModel)

	next, _ = locked.Update(tea.KeyMsg{Type: tea.KeyDown})
	locked = next.(*uiModel)
	if got := locked.view.OngoingScroll(); got <= afterUp {
		t.Fatalf("expected down to scroll transcript while reviewer runs, got %d from %d", got, afterUp)
	}

	next, _ = locked.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	locked = next.(*uiModel)
	if locked.input != "keep this draftx" {
		t.Fatalf("expected input editable while reviewer runs, got %q", locked.input)
	}
}

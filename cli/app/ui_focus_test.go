package app

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestUIModelTracksTerminalFocus(t *testing.T) {
	m := newProjectedStaticUIModel()
	if m.TerminalFocusKnown() {
		t.Fatal("expected terminal focus to start unknown")
	}
	if m.TerminalFocused() {
		t.Fatal("expected unknown terminal focus to require attention")
	}

	next, _ := m.Update(tea.BlurMsg{})
	updated := next.(*uiModel)
	if !updated.TerminalFocusKnown() {
		t.Fatal("expected terminal blur to mark focus known")
	}
	if updated.TerminalFocused() {
		t.Fatal("expected terminal blur to mark model unfocused")
	}

	next, _ = updated.Update(tea.FocusMsg{})
	updated = next.(*uiModel)
	if !updated.TerminalFocused() {
		t.Fatal("expected terminal focus to mark model focused")
	}
}

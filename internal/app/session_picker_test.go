package app

import (
	"fmt"
	"testing"
	"time"

	"builder/internal/session"
	tea "github.com/charmbracelet/bubbletea"
)

func TestSessionPickerScrollsAndSelects(t *testing.T) {
	now := time.Date(2026, time.February, 8, 12, 0, 0, 0, time.UTC)
	summaries := make([]session.Summary, 0, 20)
	for i := 0; i < 20; i++ {
		summaries = append(summaries, session.Summary{
			SessionID: fmt.Sprintf("s-%02d", i),
			UpdatedAt: now.Add(-time.Duration(i) * time.Minute),
		})
	}

	m := newSessionPickerModel(summaries)
	for i := 0; i < 15; i++ {
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m = next.(*sessionPickerModel)
	}

	if m.cursor != 15 {
		t.Fatalf("cursor=%d want 15", m.cursor)
	}
	if m.offset == 0 {
		t.Fatalf("offset should advance for scroll")
	}

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(*sessionPickerModel)
	if m.result.Session == nil {
		t.Fatal("expected selected session")
	}
	if m.result.Session.SessionID != summaries[15].SessionID {
		t.Fatalf("selected=%s want %s", m.result.Session.SessionID, summaries[15].SessionID)
	}
}

func TestSessionPickerNewAndCancel(t *testing.T) {
	m := newSessionPickerModel(nil)

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	m = next.(*sessionPickerModel)
	if !m.result.CreateNew {
		t.Fatal("expected create-new result")
	}

	m = newSessionPickerModel(nil)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	m = next.(*sessionPickerModel)
	if !m.result.Canceled {
		t.Fatal("expected canceled result")
	}
}

package app

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"builder/internal/session"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
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

	m := newSessionPickerModel(summaries, "dark")
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 8})
	m = next.(*sessionPickerModel)
	for i := 0; i < 16; i++ {
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m = next.(*sessionPickerModel)
	}

	if m.cursor != 16 {
		t.Fatalf("cursor=%d want 16", m.cursor)
	}
	if m.offset == 0 {
		t.Fatalf("offset should advance for scroll")
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(*sessionPickerModel)
	if m.result.Session == nil {
		t.Fatal("expected selected session")
	}
	if m.result.Session.SessionID != summaries[15].SessionID {
		t.Fatalf("selected=%s want %s", m.result.Session.SessionID, summaries[15].SessionID)
	}
}

func TestSessionPickerEnterDefaultsToCreateNew(t *testing.T) {
	m := newSessionPickerModel(nil, "dark")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(*sessionPickerModel)
	if !m.result.CreateNew {
		t.Fatal("expected default selection to create a new session")
	}
}

func TestSessionPickerNewHotkeyAndCancel(t *testing.T) {
	m := newSessionPickerModel(nil, "dark")

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	m = next.(*sessionPickerModel)
	if !m.result.CreateNew {
		t.Fatal("expected create-new result")
	}

	m = newSessionPickerModel(nil, "dark")
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	m = next.(*sessionPickerModel)
	if !m.result.Canceled {
		t.Fatal("expected canceled result")
	}
}

func TestSessionPickerIgnoresMouseSGRRunes(t *testing.T) {
	m := newSessionPickerModel(nil, "dark")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("[<64;81;40M[<65;80;39M")})
	m = next.(*sessionPickerModel)
	if m.result.CreateNew || m.result.Canceled || m.result.Session != nil {
		t.Fatalf("expected mouse sgr runes ignored, got result=%+v", m.result)
	}
}

func TestSessionPickerViewOmitsHotkeyLegend(t *testing.T) {
	m := newSessionPickerModel(nil, "dark")
	out := ansi.Strip(m.View())
	if !strings.Contains(out, "Select session") {
		t.Fatalf("expected title in output, got %q", out)
	}
	if strings.Contains(out, "Enter=resume") {
		t.Fatalf("expected hotkey legend removed, got %q", out)
	}
}

func TestSessionPickerPrefersSessionName(t *testing.T) {
	now := time.Date(2026, time.February, 8, 12, 0, 0, 0, time.UTC)
	m := newSessionPickerModel([]session.Summary{{
		SessionID: "abc123",
		Name:      "Incident Triage",
		UpdatedAt: now,
	}}, "dark")
	out := ansi.Strip(m.View())
	if !strings.Contains(out, "Incident Triage") {
		t.Fatalf("expected named session in output, got %q", out)
	}
	if strings.Contains(out, "abc123") {
		t.Fatalf("expected id hidden when name is present, got %q", out)
	}
}

package app

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"builder/cli/tui"
	"builder/shared/clientui"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestSessionPickerScrollsAndSelects(t *testing.T) {
	now := time.Date(2026, time.February, 8, 12, 0, 0, 0, time.UTC)
	summaries := make([]clientui.SessionSummary, 0, 20)
	for i := 0; i < 20; i++ {
		summaries = append(summaries, clientui.SessionSummary{
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

func TestSessionPickerHeaderUsesAppForeground(t *testing.T) {
	m := newSessionPickerModel(nil, "dark")
	header := m.renderHeader()
	expectedPrefix := strings.TrimSuffix(tui.ApplyThemeDefaultForeground("x", "dark"), "x\x1b[0m")
	if !strings.HasPrefix(header, expectedPrefix) {
		t.Fatalf("expected session picker header to start with app foreground, got %q", header)
	}
	if stripped := ansi.Strip(header); !strings.Contains(stripped, "Select session") {
		t.Fatalf("expected session picker header text preserved, got %q", stripped)
	}
}

func TestSessionPickerPrefersSessionName(t *testing.T) {
	now := time.Date(2026, time.February, 8, 12, 0, 0, 0, time.UTC)
	m := newSessionPickerModel([]clientui.SessionSummary{{
		SessionID:          "abc123",
		Name:               "Incident Triage",
		FirstPromptPreview: "Investigate broken startup flow",
		UpdatedAt:          now,
	}}, "dark")
	out := ansi.Strip(m.View())
	if !strings.Contains(out, "Incident Triage") {
		t.Fatalf("expected named session in output, got %q", out)
	}
	if !strings.Contains(out, "Investigate broken startup flow") {
		t.Fatalf("expected preview visible under session name, got %q", out)
	}
	if strings.Contains(out, "abc123") {
		t.Fatalf("expected id hidden when name is present, got %q", out)
	}
}

func TestSessionPickerUsesFirstPromptPreviewWhenUnnamed(t *testing.T) {
	now := time.Date(2026, time.February, 8, 12, 0, 0, 0, time.UTC)
	m := newSessionPickerModel([]clientui.SessionSummary{{
		SessionID:          "abc123",
		FirstPromptPreview: "Investigate broken startup flow",
		UpdatedAt:          now,
	}}, "dark")
	out := ansi.Strip(m.View())
	if !strings.Contains(out, "Investigate broken startup flow") {
		t.Fatalf("expected preview in output, got %q", out)
	}
	if !strings.Contains(out, "abc123") {
		t.Fatalf("expected session id to remain title when no name is set, got %q", out)
	}
}

func TestSessionPickerRendersPreviewOnSecondLine(t *testing.T) {
	now := time.Date(2026, time.February, 8, 12, 0, 0, 0, time.UTC)
	m := newSessionPickerModel([]clientui.SessionSummary{{
		SessionID:          "abc123",
		Name:               "Incident Triage",
		FirstPromptPreview: "Investigate broken startup flow",
		UpdatedAt:          now,
	}}, "dark")
	out := ansi.Strip(m.View())
	lines := strings.Split(out, "\n")
	titleLine := -1
	for i, line := range lines {
		if strings.Contains(line, "Incident Triage") {
			titleLine = i
			break
		}
	}
	if titleLine < 0 || titleLine+1 >= len(lines) {
		t.Fatalf("expected title line followed by preview line, got %q", out)
	}
	if !strings.Contains(lines[titleLine+1], "Investigate broken startup flow") {
		t.Fatalf("expected preview on line after title, got %q", out)
	}
}

func TestSessionPickerAddsBlankLineAfterPreview(t *testing.T) {
	now := time.Date(2026, time.February, 8, 12, 0, 0, 0, time.UTC)
	m := newSessionPickerModel([]clientui.SessionSummary{
		{
			SessionID:          "abc123",
			Name:               "Incident Triage",
			FirstPromptPreview: "Investigate broken startup flow",
			UpdatedAt:          now,
		},
		{
			SessionID: "def456",
			Name:      "Follow-up",
			UpdatedAt: now.Add(-time.Minute),
		},
	}, "dark")
	out := ansi.Strip(m.View())
	lines := strings.Split(out, "\n")
	previewLine := -1
	for i, line := range lines {
		if strings.Contains(line, "Investigate broken startup flow") {
			previewLine = i
			break
		}
	}
	if previewLine < 0 || previewLine+2 >= len(lines) {
		t.Fatalf("expected preview followed by blank line and next item, got %q", out)
	}
	if strings.TrimSpace(lines[previewLine+1]) != "" {
		t.Fatalf("expected blank separator after preview, got %q", out)
	}
	if !strings.Contains(lines[previewLine+2], "Follow-up") {
		t.Fatalf("expected next item after blank separator, got %q", out)
	}
}

func TestSessionPickerAddsBlankLineAfterCreateNewSession(t *testing.T) {
	now := time.Date(2026, time.February, 8, 12, 0, 0, 0, time.UTC)
	m := newSessionPickerModel([]clientui.SessionSummary{{
		SessionID: "abc123",
		Name:      "Incident Triage",
		UpdatedAt: now,
	}}, "dark")
	out := ansi.Strip(m.View())
	lines := strings.Split(out, "\n")
	createLine := -1
	for i, line := range lines {
		if strings.Contains(line, "Create a new session") {
			createLine = i
			break
		}
	}
	if createLine < 0 || createLine+2 >= len(lines) {
		t.Fatalf("expected create-new row followed by blank line and next item, got %q", out)
	}
	if strings.TrimSpace(lines[createLine+1]) != "" {
		t.Fatalf("expected blank separator after create-new row, got %q", out)
	}
	if !strings.Contains(lines[createLine+2], "Incident Triage") {
		t.Fatalf("expected next item after blank separator, got %q", out)
	}
}

func TestSessionPickerAddsBlankLineAfterSessionWithoutPreview(t *testing.T) {
	now := time.Date(2026, time.February, 8, 12, 0, 0, 0, time.UTC)
	m := newSessionPickerModel([]clientui.SessionSummary{
		{
			SessionID: "abc123",
			Name:      "Incident Triage",
			UpdatedAt: now,
		},
		{
			SessionID: "def456",
			Name:      "Follow-up",
			UpdatedAt: now.Add(-time.Minute),
		},
	}, "dark")
	out := ansi.Strip(m.View())
	lines := strings.Split(out, "\n")
	incidentLine := -1
	for i, line := range lines {
		if strings.Contains(line, "Incident Triage") {
			incidentLine = i
			break
		}
	}
	if incidentLine < 0 || incidentLine+2 >= len(lines) {
		t.Fatalf("expected session line followed by blank line and next item, got %q", out)
	}
	if strings.TrimSpace(lines[incidentLine+1]) != "" {
		t.Fatalf("expected blank separator after session without preview, got %q", out)
	}
	if !strings.Contains(lines[incidentLine+2], "Follow-up") {
		t.Fatalf("expected next item after blank separator, got %q", out)
	}
}

func TestSessionPickerScrollsWithTwoLineEntries(t *testing.T) {
	now := time.Date(2026, time.February, 8, 12, 0, 0, 0, time.UTC)
	summaries := make([]clientui.SessionSummary, 0, 8)
	for i := 0; i < 8; i++ {
		summaries = append(summaries, clientui.SessionSummary{
			SessionID:          fmt.Sprintf("s-%02d", i),
			Name:               fmt.Sprintf("Session %d", i),
			FirstPromptPreview: fmt.Sprintf("Prompt %d", i),
			UpdatedAt:          now.Add(-time.Duration(i) * time.Minute),
		})
	}

	m := newSessionPickerModel(summaries, "dark")
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 8})
	m = next.(*sessionPickerModel)
	for i := 0; i < 4; i++ {
		next, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m = next.(*sessionPickerModel)
	}

	if m.cursor != 4 {
		t.Fatalf("cursor=%d want 4", m.cursor)
	}
	if m.offset == 0 {
		t.Fatalf("offset should advance for two-line rows")
	}
	visible := ansi.Strip(m.View())
	if !strings.Contains(visible, "Session 3") || !strings.Contains(visible, "Prompt 3") {
		t.Fatalf("expected selected neighborhood to remain visible, got %q", visible)
	}
}

func TestSessionPickerScrollsWithSingleLineEntriesAndBlankSeparators(t *testing.T) {
	now := time.Date(2026, time.February, 8, 12, 0, 0, 0, time.UTC)
	m := newSessionPickerModel([]clientui.SessionSummary{
		{
			SessionID: "s-00",
			Name:      "Session 0",
			UpdatedAt: now,
		},
		{
			SessionID: "s-01",
			Name:      "Session 1",
			UpdatedAt: now.Add(-time.Minute),
		},
		{
			SessionID: "s-02",
			Name:      "Session 2",
			UpdatedAt: now.Add(-2 * time.Minute),
		},
	}, "dark")
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 5})
	m = next.(*sessionPickerModel)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = next.(*sessionPickerModel)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = next.(*sessionPickerModel)

	if m.cursor != 2 {
		t.Fatalf("cursor=%d want 2", m.cursor)
	}
	if m.offset != 1 {
		t.Fatalf("offset=%d want 1", m.offset)
	}
	out := ansi.Strip(m.View())
	if !strings.Contains(out, "Session 0") || !strings.Contains(out, "Session 1") {
		t.Fatalf("expected adjacent single-line sessions visible, got %q", out)
	}
	lines := strings.Split(out, "\n")
	session0Line := -1
	for i, line := range lines {
		if strings.Contains(line, "Session 0") {
			session0Line = i
			break
		}
	}
	if session0Line < 0 || session0Line+2 >= len(lines) {
		t.Fatalf("expected visible session followed by blank separator and cursor row, got %q", out)
	}
	if strings.TrimSpace(lines[session0Line+1]) != "" {
		t.Fatalf("expected blank separator between single-line sessions, got %q", out)
	}
	if !strings.Contains(lines[session0Line+2], "Session 1") {
		t.Fatalf("expected cursor row after blank separator, got %q", out)
	}
	if strings.Contains(out, "Session 2") {
		t.Fatalf("did not expect extra session to fit in tight viewport, got %q", out)
	}
}

func TestSessionPickerTightViewportShowsTitleWithoutOverflowingPreview(t *testing.T) {
	now := time.Date(2026, time.February, 8, 12, 0, 0, 0, time.UTC)
	m := newSessionPickerModel([]clientui.SessionSummary{{
		SessionID:          "abc123",
		Name:               "Incident Triage",
		FirstPromptPreview: "Investigate broken startup flow",
		UpdatedAt:          now,
	}}, "dark")
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 3})
	m = next.(*sessionPickerModel)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = next.(*sessionPickerModel)

	out := ansi.Strip(m.View())
	if !strings.Contains(out, "Incident Triage") {
		t.Fatalf("expected title visible in tight viewport, got %q", out)
	}
	if strings.Contains(out, "Investigate broken startup flow") {
		t.Fatalf("expected preview hidden when only one content line fits, got %q", out)
	}
	lines := strings.Split(out, "\n")
	contentLines := 0
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			contentLines++
		}
	}
	if contentLines != 2 {
		// header + one visible content line
		t.Fatalf("expected header plus one content line, got %q", out)
	}
}

func TestSessionPickerKeepsMixedPreviewViewportVisibleWithoutExtraScroll(t *testing.T) {
	now := time.Date(2026, time.February, 8, 12, 0, 0, 0, time.UTC)
	m := newSessionPickerModel([]clientui.SessionSummary{
		{
			SessionID:          "s-00",
			Name:               "Session 0",
			FirstPromptPreview: "Prompt 0",
			UpdatedAt:          now,
		},
		{
			SessionID:          "s-01",
			Name:               "Session 1",
			FirstPromptPreview: "Prompt 1",
			UpdatedAt:          now.Add(-time.Minute),
		},
		{
			SessionID:          "s-02",
			Name:               "Session 2",
			FirstPromptPreview: "Prompt 2",
			UpdatedAt:          now.Add(-2 * time.Minute),
		},
	}, "dark")
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 6})
	m = next.(*sessionPickerModel)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = next.(*sessionPickerModel)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = next.(*sessionPickerModel)

	if m.cursor != 2 {
		t.Fatalf("cursor=%d want 2", m.cursor)
	}
	if m.offset != 1 {
		t.Fatalf("offset=%d want 1", m.offset)
	}
	out := ansi.Strip(m.View())
	if !strings.Contains(out, "Session 0") || !strings.Contains(out, "Prompt 0") {
		t.Fatalf("expected prior preview row to remain visible, got %q", out)
	}
	if !strings.Contains(out, "Session 1") {
		t.Fatalf("expected cursor row visible, got %q", out)
	}
	if strings.Contains(out, "Prompt 1") {
		t.Fatalf("expected cursor row preview to degrade away in mixed viewport, got %q", out)
	}
}

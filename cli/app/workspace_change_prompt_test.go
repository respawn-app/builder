package app

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestWorkspaceChangePromptDefaultsToNo(t *testing.T) {
	m := newWorkspaceChangePromptModel("/tmp/old", "/tmp/new", "dark")
	if m.cursor != 1 {
		t.Fatalf("cursor = %d, want 1", m.cursor)
	}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(*workspaceChangePromptModel)
	if m.result.Rebind {
		t.Fatal("expected default enter action to return to picker")
	}
}

func TestWorkspaceChangePromptYesHotkeyRebinds(t *testing.T) {
	m := newWorkspaceChangePromptModel("/tmp/old", "/tmp/new", "dark")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	m = next.(*workspaceChangePromptModel)
	if !m.result.Rebind {
		t.Fatal("expected y hotkey to choose rebind")
	}
}

func TestWorkspaceChangePromptRenderHeaderIsNeverInset(t *testing.T) {
	m := newWorkspaceChangePromptModel("/tmp/old", "/tmp/new", "dark")
	if got := ansi.Strip(m.renderHeader()); got != "Workspace changed" {
		t.Fatalf("renderHeader = %q, want exact unpadded title", got)
	}
}

func TestWorkspaceChangePromptViewIncludesTitleAndPaths(t *testing.T) {
	m := newWorkspaceChangePromptModel("/tmp/old", "/tmp/new", "dark")
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 12})
	m = next.(*workspaceChangePromptModel)
	out := ansi.Strip(m.View())
	lines := strings.Split(out, "\n")
	if !strings.Contains(out, "Workspace changed") {
		t.Fatalf("expected title in output, got %q", out)
	}
	if len(lines) == 0 || strings.HasPrefix(lines[0], " ") {
		t.Fatalf("did not expect title padding, got first line %q", lines[0])
	}
	if !strings.Contains(out, "/tmp/old") {
		t.Fatalf("expected previous path in output, got %q", out)
	}
	if !strings.Contains(out, "/tmp/new") {
		t.Fatalf("expected current path in output, got %q", out)
	}
	if !strings.Contains(out, "1. Yes") || !strings.Contains(out, "2. No") {
		t.Fatalf("expected yes/no options in output, got %q", out)
	}
	if strings.Contains(out, "─") {
		t.Fatalf("did not expect framed border lines, got %q", out)
	}
	if got := len(lines); got != 12 {
		t.Fatalf("line count = %d, want 12", got)
	}
	descriptionLine := -1
	optionLine := -1
	for i, line := range lines {
		if strings.Contains(line, "This session started in") {
			descriptionLine = i
		}
		if strings.Contains(line, "1. Yes") {
			optionLine = i
			break
		}
	}
	if descriptionLine < 0 || optionLine < 0 || optionLine-descriptionLine < 2 {
		t.Fatalf("expected blank line between description and options, got %q", out)
	}
}

func TestWorkspaceChangePromptNarrowViewportStillShowsOptionsAndFillsHeight(t *testing.T) {
	m := newWorkspaceChangePromptModel("/tmp/old-workspace", "/tmp/new-workspace", "dark")
	next, _ := m.Update(tea.WindowSizeMsg{Width: 40, Height: 6})
	m = next.(*workspaceChangePromptModel)
	out := ansi.Strip(m.View())
	if got := len(strings.Split(out, "\n")); got != 6 {
		t.Fatalf("line count = %d, want 6", got)
	}
	if !strings.Contains(out, "Workspace changed") {
		t.Fatalf("expected title in output, got %q", out)
	}
	if !strings.Contains(out, "1. Yes") || !strings.Contains(out, "2. No") {
		t.Fatalf("expected options visible in tight viewport, got %q", out)
	}
	if strings.Contains(out, "─") {
		t.Fatalf("did not expect framed border lines, got %q", out)
	}
}

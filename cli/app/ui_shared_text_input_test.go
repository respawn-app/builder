package app

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	xansi "github.com/charmbracelet/x/ansi"
)

func TestUISharedTextInputUsesSharedGraphemeEditing(t *testing.T) {
	input := newUISharedTextInput("a👍e\u0301b")
	input.Focus()
	input.SetPosition(len([]rune("a👍e\u0301")))

	input.Update(tea.KeyMsg{Type: tea.KeyLeft})
	if got, want := input.Position(), len([]rune("a👍")); got != want {
		t.Fatalf("cursor after left = %d, want %d", got, want)
	}

	input.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	if got := input.Value(); got != "ae\u0301b" {
		t.Fatalf("value after backspace = %q", got)
	}
	if got, want := input.Position(), len([]rune("a")); got != want {
		t.Fatalf("cursor after backspace = %d, want %d", got, want)
	}
}

func TestUISharedTextInputKeepsSingleLineInvariant(t *testing.T) {
	input := newUISharedTextInput("")
	input.Focus()

	input.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a\nb")})
	if got := input.Value(); got != "ab" {
		t.Fatalf("value = %q, want single-line text", got)
	}
	if got, want := input.Position(), len([]rune("ab")); got != want {
		t.Fatalf("cursor = %d, want %d", got, want)
	}
}

func TestUISharedTextInputMultilinePastePreservesInsertionCursor(t *testing.T) {
	input := newUISharedTextInput("ac")
	input.Focus()
	input.SetPosition(1)

	input.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b\nd")})
	if got := input.Value(); got != "abdc" {
		t.Fatalf("value = %q, want newline-stripped insertion", got)
	}
	if got, want := input.Position(), len([]rune("abd")); got != want {
		t.Fatalf("cursor = %d, want %d", got, want)
	}
}

func TestUISharedTextInputForwardDeleteUsesSharedEditor(t *testing.T) {
	input := newUISharedTextInput("a👍b")
	input.Focus()
	input.SetPosition(len([]rune("a")))

	input.Update(tea.KeyMsg{Type: tea.KeyDelete})
	if got := input.Value(); got != "ab" {
		t.Fatalf("value after forward delete = %q", got)
	}
	if got, want := input.Position(), len([]rune("a")); got != want {
		t.Fatalf("cursor after forward delete = %d, want %d", got, want)
	}
}

func TestUISharedTextInputDeleteCurrentLineUsesSharedPolicy(t *testing.T) {
	input := newUISharedTextInput("project name")
	input.Focus()
	input.SetPosition(len([]rune("project")))

	input.Update(tea.KeyMsg{Type: keyTypeSuperBackspaceCSI})
	if got := input.Value(); got != "" {
		t.Fatalf("value after delete current line = %q, want empty", got)
	}
	if got := input.Position(); got != 0 {
		t.Fatalf("cursor after delete current line = %d, want 0", got)
	}
}

func TestStartupEditableInputMasksAndRendersPlaceholder(t *testing.T) {
	masked := xansi.Strip(renderStartupEditableInput(16, 8, "dark", uiEditableInputRenderSpec{
		Prefix:       "› ",
		Text:         "secret",
		CursorIndex:  -1,
		RenderCursor: true,
		Mask:         '*',
	}))
	if strings.Contains(masked, "secret") {
		t.Fatalf("masked startup input exposed sensitive text: %q", masked)
	}
	if !strings.Contains(masked, "› ******") {
		t.Fatalf("masked startup input missing mask runes: %q", masked)
	}

	placeholder := xansi.Strip(renderStartupEditableInput(16, 8, "dark", uiEditableInputRenderSpec{
		Prefix:       "› ",
		Placeholder:  "project name",
		RenderCursor: true,
	}))
	if !strings.Contains(placeholder, "› project name") {
		t.Fatalf("startup input missing placeholder: %q", placeholder)
	}
}

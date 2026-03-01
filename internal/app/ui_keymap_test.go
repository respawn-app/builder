package app

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestNormalizeKeyMsgStripsConcatenatedMouseSGRRunes(t *testing.T) {
	message := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("[<64;74;25M[<64;74;25M[<65;74;25M")}
	_, ok := normalizeKeyMsg(message)
	if ok {
		t.Fatal("expected concatenated mouse sgr reports to be consumed")
	}
}

func TestNormalizeKeyMsgStripsEscPrefixedMouseSGRRunes(t *testing.T) {
	message := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("\x1b[<64;74;25M")}
	_, ok := normalizeKeyMsg(message)
	if ok {
		t.Fatal("expected esc-prefixed mouse sgr report to be consumed")
	}
}

func TestNormalizeKeyMsgPreservesNonMouseRunes(t *testing.T) {
	message := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("hello")}
	normalized, ok := normalizeKeyMsg(message)
	if !ok {
		t.Fatal("expected non-mouse runes to be preserved")
	}
	if string(normalized.Runes) != "hello" {
		t.Fatalf("expected runes unchanged, got %q", string(normalized.Runes))
	}
}

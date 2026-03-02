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

func TestNormalizeKeyMsgRecognizesShiftEnterCSIUVariants(t *testing.T) {
	tests := []struct {
		name     string
		rendered string
	}{
		{name: "bare csi-u", rendered: "?CSI[49 51 59 50 117]?"},
		{name: "esc prefixed csi-u", rendered: "?CSI[50 55 59 50 59 49 51 117]?"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			normalized, ok := normalizeKeyMsg(testUnknownCSISequence{rendered: tc.rendered})
			if !ok {
				t.Fatal("expected shift+enter csi sequence to normalize")
			}
			if normalized.Type != keyTypeShiftEnterCSI {
				t.Fatalf("expected keyTypeShiftEnterCSI, got %v", normalized.Type)
			}
		})
	}
}

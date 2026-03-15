package app

import (
	"reflect"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

type testBubbleTeaUnknownCSISequence []byte

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
		name string
		seq  string
	}{
		{name: "bare csi-u", seq: "\x1b[13;2u"},
		{name: "esc prefixed csi-u", seq: "\x1b[27;2;13u"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			msg := adaptCustomKeyMsg(testBubbleTeaUnknownCSISequence(tc.seq))
			normalized, ok := normalizeKeyMsg(msg)
			if !ok {
				t.Fatal("expected shift+enter csi sequence to normalize")
			}
			if normalized.Type != keyTypeShiftEnterCSI {
				t.Fatalf("expected keyTypeShiftEnterCSI, got %v", normalized.Type)
			}
		})
	}
}

func TestAdaptCustomKeyMsgLeavesNonCustomUnknownCSIUntouched(t *testing.T) {
	msg := testBubbleTeaUnknownCSISequence("\x1b[1;9A")
	adapted := adaptCustomKeyMsg(msg)
	if reflect.TypeOf(adapted) != reflect.TypeOf(msg) {
		t.Fatalf("expected non-custom unknown csi to remain untouched, got %T", adapted)
	}
}

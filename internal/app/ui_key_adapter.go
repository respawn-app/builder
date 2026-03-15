package app

import (
	"reflect"

	tea "github.com/charmbracelet/bubbletea"
)

func adaptCustomKeyMsg(msg tea.Msg) tea.Msg {
	seq, ok := extractUnknownCSISequence(msg)
	if !ok {
		return msg
	}

	switch {
	case isCtrlEnterCSISequence(seq):
		return customKeyMsg{Kind: customKeyCtrlEnter}
	case isShiftEnterCSISequence(seq):
		return customKeyMsg{Kind: customKeyShiftEnter}
	case isCtrlBackspaceCSISequence(seq):
		return customKeyMsg{Kind: customKeyCtrlBackspace}
	case isSuperBackspaceCSISequence(seq):
		return customKeyMsg{Kind: customKeySuperBackspace}
	default:
		return msg
	}
}

func extractUnknownCSISequence(msg tea.Msg) (string, bool) {
	if bytes, ok := decodeUnknownCSISequenceMsgBytes(msg); ok {
		return decodeUnknownCSISequenceBytes(bytes)
	}
	return "", false
}

func decodeUnknownCSISequenceMsgBytes(msg tea.Msg) ([]byte, bool) {
	value := reflect.ValueOf(msg)
	if !value.IsValid() || value.Kind() != reflect.Slice {
		return nil, false
	}
	if value.Type().Elem().Kind() != reflect.Uint8 {
		return nil, false
	}
	bytes := make([]byte, value.Len())
	for i := range len(bytes) {
		part := value.Index(i).Uint()
		if part > 255 {
			return nil, false
		}
		bytes[i] = byte(part)
	}
	return bytes, true
}

func decodeUnknownCSISequenceBytes(bytes []byte) (string, bool) {
	if len(bytes) < 3 {
		return "", false
	}
	if bytes[0] != '\x1b' || bytes[1] != '[' {
		return "", false
	}
	return string(bytes[2:]), true
}

func customKeyProgramFilter(_ tea.Model, msg tea.Msg) tea.Msg {
	return adaptCustomKeyMsg(msg)
}

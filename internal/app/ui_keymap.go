package app

import (
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

const (
	keyTypeCtrlEnterCSI      tea.KeyType = -1024
	keyTypeShiftEnterCSI     tea.KeyType = -1025
	keyTypeCtrlBackspaceCSI  tea.KeyType = -1026
	keyTypeSuperBackspaceCSI tea.KeyType = -1027
)

func normalizeKeyMsg(msg tea.Msg) (tea.KeyMsg, bool) {
	if keyMsg, ok := msg.(tea.KeyMsg); ok {
		if keyMsg.Type == tea.KeyRunes {
			filtered, removed := stripMouseSGRRunes(keyMsg.Runes)
			if removed {
				if len(filtered) == 0 {
					return tea.KeyMsg{}, false
				}
				keyMsg.Runes = filtered
			}
		}
		return keyMsg, true
	}
	seq, ok := parseUnknownCSISequence(msg)
	if !ok {
		return tea.KeyMsg{}, false
	}
	if isCtrlEnterCSISequence(seq) {
		return tea.KeyMsg{Type: keyTypeCtrlEnterCSI}, true
	}
	if isShiftEnterCSISequence(seq) {
		return tea.KeyMsg{Type: keyTypeShiftEnterCSI}, true
	}
	if isCtrlBackspaceCSISequence(seq) {
		return tea.KeyMsg{Type: keyTypeCtrlBackspaceCSI}, true
	}
	if isSuperBackspaceCSISequence(seq) {
		return tea.KeyMsg{Type: keyTypeSuperBackspaceCSI}, true
	}
	return tea.KeyMsg{}, false
}

func parseUnknownCSISequence(msg tea.Msg) (string, bool) {
	stringer, ok := msg.(fmt.Stringer)
	if !ok {
		return "", false
	}
	raw := stringer.String()
	if !strings.HasPrefix(raw, "?CSI[") || !strings.HasSuffix(raw, "]?") {
		return "", false
	}
	body := strings.TrimSuffix(strings.TrimPrefix(raw, "?CSI["), "]?")
	fields := strings.Fields(body)
	if len(fields) == 0 {
		return "", false
	}
	bytes := make([]byte, 0, len(fields))
	for _, field := range fields {
		value, err := strconv.Atoi(field)
		if err != nil || value < 0 || value > 255 {
			return "", false
		}
		bytes = append(bytes, byte(value))
	}
	return string(bytes), true
}

func isCtrlEnterCSISequence(seq string) bool {
	switch seq {
	case "13;5u", "13;5~", "27;5;13u", "27;5;13~":
		return true
	default:
		return false
	}
}

func isShiftEnterCSISequence(seq string) bool {
	switch seq {
	case "13;2u", "13;2~", "27;2;13u", "27;2;13~":
		return true
	default:
		return false
	}
}

func isCtrlBackspaceCSISequence(seq string) bool {
	switch seq {
	case "127;5u", "127;5~", "8;5u", "8;5~", "27;5;127u", "27;5;127~", "27;5;8u", "27;5;8~":
		return true
	default:
		return false
	}
}

func isSuperBackspaceCSISequence(seq string) bool {
	switch seq {
	case "127;9u", "127;9~", "8;9u", "8;9~", "27;9;127u", "27;9;127~", "27;9;8u", "27;9;8~":
		return true
	default:
		return false
	}
}

package app

import (
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

func normalizeKeyMsg(msg tea.Msg) (tea.KeyMsg, bool) {
	if keyMsg, ok := msg.(tea.KeyMsg); ok {
		return keyMsg, true
	}
	seq, ok := parseUnknownCSISequence(msg)
	if !ok || !isCtrlEnterCSISequence(seq) {
		return tea.KeyMsg{}, false
	}
	return tea.KeyMsg{Type: tea.KeyCtrlJ}, true
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

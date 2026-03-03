package app

import (
	"fmt"
	"runtime"
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
	normalized, ok, _ := normalizeKeyMsgWithSource(msg)
	return normalized, ok
}

func normalizeKeyMsgWithSource(msg tea.Msg) (tea.KeyMsg, bool, string) {
	if keyMsg, ok := msg.(tea.KeyMsg); ok {
		if keyMsg.Type == tea.KeyRunes {
			filtered, removed := stripMouseSGRRunes(keyMsg.Runes)
			if removed {
				if len(filtered) == 0 {
					return tea.KeyMsg{}, false, ""
				}
				keyMsg.Runes = filtered
			}
		}
		return keyMsg, true, "keymsg"
	}
	seq, ok := parseUnknownCSISequence(msg)
	if !ok {
		return tea.KeyMsg{}, false, ""
	}
	if isCtrlEnterCSISequence(seq) {
		return tea.KeyMsg{Type: keyTypeCtrlEnterCSI}, true, "unknown_csi"
	}
	if isShiftEnterCSISequence(seq) {
		return tea.KeyMsg{Type: keyTypeShiftEnterCSI}, true, "unknown_csi"
	}
	if isCtrlBackspaceCSISequence(seq) {
		return tea.KeyMsg{Type: keyTypeCtrlBackspaceCSI}, true, "unknown_csi"
	}
	if isSuperBackspaceCSISequence(seq) {
		return tea.KeyMsg{Type: keyTypeSuperBackspaceCSI}, true, "unknown_csi"
	}
	return tea.KeyMsg{}, false, ""
}

func isDeleteCurrentLineKey(msg tea.KeyMsg) bool {
	keyString := strings.ToLower(msg.String())
	if msg.Type == keyTypeCtrlBackspaceCSI || msg.Type == keyTypeSuperBackspaceCSI {
		return true
	}
	if runtime.GOOS == "darwin" && (msg.Type == tea.KeyCtrlU || keyString == "ctrl+u") {
		return true
	}
	return keyString == "ctrl+backspace" || keyString == "cmd+backspace" || keyString == "super+backspace"
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
	modifier, ok := parseBackspaceCSIModifier(seq)
	if !ok {
		return false
	}
	return csiModifierHasCtrl(modifier)
}

func isSuperBackspaceCSISequence(seq string) bool {
	modifier, ok := parseBackspaceCSIModifier(seq)
	if !ok {
		return false
	}
	return csiModifierHasSuper(modifier)
}

func parseBackspaceCSIModifier(seq string) (int, bool) {
	if len(seq) < 3 {
		return 0, false
	}
	terminator := seq[len(seq)-1]
	if terminator != 'u' && terminator != '~' {
		return 0, false
	}
	body := seq[:len(seq)-1]
	parts := strings.Split(body, ";")
	if len(parts) < 2 {
		return 0, false
	}

	modifierIdx := 1
	keyCodeIdx := 0
	if parts[0] == "27" {
		if len(parts) < 3 {
			return 0, false
		}
		modifierIdx = 1
		keyCodeIdx = 2
	}

	modifier, ok := parseCSIParamInt(parts[modifierIdx])
	if !ok {
		return 0, false
	}
	keyCode, ok := parseCSIParamInt(parts[keyCodeIdx])
	if !ok {
		return 0, false
	}
	if keyCode != 127 && keyCode != 8 {
		return 0, false
	}
	return modifier, true
}

func parseCSIParamInt(field string) (int, bool) {
	if strings.TrimSpace(field) == "" {
		return 0, false
	}
	valueText := field
	if idx := strings.Index(valueText, ":"); idx >= 0 {
		valueText = valueText[:idx]
	}
	value, err := strconv.Atoi(valueText)
	if err != nil {
		return 0, false
	}
	return value, true
}

func csiModifierHasCtrl(modifier int) bool {
	if modifier <= 1 {
		return false
	}
	return (modifier-1)&4 != 0
}

func csiModifierHasSuper(modifier int) bool {
	if modifier <= 1 {
		return false
	}
	return (modifier-1)&8 != 0
}

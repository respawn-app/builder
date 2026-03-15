package app

import (
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

type customKeyKind uint8

const (
	customKeyUnknown customKeyKind = iota
	customKeyCtrlEnter
	customKeyShiftEnter
	customKeyCtrlBackspace
	customKeySuperBackspace
)

type customKeyMsg struct {
	Kind customKeyKind
}

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
	customKey, ok := msg.(customKeyMsg)
	if !ok {
		return tea.KeyMsg{}, false, ""
	}
	switch customKey.Kind {
	case customKeyCtrlEnter:
		return tea.KeyMsg{Type: keyTypeCtrlEnterCSI}, true, "custom_key"
	case customKeyShiftEnter:
		return tea.KeyMsg{Type: keyTypeShiftEnterCSI}, true, "custom_key"
	case customKeyCtrlBackspace:
		return tea.KeyMsg{Type: keyTypeCtrlBackspaceCSI}, true, "custom_key"
	case customKeySuperBackspace:
		return tea.KeyMsg{Type: keyTypeSuperBackspaceCSI}, true, "custom_key"
	default:
		return tea.KeyMsg{}, false, ""
	}
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

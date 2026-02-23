package app

import (
	"strings"
	"unicode"

	"builder/internal/app/commands"
)

const slashCommandPickerLines = 7

type slashCommandPickerState struct {
	visible   bool
	matches   []commands.Command
	selection int
	start     int
}

func parseSlashCommandInput(input string) (active bool, token string, argumentMode bool) {
	trimmed := strings.TrimLeftFunc(input, unicode.IsSpace)
	if trimmed == "" || trimmed[0] != '/' {
		return false, "", false
	}
	payload := trimmed[1:]
	if payload == "" {
		return true, "", false
	}
	spaceIdx := strings.IndexFunc(payload, unicode.IsSpace)
	if spaceIdx < 0 {
		return true, payload, false
	}
	return true, payload[:spaceIdx], true
}

func normalizeSlashCommandToken(token string) string {
	return strings.ToLower(strings.TrimSpace(token))
}

func (m *uiModel) refreshSlashCommandFilterFromInput() {
	active, token, argumentMode := parseSlashCommandInput(m.input)
	if !active || argumentMode {
		m.slashCommandFilter = ""
		m.slashCommandFilterSet = false
		m.slashCommandSelection = 0
		return
	}
	normalized := normalizeSlashCommandToken(token)
	if !m.slashCommandFilterSet || m.slashCommandFilter != normalized {
		m.slashCommandSelection = 0
	}
	m.slashCommandFilter = normalized
	m.slashCommandFilterSet = true
	m.clampSlashCommandSelection()
}

func (m *uiModel) currentSlashCommandQuery(token string) string {
	if m.slashCommandFilterSet {
		return m.slashCommandFilter
	}
	return normalizeSlashCommandToken(token)
}

func (m *uiModel) currentSlashCommandMatches(token string) []commands.Command {
	if m.commandRegistry == nil {
		return nil
	}
	matches := m.commandRegistry.Match(m.currentSlashCommandQuery(token))
	if m.hasParentSession() {
		return matches
	}
	filtered := make([]commands.Command, 0, len(matches))
	for _, command := range matches {
		if strings.TrimSpace(command.Name) == "back" {
			continue
		}
		filtered = append(filtered, command)
	}
	return filtered
}

func (m *uiModel) hasParentSession() bool {
	if m.engine == nil {
		return false
	}
	return strings.TrimSpace(m.engine.ParentSessionID()) != ""
}

func (m *uiModel) clampSlashCommandSelection() {
	if m.commandRegistry == nil {
		m.slashCommandSelection = 0
		return
	}
	matches := m.commandRegistry.Match(m.slashCommandFilter)
	if len(matches) == 0 {
		m.slashCommandSelection = 0
		return
	}
	m.slashCommandSelection = clampSlashPickerIndex(m.slashCommandSelection, 0, len(matches)-1)
}

func (m *uiModel) slashCommandPicker() slashCommandPickerState {
	if m.rollbackMode {
		return slashCommandPickerState{}
	}
	active, token, argumentMode := parseSlashCommandInput(m.input)
	if !active || argumentMode || m.inputSubmitLocked || m.activeAsk != nil {
		return slashCommandPickerState{}
	}
	matches := m.currentSlashCommandMatches(token)
	selection := 0
	if len(matches) > 0 {
		selection = clampSlashPickerIndex(m.slashCommandSelection, 0, len(matches)-1)
	}
	start := 0
	if len(matches) > slashCommandPickerLines {
		start = selection - (slashCommandPickerLines / 2)
		maxStart := len(matches) - slashCommandPickerLines
		if start < 0 {
			start = 0
		}
		if start > maxStart {
			start = maxStart
		}
	}
	return slashCommandPickerState{
		visible:   true,
		matches:   matches,
		selection: selection,
		start:     start,
	}
}

func (m *uiModel) navigateSlashCommandPicker(delta int) bool {
	state := m.slashCommandPicker()
	if !state.visible || len(state.matches) == 0 {
		return false
	}
	nextSelection := clampSlashPickerIndex(state.selection+delta, 0, len(state.matches)-1)
	m.slashCommandSelection = nextSelection
	m.input = "/" + state.matches[nextSelection].Name
	m.inputCursor = -1
	return true
}

func clampSlashPickerIndex(value, minValue, maxValue int) int {
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

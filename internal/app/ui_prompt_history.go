package app

import (
	"strings"

	"builder/internal/tui"

	tea "github.com/charmbracelet/bubbletea"
)

func (m *uiModel) loadPromptHistory(history []string) {
	for _, raw := range history {
		if text := preservePromptHistoryText(raw); text != "" {
			m.promptHistory = append(m.promptHistory, text)
		}
	}
}

func (m *uiModel) seedPromptHistoryFromTranscriptEntries(entries []tui.TranscriptEntry) {
	if len(m.promptHistory) > 0 {
		return
	}
	for _, entry := range entries {
		if strings.TrimSpace(entry.Role) != "user" {
			continue
		}
		if text := preservePromptHistoryText(entry.Text); text != "" {
			m.promptHistory = append(m.promptHistory, text)
		}
	}
}

func preservePromptHistoryText(text string) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}
	return text
}

func (m *uiModel) resetPromptHistoryNavigation() {
	m.promptHistorySelection = -1
	m.promptHistoryDraft = ""
	m.promptHistoryDraftCursor = -1
}

func (m *uiModel) promptHistorySelectionActive() bool {
	return m.promptHistorySelection >= 0 && m.promptHistorySelection < len(m.promptHistory)
}

func (m *uiModel) selectedPromptHistoryText() (string, bool) {
	if !m.promptHistorySelectionActive() {
		return "", false
	}
	return m.promptHistory[m.promptHistorySelection], true
}

func (m *uiModel) promptHistorySelectionMatchesInput() bool {
	selected, ok := m.selectedPromptHistoryText()
	if !ok {
		return false
	}
	return m.input == selected
}

func (m *uiModel) promptHistoryCursorAtBoundary() bool {
	if !m.promptHistorySelectionMatchesInput() {
		return false
	}
	cursor := m.cursorIndex()
	return cursor == 0 || cursor == len([]rune(m.input))
}

func (m *uiModel) shouldSuppressSlashCommandPicker() bool {
	return m.promptHistorySelectionMatchesInput()
}

func (m *uiModel) syncPromptHistorySelectionToInput() {
	if !m.promptHistorySelectionActive() {
		return
	}
	if m.promptHistorySelectionMatchesInput() {
		return
	}
	m.resetPromptHistoryNavigation()
}

func (m *uiModel) shouldAttemptPromptHistoryNavigation(delta int) bool {
	if delta == 0 {
		return false
	}
	if m.input == "" {
		return true
	}
	return m.promptHistoryCursorAtBoundary()
}

func (m *uiModel) navigatePromptHistory(delta int) bool {
	if len(m.promptHistory) == 0 || delta == 0 {
		return false
	}
	if delta < 0 {
		return m.navigatePromptHistoryUp()
	}
	return m.navigatePromptHistoryDown()
}

func (m *uiModel) navigatePromptHistoryUp() bool {
	if m.input == "" {
		m.promptHistoryDraft = m.input
		m.promptHistoryDraftCursor = m.inputCursor
		m.promptHistorySelection = len(m.promptHistory) - 1
		m.applyPromptHistorySelection()
		return true
	}
	if !m.promptHistoryCursorAtBoundary() {
		return false
	}
	if m.promptHistorySelection == 0 {
		return false
	}
	m.promptHistorySelection--
	m.applyPromptHistorySelection()
	return true
}

func (m *uiModel) navigatePromptHistoryDown() bool {
	if m.input == "" || !m.promptHistoryCursorAtBoundary() {
		return false
	}
	if m.promptHistorySelection == len(m.promptHistory)-1 {
		m.input = m.promptHistoryDraft
		m.inputCursor = m.promptHistoryDraftCursor
		m.refreshSlashCommandFilterFromInput()
		m.resetPromptHistoryNavigation()
		return true
	}
	m.promptHistorySelection++
	m.applyPromptHistorySelection()
	return true
}

func (m *uiModel) applyPromptHistorySelection() {
	if !m.promptHistorySelectionActive() {
		return
	}
	m.input = m.promptHistory[m.promptHistorySelection]
	m.inputCursor = -1
	m.refreshSlashCommandFilterFromInput()
}

func (m *uiModel) recordPromptHistory(text string) tea.Cmd {
	if text = preservePromptHistoryText(text); text == "" {
		return nil
	}
	m.promptHistory = append(m.promptHistory, text)
	m.resetPromptHistoryNavigation()
	if m.engine == nil {
		return nil
	}
	return func() tea.Msg {
		if err := m.engine.RecordPromptHistory(text); err != nil {
			return promptHistoryPersistErrMsg{err: err}
		}
		return nil
	}
}

func ringBellCmd() tea.Cmd {
	return func() tea.Msg {
		writeTerminalSequence(terminalBell)
		return nil
	}
}

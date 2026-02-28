package app

import (
	"builder/internal/config"
	"builder/internal/tui"

	tea "github.com/charmbracelet/bubbletea"
)

func shouldStartMainUIInAltScreen(policy config.TUIAlternateScreenPolicy) bool {
	return policy == config.TUIAlternateScreenAlways
}

func shouldUseDetailAltScreen(policy config.TUIAlternateScreenPolicy) bool {
	return false
}

func shouldUseSessionPickerAltScreen(policy config.TUIAlternateScreenPolicy) bool {
	return policy != config.TUIAlternateScreenNever
}

func (m *uiModel) toggleTranscriptMode() tea.Cmd {
	prevMode := m.view.Mode()
	m.forwardToView(tui.ToggleModeMsg{})
	nextMode := m.view.Mode()
	transitionCmd := m.altScreenCmdForModeTransition(prevMode, nextMode)
	if transitionCmd == nil {
		return tea.ClearScreen
	}
	return tea.Sequence(transitionCmd, tea.ClearScreen)
}

func (m *uiModel) altScreenCmdForModeTransition(prev, next tui.Mode) tea.Cmd {
	if prev == next {
		return nil
	}
	if !shouldUseDetailAltScreen(m.tuiAlternateScreen) {
		return nil
	}
	return nil
}

package app

import (
	"builder/internal/config"
	"builder/internal/tui"
	"os"

	tea "github.com/charmbracelet/bubbletea"
)

func shouldStartMainUIInAltScreen(policy config.TUIAlternateScreenPolicy) bool {
	return policy == config.TUIAlternateScreenAlways
}

func shouldUseDetailAltScreen(policy config.TUIAlternateScreenPolicy) bool {
	return policy != config.TUIAlternateScreenNever
}

func shouldUseSessionPickerAltScreen(policy config.TUIAlternateScreenPolicy) bool {
	return policy != config.TUIAlternateScreenNever
}

var writeTerminalSequence = func(sequence string) {
	_, _ = os.Stdout.WriteString(sequence)
}

func (m *uiModel) toggleTranscriptMode() tea.Cmd {
	prevMode := m.view.Mode()
	m.forwardToView(tui.ToggleModeMsg{})
	nextMode := m.view.Mode()
	transitionCmd := m.altScreenCmdForModeTransition(prevMode, nextMode)
	if transitionCmd != nil {
		if m.usesNativeScrollback() {
			return transitionCmd
		}
		return tea.Sequence(transitionCmd, tea.ClearScreen)
	}
	if m.usesNativeScrollback() {
		return nil
	}
	return tea.ClearScreen
}

func (m *uiModel) altScreenCmdForModeTransition(prev, next tui.Mode) tea.Cmd {
	if prev == next {
		return nil
	}
	if !shouldUseDetailAltScreen(m.tuiAlternateScreen) {
		return nil
	}
	if next == tui.ModeDetail && !m.altScreenActive {
		m.altScreenActive = true
		return tea.Sequence(tea.EnterAltScreen, enableAlternateScrollCmd())
	}
	if next == tui.ModeDetail && m.altScreenActive {
		return enableAlternateScrollCmd()
	}
	if prev == tui.ModeDetail && m.altScreenActive && m.tuiAlternateScreen != config.TUIAlternateScreenAlways {
		m.altScreenActive = false
		return tea.Sequence(disableAlternateScrollCmd(), tea.ExitAltScreen)
	}
	if prev == tui.ModeDetail && m.altScreenActive {
		return disableAlternateScrollCmd()
	}
	return nil
}

func enableAlternateScrollCmd() tea.Cmd {
	return func() tea.Msg {
		writeTerminalSequence("\x1b[?1007h")
		return nil
	}
}

func disableAlternateScrollCmd() tea.Cmd {
	return func() tea.Msg {
		writeTerminalSequence("\x1b[?1007l")
		return nil
	}
}

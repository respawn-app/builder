package app

import (
	"builder/internal/config"
	"builder/internal/tui"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
)

const (
	ansiEnableAlternateScroll  = "\x1b[?1007h"
	ansiDisableAlternateScroll = "\x1b[?1007l"
)

func shouldStartMainUIInAltScreen(policy config.TUIAlternateScreenPolicy) bool {
	return policy == config.TUIAlternateScreenAlways
}

func shouldUseDetailAltScreen(policy config.TUIAlternateScreenPolicy) bool {
	return true
}

func shouldUseSessionPickerAltScreen(policy config.TUIAlternateScreenPolicy) bool {
	return policy != config.TUIAlternateScreenNever
}

func terminalControlSeq(seq string) tea.Cmd {
	return func() tea.Msg {
		_, _ = fmt.Fprint(os.Stdout, seq)
		return nil
	}
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
	alwaysPolicy := m.tuiAlternateScreen == config.TUIAlternateScreenAlways
	if next == tui.ModeDetail {
		if alwaysPolicy {
			return terminalControlSeq(ansiEnableAlternateScroll)
		}
		if m.altScreenActive {
			return terminalControlSeq(ansiEnableAlternateScroll)
		}
		m.altScreenActive = true
		return tea.Sequence(tea.EnterAltScreen, terminalControlSeq(ansiEnableAlternateScroll))
	}
	if prev == tui.ModeDetail {
		if alwaysPolicy {
			return terminalControlSeq(ansiDisableAlternateScroll)
		}
		if !m.altScreenActive {
			return terminalControlSeq(ansiDisableAlternateScroll)
		}
		m.altScreenActive = false
		return tea.Sequence(terminalControlSeq(ansiDisableAlternateScroll), tea.ExitAltScreen)
	}
	return nil
}

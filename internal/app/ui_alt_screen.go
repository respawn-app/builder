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

func shouldUseStartupPickerAltScreen(policy config.TUIAlternateScreenPolicy) bool {
	return policy != config.TUIAlternateScreenNever
}

var writeTerminalSequence = func(sequence string) {
	_, _ = os.Stdout.WriteString(sequence)
}

func (m *uiModel) toggleTranscriptMode() tea.Cmd {
	target := tui.ModeDetail
	if m.view.Mode() == tui.ModeDetail {
		target = tui.ModeOngoing
	}
	return m.transitionTranscriptMode(target, false, true)
}

func (m *uiModel) toggleTranscriptModeWithNativeReplay(emitNativeReplay bool) tea.Cmd {
	target := tui.ModeDetail
	if m.view.Mode() == tui.ModeDetail {
		target = tui.ModeOngoing
	}
	return m.transitionTranscriptMode(target, false, emitNativeReplay)
}

func (m *uiModel) toggleTranscriptModeWithOptions(emitNativeReplay bool, skipDetailWarmup bool) tea.Cmd {
	target := tui.ModeDetail
	if m.view.Mode() == tui.ModeDetail {
		target = tui.ModeOngoing
	}
	return m.transitionTranscriptMode(target, skipDetailWarmup, emitNativeReplay)
}

func (m *uiModel) transitionTranscriptMode(target tui.Mode, skipDetailWarmup bool, emitNativeReplay bool) tea.Cmd {
	prevMode := m.view.Mode()
	m.forwardToView(tui.SetModeMsg{Mode: target, SkipDetailWarmup: skipDetailWarmup})
	nextMode := m.view.Mode()
	if nextMode != tui.ModeOngoing {
		m.helpVisible = false
	} else if prevMode != nextMode && m.inputMode() == uiInputModeMain {
		m.restorePrimaryInputMode()
	}
	if prevMode != nextMode {
		m.invalidateNativeResizeReplay()
	}
	transitionCmd := m.altScreenCmdForModeTransition(prevMode, nextMode)
	nativeReplayCmd := m.nativeReplayCmdForModeTransition(prevMode, nextMode, emitNativeReplay && transitionCmd == nil, emitNativeReplay)
	if transitionCmd == nil && nativeReplayCmd == nil {
		return tea.ClearScreen
	}
	return sequenceCmds(transitionCmd, nativeReplayCmd)
}

func (m *uiModel) nativeReplayCmdForModeTransition(prev, next tui.Mode, forceFull bool, enabled bool) tea.Cmd {
	if !enabled {
		return nil
	}
	if prev != tui.ModeDetail || next != tui.ModeOngoing {
		return nil
	}
	return m.emitCurrentNativeScrollbackState(forceFull)
}

func sequenceCmds(cmds ...tea.Cmd) tea.Cmd {
	filtered := make([]tea.Cmd, 0, len(cmds))
	for _, cmd := range cmds {
		if cmd != nil {
			filtered = append(filtered, cmd)
		}
	}
	if len(filtered) == 0 {
		return nil
	}
	if len(filtered) == 1 {
		return filtered[0]
	}
	return tea.Sequence(filtered...)
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

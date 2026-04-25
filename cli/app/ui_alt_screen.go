package app

import (
	"builder/cli/tui"
	"builder/shared/config"
	"os"
	"time"

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
	if prevMode != nextMode && nextMode == tui.ModeDetail {
		m.primeDetailTranscriptFromCurrentTail()
	}
	if nextMode != tui.ModeOngoing {
		m.helpVisible = false
	} else if prevMode != nextMode && m.inputMode() == uiInputModeMain {
		m.restorePrimaryInputMode()
	}
	if prevMode != nextMode {
		m.invalidateNativeResizeReplay()
	}
	clearCmd := m.clearCmdForModeTransition(prevMode, nextMode)
	transitionCmd := m.altScreenCmdForModeTransition(prevMode, nextMode)
	nativeReplayCmd := m.nativeReplayCmdForModeTransition(prevMode, nextMode, emitNativeReplay)
	detailLoadCmd := m.detailLoadCmdForModeTransition(prevMode, nextMode)
	if clearCmd == nil && transitionCmd == nil && nativeReplayCmd == nil && detailLoadCmd == nil {
		return nil
	}
	return sequenceCmds(clearCmd, transitionCmd, nativeReplayCmd, detailLoadCmd)
}

func (m *uiModel) clearCmdForModeTransition(prev, next tui.Mode) tea.Cmd {
	if prev == next {
		return nil
	}
	if next != tui.ModeDetail {
		return nil
	}
	if shouldUseDetailAltScreen(m.tuiAlternateScreen) {
		return nil
	}
	return tea.ClearScreen
}

func (m *uiModel) detailLoadCmdForModeTransition(prev, next tui.Mode) tea.Cmd {
	if prev == next || next != tui.ModeDetail {
		return nil
	}
	m.detailTranscript.totalEntries = max(m.detailTranscript.totalEntries, m.view.TranscriptTotalEntries())
	return tea.Tick(time.Millisecond, func(time.Time) tea.Msg {
		return detailTranscriptLoadMsg{}
	})
}

func (m *uiModel) nativeReplayCmdForModeTransition(prev, next tui.Mode, enabled bool) tea.Cmd {
	if !enabled {
		return nil
	}
	if prev != tui.ModeDetail || next != tui.ModeOngoing {
		return nil
	}
	// Detail-mode transcript changes may append newly committed suffix rows on return.
	// If a spilled streaming assistant committed while detail was active, finalize that
	// deferred tail through the normal sync path; otherwise preserve the existing
	// append-only replay path for deferred committed deltas.
	m.armNativeHistoryReplayPermit(nativeHistoryReplayPermitModeRestore)
	committedEntries := committedTranscriptEntriesForApp(m.transcriptEntries)
	if m.canFinalizeNativeStreamingCommit(committedEntries, len(committedEntries)) {
		return m.syncNativeHistoryFromTranscript()
	}
	if len(committedEntries) > 0 && !m.nativeProjection.Empty() {
		projection := committedTranscriptProjectionForApp(m.view, m.transcriptEntries)
		if _, ok := projection.RenderAppendDeltaFrom(m.nativeProjection, tui.TranscriptDivider); !ok {
			m.rebaseNativeProjection(projection, m.transcriptBaseOffset, len(committedEntries))
			m.acceptNativeProjectionWithoutReplay(projection)
			return nil
		}
	}
	if m.nativeProjection.Empty() && len(committedEntries) > 0 {
		projection := committedTranscriptProjectionForApp(m.view, m.transcriptEntries)
		m.rebaseNativeProjection(projection, m.transcriptBaseOffset, len(committedEntries))
		m.acceptNativeProjectionWithoutReplay(projection)
		return nil
	}
	return m.emitCurrentNativeScrollbackState(false)
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

package app

import (
	"fmt"
	"strings"
	"time"

	"builder/internal/tui"

	tea "github.com/charmbracelet/bubbletea"
)

func (c uiInputController) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	m := c.model
	inputState := m.inputModeState()
	keyString := strings.ToLower(msg.String())
	if msg.Type != tea.KeyEnter && msg.Type != keyTypeShiftEnterCSI {
		c.clearPendingCSIShiftEnter()
	}
	if msg.Type != tea.KeyEsc {
		m.lastEscAt = time.Time{}
	}
	if inputState.Mode == uiInputModeRollbackSelection {
		return c.handleRollbackSelectionKey(msg)
	}
	if inputState.Mode == uiInputModeProcessList {
		next, cmd := c.handleProcessListKey(msg)
		next.(*uiModel).syncViewport()
		return next, cmd
	}
	if keyString == "tab" || keyString == "ctrl+enter" || msg.Type == keyTypeCtrlEnterCSI {
		text := strings.TrimSpace(m.input)
		if text == "" {
			return m, nil
		}
		if inputState.Mode == uiInputModeRollbackEdit && !inputState.Busy {
			return c.startRollbackFork(text)
		}
		return c.queueOrStartSubmission(text)
	}
	if isDeleteCurrentLineKey(msg) {
		if m.isInputLocked() {
			return m, nil
		}
		m.deleteCurrentInputLine()
		return m, nil
	}
	if !m.isInputLocked() && !msg.Alt {
		switch msg.Type {
		case tea.KeyUp, tea.KeyLeft:
			if m.navigateSlashCommandPicker(-1) {
				return m, nil
			}
		case tea.KeyDown, tea.KeyRight:
			if m.navigateSlashCommandPicker(1) {
				return m, nil
			}
		}
	}

	switch msg.Type {
	case tea.KeyCtrlC:
		if m.busy {
			if m.engine != nil {
				_ = m.engine.Interrupt()
			}
			c.releaseLockedInjectedInput(true)
			c.restoreQueuedMessagesIntoInput()
			m.busy = false
			m.activity = uiActivityInterrupted
			m.clearReviewerState()
			return m, nil
		}
		m.exitAction = UIActionExit
		return m, tea.Quit
	case tea.KeyShiftTab, tea.KeyCtrlT:
		return m, m.toggleTranscriptMode()
	case tea.KeyEsc:
		if inputState.Mode == uiInputModeRollbackEdit {
			if strings.TrimSpace(m.input) == "" {
				return m, c.cancelRollbackEditingToSelectionFlowCmd()
			}
			return m, nil
		}
		if m.view.Mode() != tui.ModeOngoing {
			return m, nil
		}
		if m.busy || m.isInputLocked() || strings.TrimSpace(m.input) != "" {
			return m, nil
		}
		now := time.Now()
		if !m.lastEscAt.IsZero() && now.Sub(m.lastEscAt) <= rollbackDoubleEscWindow {
			m.lastEscAt = time.Time{}
			return m, c.startRollbackSelectionFlowCmd()
		}
		m.lastEscAt = now
		return m, nil
	case tea.KeyEnter:
		c.normalizePendingCSIShiftEnterOnEnter()
		text := strings.TrimSpace(m.input)
		if text == "" {
			if !m.busy && len(m.queued) > 0 {
				next := m.popQueued()
				return m, c.startSubmission(next)
			}
			return m, nil
		}
		if inputState.Mode == uiInputModeRollbackEdit && !inputState.Busy {
			return c.startRollbackFork(text)
		}
		if command, knownCommand := m.commandRegistry.Command(text); knownCommand {
			if m.busy {
				if !command.RunWhileBusy {
					m.clearInput()
					return m, c.showErrorStatus(fmt.Sprintf("cannot run /%s while model is working", command.Name))
				}
				if commandResult := m.commandRegistry.Execute(text); commandResult.Handled {
					m.clearInput()
					return c.applyCommandResult(commandResult)
				}
			}
		}
		_, isUserShell := parseUserShellCommand(text)
		if m.busy {
			if isUserShell {
				m.queueInput(text)
				return m, nil
			}
			m.lockInjectedInput(text)
			return m, nil
		}
		if commandResult := m.commandRegistry.Execute(text); commandResult.Handled {
			m.clearInput()
			return c.applyCommandResult(commandResult)
		}
		m.clearInput()
		return m, c.startSubmission(text)
	case tea.KeyCtrlJ, keyTypeShiftEnterCSI:
		if m.isInputLocked() {
			return m, nil
		}
		m.insertInputRunes([]rune{'\n'})
		if msg.Type == keyTypeShiftEnterCSI {
			c.markPendingCSIShiftEnter()
		}
		return m, nil
	case tea.KeyBackspace:
		if m.isInputLocked() {
			return m, nil
		}
		m.backspaceInput()
		return m, nil
	case tea.KeySpace:
		if m.isInputLocked() {
			return m, nil
		}
		m.insertInputRunes([]rune{' '})
		return m, nil
	case tea.KeyLeft:
		if m.isInputLocked() {
			return m, nil
		}
		if msg.Alt {
			m.moveCursorWordLeft()
			return m, nil
		}
		m.moveCursorLeft()
		return m, nil
	case tea.KeyRight:
		if m.isInputLocked() {
			return m, nil
		}
		if msg.Alt {
			m.moveCursorWordRight()
			return m, nil
		}
		m.moveCursorRight()
		return m, nil
	case tea.KeyHome, tea.KeyCtrlA:
		if m.isInputLocked() {
			return m, nil
		}
		m.moveCursorStart()
		return m, nil
	case tea.KeyEnd, tea.KeyCtrlE, tea.KeyCtrlEnd:
		if m.isInputLocked() {
			return m, nil
		}
		m.moveCursorEnd()
		return m, nil
	case tea.KeyCtrlLeft:
		if m.isInputLocked() {
			return m, nil
		}
		m.moveCursorWordLeft()
		return m, nil
	case tea.KeyCtrlRight:
		if m.isInputLocked() {
			return m, nil
		}
		m.moveCursorWordRight()
		return m, nil
	case tea.KeyUp:
		if m.isInputLocked() {
			if m.usesNativeScrollback() && m.view.Mode() == tui.ModeOngoing {
				return m, nil
			}
			m.forwardToView(tea.KeyMsg{Type: tea.KeyUp})
			return m, nil
		}
		moved := m.moveCursorUpLine()
		if !moved && !strings.ContainsRune(m.input, '\n') {
			if m.usesNativeScrollback() && m.view.Mode() == tui.ModeOngoing {
				return m, nil
			}
			m.forwardToView(tea.KeyMsg{Type: tea.KeyUp})
		}
		return m, nil
	case tea.KeyDown:
		if m.isInputLocked() {
			if m.usesNativeScrollback() && m.view.Mode() == tui.ModeOngoing {
				return m, nil
			}
			m.forwardToView(tea.KeyMsg{Type: tea.KeyDown})
			return m, nil
		}
		moved := m.moveCursorDownLine()
		if !moved && !strings.ContainsRune(m.input, '\n') {
			if m.usesNativeScrollback() && m.view.Mode() == tui.ModeOngoing {
				return m, nil
			}
			m.forwardToView(tea.KeyMsg{Type: tea.KeyDown})
		}
		return m, nil
	case tea.KeyPgUp:
		if m.usesNativeScrollback() && m.view.Mode() == tui.ModeOngoing {
			return m, nil
		}
		m.forwardToView(tea.KeyMsg{Type: tea.KeyPgUp})
		return m, nil
	case tea.KeyPgDown:
		if m.usesNativeScrollback() && m.view.Mode() == tui.ModeOngoing {
			return m, nil
		}
		m.forwardToView(tea.KeyMsg{Type: tea.KeyPgDown})
		return m, nil
	default:
		if keyString == "shift+enter" {
			if m.isInputLocked() {
				return m, nil
			}
			m.insertInputRunes([]rune{'\n'})
			return m, nil
		}
		if msg.Type == tea.KeyRunes {
			if m.isInputLocked() {
				return m, nil
			}
			m.insertInputRunes(msg.Runes)
		}
		return m, nil
	}
}

func (c uiInputController) handleRollbackSelectionKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	m := c.model
	switch msg.Type {
	case tea.KeyCtrlC:
		m.exitAction = UIActionExit
		if overlayCmd := m.popRollbackOverlayIfNeeded(); overlayCmd != nil {
			m.stopRollbackSelectionMode()
			return m, tea.Sequence(overlayCmd, tea.Quit)
		}
		return m, tea.Quit
	case tea.KeyEsc:
		return m, c.stopRollbackSelectionFlowCmd()
	case tea.KeyUp:
		m.moveRollbackSelection(-1)
		return m, nil
	case tea.KeyDown:
		m.moveRollbackSelection(1)
		return m, nil
	case tea.KeyEnter:
		return m, c.beginRollbackEditingFlowCmd()
	case tea.KeyPgUp, tea.KeyPgDown:
		m.forwardToView(msg)
		return m, nil
	default:
		return m, nil
	}
}

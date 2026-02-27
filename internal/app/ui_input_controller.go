package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"builder/internal/app/commands"
	"builder/internal/llm"
	"builder/internal/tui"

	tea "github.com/charmbracelet/bubbletea"
)

type uiInputController struct {
	model *uiModel
}

var spinnerFrames = []string{"|", "/", "-", "\\"}
var spinnerTickInterval = 360 * time.Millisecond
var transientStatusDuration = 2200 * time.Millisecond
var errSubmissionInterrupted = errors.New("interrupted")
var rollbackDoubleEscWindow = 500 * time.Millisecond

func (c uiInputController) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	m := c.model
	keyString := strings.ToLower(msg.String())
	if msg.Type != tea.KeyEsc {
		m.lastEscAt = time.Time{}
	}
	if m.rollbackMode {
		switch msg.Type {
		case tea.KeyCtrlC:
			m.exitAction = UIActionExit
			return m, tea.Quit
		case tea.KeyEsc:
			m.stopRollbackSelectionMode()
			return m, nil
		case tea.KeyUp:
			m.moveRollbackSelection(-1)
			return m, nil
		case tea.KeyDown:
			m.moveRollbackSelection(1)
			return m, nil
		case tea.KeyEnter:
			m.beginRollbackEditing()
			return m, nil
		case tea.KeyPgUp, tea.KeyPgDown:
			m.forwardToView(msg)
			return m, nil
		default:
			return m, nil
		}
	}
	if keyString == "tab" || keyString == "ctrl+enter" || msg.Type == keyTypeCtrlEnterCSI {
		text := strings.TrimSpace(m.input)
		if text == "" {
			return m, nil
		}
		if m.rollbackEditing && !m.busy {
			m.nextForkUserMessageIndex = m.rollbackSelectedUserMessageIndex
			m.nextSessionInitialPrompt = text
			m.exitAction = UIActionForkRollback
			m.rollbackEditing = false
			return m, tea.Quit
		}
		if m.busy {
			if m.isInputLocked() {
				return m, nil
			}
			m.queued = append(m.queued, text)
			m.clearInput()
			m.activity = uiActivityQueued
			return m, nil
		}
		m.queued = append(m.queued, text)
		m.clearInput()
		if !m.busy {
			next := m.popQueued()
			return m, c.startSubmission(next)
		}
		m.activity = uiActivityQueued
		return m, nil
	}
	if msg.Type == keyTypeCtrlBackspaceCSI || msg.Type == keyTypeSuperBackspaceCSI ||
		keyString == "ctrl+backspace" || keyString == "cmd+backspace" || keyString == "super+backspace" {
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
	case tea.KeyShiftTab:
		m.forwardToView(tui.ToggleModeMsg{})
		return m, nil
	case tea.KeyEsc:
		if m.rollbackEditing {
			if strings.TrimSpace(m.input) == "" {
				m.cancelRollbackEditingBackToSelection()
			}
			return m, nil
		}
		if m.busy || m.isInputLocked() || strings.TrimSpace(m.input) != "" {
			return m, nil
		}
		now := time.Now()
		if !m.lastEscAt.IsZero() && now.Sub(m.lastEscAt) <= rollbackDoubleEscWindow {
			m.lastEscAt = time.Time{}
			m.startRollbackSelectionMode()
			return m, nil
		}
		m.lastEscAt = now
		return m, nil
	case tea.KeyEnter:
		text := strings.TrimSpace(m.input)
		if text == "" {
			if !m.busy && len(m.queued) > 0 {
				next := m.popQueued()
				return m, c.startSubmission(next)
			}
			return m, nil
		}
		if m.rollbackEditing && !m.busy {
			m.nextForkUserMessageIndex = m.rollbackSelectedUserMessageIndex
			m.nextSessionInitialPrompt = text
			m.exitAction = UIActionForkRollback
			m.rollbackEditing = false
			return m, tea.Quit
		}
		if m.reviewerBlocking {
			return m, nil
		}
		if command, knownCommand := m.commandRegistry.Command(text); knownCommand {
			if m.busy {
				if !command.RunWhileBusy {
					m.clearInput()
					return m, c.showTransientStatus(fmt.Sprintf("cannot run /%s while model is working", command.Name))
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
				m.queued = append(m.queued, text)
				m.clearInput()
				m.activity = uiActivityQueued
				return m, nil
			}
			if m.engine != nil {
				m.engine.QueueUserMessage(text)
			}
			m.pendingInjected = append(m.pendingInjected, text)
			m.lockedInjectText = text
			m.inputSubmitLocked = true
			m.activity = uiActivityQueued
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
			return m, nil
		}
		moved := m.moveCursorUpLine()
		if !moved && !strings.ContainsRune(m.input, '\n') {
			m.forwardToView(tea.KeyMsg{Type: tea.KeyUp})
		}
		return m, nil
	case tea.KeyDown:
		if m.isInputLocked() {
			return m, nil
		}
		moved := m.moveCursorDownLine()
		if !moved && !strings.ContainsRune(m.input, '\n') {
			m.forwardToView(tea.KeyMsg{Type: tea.KeyDown})
		}
		return m, nil
	case tea.KeyPgUp:
		m.forwardToView(tea.KeyMsg{Type: tea.KeyPgUp})
		return m, nil
	case tea.KeyPgDown:
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

func (c uiInputController) applyCommandResult(commandResult commands.Result) (tea.Model, tea.Cmd) {
	m := c.model
	if commandResult.SubmitUser && commandResult.FreshConversation {
		m.nextSessionInitialPrompt = commandResult.User
		m.nextParentSessionID = m.sessionID
		m.exitAction = UIActionNewSession
		return m, tea.Quit
	}
	if commandResult.SubmitUser {
		return m, c.startSubmission(commandResult.User)
	}
	if commandResult.Text != "" {
		if m.engine != nil {
			m.engine.AppendLocalEntry("system", commandResult.Text)
		} else {
			m.forwardToView(tui.AppendTranscriptMsg{Role: "system", Text: commandResult.Text})
		}
	}
	switch commandResult.Action {
	case commands.ActionExit:
		m.exitAction = UIActionExit
		return m, tea.Quit
	case commands.ActionNew:
		m.nextParentSessionID = m.sessionID
		m.exitAction = UIActionNewSession
		return m, tea.Quit
	case commands.ActionResume:
		m.exitAction = UIActionResume
		return m, tea.Quit
	case commands.ActionBack:
		if m.engine == nil || strings.TrimSpace(m.engine.ParentSessionID()) == "" {
			if m.engine != nil {
				m.engine.AppendLocalEntry("system", "No parent session available")
			} else {
				m.forwardToView(tui.AppendTranscriptMsg{Role: "system", Text: "No parent session available"})
			}
			return m, nil
		}
		m.nextSessionID = strings.TrimSpace(m.engine.ParentSessionID())
		m.exitAction = UIActionOpenSession
		return m, tea.Quit
	case commands.ActionLogout:
		m.exitAction = UIActionLogout
		return m, tea.Quit
	case commands.ActionSetName:
		if m.engine != nil {
			if err := m.engine.SetSessionName(commandResult.SessionName); err != nil {
				m.engine.AppendLocalEntry("error", formatSubmissionError(err))
				return m, nil
			}
		}
		m.sessionName = strings.TrimSpace(commandResult.SessionName)
		return m, tea.SetWindowTitle(m.windowTitle())
	case commands.ActionCompact:
		return m, c.startCompaction(commandResult.Args)
	}
	return m, nil
}

func (c uiInputController) showTransientStatus(message string) tea.Cmd {
	m := c.model
	m.transientStatusToken++
	token := m.transientStatusToken
	m.transientStatus = strings.TrimSpace(message)
	return tea.Tick(transientStatusDuration, func(time.Time) tea.Msg {
		return clearTransientStatusMsg{token: token}
	})
}

func (c uiInputController) startSubmission(text string) tea.Cmd {
	m := c.model
	m.clearReviewerState()
	command, isUserShell := parseUserShellCommand(text)
	m.busy = true
	m.activity = uiActivityRunning
	m.sawAssistantDelta = false
	if isUserShell {
		m.logf("step.user_shell.start command_chars=%d", len(command))
	} else {
		m.logf("step.start user_chars=%d", len(text))
	}
	if m.engine == nil {
		if isUserShell {
			m.forwardToView(tui.AppendTranscriptMsg{Role: "tool_call", Text: command})
		} else {
			m.forwardToView(tui.AppendTranscriptMsg{Role: "user", Text: text})
		}
	}
	m.syncViewport()
	if isUserShell {
		return tea.Batch(c.submitUserShellCmd(command), tickSpinner())
	}
	return tea.Batch(c.submitCmd(text), tickSpinner())
}

func (c uiInputController) submitCmd(text string) tea.Cmd {
	m := c.model
	return func() tea.Msg {
		if m.engine == nil {
			return submitDoneMsg{err: errors.New("runtime engine is not configured")}
		}
		msg, err := m.engine.SubmitUserMessage(context.Background(), text)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return submitDoneMsg{err: errSubmissionInterrupted}
			}
			return submitDoneMsg{err: err}
		}
		return submitDoneMsg{message: msg.Content}
	}
}

func (c uiInputController) submitUserShellCmd(command string) tea.Cmd {
	m := c.model
	return func() tea.Msg {
		if m.engine == nil {
			return submitDoneMsg{err: errors.New("runtime engine is not configured")}
		}
		_, err := m.engine.SubmitUserShellCommand(context.Background(), command)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return submitDoneMsg{err: errSubmissionInterrupted}
			}
			return submitDoneMsg{err: err}
		}
		return submitDoneMsg{}
	}
}

func (c uiInputController) startCompaction(args string) tea.Cmd {
	m := c.model
	m.clearReviewerState()
	m.busy = true
	m.activity = uiActivityRunning
	m.compacting = true
	m.sawAssistantDelta = false
	m.logf("compaction.start args_chars=%d", len(strings.TrimSpace(args)))
	m.syncViewport()
	return tea.Batch(c.compactCmd(args), tickSpinner())
}

func (c uiInputController) compactCmd(args string) tea.Cmd {
	m := c.model
	return func() tea.Msg {
		if m.engine == nil {
			return compactDoneMsg{err: errors.New("runtime engine is not configured")}
		}
		return compactDoneMsg{err: m.engine.CompactContext(context.Background(), args)}
	}
}

func (c uiInputController) handleSubmitDone(msg submitDoneMsg) (tea.Model, tea.Cmd) {
	m := c.model
	m.busy = false
	m.clearReviewerState()
	m.spinnerFrame = 0
	if msg.err != nil {
		c.unlockInputAfterSubmissionError()
		if errors.Is(msg.err, errSubmissionInterrupted) {
			c.restoreQueuedMessagesIntoInput()
			m.activity = uiActivityInterrupted
			m.logf("step.interrupted")
			m.syncViewport()
			return m, nil
		}
		detailErr := formatSubmissionError(msg.err)
		m.activity = uiActivityError
		if m.engine != nil {
			m.engine.AppendLocalEntry("error", detailErr)
		} else {
			m.forwardToView(tui.AppendTranscriptMsg{Role: "error", Text: detailErr})
		}
		m.logf("step.error err=%q", detailErr)
		if len(m.queued) > 0 {
			next := m.popQueued()
			return m, c.startSubmission(next)
		}
		m.syncViewport()
		return m, nil
	}

	m.activity = uiActivityIdle
	if m.engine == nil {
		if !m.sawAssistantDelta && msg.message != "" {
			m.forwardToView(tui.StreamAssistantMsg{Delta: msg.message})
		}
		m.forwardToView(tui.CommitAssistantMsg{})
	}
	m.logf("step.done assistant_chars=%d", len(msg.message))
	m.sawAssistantDelta = false
	if len(m.queued) > 0 {
		next := m.popQueued()
		return m, c.startSubmission(next)
	}
	m.syncViewport()
	return m, nil
}

func (c uiInputController) restoreQueuedMessagesIntoInput() {
	m := c.model
	if len(m.queued) == 0 {
		return
	}
	joined := strings.Join(m.queued, "\n\n")
	m.queued = nil
	if strings.TrimSpace(m.input) == "" {
		m.input = joined
	} else {
		m.input = strings.TrimRight(m.input, "\n") + "\n\n" + joined
	}
	m.inputCursor = -1
	m.refreshSlashCommandFilterFromInput()
}

func (c uiInputController) unlockInputAfterSubmissionError() {
	c.releaseLockedInjectedInput(true)
}

func (c uiInputController) releaseLockedInjectedInput(discardEngineQueue bool) {
	m := c.model
	if !m.inputSubmitLocked {
		return
	}
	locked := strings.TrimSpace(m.lockedInjectText)
	if locked != "" {
		filtered := m.pendingInjected[:0]
		for _, pending := range m.pendingInjected {
			if strings.TrimSpace(pending) == locked {
				continue
			}
			filtered = append(filtered, pending)
		}
		m.pendingInjected = filtered
		if discardEngineQueue && m.engine != nil {
			m.engine.DiscardQueuedUserMessagesMatching(locked)
		}
	}
	m.inputSubmitLocked = false
	m.lockedInjectText = ""
}

func (c uiInputController) handleSpinnerTick() (tea.Model, tea.Cmd) {
	m := c.model
	if !m.busy {
		return m, nil
	}
	m.spinnerFrame = (m.spinnerFrame + 1) % len(spinnerFrames)
	m.syncViewport()
	return m, tickSpinner()
}

func (c uiInputController) handleCompactDone(msg compactDoneMsg) (tea.Model, tea.Cmd) {
	m := c.model
	m.busy = false
	m.clearReviewerState()
	m.compacting = false
	m.spinnerFrame = 0
	c.releaseLockedInjectedInput(true)
	if msg.err != nil {
		detailErr := formatSubmissionError(msg.err)
		m.activity = uiActivityError
		if m.engine != nil {
			m.engine.AppendLocalEntry("error", detailErr)
		} else {
			m.forwardToView(tui.AppendTranscriptMsg{Role: "error", Text: detailErr})
		}
		m.logf("compaction.error err=%q", detailErr)
		m.syncViewport()
		return m, nil
	}

	m.activity = uiActivityIdle
	m.logf("compaction.done")
	m.syncViewport()
	return m, nil
}

func (m *uiModel) popQueued() string {
	if len(m.queued) == 0 {
		return ""
	}
	next := m.queued[0]
	m.queued = m.queued[1:]
	return next
}

func formatSubmissionError(err error) string {
	if err == nil {
		return ""
	}
	var statusErr *llm.APIStatusError
	if errors.As(err, &statusErr) {
		body := statusErr.Body
		if strings.TrimSpace(body) == "" {
			body = "<empty error body>"
		}
		return fmt.Sprintf("openai status %d\nresponse body:\n%s", statusErr.StatusCode, body)
	}
	return err.Error()
}

func tickSpinner() tea.Cmd {
	return tea.Tick(spinnerTickInterval, func(time.Time) tea.Msg {
		return spinnerTickMsg{}
	})
}

func parseUserShellCommand(text string) (string, bool) {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, "$") {
		return "", false
	}
	command := strings.TrimSpace(strings.TrimPrefix(trimmed, "$"))
	if command == "" {
		return "", false
	}
	return command, true
}

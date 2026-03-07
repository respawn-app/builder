package app

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"builder/internal/app/commands"
	"builder/internal/llm"
	"builder/internal/runtime"
	"builder/internal/tui"

	tea "github.com/charmbracelet/bubbletea"
)

type uiInputController struct {
	model *uiModel
}

func (c uiInputController) rollbackTransitionCmd() tea.Cmd {
	if !c.model.altScreenActive {
		return nil
	}
	return tea.ClearScreen
}

func (c uiInputController) startRollbackSelectionFlowCmd() tea.Cmd {
	m := c.model
	if !m.startRollbackSelectionMode() {
		return nil
	}
	overlayCmd := m.pushRollbackOverlayIfNeeded()
	if overlayCmd != nil {
		m.focusRollbackSelection()
		return overlayCmd
	}
	return c.rollbackTransitionCmd()
}

func (c uiInputController) stopRollbackSelectionFlowCmd() tea.Cmd {
	m := c.model
	overlayCmd := m.popRollbackOverlayIfNeeded()
	m.stopRollbackSelectionMode()
	if overlayCmd != nil {
		return overlayCmd
	}
	return c.rollbackTransitionCmd()
}

func (c uiInputController) beginRollbackEditingFlowCmd() tea.Cmd {
	m := c.model
	if !m.beginRollbackEditing() {
		return nil
	}
	overlayCmd := m.popRollbackOverlayIfNeeded()
	if overlayCmd != nil {
		return overlayCmd
	}
	return c.rollbackTransitionCmd()
}

func (c uiInputController) cancelRollbackEditingToSelectionFlowCmd() tea.Cmd {
	m := c.model
	if !m.cancelRollbackEditingBackToSelection() {
		return nil
	}
	overlayCmd := m.pushRollbackOverlayIfNeeded()
	if overlayCmd != nil {
		m.focusRollbackSelection()
		return overlayCmd
	}
	return c.rollbackTransitionCmd()
}

var spinnerFrames = []string{"|", "/", "-", "\\"}
var spinnerTickInterval = 360 * time.Millisecond
var transientStatusDuration = 2200 * time.Millisecond
var errSubmissionInterrupted = errors.New("interrupted")
var rollbackDoubleEscWindow = 500 * time.Millisecond
var csiShiftEnterDedupWindow = 120 * time.Millisecond

func (c uiInputController) markPendingCSIShiftEnter() {
	m := c.model
	m.pendingCSIShiftEnter = true
	m.pendingCSIShiftEnterAt = time.Now()
}

func (c uiInputController) clearPendingCSIShiftEnter() {
	m := c.model
	m.pendingCSIShiftEnter = false
	m.pendingCSIShiftEnterAt = time.Time{}
}

func (c uiInputController) normalizePendingCSIShiftEnterOnEnter() {
	m := c.model
	if !m.pendingCSIShiftEnter {
		return
	}
	if m.pendingCSIShiftEnterAt.IsZero() || time.Since(m.pendingCSIShiftEnterAt) > csiShiftEnterDedupWindow {
		c.clearPendingCSIShiftEnter()
		return
	}
	if strings.HasSuffix(m.input, "\n") {
		m.input = strings.TrimSuffix(m.input, "\n")
		m.inputCursor = -1
		m.refreshSlashCommandFilterFromInput()
	}
	c.clearPendingCSIShiftEnter()
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
	case commands.ActionSetThinking:
		requested := strings.TrimSpace(commandResult.ThinkingLevel)
		if requested == "" {
			current := strings.TrimSpace(m.thinkingLevel)
			if m.engine != nil {
				current = m.engine.ThinkingLevel()
			}
			if current == "" {
				current = "unknown"
			}
			if m.engine != nil {
				m.engine.AppendLocalEntry("system", "Thinking level is "+current)
			} else {
				m.forwardToView(tui.AppendTranscriptMsg{Role: "system", Text: "Thinking level is " + current})
			}
			return m, nil
		}
		normalized, ok := runtime.NormalizeThinkingLevel(requested)
		if !ok {
			errText := "invalid thinking level " + strconv.Quote(requested) + " (expected low|medium|high|xhigh)"
			if m.engine != nil {
				m.engine.AppendLocalEntry("error", errText)
			} else {
				m.forwardToView(tui.AppendTranscriptMsg{Role: "error", Text: errText})
			}
			return m, nil
		}
		if m.engine != nil {
			if err := m.engine.SetThinkingLevel(normalized); err != nil {
				m.engine.AppendLocalEntry("error", formatSubmissionError(err))
				return m, nil
			}
			m.thinkingLevel = m.engine.ThinkingLevel()
			m.engine.AppendLocalEntry("system", "Thinking level set to "+m.thinkingLevel)
			return m, nil
		}
		m.thinkingLevel = normalized
		m.forwardToView(tui.AppendTranscriptMsg{Role: "system", Text: "Thinking level set to " + m.thinkingLevel})
		return m, nil
	case commands.ActionSetSupervisor:
		requested := strings.ToLower(strings.TrimSpace(commandResult.SupervisorMode))
		currentEnabled, currentMode := m.reviewerInvocationState()
		targetEnabled := currentEnabled
		switch requested {
		case "":
			targetEnabled = !currentEnabled
		case "on":
			targetEnabled = true
		case "off":
			targetEnabled = false
		default:
			errText := "invalid supervisor mode " + strconv.Quote(requested) + " (expected on|off)"
			if m.engine != nil {
				m.engine.AppendLocalEntry("error", errText)
			} else {
				m.forwardToView(tui.AppendTranscriptMsg{Role: "error", Text: errText})
			}
			return m, nil
		}

		changed := false
		nextMode := currentMode
		if m.engine != nil {
			var err error
			changed, nextMode, err = m.engine.SetReviewerEnabled(targetEnabled)
			if err != nil {
				m.engine.AppendLocalEntry("error", formatSubmissionError(err))
				return m, nil
			}
		} else {
			nextMode = "off"
			if targetEnabled {
				nextMode = "edits"
			}
			changed = currentEnabled != targetEnabled
		}
		m.reviewerMode = nextMode
		m.reviewerEnabled = nextMode != "off"
		status := reviewerToggleStatusMessage(m.reviewerEnabled, nextMode, changed)
		if m.engine != nil {
			m.engine.AppendLocalEntry("system", status)
		} else {
			m.forwardToView(tui.AppendTranscriptMsg{Role: "system", Text: status})
		}
		return m, c.showTransientStatus(status)
	case commands.ActionSetAutoCompaction:
		requested := strings.ToLower(strings.TrimSpace(commandResult.AutoCompactionMode))
		currentEnabled := m.autoCompactionState()
		currentCompactionMode := "native"
		if m.engine != nil {
			currentCompactionMode = m.engine.CompactionMode()
		}
		targetEnabled := currentEnabled
		switch requested {
		case "":
			targetEnabled = !currentEnabled
		case "on":
			targetEnabled = true
		case "off":
			targetEnabled = false
		default:
			errText := "invalid autocompaction mode " + strconv.Quote(requested) + " (expected on|off)"
			if m.engine != nil {
				m.engine.AppendLocalEntry("error", errText)
			} else {
				m.forwardToView(tui.AppendTranscriptMsg{Role: "error", Text: errText})
			}
			return m, nil
		}

		changed := false
		nextEnabled := currentEnabled
		if m.engine != nil {
			changed, nextEnabled = m.engine.SetAutoCompactionEnabled(targetEnabled)
		} else {
			nextEnabled = targetEnabled
			changed = currentEnabled != targetEnabled
		}
		m.autoCompactionEnabled = nextEnabled
		status := autoCompactionToggleStatusMessage(nextEnabled, changed, currentCompactionMode)
		if m.engine != nil {
			m.engine.AppendLocalEntry("system", status)
		} else {
			m.forwardToView(tui.AppendTranscriptMsg{Role: "system", Text: status})
		}
		return m, c.showTransientStatus(status)
	case commands.ActionCompact:
		return m, c.startCompaction(commandResult.Args)
	case commands.ActionProcesses:
		args := strings.Fields(strings.TrimSpace(commandResult.Args))
		if len(args) == 0 {
			m.openProcessList()
			return m, nil
		}
		action := strings.ToLower(strings.TrimSpace(args[0]))
		id := ""
		if len(args) > 1 {
			id = strings.TrimSpace(args[1])
		}
		return c.runProcessAction(action, id)
	}
	return m, nil
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

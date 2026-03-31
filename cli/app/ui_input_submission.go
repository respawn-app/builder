package app

import (
	"context"
	"errors"
	"strings"

	"builder/cli/tui"
	"builder/server/session"

	tea "github.com/charmbracelet/bubbletea"
)

func (c uiInputController) startSubmission(text string) tea.Cmd {
	m := c.model
	c.startBusyActivity(false)
	command, isUserShell := parseUserShellCommand(text)
	if isUserShell {
		m.logf("step.user_shell.start command_chars=%d", len(command))
	} else {
		m.logf("step.start user_chars=%d", len(text))
	}
	if m.engine == nil {
		if isUserShell {
			m.forwardToView(tui.AppendTranscriptMsg{Role: "tool_call", Text: command})
		} else {
			m.conversationFreshness = session.ConversationFreshnessEstablished
			m.forwardToView(tui.AppendTranscriptMsg{Role: "user", Text: text})
		}
	}
	m.syncViewport()
	if isUserShell {
		return tea.Batch(c.submitUserShellCmd(command), m.ensureSpinnerTicking())
	}
	if m.engine != nil {
		m.preSubmitCheckToken++
		token := m.preSubmitCheckToken
		m.pendingPreSubmitText = text
		m.queued = append(m.queued, text)
		return tea.Batch(c.preSubmitCompactionCheckCmd(token, text), m.ensureSpinnerTicking())
	}
	return tea.Batch(c.submitCmd(text), m.ensureSpinnerTicking())
}

func (c uiInputController) startSubmissionWithPromptHistory(text string) tea.Cmd {
	m := c.model
	_, isUserShell := parseUserShellCommand(text)
	if m.engine != nil && !isUserShell {
		return c.startSubmission(text)
	}
	return sequenceCmds(m.recordPromptHistory(text), c.startSubmission(text))
}

func (c uiInputController) preSubmitCompactionCheckCmd(token uint64, text string) tea.Cmd {
	m := c.model
	return func() tea.Msg {
		if m.engine == nil {
			return preSubmitCompactionCheckDoneMsg{token: token, text: text}
		}
		shouldCompact, err := m.engine.ShouldCompactBeforeUserMessage(context.Background(), text)
		return preSubmitCompactionCheckDoneMsg{token: token, text: text, shouldCompact: shouldCompact, err: err}
	}
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
	c.startBusyActivity(true)
	m.logf("compaction.start args_chars=%d", len(strings.TrimSpace(args)))
	m.syncViewport()
	return tea.Batch(c.compactCmd(args), m.ensureSpinnerTicking())
}

func (c uiInputController) startPreSubmitCompaction() tea.Cmd {
	m := c.model
	c.startBusyActivity(true)
	m.logf("compaction.pre_submit.start")
	m.syncViewport()
	return tea.Batch(c.preSubmitCompactCmd(), m.ensureSpinnerTicking())
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

func (c uiInputController) preSubmitCompactCmd() tea.Cmd {
	m := c.model
	return func() tea.Msg {
		if m.engine == nil {
			return compactDoneMsg{err: errors.New("runtime engine is not configured")}
		}
		return compactDoneMsg{err: m.engine.CompactContextForPreSubmit(context.Background())}
	}
}

func (c uiInputController) startBusyActivity(compacting bool) {
	m := c.model
	m.clearReviewerState()
	m.busy = true
	m.activity = uiActivityRunning
	m.sawAssistantDelta = false
	if compacting {
		m.compacting = true
	}
}

func (c uiInputController) finishBusyActivity(compacting bool) {
	m := c.model
	m.busy = false
	m.clearReviewerState()
	m.spinnerFrame = 0
	if !m.shouldAnimateSpinner() {
		m.stopSpinnerTicking()
	}
	if compacting {
		m.compacting = false
	}
}

func (c uiInputController) handleSubmitDone(msg submitDoneMsg) (tea.Model, tea.Cmd) {
	m := c.model
	c.finishBusyActivity(false)
	m.pendingPreSubmitText = ""
	if msg.err != nil {
		c.unlockInputAfterSubmissionError()
		c.restorePendingInjectedIntoInput()
		if errors.Is(msg.err, errSubmissionInterrupted) {
			c.restoreQueuedMessagesIntoInput()
			m.activity = uiActivityInterrupted
			m.logf("step.interrupted")
			m.syncViewport()
			return m, nil
		}
		detailErr := formatSubmissionError(msg.err)
		m.activity = uiActivityError
		m.appendLocalEntry("error", detailErr)
		m.logf("step.error err=%q", detailErr)
		if len(m.queued) > 0 && strings.TrimSpace(m.input) == "" {
			return c.flushQueuedInputs(queueDrainAuto)
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
		return c.flushQueuedInputs(queueDrainAuto)
	}
	m.syncViewport()
	return m, nil
}

func (c uiInputController) handlePreSubmitCompactionCheckDone(msg preSubmitCompactionCheckDoneMsg) (tea.Model, tea.Cmd) {
	m := c.model
	if msg.token != m.preSubmitCheckToken {
		return m, nil
	}
	if msg.err != nil {
		c.finishBusyActivity(false)
		c.releaseLockedInjectedInput(true)
		m.discardQueuedText(msg.text)
		c.restorePendingInjectedIntoInput()
		c.restorePendingPreSubmitTextIntoInput()
		detailErr := formatSubmissionError(msg.err)
		m.activity = uiActivityError
		m.appendLocalEntry("error", detailErr)
		m.logf("step.pre_submit_check.error err=%q", detailErr)
		m.syncViewport()
		return m, nil
	}
	if !msg.shouldCompact {
		m.discardQueuedText(msg.text)
		m.logf("step.pre_submit_check.submit user_chars=%d", len(msg.text))
		return m, sequenceCmds(m.recordPromptHistory(msg.text), c.submitCmd(msg.text))
	}
	m.logf("step.pre_submit_check.compact_then_submit user_chars=%d", len(msg.text))
	return m, c.startPreSubmitCompaction()
}

func (c uiInputController) handleSpinnerTick(msg spinnerTickMsg) (tea.Model, tea.Cmd) {
	m := c.model
	if msg.token == 0 || msg.token != m.spinnerTickToken {
		return m, nil
	}
	if !m.shouldAnimateSpinner() {
		m.stopSpinnerTicking()
		return m, nil
	}
	frameCount := len(pendingToolSpinner.Frames)
	if frameCount <= 0 {
		frameCount = 1
	}
	m.spinnerFrame = (m.spinnerFrame + 1) % frameCount
	m.syncViewport()
	return m, tickSpinner(msg.token)
}

func (c uiInputController) handleCompactDone(msg compactDoneMsg) (tea.Model, tea.Cmd) {
	m := c.model
	c.finishBusyActivity(true)
	c.releaseLockedInjectedInput(true)
	if msg.err != nil {
		m.discardQueuedText(m.pendingPreSubmitText)
		c.restorePendingInjectedIntoInput()
		c.restorePendingPreSubmitTextIntoInput()
		detailErr := formatSubmissionError(msg.err)
		m.activity = uiActivityError
		m.appendLocalEntry("error", detailErr)
		m.logf("compaction.error err=%q", detailErr)
		m.syncViewport()
		return m, nil
	}

	m.activity = uiActivityIdle
	m.logf("compaction.done")
	if len(m.queued) > 0 {
		return c.flushQueuedInputs(queueDrainAuto)
	}
	if m.engine != nil && m.engine.HasQueuedUserWork() {
		return m, c.startQueuedInjectionSubmission()
	}
	m.syncViewport()
	return m, nil
}

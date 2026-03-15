package app

import (
	"context"
	"errors"
	"strings"

	"builder/internal/tui"

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
	c.startBusyActivity(true)
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
	if compacting {
		m.compacting = false
	}
}

func (c uiInputController) handleSubmitDone(msg submitDoneMsg) (tea.Model, tea.Cmd) {
	m := c.model
	c.finishBusyActivity(false)
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
		m.appendLocalEntry("error", detailErr)
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
	c.finishBusyActivity(true)
	c.releaseLockedInjectedInput(true)
	if msg.err != nil {
		detailErr := formatSubmissionError(msg.err)
		m.activity = uiActivityError
		m.appendLocalEntry("error", detailErr)
		m.logf("compaction.error err=%q", detailErr)
		m.syncViewport()
		return m, nil
	}

	m.activity = uiActivityIdle
	m.logf("compaction.done")
	m.syncViewport()
	return m, nil
}

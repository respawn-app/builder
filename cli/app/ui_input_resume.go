package app

import (
	"context"
	"errors"

	tea "github.com/charmbracelet/bubbletea"
)

func (c uiInputController) startQueuedInjectionSubmission() tea.Cmd {
	m := c.model
	if !m.hasQueuedRuntimeUserWork() {
		return nil
	}
	c.startBusyActivity(false)
	m.logf("step.resume_queued_injected pending_injected=%d", len(m.pendingInjected))
	m.syncViewport()
	return tea.Batch(c.submitQueuedUserMessagesCmd(), c.model.ensureSpinnerTicking())
}

func (c uiInputController) submitQueuedUserMessagesCmd() tea.Cmd {
	m := c.model
	return func() tea.Msg {
		if !m.hasRuntimeClient() {
			return submitDoneMsg{err: errors.New("runtime engine is not configured")}
		}
		msg, err := m.submitQueuedRuntimeUserMessages(context.Background())
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return submitDoneMsg{err: errSubmissionInterrupted}
			}
			return submitDoneMsg{err: err}
		}
		return submitDoneMsg{message: msg}
	}
}

package app

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

type queueDrainMode uint8

const (
	queueDrainOne queueDrainMode = iota
	queueDrainAuto
)

func (m *uiModel) queueInput(text string) {
	m.queued = append(m.queued, text)
	m.clearInput()
	m.activity = uiActivityQueued
}

func (m *uiModel) lockInjectedInput(text string) {
	if m.engine != nil {
		m.engine.QueueUserMessage(text)
	}
	m.pendingInjected = append(m.pendingInjected, text)
	m.lockedInjectText = text
	m.inputSubmitLocked = true
	m.activity = uiActivityQueued
}

func (c uiInputController) queueOrStartSubmission(text string) (tea.Model, tea.Cmd) {
	m := c.model
	if m.isInputLocked() {
		return m, nil
	}
	draftText, draftCursor, restoreDraft := m.capturePromptHistoryDraftForReuse()
	m.queueInput(text)
	m.restoreCapturedPromptHistoryDraft(draftText, draftCursor, restoreDraft)
	if m.busy {
		return m, nil
	}
	return c.flushQueuedInputs(queueDrainOne)
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

func (c uiInputController) flushQueuedInputs(mode queueDrainMode) (tea.Model, tea.Cmd) {
	m := c.model
	if len(m.queued) == 0 {
		return m, nil
	}
	cmds := make([]tea.Cmd, 0, 2)
	for len(m.queued) > 0 {
		next := m.popQueued()
		if cmd := c.dispatchQueuedInput(next); cmd != nil {
			cmds = append(cmds, cmd)
		}
		if mode == queueDrainOne || !m.shouldContinueQueuedInputAutoDrain() {
			break
		}
	}
	return m, tea.Batch(cmds...)
}

func (c uiInputController) dispatchQueuedInput(text string) tea.Cmd {
	m := c.model
	if m.commandRegistry != nil {
		if _, knownCommand := m.commandRegistry.Command(text); knownCommand {
			if commandResult := m.commandRegistry.Execute(text); commandResult.Handled {
				_, cmd := c.applyCommandResult(commandResult)
				return sequenceCmds(m.recordPromptHistory(text), cmd)
			}
		}
	}
	return sequenceCmds(m.recordPromptHistory(text), c.startSubmission(text))
}

func (m *uiModel) shouldContinueQueuedInputAutoDrain() bool {
	if len(m.queued) == 0 || m.busy || m.isInputLocked() || m.exitAction != UIActionNone || m.activeAsk != nil {
		return false
	}
	if m.inputMode() != uiInputModeMain {
		return false
	}
	return strings.TrimSpace(m.input) == ""
}

func (m *uiModel) popQueued() string {
	if len(m.queued) == 0 {
		return ""
	}
	next := m.queued[0]
	m.queued = m.queued[1:]
	return next
}

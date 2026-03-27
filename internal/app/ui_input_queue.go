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

func (m *uiModel) enqueueInjectedInput(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return false
	}
	if m.engine != nil {
		m.engine.QueueUserMessage(trimmed)
	}
	m.pendingInjected = append(m.pendingInjected, trimmed)
	m.activity = uiActivityQueued
	return true
}

func (m *uiModel) queueInjectedInput(text string) {
	if !m.enqueueInjectedInput(text) {
		return
	}
	m.clearInput()
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

func (c uiInputController) restorePendingPreSubmitTextIntoInput() {
	m := c.model
	pending := strings.TrimSpace(m.pendingPreSubmitText)
	if pending == "" {
		return
	}
	m.pendingPreSubmitText = ""
	if strings.TrimSpace(m.input) == "" {
		m.input = pending
	} else {
		m.input = strings.TrimRight(m.input, "\n") + "\n\n" + pending
	}
	m.inputCursor = -1
	m.refreshSlashCommandFilterFromInput()
}

func (c uiInputController) restorePendingInjectedIntoInput() {
	m := c.model
	if len(m.pendingInjected) == 0 {
		return
	}
	pending := append([]string(nil), m.pendingInjected...)
	if m.engine != nil {
		for _, text := range pending {
			m.engine.DiscardQueuedUserMessagesMatching(text)
		}
	}
	joined := strings.Join(pending, "\n\n")
	m.pendingInjected = nil
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
				return finalizeSlashCommandCmd(commandResult.Action, cmd, m.recordPromptHistory(text))
			}
		}
	}
	return c.startSubmissionWithPromptHistory(text)
}

func (m *uiModel) shouldContinueQueuedInputAutoDrain() bool {
	if len(m.queued) == 0 || m.busy || m.isInputLocked() || m.exitAction != UIActionNone || m.ask.hasCurrent() {
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

func (m *uiModel) discardQueuedText(text string) bool {
	needle := strings.TrimSpace(text)
	if needle == "" {
		return false
	}
	for i := 0; i < len(m.queued); i++ {
		if strings.TrimSpace(m.queued[i]) != needle {
			continue
		}
		m.queued = append(m.queued[:i], m.queued[i+1:]...)
		return true
	}
	return false
}

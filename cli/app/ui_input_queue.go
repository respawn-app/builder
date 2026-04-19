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
}

func (m *uiModel) enqueueInjectedInput(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return false
	}
	if m.hasRuntimeClient() {
		m.queueRuntimeUserMessage(trimmed)
	}
	m.pendingInjected = append(m.pendingInjected, trimmed)
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
	if c.blockDisconnectedSubmission(false, "") {
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

func (c uiInputController) blockDisconnectedSubmission(restoreHidden bool, submittedText string) bool {
	m := c.model
	if !m.runtimeDisconnectStatusVisible() {
		return false
	}
	if restoreHidden {
		c.restorePendingInjectedIntoInput()
		c.restoreSubmittedTextIntoInput(submittedText)
		c.restoreQueuedMessagesIntoInput()
	}
	m.activity = uiActivityError
	m.syncViewport()
	return true
}

func (c uiInputController) restoreQueuedMessagesIntoInput() {
	m := c.model
	if len(m.queued) == 0 {
		return
	}
	joined := strings.Join(m.queued, "\n\n")
	m.queued = nil
	newInput := joined
	if strings.TrimSpace(m.input) == "" {
		newInput = joined
	} else {
		newInput = strings.TrimRight(m.input, "\n") + "\n\n" + joined
	}
	m.replaceMainInput(newInput, -1)
}

func (c uiInputController) restorePendingPreSubmitTextIntoInput() {
	m := c.model
	pending := strings.TrimSpace(m.pendingPreSubmitText)
	if pending == "" {
		return
	}
	m.pendingPreSubmitText = ""
	newInput := pending
	if strings.TrimSpace(m.input) == "" {
		newInput = pending
	} else {
		newInput = strings.TrimRight(m.input, "\n") + "\n\n" + pending
	}
	m.replaceMainInput(newInput, -1)
}

func (c uiInputController) restoreSubmittedTextIntoInput(text string) {
	m := c.model
	submitted := strings.TrimSpace(text)
	if submitted == "" {
		return
	}
	newInput := submitted
	if strings.TrimSpace(m.input) != "" {
		newInput = strings.TrimRight(m.input, "\n") + "\n\n" + submitted
	}
	m.replaceMainInput(newInput, -1)
}

func (c uiInputController) restorePendingInjectedIntoInput() {
	m := c.model
	if len(m.pendingInjected) == 0 {
		return
	}
	pending := append([]string(nil), m.pendingInjected...)
	if m.hasRuntimeClient() {
		for _, text := range pending {
			m.discardQueuedRuntimeUserMessagesMatching(text)
		}
	}
	joined := strings.Join(pending, "\n\n")
	m.pendingInjected = nil
	newInput := joined
	if strings.TrimSpace(m.input) == "" {
		newInput = joined
	} else {
		newInput = strings.TrimRight(m.input, "\n") + "\n\n" + joined
	}
	m.replaceMainInput(newInput, -1)
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
		if discardEngineQueue && m.hasRuntimeClient() {
			m.discardQueuedRuntimeUserMessagesMatching(locked)
		}
	}
	m.inputSubmitLocked = false
	m.lockedInjectText = ""
}

func (c uiInputController) flushQueuedInputs(mode queueDrainMode) (tea.Model, tea.Cmd) {
	m := c.model
	if c.blockDisconnectedSubmission(true, "") {
		return m, nil
	}
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

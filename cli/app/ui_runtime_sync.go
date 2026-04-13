package app

import (
	"strconv"
	"strings"
	"time"

	"builder/cli/tui"
	"builder/shared/clientui"
	"builder/shared/transcriptdiag"
	tea "github.com/charmbracelet/bubbletea"
)

var uiRuntimeHydrationRetryDelay = 750 * time.Millisecond

func (m *uiModel) requestRuntimeMainViewRefresh() tea.Cmd {
	if !m.hasRuntimeClient() {
		return nil
	}
	m.runtimeMainViewToken++
	token := m.runtimeMainViewToken
	client := m.runtimeClient()
	return func() tea.Msg {
		view, err := client.RefreshMainView()
		return runtimeMainViewRefreshedMsg{token: token, view: view, err: err}
	}
}

func (m *uiModel) requestRuntimeTranscriptSync() tea.Cmd {
	if !m.hasRuntimeClient() {
		return nil
	}
	return m.startRuntimeTranscriptPageRequest(m.transcriptRequestForCurrentMode(), false, clientui.TranscriptRecoveryCauseNone)
}

func (m *uiModel) requestRuntimeTranscriptSyncForContinuityLoss(cause clientui.TranscriptRecoveryCause) tea.Cmd {
	if !m.hasRuntimeClient() {
		return nil
	}
	return m.startRuntimeTranscriptPageRequest(m.transcriptRequestForCurrentMode(), false, cause)
}

func (m *uiModel) requestRuntimeTranscriptPage(request clientui.TranscriptPageRequest) tea.Cmd {
	return m.startRuntimeTranscriptPageRequest(request, true, clientui.TranscriptRecoveryCauseNone)
}

func (m *uiModel) startRuntimeTranscriptPageRequest(request clientui.TranscriptPageRequest, allowDuplicateSkip bool, recoveryCause clientui.TranscriptRecoveryCause) tea.Cmd {
	request = normalizeRuntimeTranscriptRequest(request)
	if m.runtimeTranscriptBusy {
		m.runtimeTranscriptDirty = true
		m.logf("ui.runtime.transcript.mark_dirty")
		return nil
	}
	if allowDuplicateSkip && m.shouldSkipTranscriptPageRequest(request) {
		m.logf("ui.runtime.transcript.skip_duplicate mode=%s offset=%d limit=%d window=%s", m.view.Mode(), request.Offset, request.Limit, request.Window)
		return nil
	}
	m.runtimeTranscriptBusy = true
	m.runtimeTranscriptDirty = false
	m.runtimeTranscriptToken++
	token := m.runtimeTranscriptToken
	client := m.runtimeClient()
	if client == nil {
		m.runtimeTranscriptBusy = false
		return nil
	}
	m.logf("ui.runtime.transcript.start token=%d", token)
	fields := map[string]string{
		"session_id":            m.sessionID,
		"mode":                  string(m.view.Mode()),
		"path":                  "hydrate",
		"token":                 strconv.FormatUint(token, 10),
		"recovery_cause":        string(recoveryCause),
		"current_revision":      strconv.FormatInt(m.transcriptRevision, 10),
		"transcript_live_dirty": strconv.FormatBool(m.transcriptLiveDirty),
		"reasoning_live_dirty":  strconv.FormatBool(m.reasoningLiveDirty),
	}
	for key, value := range transcriptdiag.RequestFields(request) {
		fields[key] = value
	}
	m.logTranscriptDiag(transcriptdiag.FormatLine("transcript.diag.client.hydrate_start", fields))
	return func() tea.Msg {
		var (
			transcript clientui.TranscriptPage
			err        error
		)
		if allowDuplicateSkip {
			transcript, err = client.LoadTranscriptPage(request)
		} else {
			transcript, err = client.RefreshTranscriptPage(request)
		}
		return runtimeTranscriptRefreshedMsg{token: token, req: request, transcript: transcript, recoveryCause: recoveryCause, err: err}
	}
}

func (m *uiModel) shouldSkipTranscriptPageRequest(req clientui.TranscriptPageRequest) bool {
	if m.runtimeTranscriptDirty || m.transcriptLiveDirty || m.reasoningLiveDirty {
		return false
	}
	if m.view.Mode() != tui.ModeDetail {
		return false
	}
	if !m.detailTranscript.loaded {
		return false
	}
	return pageRequestEqual(m.detailTranscript.lastRequest, req)
}

func (m *uiModel) scheduleRuntimeTranscriptRetry(cause clientui.TranscriptRecoveryCause) tea.Cmd {
	if !m.hasRuntimeClient() {
		return nil
	}
	m.runtimeTranscriptRetry++
	token := m.runtimeTranscriptRetry
	m.logf("ui.runtime.transcript.retry_scheduled token=%d cause=%s delay=%s", token, cause, uiRuntimeHydrationRetryDelay)
	return tea.Tick(uiRuntimeHydrationRetryDelay, func(time.Time) tea.Msg {
		return runtimeTranscriptRetryMsg{token: token, recoveryCause: cause}
	})
}

func continuityRecoveryCauseFromHydrateFailure(cause clientui.TranscriptRecoveryCause) clientui.TranscriptRecoveryCause {
	if cause != clientui.TranscriptRecoveryCauseNone {
		return cause
	}
	return clientui.TranscriptRecoveryCauseHydrateRetry
}

func (m *uiModel) handleRuntimeMainViewRefreshed(msg runtimeMainViewRefreshedMsg) tea.Cmd {
	if msg.token != m.runtimeMainViewToken {
		return nil
	}
	if msg.err != nil {
		m.observeRuntimeRequestResult(msg.err)
		m.logf("ui.runtime.main_view err=%q", msg.err.Error())
		return nil
	}
	m.observeRuntimeRequestResult(nil)
	return m.runtimeAdapter().applyProjectedSessionMetadata(msg.view.Session)
}

func (m *uiModel) handleRuntimeTranscriptRefreshed(msg runtimeTranscriptRefreshedMsg) tea.Cmd {
	if msg.token != m.runtimeTranscriptToken {
		return nil
	}
	m.runtimeTranscriptBusy = false
	if msg.err != nil {
		m.invalidateTransientTranscriptState()
		m.observeRuntimeRequestResult(msg.err)
		m.logf("ui.runtime.transcript err=%q", msg.err.Error())
		m.logTranscriptDiag(transcriptdiag.FormatLine("transcript.diag.client.hydrate_response", map[string]string{
			"session_id":     m.sessionID,
			"mode":           string(m.view.Mode()),
			"path":           "hydrate",
			"token":          strconv.FormatUint(msg.token, 10),
			"recovery_cause": string(msg.recoveryCause),
			"err":            msg.err.Error(),
		}))
		resumeCmd := tea.Cmd(nil)
		if m.waitRuntimeEventAfterHydration {
			m.waitRuntimeEventAfterHydration = false
			resumeCmd = m.waitRuntimeEventCmd()
		}
		return tea.Batch(m.scheduleRuntimeTranscriptRetry(continuityRecoveryCauseFromHydrateFailure(msg.recoveryCause)), resumeCmd)
	}
	m.observeRuntimeRequestResult(nil)
	m.logTranscriptPageDiag("transcript.diag.client.hydrate_response", msg.req, msg.transcript, map[string]string{
		"path":           "hydrate",
		"token":          strconv.FormatUint(msg.token, 10),
		"recovery_cause": string(msg.recoveryCause),
	})
	recovered := m.runtimeTranscriptRetry != 0
	if m.runtimeTranscriptRetry != 0 {
		m.runtimeTranscriptRetry++
	}
	if recovered {
		m.logf("ui.runtime.transcript.recovered token=%d", msg.token)
	}
	applyCmd := m.runtimeAdapter().applyRuntimeTranscriptPageWithRecovery(msg.req, msg.transcript, msg.recoveryCause)
	if !m.runtimeTranscriptDirty {
		if m.pendingQueuedDrainAfterHydration {
			m.queuedDrainReadyAfterHydration = true
		}
		resumeCmd := tea.Cmd(nil)
		if m.waitRuntimeEventAfterHydration {
			m.waitRuntimeEventAfterHydration = false
			resumeCmd = m.waitRuntimeEventCmd()
		}
		return sequenceCmds(applyCmd, m.flushQueuedInputsAfterHydration(), resumeCmd)
	}
	m.runtimeTranscriptDirty = false
	m.logf("ui.runtime.transcript.repeat_after_dirty token=%d", msg.token)
	return sequenceCmds(applyCmd, m.requestRuntimeTranscriptSync())
}

func (m *uiModel) flushQueuedInputsAfterHydration() tea.Cmd {
	if m == nil || !m.pendingQueuedDrainAfterHydration {
		return nil
	}
	if !m.queuedDrainReadyAfterHydration {
		return nil
	}
	if m.busy || m.isInputLocked() {
		if len(m.queued) == 0 || strings.TrimSpace(m.pendingPreSubmitText) != "" {
			m.pendingQueuedDrainAfterHydration = false
			m.queuedDrainReadyAfterHydration = false
		}
		return nil
	}
	m.pendingQueuedDrainAfterHydration = false
	m.queuedDrainReadyAfterHydration = false
	if len(m.queued) == 0 {
		m.inputController().notifyTurnQueueDrainedIfIdle()
		return nil
	}
	_, cmd := m.inputController().flushQueuedInputs(queueDrainAuto)
	m.inputController().notifyTurnQueueDrainedIfIdle()
	return cmd
}

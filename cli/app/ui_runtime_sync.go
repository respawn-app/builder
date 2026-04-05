package app

import (
	"strconv"
	"time"

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
	if m.runtimeTranscriptBusy {
		m.runtimeTranscriptDirty = true
		m.logf("ui.runtime.transcript.mark_dirty")
		return nil
	}
	m.runtimeTranscriptBusy = true
	m.runtimeTranscriptDirty = false
	m.runtimeTranscriptToken++
	token := m.runtimeTranscriptToken
	client := m.runtimeClient()
	req := m.transcriptRequestForCurrentMode()
	m.logf("ui.runtime.transcript.start token=%d", token)
	fields := map[string]string{
		"session_id":            m.sessionID,
		"mode":                  string(m.view.Mode()),
		"path":                  "hydrate",
		"token":                 strconv.FormatUint(token, 10),
		"current_revision":      strconv.FormatInt(m.transcriptRevision, 10),
		"transcript_live_dirty": strconv.FormatBool(m.transcriptLiveDirty),
		"reasoning_live_dirty":  strconv.FormatBool(m.reasoningLiveDirty),
	}
	for key, value := range transcriptdiag.RequestFields(req) {
		fields[key] = value
	}
	m.logTranscriptDiag(transcriptdiag.FormatLine("transcript.diag.client.hydrate_start", fields))
	return func() tea.Msg {
		transcript, err := client.LoadTranscriptPage(req)
		return runtimeTranscriptRefreshedMsg{token: token, req: req, transcript: transcript, err: err}
	}
}

func (m *uiModel) scheduleRuntimeTranscriptRetry() tea.Cmd {
	if !m.hasRuntimeClient() {
		return nil
	}
	m.runtimeTranscriptRetry++
	token := m.runtimeTranscriptRetry
	m.logf("ui.runtime.transcript.retry_scheduled token=%d delay=%s", token, uiRuntimeHydrationRetryDelay)
	return tea.Tick(uiRuntimeHydrationRetryDelay, func(time.Time) tea.Msg {
		return runtimeTranscriptRetryMsg{token: token}
	})
}

func (m *uiModel) handleRuntimeMainViewRefreshed(msg runtimeMainViewRefreshedMsg) tea.Cmd {
	if msg.token != m.runtimeMainViewToken {
		return nil
	}
	if msg.err != nil {
		m.logf("ui.runtime.main_view err=%q", msg.err.Error())
		return nil
	}
	return m.runtimeAdapter().applyProjectedSessionMetadata(msg.view.Session)
}

func (m *uiModel) handleRuntimeTranscriptRefreshed(msg runtimeTranscriptRefreshedMsg) tea.Cmd {
	if msg.token != m.runtimeTranscriptToken {
		return nil
	}
	m.runtimeTranscriptBusy = false
	if msg.err != nil {
		m.logf("ui.runtime.transcript err=%q", msg.err.Error())
		m.logTranscriptDiag(transcriptdiag.FormatLine("transcript.diag.client.hydrate_response", map[string]string{
			"session_id": m.sessionID,
			"mode":       string(m.view.Mode()),
			"path":       "hydrate",
			"token":      strconv.FormatUint(msg.token, 10),
			"err":        msg.err.Error(),
		}))
		return m.scheduleRuntimeTranscriptRetry()
	}
	m.logTranscriptPageDiag("transcript.diag.client.hydrate_response", msg.req, msg.transcript, map[string]string{
		"path":  "hydrate",
		"token": strconv.FormatUint(msg.token, 10),
	})
	recovered := m.runtimeTranscriptRetry != 0
	if m.runtimeTranscriptRetry != 0 {
		m.runtimeTranscriptRetry++
	}
	if recovered {
		m.logf("ui.runtime.transcript.recovered token=%d", msg.token)
	}
	applyCmd := m.runtimeAdapter().applyRuntimeTranscriptPage(msg.req, msg.transcript)
	if !m.runtimeTranscriptDirty {
		return applyCmd
	}
	m.runtimeTranscriptDirty = false
	m.logf("ui.runtime.transcript.repeat_after_dirty token=%d", msg.token)
	return sequenceCmds(applyCmd, m.requestRuntimeTranscriptSync())
}

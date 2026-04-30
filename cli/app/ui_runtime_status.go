package app

import (
	"strings"

	"builder/cli/tui"
	"builder/server/llm"
	"builder/shared/clientui"
)

func (m *uiModel) runtimeMainView() clientui.RuntimeMainView {
	if client := m.runtimeClient(); client != nil {
		return client.MainView()
	}
	return clientui.RuntimeMainView{
		Status:  m.localRuntimeStatus(),
		Session: m.localRuntimeSessionView(),
	}
}

func (m *uiModel) refreshRuntimeMainView() clientui.RuntimeMainView {
	if client := m.runtimeClient(); client != nil {
		view, err := client.RefreshMainView()
		if err == nil {
			m.observeRuntimeRequestResult(nil)
			return view
		}
		m.observeRuntimeRequestResult(err)
		return client.MainView()
	}
	return clientui.RuntimeMainView{
		Status:  m.localRuntimeStatus(),
		Session: m.localRuntimeSessionView(),
	}
}

func (m *uiModel) runtimeStatus() clientui.RuntimeStatus {
	view := m.runtimeMainView()
	status := view.Status
	if m.runtimeContextUsageAppliesTo(view.Session.SessionID) {
		status.ContextUsage = m.runtimeContextUsage
	}
	return status
}

func (m *uiModel) refreshRuntimeStatus() clientui.RuntimeStatus {
	view := m.refreshRuntimeMainView()
	status := view.Status
	if m.runtimeContextUsageAppliesTo(view.Session.SessionID) {
		status.ContextUsage = m.runtimeContextUsage
	}
	return status
}

func (m *uiModel) applyRuntimeEventStatus(evt clientui.Event) {
	if m == nil || evt.ContextUsage == nil {
		return
	}
	m.setRuntimeContextUsage(m.currentRuntimeSessionID(), *evt.ContextUsage)
	if observer, ok := m.runtimeClient().(interface{ observeRuntimeEventStatus(clientui.Event) }); ok {
		observer.observeRuntimeEventStatus(evt)
	}
}

func (m *uiModel) setRuntimeContextUsage(sessionID string, usage clientui.RuntimeContextUsage) {
	if m == nil {
		return
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		m.runtimeContextUsage = clientui.RuntimeContextUsage{}
		m.runtimeContextUsageSession = ""
		return
	}
	m.runtimeContextUsage = usage
	m.runtimeContextUsageSession = sessionID
}

func (m *uiModel) runtimeContextUsageAppliesTo(sessionID string) bool {
	if m == nil || m.runtimeContextUsage.WindowTokens <= 0 {
		return false
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		sessionID = strings.TrimSpace(m.sessionID)
	}
	return sessionID != "" && strings.TrimSpace(m.runtimeContextUsageSession) == sessionID
}

func (m *uiModel) currentRuntimeSessionID() string {
	if m == nil {
		return ""
	}
	if sessionID := strings.TrimSpace(m.sessionID); sessionID != "" {
		return sessionID
	}
	if client := m.runtimeClient(); client != nil {
		return strings.TrimSpace(client.MainView().Session.SessionID)
	}
	return ""
}

func (m *uiModel) refreshRuntimeSessionView() clientui.RuntimeSessionView {
	return m.refreshRuntimeMainView().Session
}

func (m *uiModel) tryRefreshRuntimeSessionView() (clientui.RuntimeSessionView, bool) {
	if client := m.runtimeClient(); client != nil {
		view, err := client.RefreshMainView()
		m.observeRuntimeRequestResult(err)
		if err != nil {
			return clientui.RuntimeSessionView{}, false
		}
		return view.Session, true
	}
	return m.localRuntimeSessionView(), true
}

func (m *uiModel) runtimeTranscript() clientui.TranscriptPage {
	if client := m.runtimeClient(); client != nil {
		return client.Transcript()
	}
	return m.localRuntimeTranscript()
}

func (m *uiModel) startupRuntimeTranscript() clientui.TranscriptPage {
	if client := m.runtimeClient(); client != nil {
		if _, ok := client.(*sessionRuntimeClient); ok {
			return m.refreshRuntimeTranscript()
		}
	}
	return m.runtimeTranscript()
}

func (m *uiModel) refreshRuntimeTranscript() clientui.TranscriptPage {
	if client := m.runtimeClient(); client != nil {
		page, err := client.RefreshTranscript()
		if err == nil {
			m.observeRuntimeRequestResult(nil)
			return page
		}
		m.observeRuntimeRequestResult(err)
		return m.localRuntimeTranscript()
	}
	return m.localRuntimeTranscript()
}

func (m *uiModel) localRuntimeStatus() clientui.RuntimeStatus {
	return clientui.RuntimeStatus{
		ReviewerFrequency:                 m.reviewerMode,
		ReviewerEnabled:                   m.reviewerEnabled,
		AutoCompactionEnabled:             m.autoCompactionEnabled,
		FastModeAvailable:                 m.fastModeAvailable,
		FastModeEnabled:                   m.fastModeEnabled,
		ConversationFreshness:             m.conversationFreshness,
		LastCommittedAssistantFinalAnswer: localLastCommittedAssistantFinalAnswer(m.transcriptEntries),
		ThinkingLevel:                     m.thinkingLevel,
	}
}

func localLastCommittedAssistantFinalAnswer(entries []tui.TranscriptEntry) string {
	answer := ""
	for _, entry := range entries {
		if !transcriptEntryCommittedForApp(entry) {
			break
		}
		if !transcriptEntryAffectsCommittedAssistantFinalAnswer(entry) {
			continue
		}
		if entry.Role == tui.TranscriptRoleAssistant && entry.Phase == llm.MessagePhaseFinal && strings.TrimSpace(entry.Text) != "" {
			answer = entry.Text
			continue
		}
		answer = ""
	}
	return answer
}

func transcriptEntryAffectsCommittedAssistantFinalAnswer(entry tui.TranscriptEntry) bool {
	switch entry.Role {
	case "", tui.TranscriptRoleSystem, tui.TranscriptRoleError, tui.TranscriptRoleWarning, tui.TranscriptRoleCacheWarning, tui.TranscriptRoleReviewerStatus, tui.TranscriptRoleReviewerSuggestions, tui.TranscriptRoleDeveloperFeedback:
		return false
	case tui.TranscriptRoleDeveloperErrorFeedback:
		return false
	default:
		return true
	}
}

func (m *uiModel) localRuntimeTranscript() clientui.TranscriptPage {
	committedEntries := committedTranscriptEntriesForApp(m.transcriptEntries)
	entries := make([]clientui.ChatEntry, 0, len(committedEntries))
	for _, entry := range committedEntries {
		entries = append(entries, clientui.ChatEntry{
			Visibility:        entry.Visibility,
			Role:              tui.TranscriptRoleToWire(entry.Role),
			Text:              entry.Text,
			OngoingText:       entry.OngoingText,
			Phase:             string(entry.Phase),
			MessageType:       string(entry.MessageType),
			SourcePath:        entry.SourcePath,
			CompactLabel:      entry.CompactLabel,
			ToolResultSummary: entry.ToolResultSummary,
			ToolCallID:        entry.ToolCallID,
		})
	}
	totalEntries := m.transcriptTotalEntries
	if totalEntries < m.transcriptBaseOffset+len(entries) {
		totalEntries = m.transcriptBaseOffset + len(entries)
	}
	return clientui.TranscriptPage{
		SessionID:             m.sessionID,
		SessionName:           m.sessionName,
		ConversationFreshness: m.conversationFreshness,
		Revision:              m.transcriptRevision,
		TotalEntries:          totalEntries,
		Offset:                m.transcriptBaseOffset,
		Entries:               entries,
		Ongoing:               m.view.OngoingStreamingText(),
	}
}

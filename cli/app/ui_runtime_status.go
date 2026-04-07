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
	return m.runtimeMainView().Status
}

func (m *uiModel) refreshRuntimeStatus() clientui.RuntimeStatus {
	return m.refreshRuntimeMainView().Status
}

func (m *uiModel) refreshRuntimeSessionView() clientui.RuntimeSessionView {
	return m.refreshRuntimeMainView().Session
}

func (m *uiModel) runtimeTranscript() clientui.TranscriptPage {
	if client := m.runtimeClient(); client != nil {
		return client.Transcript()
	}
	return m.localRuntimeTranscript()
}

func (m *uiModel) refreshRuntimeTranscript() clientui.TranscriptPage {
	if client := m.runtimeClient(); client != nil {
		page, err := client.RefreshTranscript()
		if err == nil {
			m.observeRuntimeRequestResult(nil)
			return page
		}
		m.observeRuntimeRequestResult(err)
		return client.Transcript()
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
		if !transcriptEntryAffectsCommittedAssistantFinalAnswer(entry) {
			continue
		}
		if strings.TrimSpace(entry.Role) == "assistant" && entry.Phase == llm.MessagePhaseFinal && strings.TrimSpace(entry.Text) != "" {
			answer = entry.Text
			continue
		}
		answer = ""
	}
	return answer
}

func transcriptEntryAffectsCommittedAssistantFinalAnswer(entry tui.TranscriptEntry) bool {
	switch strings.TrimSpace(entry.Role) {
	case "", "system", "error", "warning", "cache_warning", "reviewer_status", "reviewer_suggestions", "compaction_notice", "tool_question_error":
		return false
	default:
		return true
	}
}

func (m *uiModel) localRuntimeTranscript() clientui.TranscriptPage {
	entries := make([]clientui.ChatEntry, 0, len(m.transcriptEntries))
	for _, entry := range m.transcriptEntries {
		entries = append(entries, clientui.ChatEntry{
			Role:        entry.Role,
			Text:        entry.Text,
			OngoingText: entry.OngoingText,
			Phase:       string(entry.Phase),
			ToolCallID:  entry.ToolCallID,
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

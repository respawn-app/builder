package app

import "builder/shared/clientui"

func (m *uiModel) runtimeStatus() clientui.RuntimeStatus {
	if client := m.runtimeClient(); client != nil {
		return client.Status()
	}
	return clientui.RuntimeStatus{
		ReviewerFrequency:     m.reviewerMode,
		ReviewerEnabled:       m.reviewerEnabled,
		AutoCompactionEnabled: m.autoCompactionEnabled,
		FastModeAvailable:     m.fastModeAvailable,
		FastModeEnabled:       m.fastModeEnabled,
		ConversationFreshness: m.conversationFreshness,
		ThinkingLevel:         m.thinkingLevel,
	}
}

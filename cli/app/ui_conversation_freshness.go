package app

import "builder/shared/clientui"

func (m *uiModel) currentConversationFreshness() clientui.ConversationFreshness {
	if cached := m.cachedRuntimeStatus().ConversationFreshness; cached == clientui.ConversationFreshnessEstablished || cached == clientui.ConversationFreshnessFresh {
		m.conversationFreshness = cached
	}
	return m.conversationFreshness
}

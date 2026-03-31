package app

import "builder/shared/clientui"

func (m *uiModel) currentConversationFreshness() clientui.ConversationFreshness {
	m.conversationFreshness = m.runtimeStatus().ConversationFreshness
	return m.conversationFreshness
}

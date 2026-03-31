package app

import "builder/shared/clientui"

func (m *uiModel) currentConversationFreshness() clientui.ConversationFreshness {
	if m.engine == nil {
		return m.conversationFreshness
	}
	m.conversationFreshness = m.engine.ConversationFreshness()
	return m.conversationFreshness
}

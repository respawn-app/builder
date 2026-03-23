package app

import "builder/internal/session"

func (m *uiModel) currentConversationFreshness() session.ConversationFreshness {
	if m.engine == nil {
		return m.conversationFreshness
	}
	m.conversationFreshness = m.engine.ConversationFreshness()
	return m.conversationFreshness
}

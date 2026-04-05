package app

import (
	"builder/server/session"
	"builder/shared/clientui"
)

func mapConversationFreshness(freshness session.ConversationFreshness) clientui.ConversationFreshness {
	if freshness.IsFresh() {
		return clientui.ConversationFreshnessFresh
	}
	return clientui.ConversationFreshnessEstablished
}

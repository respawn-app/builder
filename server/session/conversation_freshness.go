package session

import "encoding/json"

type ConversationFreshness uint8

const (
	ConversationFreshnessFresh ConversationFreshness = iota
	ConversationFreshnessEstablished
)

func (f ConversationFreshness) IsFresh() bool {
	return f == ConversationFreshnessFresh
}

func conversationFreshnessFromEvents(events []Event) ConversationFreshness {
	for _, evt := range events {
		if hasVisibleUserMessageEvent(evt.Kind, evt.Payload) {
			return ConversationFreshnessEstablished
		}
	}
	return ConversationFreshnessFresh
}

func hasVisibleUserMessageEvent(kind string, payload json.RawMessage) bool {
	_, ok := visibleUserMessageFromEvent(kind, payload)
	return ok
}

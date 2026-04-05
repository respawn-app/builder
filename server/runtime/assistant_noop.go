package runtime

import (
	"strings"

	"builder/server/llm"
)

func isNoopAssistantText(text string) bool {
	return strings.TrimSpace(text) == reviewerNoopToken
}

func isNoopFinalAnswer(msg llm.Message) bool {
	return msg.Phase == llm.MessagePhaseFinal && isNoopAssistantText(msg.Content)
}

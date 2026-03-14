package runtime

import (
	"strings"

	"builder/internal/llm"
)

func isNoopFinalAnswer(msg llm.Message) bool {
	return msg.Phase == llm.MessagePhaseFinal && strings.TrimSpace(msg.Content) == reviewerNoopToken
}

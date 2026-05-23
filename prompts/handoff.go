package prompts

import (
	"strconv"
	"strings"
)

const (
	HandoffFutureAgentMessagePrefix = `The previous agent also left an additional message: `
)

func FormatHandoffFutureAgentMessage(content string) string {
	return HandoffFutureAgentMessagePrefix + strconv.Quote(strings.TrimSpace(content))
}

package session

import (
	"encoding/json"
	"strings"

	"builder/prompts"
)

const firstPromptPreviewMaxChars = 120

type persistedMessageEnvelope struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func firstPromptPreviewFromEvent(kind string, payload json.RawMessage) (string, bool) {
	msg, ok := visibleUserMessageFromEvent(kind, payload)
	if !ok {
		return "", false
	}

	preview := normalizeFirstPromptPreview(msg.Content)
	if preview == "" {
		return "", false
	}
	return preview, true
}

func visibleUserMessageFromEvent(kind string, payload json.RawMessage) (persistedMessageEnvelope, bool) {
	if strings.TrimSpace(kind) != "message" || len(payload) == 0 {
		return persistedMessageEnvelope{}, false
	}

	var msg persistedMessageEnvelope
	if err := json.Unmarshal(payload, &msg); err != nil {
		return persistedMessageEnvelope{}, false
	}
	if !isVisibleUserMessage(msg) {
		return persistedMessageEnvelope{}, false
	}
	return msg, true
}

func normalizeFirstPromptPreview(content string) string {
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if trimmed == "" {
			continue
		}
		return truncatePromptPreview(trimmed, firstPromptPreviewMaxChars)
	}
	return ""
}

func truncatePromptPreview(text string, maxChars int) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" || maxChars <= 0 {
		return trimmed
	}
	runes := []rune(trimmed)
	if len(runes) <= maxChars {
		return trimmed
	}
	if maxChars == 1 {
		return "…"
	}
	return string(runes[:maxChars-1]) + "…"
}

func isVisibleUserMessage(msg persistedMessageEnvelope) bool {
	if strings.TrimSpace(msg.Role) != "user" {
		return false
	}
	content := strings.TrimSpace(msg.Content)
	if content == "" {
		return false
	}
	if strings.HasPrefix(content, prompts.CompactionSummaryPrefix+"\n") {
		return false
	}
	return true
}

package session

import (
	"encoding/json"
	"strings"
)

const promptHistoryEventKind = "prompt_history"

type promptHistoryEnvelope struct {
	Text string `json:"text"`
}

func normalizePromptHistoryText(text string) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}
	return text
}

func promptHistoryFromEvents(events []Event) []string {
	out := make([]string, 0)
	sawPromptHistory := false
	for _, evt := range events {
		if strings.TrimSpace(evt.Kind) != promptHistoryEventKind || len(evt.Payload) == 0 {
			continue
		}
		sawPromptHistory = true
		var entry promptHistoryEnvelope
		if err := json.Unmarshal(evt.Payload, &entry); err != nil {
			continue
		}
		if text := normalizePromptHistoryText(entry.Text); text != "" {
			out = append(out, text)
		}
	}
	if sawPromptHistory {
		return out
	}

	for _, evt := range events {
		if strings.TrimSpace(evt.Kind) != "message" || len(evt.Payload) == 0 {
			continue
		}
		var msg persistedMessageEnvelope
		if err := json.Unmarshal(evt.Payload, &msg); err != nil {
			continue
		}
		if !isVisibleUserMessage(msg) {
			continue
		}
		if text := normalizePromptHistoryText(msg.Content); text != "" {
			out = append(out, text)
		}
	}
	return out
}

func (s *Store) ReadPromptHistory() ([]string, error) {
	events, err := s.ReadEvents()
	if err != nil {
		return nil, err
	}
	return promptHistoryFromEvents(events), nil
}

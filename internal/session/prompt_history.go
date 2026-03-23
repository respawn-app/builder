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
	firstPromptHistorySeq := int64(0)
	for _, evt := range events {
		if strings.TrimSpace(evt.Kind) != promptHistoryEventKind || len(evt.Payload) == 0 {
			continue
		}
		firstPromptHistorySeq = evt.Seq
		break
	}

	legacy := make([]string, 0)
	for _, evt := range events {
		if firstPromptHistorySeq > 0 && evt.Seq >= firstPromptHistorySeq {
			break
		}
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
			legacy = append(legacy, text)
		}
	}

	out := append([]string(nil), legacy...)
	for _, evt := range events {
		if strings.TrimSpace(evt.Kind) != promptHistoryEventKind || len(evt.Payload) == 0 {
			continue
		}
		var entry promptHistoryEnvelope
		if err := json.Unmarshal(evt.Payload, &entry); err != nil {
			continue
		}
		if text := normalizePromptHistoryText(entry.Text); text != "" {
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

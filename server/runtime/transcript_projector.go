package runtime

import (
	"encoding/json"
	"fmt"
	"strings"

	"builder/server/llm"
	"builder/server/session"
)

// TranscriptProjector reconstructs transcript-visible state from persisted events
// without needing a full runtime instance.
type TranscriptProjector struct {
	chat *chatStore
}

func NewTranscriptProjector() *TranscriptProjector {
	return &TranscriptProjector{chat: newChatStore()}
}

func (p *TranscriptProjector) ApplyPersistedEvent(evt session.Event) error {
	if p == nil || p.chat == nil {
		return nil
	}
	switch strings.TrimSpace(evt.Kind) {
	case "message":
		var msg llm.Message
		if err := json.Unmarshal(evt.Payload, &msg); err != nil {
			return fmt.Errorf("decode message event: %w", err)
		}
		p.chat.appendMessage(msg)
	case "tool_completed":
		if err := p.chat.restoreToolCompletionPayload(evt.Payload); err != nil {
			return err
		}
	case "local_entry":
		var entry storedLocalEntry
		if err := json.Unmarshal(evt.Payload, &entry); err != nil {
			return fmt.Errorf("decode local_entry event: %w", err)
		}
		p.chat.appendLocalEntryWithOngoingText(entry.Role, entry.Text, entry.OngoingText)
	case "history_replaced":
		var payload historyReplacementPayload
		if err := json.Unmarshal(evt.Payload, &payload); err != nil {
			return fmt.Errorf("decode history_replaced event: %w", err)
		}
		if strings.TrimSpace(payload.Engine) == "reviewer_rollback" {
			p.chat.restoreHistoryItems(payload.Items)
		} else {
			p.chat.replaceHistory(payload.Items)
		}
	}
	return nil
}

func (p *TranscriptProjector) ChatSnapshot() ChatSnapshot {
	if p == nil || p.chat == nil {
		return ChatSnapshot{}
	}
	return p.chat.snapshot()
}

func (p *TranscriptProjector) OngoingTailSnapshot(maxEntries int) TranscriptWindowSnapshot {
	if p == nil || p.chat == nil {
		return TranscriptWindowSnapshot{}
	}
	return p.chat.ongoingTailSnapshot(maxEntries)
}

func (p *TranscriptProjector) LastCommittedAssistantFinalAnswer() string {
	if p == nil || p.chat == nil {
		return ""
	}
	messages := p.chat.snapshotMessages()
	for idx := len(messages) - 1; idx >= 0; idx-- {
		message := messages[idx]
		if shouldSkipTrailingAssistantHandoffMessage(message) {
			continue
		}
		if message.Role != llm.RoleAssistant {
			return ""
		}
		if message.Phase != llm.MessagePhaseFinal {
			return ""
		}
		if strings.TrimSpace(message.Content) == "" {
			return ""
		}
		return message.Content
	}
	return ""
}

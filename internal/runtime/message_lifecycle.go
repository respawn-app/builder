package runtime

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"builder/internal/llm"
)

type defaultMessageLifecycle struct {
	engine     *Engine
	background backgroundNoticeScheduler
}

func (m *defaultMessageLifecycle) RestoreMessages() error {
	e := m.engine
	events, err := e.store.ReadEvents()
	if err != nil {
		return err
	}
	for _, evt := range events {
		switch evt.Kind {
		case "message":
			var msg llm.Message
			if err := json.Unmarshal(evt.Payload, &msg); err != nil {
				return fmt.Errorf("decode message event: %w", err)
			}
			e.chat.appendMessage(msg)
		case "tool_completed":
			if err := e.chat.restoreToolCompletionPayload(evt.Payload); err != nil {
				return err
			}
		case "local_entry":
			var entry storedLocalEntry
			if err := json.Unmarshal(evt.Payload, &entry); err != nil {
				return fmt.Errorf("decode local_entry event: %w", err)
			}
			e.chat.appendLocalEntryWithOngoingText(entry.Role, entry.Text, entry.OngoingText)
		case "history_replaced":
			var payload historyReplacementPayload
			if err := json.Unmarshal(evt.Payload, &payload); err != nil {
				return fmt.Errorf("decode history_replaced event: %w", err)
			}
			if strings.TrimSpace(payload.Engine) == "reviewer_rollback" {
				e.chat.restoreHistoryItems(payload.Items)
			} else {
				e.chat.replaceHistory(payload.Items)
				e.compactionCount++
			}
		}
	}
	return nil
}
func normalizeQueuedUserMessages(messages []string) []string {
	out := make([]string, 0, len(messages))
	for _, message := range messages {
		trimmed := strings.TrimSpace(message)
		if trimmed == "" {
			continue
		}
		out = append(out, trimmed)
	}
	return out
}

func (m *defaultMessageLifecycle) FlushPendingUserInjections(stepID string) (int, error) {
	e := m.engine
	e.mu.Lock()
	pending := append([]string(nil), e.pendingInjected...)
	e.pendingInjected = nil
	e.mu.Unlock()
	flushed := 0
	pendingNotices := []llm.Message(nil)
	if m.background != nil {
		pendingNotices = m.background.DrainPendingNotices()
	}

	queuedMessages := normalizeQueuedUserMessages(pending)
	if len(queuedMessages) > 0 {
		joined := strings.Join(queuedMessages, "\n\n")
		if err := e.appendUserMessageWithoutConversationUpdate(stepID, joined); err != nil {
			return flushed, err
		}
		flushed++
		e.emit(Event{Kind: EventUserMessageFlushed, StepID: stepID, UserMessage: joined, UserMessageBatch: queuedMessages})
		e.emit(Event{Kind: EventConversationUpdated, StepID: stepID})
	}
	for _, notice := range pendingNotices {
		if err := e.appendMessage(stepID, notice); err != nil {
			return flushed, err
		}
		flushed++
	}
	return flushed, nil
}

func (m *defaultMessageLifecycle) InjectAgentsIfNeeded(stepID string) error {
	e := m.engine
	meta := e.store.Meta()
	if meta.AgentsInjected {
		return nil
	}
	builder := newMetaContextBuilder(meta.WorkspaceRoot, e.cfg.Model, e.ThinkingLevel(), e.cfg.DisabledSkills, time.Now())
	metaResult, err := builder.Build(metaContextBuildOptions{
		IncludeAgents:        true,
		IncludeSkills:        true,
		IncludeEnvironment:   true,
		IncludeSkillWarnings: true,
	})
	if err != nil {
		return err
	}
	for _, message := range metaResult.OrderedInjectionMessages() {
		if err := e.appendMessage(stepID, message); err != nil {
			return err
		}
	}

	return e.store.MarkAgentsInjected()
}

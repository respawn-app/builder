package runtime

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"builder/internal/llm"
)

type defaultMessageLifecycle struct {
	engine *Engine
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
			entry = normalizeRestoredLocalEntry(entry)
			e.chat.appendLocalEntryWithOngoingText(entry.Role, entry.Text, entry.OngoingText)
		case "history_replaced":
			var payload historyReplacementPayload
			if err := json.Unmarshal(evt.Payload, &payload); err != nil {
				return fmt.Errorf("decode history_replaced event: %w", err)
			}
			if strings.TrimSpace(payload.Engine) == "reviewer_rollback" {
				e.chat.restoreMessagesFromItems(payload.Items)
			} else {
				e.chat.replaceHistory(payload.Items)
				e.compactionCount++
			}
		}
	}
	return nil
}

func normalizeRestoredLocalEntry(entry storedLocalEntry) storedLocalEntry {
	role := strings.TrimSpace(entry.Role)
	if role == "reviewer_suggestions" {
		entry.OngoingText = strings.TrimSpace(entry.Text)
		return entry
	}
	if role != "reviewer_status" {
		return entry
	}
	entry.Text = normalizeLegacyReviewerStatusText(entry.Text)
	if strings.TrimSpace(entry.OngoingText) != "" {
		entry.OngoingText = normalizeLegacyReviewerStatusText(entry.OngoingText)
	}
	return entry
}

func normalizeLegacyReviewerStatusText(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}
	firstLine := trimmed
	if head, _, ok := strings.Cut(trimmed, "\n"); ok {
		firstLine = strings.TrimSpace(head)
	}
	if normalized, ok := normalizeLegacyReviewerStatusHeader(firstLine); ok {
		if cacheLine := extractReviewerCacheHitLine(trimmed); cacheLine != "" {
			return normalized + "\n\n" + cacheLine
		}
		return normalized
	}
	return text
}

func normalizeLegacyReviewerStatusHeader(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	if normalized, ok := normalizeLegacyReviewerStatusAppliedHeader(trimmed, "Supervisor ran, applied "); ok {
		return normalized, true
	}
	if normalized, ok := normalizeLegacyReviewerStatusAppliedHeader(trimmed, "Supervisor ran: applied "); ok {
		return normalized, true
	}
	if normalized, ok := normalizeLegacyReviewerStatusIgnoredHeader(trimmed, "Supervisor ran, ignored "); ok {
		return normalized, true
	}
	if normalized, ok := normalizeLegacyReviewerStatusIgnoredHeader(trimmed, "Supervisor ran: ignored "); ok {
		return normalized, true
	}
	if normalized, ok := normalizeLegacyReviewerStatusFollowUpFailedHeader(trimmed, "Supervisor ran, follow-up failed after "); ok {
		return normalized, true
	}
	if normalized, ok := normalizeLegacyReviewerStatusFollowUpFailedHeader(trimmed, "Supervisor ran: follow-up failed after "); ok {
		return normalized, true
	}
	return "", false
}

func normalizeLegacyReviewerStatusAppliedHeader(line, prefix string) (string, bool) {
	rest, ok := strings.CutPrefix(line, prefix)
	if !ok {
		return "", false
	}
	label := strings.TrimSpace(strings.TrimSuffix(rest, ":"))
	if label == "" || label == rest {
		return "", false
	}
	return fmt.Sprintf("Supervisor ran: %s, applied.", label), true
}

func normalizeLegacyReviewerStatusIgnoredHeader(line, prefix string) (string, bool) {
	rest, ok := strings.CutPrefix(line, prefix)
	if !ok {
		return "", false
	}
	label := strings.TrimSpace(strings.TrimSuffix(rest, ":"))
	if label == "" || label == rest {
		return "", false
	}
	return fmt.Sprintf("Supervisor ran: %s, no changes applied.", label), true
}

func normalizeLegacyReviewerStatusFollowUpFailedHeader(line, prefix string) (string, bool) {
	rest, ok := strings.CutPrefix(line, prefix)
	if !ok {
		return "", false
	}
	label, tail, haveTail := strings.Cut(rest, ":")
	label = strings.TrimSpace(label)
	if label == "" {
		return "", false
	}
	errorText := ""
	if haveTail {
		errorText = strings.TrimSpace(tail)
	}
	if errorText != "" {
		return fmt.Sprintf("Supervisor ran: %s, but follow-up failed: %s", label, errorText), true
	}
	return fmt.Sprintf("Supervisor ran: %s, but follow-up failed.", label), true
}

func extractReviewerCacheHitLine(text string) string {
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasSuffix(trimmed, "cache hit") && strings.Contains(trimmed, "%") {
			return trimmed
		}
	}
	return ""
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
	pendingNotices := append([]llm.Message(nil), e.pendingNotices...)
	e.pendingNotices = nil
	e.noticeScheduled = false
	e.mu.Unlock()
	flushed := 0

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
	paths, err := agentsInjectionPaths(meta.WorkspaceRoot)
	if err != nil {
		return err
	}

	for _, path := range paths {
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			if errors.Is(readErr, os.ErrNotExist) {
				continue
			}
			return fmt.Errorf("read AGENTS.md: %w", readErr)
		}
		injected := fmt.Sprintf("%s\nsource: %s\n\n```%s\n%s\n```", agentsInjectedHeader, path, agentsInjectedFenceLabel, string(data))
		if err := e.appendMessage(stepID, llm.Message{Role: llm.RoleDeveloper, MessageType: llm.MessageTypeAgentsMD, Content: injected}); err != nil {
			return err
		}
	}
	skills, issues, err := discoverInjectedSkills(meta.WorkspaceRoot, normalizedDisabledSkills(e.cfg.DisabledSkills))
	if err != nil {
		return err
	}
	for _, issue := range issues {
		if err := e.appendMessage(stepID, llm.Message{Role: llm.RoleDeveloper, MessageType: llm.MessageTypeErrorFeedback, Content: formatSkillDiscoveryWarning(issue)}); err != nil {
			return err
		}
	}
	if len(skills) > 0 {
		if err := e.appendMessage(stepID, llm.Message{Role: llm.RoleDeveloper, MessageType: llm.MessageTypeSkills, Content: renderSkillsContext(skills)}); err != nil {
			return err
		}
	}
	environment := environmentContextMessage(meta.WorkspaceRoot, e.cfg.Model, e.ThinkingLevel(), time.Now())
	if err := e.appendMessage(stepID, llm.Message{Role: llm.RoleDeveloper, MessageType: llm.MessageTypeEnvironment, Content: environment}); err != nil {
		return err
	}

	return e.store.MarkAgentsInjected()
}

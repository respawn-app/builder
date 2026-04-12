package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

var errForkReplayBoundary = errors.New("fork replay boundary reached")

func ForkAtUserMessage(parent *Store, userMessageIndex int, forkName string) (*Store, error) {
	if parent == nil {
		return nil, fmt.Errorf("parent store is required")
	}
	if userMessageIndex <= 0 {
		return nil, fmt.Errorf("user message index must be >= 1")
	}

	parentMeta := parent.Meta()
	replay := make([]ReplayEvent, 0)
	visibleUserCount := 0
	err := parent.WalkEvents(func(evt Event) error {
		if hasVisibleUserMessageEvent(evt.Kind, evt.Payload) {
			visibleUserCount++
			if visibleUserCount == userMessageIndex {
				return errForkReplayBoundary
			}
		}
		replay = append(replay, ReplayEvent{StepID: evt.StepID, Kind: evt.Kind, Payload: append([]byte(nil), evt.Payload...)})
		return nil
	})
	if err != nil && !errors.Is(err, errForkReplayBoundary) {
		return nil, fmt.Errorf("read parent events: %w", err)
	}

	if visibleUserCount < userMessageIndex {
		return nil, fmt.Errorf("user message index %d is out of range", userMessageIndex)
	}

	containerDir := filepath.Dir(parent.Dir())
	child, err := newLazyWithStoreOptions(containerDir, parentMeta.WorkspaceContainer, parentMeta.WorkspaceRoot, parent.options)
	if err != nil {
		return nil, err
	}

	child.mu.Lock()
	child.meta.Locked = cloneLockedContract(parentMeta.Locked)
	child.meta.AgentsInjected = parentMeta.AgentsInjected
	child.meta.CompactionSoonReminderIssued = reminderIssuedFromReplayEvents(replay)
	child.meta.UsageState = nil
	child.meta.ParentSessionID = parentMeta.SessionID
	child.meta.Name = strings.TrimSpace(forkName)
	child.meta.Continuation = cloneContinuationContext(parentMeta.Continuation)
	child.mu.Unlock()

	if len(replay) == 0 {
		if err := child.EnsureDurable(); err != nil {
			return nil, fmt.Errorf("persist empty fork replay: %w", err)
		}
		return child, nil
	}
	if _, err := child.AppendReplayEvents(replay); err != nil {
		return nil, fmt.Errorf("append fork replay events: %w", err)
	}
	return child, nil
}

func cloneLockedContract(in *LockedContract) *LockedContract {
	if in == nil {
		return nil
	}
	copyLocked := *in
	if len(in.EnabledTools) > 0 {
		copyLocked.EnabledTools = append([]string(nil), in.EnabledTools...)
	}
	return &copyLocked
}

func cloneContinuationContext(in *ContinuationContext) *ContinuationContext {
	if in == nil {
		return nil
	}
	copyContext := *in
	return &copyContext
}

func reminderIssuedFromReplayEvents(events []ReplayEvent) bool {
	issued := false
	for _, evt := range events {
		switch evt.Kind {
		case "message":
			var msg reminderEventMessage
			if err := json.Unmarshal(evt.Payload, &msg); err != nil {
				continue
			}
			if isCompactionSoonReminderMessage(msg) {
				issued = true
			}
		case "history_replaced":
			var payload struct {
				Engine string            `json:"engine"`
				Items  []json.RawMessage `json:"items"`
			}
			if err := json.Unmarshal(evt.Payload, &payload); err != nil {
				continue
			}
			if strings.TrimSpace(payload.Engine) == "reviewer_rollback" {
				issued = itemsContainCompactionSoonReminder(payload.Items)
				continue
			}
			issued = false
		}
	}
	return issued
}

type reminderMessageLike interface {
	GetRole() string
	GetMessageType() string
	GetContent() string
}

func isCompactionSoonReminderMessage(msg reminderMessageLike) bool {
	return strings.TrimSpace(msg.GetRole()) == "developer" && strings.TrimSpace(msg.GetMessageType()) == "compaction_soon_reminder" && strings.TrimSpace(msg.GetContent()) != ""
}

type reminderEventMessage struct {
	Role        string `json:"role"`
	MessageType string `json:"message_type"`
	Content     string `json:"content"`
}

func (m reminderEventMessage) GetRole() string        { return m.Role }
func (m reminderEventMessage) GetMessageType() string { return m.MessageType }
func (m reminderEventMessage) GetContent() string     { return m.Content }

func itemsContainCompactionSoonReminder(items []json.RawMessage) bool {
	for _, raw := range items {
		var item struct {
			Type        string `json:"type"`
			Role        string `json:"role"`
			MessageType string `json:"message_type"`
			Content     string `json:"content"`
		}
		if err := json.Unmarshal(raw, &item); err != nil {
			continue
		}
		if strings.TrimSpace(item.Type) != "message" {
			continue
		}
		msg := reminderEventMessage{Role: item.Role, MessageType: item.MessageType, Content: item.Content}
		if isCompactionSoonReminderMessage(msg) {
			return true
		}
	}
	return false
}

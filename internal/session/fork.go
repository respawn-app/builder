package session

import (
	"builder/prompts"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

type persistedMessageEnvelope struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func ForkAtUserMessage(parent *Store, userMessageIndex int, forkName string) (*Store, error) {
	if parent == nil {
		return nil, fmt.Errorf("parent store is required")
	}
	if userMessageIndex <= 0 {
		return nil, fmt.Errorf("user message index must be >= 1")
	}

	parentMeta := parent.Meta()
	events, err := parent.ReadEvents()
	if err != nil {
		return nil, fmt.Errorf("read parent events: %w", err)
	}

	replay := make([]ReplayEvent, 0, len(events))
	visibleUserCount := 0
	for _, evt := range events {
		if evt.Kind == "message" {
			var msg persistedMessageEnvelope
			if err := json.Unmarshal(evt.Payload, &msg); err != nil {
				return nil, fmt.Errorf("decode message event: %w", err)
			}
			if isVisibleUserMessage(msg) {
				visibleUserCount++
				if visibleUserCount == userMessageIndex {
					break
				}
			}
		}
		replay = append(replay, ReplayEvent{StepID: evt.StepID, Kind: evt.Kind, Payload: evt.Payload})
	}

	if visibleUserCount < userMessageIndex {
		return nil, fmt.Errorf("user message index %d is out of range", userMessageIndex)
	}

	containerDir := filepath.Dir(parent.Dir())
	child, err := NewLazy(containerDir, parentMeta.WorkspaceContainer, parentMeta.WorkspaceRoot)
	if err != nil {
		return nil, err
	}

	child.mu.Lock()
	child.meta.Locked = cloneLockedContract(parentMeta.Locked)
	child.meta.AgentsInjected = parentMeta.AgentsInjected
	child.meta.ParentSessionID = parentMeta.SessionID
	child.meta.Name = strings.TrimSpace(forkName)
	child.meta.Continuation = cloneContinuationContext(parentMeta.Continuation)
	child.mu.Unlock()

	if _, err := child.AppendReplayEvents(replay); err != nil {
		return nil, fmt.Errorf("append fork replay events: %w", err)
	}
	return child, nil
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

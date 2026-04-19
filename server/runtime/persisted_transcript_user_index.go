package runtime

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"builder/server/llm"
	"builder/server/session"
)

type persistedTranscriptLocalOrdinalEntry struct {
	afterMessageCount int
	role              string
}

type persistedTranscriptUserIndexResolver struct {
	targetIndex int

	transcriptEntryCount int
	userMessageCount     int
	messageCount         int

	resolved          bool
	resolvedIsUser    bool
	resolvedUserIndex int

	toolCompletions        map[string]struct{}
	assistantToolCalls     map[string]struct{}
	synthesizedToolResults map[string]struct{}
	materializedToolCalls  map[string]struct{}
}

var errPersistedTranscriptTargetResolved = errors.New("persisted transcript target resolved")

func newPersistedTranscriptUserIndexResolver(targetIndex int) *persistedTranscriptUserIndexResolver {
	return &persistedTranscriptUserIndexResolver{
		targetIndex:            targetIndex,
		toolCompletions:        map[string]struct{}{},
		assistantToolCalls:     map[string]struct{}{},
		synthesizedToolResults: map[string]struct{}{},
		materializedToolCalls:  map[string]struct{}{},
	}
}

func ResolvePersistedUserMessageIndex(walk func(func(session.Event) error) error, transcriptEntryIndex int) (int, error) {
	resolver := newPersistedTranscriptUserIndexResolver(transcriptEntryIndex)
	if walk == nil {
		return 0, fmt.Errorf("rollback fork transcript entry index %d is out of range", transcriptEntryIndex)
	}
	if err := walk(func(evt session.Event) error {
		return resolver.ApplyPersistedEvent(evt)
	}); err != nil {
		if errors.Is(err, errPersistedTranscriptTargetResolved) {
			return resolver.Result()
		}
		return 0, err
	}
	return resolver.Result()
}

func (r *persistedTranscriptUserIndexResolver) ApplyPersistedEvent(evt session.Event) error {
	if r == nil {
		return nil
	}
	switch strings.TrimSpace(evt.Kind) {
	case "message":
		var msg llm.Message
		if err := json.Unmarshal(evt.Payload, &msg); err != nil {
			return fmt.Errorf("decode message event: %w", err)
		}
		return r.applyMessage(msg)
	case "tool_completed":
		var completion storedToolCompletion
		if err := json.Unmarshal(evt.Payload, &completion); err != nil {
			return fmt.Errorf("decode tool_completed event: %w", err)
		}
		callID := strings.TrimSpace(completion.CallID)
		if callID == "" {
			return nil
		}
		r.toolCompletions[callID] = struct{}{}
		if _, ok := r.assistantToolCalls[callID]; !ok {
			return nil
		}
		if _, ok := r.materializedToolCalls[callID]; ok {
			return nil
		}
		if _, ok := r.synthesizedToolResults[callID]; ok {
			return nil
		}
		r.synthesizedToolResults[callID] = struct{}{}
		if r.appendVisibleRole("tool_result_ok") {
			return errPersistedTranscriptTargetResolved
		}
		return nil
	case "local_entry":
		var entry storedLocalEntry
		if err := json.Unmarshal(evt.Payload, &entry); err != nil {
			return fmt.Errorf("decode local_entry event: %w", err)
		}
		chatEntry := localEntryChatEntry(entry)
		if chatEntry == nil {
			return nil
		}
		return r.appendVisibleEntries(*chatEntry)
	case sessionEventCacheWarning:
		return r.appendVisibleEntries(ChatEntry{Role: cacheWarningTranscriptRole})
	case "history_replaced":
		payload, ignoredLegacy, err := decodePersistedHistoryReplacementPayload(evt.Payload)
		if err != nil {
			return fmt.Errorf("decode history_replaced event: %w", err)
		}
		if ignoredLegacy {
			return nil
		}
		return r.appendVisibleEntries(visibleChatEntriesFromResponseItems(payload.Items)...)
	default:
		return nil
	}
}

func (r *persistedTranscriptUserIndexResolver) Result() (int, error) {
	if r == nil {
		return 0, fmt.Errorf("rollback fork transcript entry index %d is out of range", 0)
	}
	if r.targetIndex < 0 {
		return 0, fmt.Errorf("rollback fork transcript entry index %d is out of range", r.targetIndex)
	}
	if r.transcriptEntryCount <= r.targetIndex || !r.resolved {
		return 0, fmt.Errorf("rollback fork transcript entry index %d is out of range", r.targetIndex)
	}
	if !r.resolvedIsUser {
		return 0, fmt.Errorf("rollback fork transcript entry %d is not a user message", r.targetIndex)
	}
	return r.resolvedUserIndex, nil
}

func (r *persistedTranscriptUserIndexResolver) applyMessage(msg llm.Message) error {
	r.messageCount++
	switch msg.Role {
	case llm.RoleUser:
		return r.appendVisibleEntries(VisibleChatEntriesFromMessage(msg)...)
	case llm.RoleAssistant:
		entries := VisibleChatEntriesFromMessage(msg)
		assistantEntryCount := 0
		for _, entry := range entries {
			if strings.TrimSpace(entry.Role) == "tool_call" {
				break
			}
			assistantEntryCount++
		}
		if err := r.appendVisibleEntries(entries[:assistantEntryCount]...); err != nil {
			return err
		}
		for idx, call := range msg.ToolCalls {
			if assistantEntryCount+idx < len(entries) {
				if err := r.appendVisibleEntries(entries[assistantEntryCount+idx]); err != nil {
					return err
				}
			}
			callID := strings.TrimSpace(call.ID)
			if callID == "" {
				continue
			}
			r.assistantToolCalls[callID] = struct{}{}
			if _, ok := r.materializedToolCalls[callID]; ok {
				continue
			}
			if _, ok := r.synthesizedToolResults[callID]; ok {
				continue
			}
			if _, ok := r.toolCompletions[callID]; ok {
				r.synthesizedToolResults[callID] = struct{}{}
				if err := r.appendVisibleEntries(ChatEntry{Role: "tool_result_ok"}); err != nil {
					return err
				}
			}
		}
		return nil
	case llm.RoleTool:
		callID := strings.TrimSpace(msg.ToolCallID)
		if callID != "" {
			r.materializedToolCalls[callID] = struct{}{}
			if _, ok := r.synthesizedToolResults[callID]; ok {
				delete(r.synthesizedToolResults, callID)
				return nil
			}
		}
		return r.appendVisibleEntries(VisibleChatEntriesFromMessage(msg)...)
	case llm.RoleDeveloper:
		return r.appendVisibleEntries(VisibleChatEntriesFromMessage(msg)...)
	}
	return nil
}

func (r *persistedTranscriptUserIndexResolver) appendVisibleEntries(entries ...ChatEntry) error {
	for _, entry := range entries {
		if r.appendVisibleRole(entry.Role) {
			return errPersistedTranscriptTargetResolved
		}
	}
	return nil
}

func (r *persistedTranscriptUserIndexResolver) appendVisibleRole(role string) bool {
	role = strings.TrimSpace(role)
	if role == "" {
		return false
	}
	reachedTarget := r.transcriptEntryCount == r.targetIndex
	if r.transcriptEntryCount == r.targetIndex {
		r.resolved = true
		r.resolvedIsUser = role == "user"
		r.resolvedUserIndex = r.userMessageCount
		if r.resolvedIsUser {
			r.resolvedUserIndex++
		}
	}
	if role == "user" {
		r.userMessageCount++
	}
	r.transcriptEntryCount++
	return reachedTarget
}

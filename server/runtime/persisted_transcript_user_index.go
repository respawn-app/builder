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
	localEntries           []persistedTranscriptLocalOrdinalEntry
}

var errPersistedTranscriptTargetResolved = errors.New("persisted transcript target resolved")

func newPersistedTranscriptUserIndexResolver(targetIndex int) *persistedTranscriptUserIndexResolver {
	return &persistedTranscriptUserIndexResolver{
		targetIndex:            targetIndex,
		toolCompletions:        map[string]struct{}{},
		assistantToolCalls:     map[string]struct{}{},
		synthesizedToolResults: map[string]struct{}{},
		materializedToolCalls:  map[string]struct{}{},
		localEntries:           nil,
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
		r.localEntries = append(r.localEntries, persistedTranscriptLocalOrdinalEntry{afterMessageCount: r.messageCount, role: strings.TrimSpace(chatEntry.Role)})
		if r.appendVisibleRole(chatEntry.Role) {
			return errPersistedTranscriptTargetResolved
		}
		return nil
	case sessionEventCacheWarning:
		r.localEntries = append(r.localEntries, persistedTranscriptLocalOrdinalEntry{afterMessageCount: r.messageCount, role: cacheWarningTranscriptRole})
		if r.appendVisibleRole(cacheWarningTranscriptRole) {
			return errPersistedTranscriptTargetResolved
		}
		return nil
	case "history_replaced":
		var payload historyReplacementPayload
		if err := json.Unmarshal(evt.Payload, &payload); err != nil {
			return fmt.Errorf("decode history_replaced event: %w", err)
		}
		if strings.TrimSpace(payload.Engine) != "reviewer_rollback" {
			return nil
		}
		return r.rebuildAfterReviewerRollback(payload.Items)
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
		if entry, ok := visibleUserTranscriptEntry(msg); ok {
			if r.appendVisibleRole(entry.Role) {
				return errPersistedTranscriptTargetResolved
			}
		}
	case llm.RoleAssistant:
		if strings.TrimSpace(msg.Content) != "" {
			if r.appendVisibleRole("assistant") {
				return errPersistedTranscriptTargetResolved
			}
		}
		for _, call := range msg.ToolCalls {
			callID := strings.TrimSpace(call.ID)
			if r.appendVisibleRole("tool_call") {
				return errPersistedTranscriptTargetResolved
			}
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
				if r.appendVisibleRole("tool_result_ok") {
					return errPersistedTranscriptTargetResolved
				}
			}
		}
	case llm.RoleTool:
		callID := strings.TrimSpace(msg.ToolCallID)
		if callID != "" {
			r.materializedToolCalls[callID] = struct{}{}
			if _, ok := r.synthesizedToolResults[callID]; ok {
				delete(r.synthesizedToolResults, callID)
				return nil
			}
		}
		if r.appendVisibleRole("tool_result_ok") {
			return errPersistedTranscriptTargetResolved
		}
	case llm.RoleDeveloper:
		if entry, ok := visibleDeveloperChatEntry(msg); ok {
			if r.appendVisibleRole(entry.Role) {
				return errPersistedTranscriptTargetResolved
			}
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

func (r *persistedTranscriptUserIndexResolver) rebuildAfterReviewerRollback(items []llm.ResponseItem) error {
	r.transcriptEntryCount = 0
	r.userMessageCount = 0
	r.messageCount = 0
	r.resolved = false
	r.resolvedIsUser = false
	r.resolvedUserIndex = 0
	r.assistantToolCalls = map[string]struct{}{}
	r.synthesizedToolResults = map[string]struct{}{}
	r.materializedToolCalls = map[string]struct{}{}

	localIndex := 0
	appendLocalEntries := func(messageCount int) {
		for localIndex < len(r.localEntries) {
			entry := r.localEntries[localIndex]
			if entry.afterMessageCount > messageCount {
				break
			}
			r.appendVisibleRole(entry.role)
			localIndex++
		}
	}
	appendLocalEntries(0)
	walker := newResponseItemMessageWalker(func(msg llm.Message) {
		_ = r.applyMessage(msg)
		appendLocalEntries(r.messageCount)
	})
	for _, item := range items {
		walker.Apply(item)
	}
	walker.Flush()
	appendLocalEntries(r.messageCount)
	return nil
}

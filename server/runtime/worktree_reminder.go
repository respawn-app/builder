package runtime

import (
	"strings"

	"builder/server/llm"
	"builder/server/session"
)

func (e *Engine) materializePendingWorktreeReminder(stepID string) error {
	state := cloneRuntimeWorktreeReminderState(e.store.Meta().WorktreeReminder)
	if !shouldInjectWorktreeReminder(state, e.compactionCountSnapshot()) {
		return nil
	}
	message, ok := worktreeReminderMessage(*state)
	if !ok {
		return nil
	}
	if latestMaterializedWorktreeReminderMatches(e.snapshotItems(), message) || latestMaterializedWorktreeReminderEntryMatches(e.ChatSnapshot().Entries, message) {
		state.HasIssuedInGeneration = true
		state.IssuedCompactionCount = e.compactionCountSnapshot()
		return e.store.SetWorktreeReminderState(state)
	}
	if err := e.appendMessage(stepID, message); err != nil {
		return err
	}
	state.HasIssuedInGeneration = true
	state.IssuedCompactionCount = e.compactionCountSnapshot()
	return e.store.SetWorktreeReminderState(state)
}

func filterHistoricalWorktreeReminderItems(items []llm.ResponseItem) []llm.ResponseItem {
	if len(items) == 0 {
		return nil
	}
	latestReminder := -1
	for idx, item := range items {
		if isWorktreeReminderResponseItem(item) {
			latestReminder = idx
		}
	}
	filtered := make([]llm.ResponseItem, 0, len(items))
	for idx, item := range items {
		if isWorktreeReminderResponseItem(item) && idx != latestReminder {
			continue
		}
		filtered = append(filtered, item)
	}
	return filtered
}

func isWorktreeReminderResponseItem(item llm.ResponseItem) bool {
	if item.Type != llm.ResponseItemTypeMessage {
		return false
	}
	return item.MessageType == llm.MessageTypeWorktreeMode || item.MessageType == llm.MessageTypeWorktreeModeExit
}

func latestMaterializedWorktreeReminderMatches(items []llm.ResponseItem, message llm.Message) bool {
	for idx := len(items) - 1; idx >= 0; idx-- {
		item := items[idx]
		if !isWorktreeReminderResponseItem(item) {
			continue
		}
		return item.Role == message.Role &&
			item.MessageType == message.MessageType &&
			strings.TrimSpace(item.Content) == strings.TrimSpace(message.Content) &&
			strings.TrimSpace(item.CompactContent) == strings.TrimSpace(message.CompactContent) &&
			strings.TrimSpace(item.SourcePath) == strings.TrimSpace(message.SourcePath)
	}
	return false
}

func latestMaterializedWorktreeReminderEntryMatches(entries []ChatEntry, message llm.Message) bool {
	for idx := len(entries) - 1; idx >= 0; idx-- {
		entry := entries[idx]
		if entry.MessageType != llm.MessageTypeWorktreeMode && entry.MessageType != llm.MessageTypeWorktreeModeExit {
			continue
		}
		return entry.MessageType == message.MessageType &&
			strings.TrimSpace(entry.Text) == strings.TrimSpace(message.Content) &&
			strings.TrimSpace(entry.OngoingText) == strings.TrimSpace(message.CompactContent) &&
			strings.TrimSpace(entry.SourcePath) == strings.TrimSpace(message.SourcePath)
	}
	return false
}

func shouldInjectWorktreeReminder(state *session.WorktreeReminderState, compactionCount int) bool {
	if state == nil {
		return false
	}
	if !state.HasIssuedInGeneration {
		return true
	}
	return state.IssuedCompactionCount != compactionCount
}

func worktreeReminderMessage(state session.WorktreeReminderState) (llm.Message, bool) {
	switch state.Mode {
	case session.WorktreeReminderModeEnter:
		return worktreeModeMetaMessage(state)
	case session.WorktreeReminderModeExit:
		return worktreeModeExitMetaMessage(state)
	default:
		return llm.Message{}, false
	}
}

func cloneRuntimeWorktreeReminderState(state *session.WorktreeReminderState) *session.WorktreeReminderState {
	if state == nil {
		return nil
	}
	copyState := *state
	copyState.Branch = strings.TrimSpace(copyState.Branch)
	copyState.WorktreePath = strings.TrimSpace(copyState.WorktreePath)
	copyState.WorkspaceRoot = strings.TrimSpace(copyState.WorkspaceRoot)
	copyState.EffectiveCwd = strings.TrimSpace(copyState.EffectiveCwd)
	return &copyState
}

package runtime

import (
	"strings"

	"builder/server/llm"
	"builder/server/session"
)

func (e *Engine) buildWorktreeReminderRequestItems(stepID string) ([]llm.ResponseItem, error) {
	state := cloneRuntimeWorktreeReminderState(e.store.Meta().WorktreeReminder)
	if !shouldInjectWorktreeReminder(state, e.compactionCountSnapshot()) {
		return nil, nil
	}
	message, ok := worktreeReminderMessage(*state)
	if !ok {
		return nil, nil
	}
	items := llm.ItemsFromMessages([]llm.Message{message})
	if strings.TrimSpace(stepID) == "" {
		return items, nil
	}
	if err := e.appendMessage(stepID, message); err != nil {
		return nil, err
	}
	state.HasIssuedInGeneration = true
	state.IssuedCompactionCount = e.compactionCountSnapshot()
	if err := e.store.SetWorktreeReminderState(state); err != nil {
		return nil, err
	}
	return items, nil
}

func filterHistoricalWorktreeReminderItems(items []llm.ResponseItem) []llm.ResponseItem {
	if len(items) == 0 {
		return nil
	}
	filtered := make([]llm.ResponseItem, 0, len(items))
	for _, item := range items {
		if item.Type == llm.ResponseItemTypeMessage {
			switch item.MessageType {
			case llm.MessageTypeWorktreeMode, llm.MessageTypeWorktreeModeExit:
				continue
			}
		}
		filtered = append(filtered, item)
	}
	return filtered
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

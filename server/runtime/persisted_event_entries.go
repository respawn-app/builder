package runtime

import (
	"encoding/json"
	"fmt"
	"strings"

	"builder/server/llm"
	"builder/server/session"
	"builder/server/tools"
	"builder/shared/config"
	"builder/shared/toolspec"
)

// VisibleChatEntriesFromPersistedEvent decodes transcript-visible entries from
// one persisted event using the provided cache-warning visibility mode.
// Callers that replay persisted transcript state must pass the active mode
// instead of assuming defaults.
func VisibleChatEntriesFromPersistedEvent(evt session.Event, cacheWarningMode config.CacheWarningMode) ([]ChatEntry, bool, error) {
	switch strings.TrimSpace(evt.Kind) {
	case "message":
		var msg llm.Message
		if err := json.Unmarshal(evt.Payload, &msg); err != nil {
			return nil, false, fmt.Errorf("decode message event: %w", err)
		}
		return VisibleChatEntriesFromMessage(msg), false, nil
	case "tool_completed":
		var completion storedToolCompletion
		if err := json.Unmarshal(evt.Payload, &completion); err != nil {
			return nil, false, fmt.Errorf("decode tool_completed event: %w", err)
		}
		entry := toolResultChatEntry(tools.Result{
			CallID:  completion.CallID,
			Name:    toolspec.ID(completion.Name),
			IsError: completion.IsError,
			Output:  completion.Output,
			Summary: completion.Summary,
		})
		return []ChatEntry{entry}, false, nil
	case "local_entry":
		var entry storedLocalEntry
		if err := json.Unmarshal(evt.Payload, &entry); err != nil {
			return nil, false, fmt.Errorf("decode local_entry event: %w", err)
		}
		chatEntry := localEntryChatEntry(entry)
		if chatEntry == nil {
			return nil, false, nil
		}
		return []ChatEntry{*chatEntry}, false, nil
	case sessionEventCacheWarning:
		chat := newChatStore()
		if err := applyPersistedCacheWarningToChat(chat, evt.Payload, cacheWarningMode); err != nil {
			return nil, false, err
		}
		return chat.snapshot().Entries, false, nil
	case "history_replaced":
		payload, ignoredLegacy, err := decodePersistedHistoryReplacementPayload(evt.Payload)
		if err != nil {
			return nil, false, fmt.Errorf("decode history_replaced event: %w", err)
		}
		if ignoredLegacy {
			return nil, false, nil
		}
		return visibleChatEntriesFromResponseItems(payload.Items), true, nil
	default:
		return nil, false, nil
	}
}

func visibleChatEntriesFromResponseItems(items []llm.ResponseItem) []ChatEntry {
	entries := make([]ChatEntry, 0, len(items))
	walker := newResponseItemMessageWalker(func(msg llm.Message) {
		entries = append(entries, VisibleChatEntriesFromMessage(msg)...)
	})
	for _, item := range items {
		walker.Apply(item)
	}
	walker.Flush()
	return entries
}

package runtime

import (
	"fmt"
	"strings"

	"builder/server/llm"
	"builder/server/tools"
)

func VisibleChatEntriesFromMessage(msg llm.Message) []ChatEntry {
	entries := make([]ChatEntry, 0, 1+len(msg.ToolCalls))
	switch msg.Role {
	case llm.RoleUser:
		content := strings.TrimSpace(msg.Content)
		if content != "" && msg.MessageType != llm.MessageTypeCompactionSummary {
			entries = append(entries, ChatEntry{Role: "user", Text: msg.Content})
		}
	case llm.RoleAssistant:
		if strings.TrimSpace(msg.Content) != "" {
			entries = append(entries, ChatEntry{Role: "assistant", Text: msg.Content, Phase: msg.Phase})
		}
		for _, call := range msg.ToolCalls {
			entries = append(entries, formatPersistedToolCall(call))
		}
	case llm.RoleTool:
		callID := strings.TrimSpace(msg.ToolCallID)
		result := tools.Result{
			CallID: callID,
			Name:   tools.ID(strings.TrimSpace(msg.Name)),
			Output: []byte(msg.Content),
		}
		if result.Name == "" {
			result.Name = tools.ID("tool")
		}
		entries = append(entries, toolResultChatEntry(result))
	case llm.RoleDeveloper:
		if entry, ok := visibleDeveloperChatEntry(msg); ok {
			entries = append(entries, entry)
		}
	}
	return entries
}

func TranscriptEntriesFromEvent(evt Event) []ChatEntry {
	switch evt.Kind {
	case EventUserMessageFlushed:
		text := strings.TrimSpace(evt.UserMessage)
		if text == "" {
			return nil
		}
		return []ChatEntry{{Role: "user", Text: evt.UserMessage}}
	case EventAssistantMessage:
		return VisibleChatEntriesFromMessage(evt.Message)
	case EventToolCallStarted:
		if evt.ToolCall == nil {
			return nil
		}
		return []ChatEntry{formatPersistedToolCall(*evt.ToolCall)}
	case EventToolCallCompleted:
		if evt.ToolResult == nil {
			return nil
		}
		return []ChatEntry{toolResultChatEntry(*evt.ToolResult)}
	case EventReviewerCompleted:
		if evt.Reviewer == nil {
			return nil
		}
		return []ChatEntry{{Role: "reviewer_status", Text: reviewerStatusText(*evt.Reviewer, nil)}}
	case EventCompactionCompleted:
		if evt.Compaction == nil {
			return nil
		}
		return []ChatEntry{{Role: "compaction_notice", Text: fmt.Sprintf("context compacted for the %s time", ordinal(evt.Compaction.Count))}}
	case EventCompactionFailed:
		if evt.Compaction == nil {
			return nil
		}
		message := fmt.Sprintf("Context compaction failed (%s): %s", evt.Compaction.Mode, evt.Compaction.Error)
		if strings.TrimSpace(evt.Compaction.Error) == "" {
			message = fmt.Sprintf("Context compaction failed (%s).", evt.Compaction.Mode)
		}
		return []ChatEntry{{Role: "error", Text: message}}
	case EventInFlightClearFailed:
		if strings.TrimSpace(evt.Error) == "" {
			return nil
		}
		return []ChatEntry{{Role: "error", Text: fmt.Sprintf("Run cleanup warning: %s", evt.Error)}}
	case EventBackgroundUpdated:
		if evt.Background == nil {
			return nil
		}
		if evt.Background.Type != "completed" && evt.Background.Type != "killed" {
			return nil
		}
		return []ChatEntry{{
			Role:        "system",
			Text:        formatBackgroundShellNotice(*evt.Background),
			OngoingText: formatBackgroundShellCompact(*evt.Background),
		}}
	default:
		return nil
	}
}

func toolResultChatEntry(result tools.Result) ChatEntry {
	role := "tool_result_ok"
	if result.IsError {
		role = "tool_result_error"
	}
	return ChatEntry{
		Role:       role,
		Text:       formatToolResult(result),
		ToolCallID: strings.TrimSpace(result.CallID),
	}
}

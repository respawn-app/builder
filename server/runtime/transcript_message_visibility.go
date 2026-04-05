package runtime

import (
	"strings"

	"builder/server/llm"
	"builder/shared/transcript"
)

func visibleUserTranscriptEntry(msg llm.Message) (ChatEntry, bool) {
	content := strings.TrimSpace(msg.Content)
	if content == "" {
		return ChatEntry{}, false
	}
	if msg.MessageType == llm.MessageTypeCompactionSummary {
		return ChatEntry{
			Role:        string(transcript.EntryRoleCompactionSummary),
			Text:        msg.Content,
			OngoingText: compactCompactionSummaryText(msg.Content),
		}, true
	}
	return ChatEntry{Role: "user", Text: msg.Content}, true
}

func visibleDeveloperChatEntry(msg llm.Message) (ChatEntry, bool) {
	if strings.TrimSpace(msg.Content) == "" {
		return ChatEntry{}, false
	}
	switch msg.MessageType {
	case llm.MessageTypeAgentsMD,
		llm.MessageTypeSkills,
		llm.MessageTypeEnvironment,
		llm.MessageTypeHeadlessMode,
		llm.MessageTypeHeadlessModeExit:
		return ChatEntry{Role: string(transcript.EntryRoleDeveloperContext), Text: msg.Content}, true
	case llm.MessageTypeCompactionSummary:
		return ChatEntry{
			Role:        string(transcript.EntryRoleCompactionSummary),
			Text:        msg.Content,
			OngoingText: compactCompactionSummaryText(msg.Content),
		}, true
	case llm.MessageTypeInterruption:
		return ChatEntry{Role: string(transcript.EntryRoleInterruption), Text: msg.Content}, true
	case llm.MessageTypeErrorFeedback:
		return ChatEntry{Role: string(transcript.EntryRoleDeveloperFeedback), Text: msg.Content}, true
	case llm.MessageTypeCompactionSoonReminder:
		return ChatEntry{Role: "warning", Text: msg.Content}, true
	case llm.MessageTypeBackgroundNotice:
		return ChatEntry{Role: "system", Text: msg.Content, OngoingText: msg.CompactContent}, true
	case llm.MessageTypeManualCompactionCarryover:
		return ChatEntry{Role: string(transcript.EntryRoleManualCompactionCarryover), Text: msg.Content}, true
	default:
		return ChatEntry{}, false
	}
}

func compactCompactionSummaryText(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}
	normalized := strings.ReplaceAll(trimmed, "\r\n", "\n")
	lines := strings.Split(normalized, "\n")
	first := ""
	remaining := false
	for idx, line := range lines {
		candidate := strings.TrimSpace(line)
		if candidate == "" {
			continue
		}
		if first == "" {
			first = candidate
			for _, tail := range lines[idx+1:] {
				if strings.TrimSpace(tail) != "" {
					remaining = true
					break
				}
			}
			break
		}
	}
	if first == "" {
		return ""
	}
	if remaining {
		return first + "\n…"
	}
	return first
}

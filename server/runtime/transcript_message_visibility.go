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
		return ChatEntry{Role: string(transcript.EntryRoleCompactionSummary), Text: msg.Content}, true
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
		return ChatEntry{Role: string(transcript.EntryRoleCompactionSummary), Text: msg.Content}, true
	case llm.MessageTypeInterruption:
		return ChatEntry{Role: string(transcript.EntryRoleInterruption), Text: msg.Content}, true
	case llm.MessageTypeErrorFeedback:
		return ChatEntry{Role: string(transcript.EntryRoleDeveloperFeedback), Text: msg.Content}, true
	case llm.MessageTypeCompactionSoonReminder:
		return ChatEntry{Role: "warning", Text: msg.Content}, true
	case llm.MessageTypeBackgroundNotice:
		return ChatEntry{Role: "system", Text: msg.Content, OngoingText: msg.CompactContent}, true
	case llm.MessageTypeHandoffFutureMessage:
		return ChatEntry{Role: string(transcript.EntryRoleDeveloperContext), Text: msg.Content}, true
	case llm.MessageTypeManualCompactionCarryover:
		return ChatEntry{Role: string(transcript.EntryRoleManualCompactionCarryover), Text: msg.Content}, true
	default:
		return ChatEntry{}, false
	}
}

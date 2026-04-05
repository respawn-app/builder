package runtime

import (
	"encoding/json"
	"fmt"
	"strings"

	"builder/server/llm"
	"builder/server/session"
	"builder/server/tools"
	"builder/shared/transcript"
)

type PersistedTranscriptScanRequest struct {
	Offset int
	Limit  int

	TrackOngoingTail bool
	TailLimit        int
}

type PersistedTranscriptScan struct {
	request PersistedTranscriptScanRequest

	totalEntries int
	pageEntries  []ChatEntry

	tailEntries             []ChatEntry
	tailStart               int
	compactionEntryStart    int
	hasCompactionCheckpoint bool

	toolCompletions map[string]tools.Result

	lastCommittedAssistantFinalAnswer string
	lastCommittedAssistantAnswerValid bool
}

func NewPersistedTranscriptScan(req PersistedTranscriptScanRequest) *PersistedTranscriptScan {
	if req.Offset < 0 {
		req.Offset = 0
	}
	if req.Limit < 0 {
		req.Limit = 0
	}
	if req.TailLimit < 0 {
		req.TailLimit = 0
	}
	return &PersistedTranscriptScan{
		request:              req,
		toolCompletions:      make(map[string]tools.Result),
		compactionEntryStart: -1,
	}
}

func (s *PersistedTranscriptScan) ApplyPersistedEvent(evt session.Event) error {
	if s == nil {
		return nil
	}
	switch strings.TrimSpace(evt.Kind) {
	case "message":
		var msg llm.Message
		if err := json.Unmarshal(evt.Payload, &msg); err != nil {
			return fmt.Errorf("decode message event: %w", err)
		}
		s.trackAssistantFinalAnswer(msg)
		s.appendVisibleMessageEntries(msg)
	case "tool_completed":
		var completion storedToolCompletion
		if err := json.Unmarshal(evt.Payload, &completion); err != nil {
			return fmt.Errorf("decode tool_completed event: %w", err)
		}
		callID := strings.TrimSpace(completion.CallID)
		if callID == "" {
			return nil
		}
		s.toolCompletions[callID] = tools.Result{
			CallID:  completion.CallID,
			Name:    tools.ID(completion.Name),
			IsError: completion.IsError,
			Output:  completion.Output,
		}
	case "local_entry":
		var entry storedLocalEntry
		if err := json.Unmarshal(evt.Payload, &entry); err != nil {
			return fmt.Errorf("decode local_entry event: %w", err)
		}
		if visible := persistedLocalChatEntry(entry); visible != nil {
			s.appendEntry(*visible)
		}
	case "history_replaced":
		s.hasCompactionCheckpoint = true
		s.compactionEntryStart = s.totalEntries
	}
	return nil
}

func (s *PersistedTranscriptScan) TotalEntries() int {
	if s == nil {
		return 0
	}
	return s.totalEntries
}

func (s *PersistedTranscriptScan) CollectedPageSnapshot() ChatSnapshot {
	if s == nil {
		return ChatSnapshot{}
	}
	return ChatSnapshot{Entries: append([]ChatEntry(nil), s.pageEntries...)}
}

func (s *PersistedTranscriptScan) OngoingTailSnapshot() TranscriptWindowSnapshot {
	if s == nil {
		return TranscriptWindowSnapshot{}
	}
	entries := append([]ChatEntry(nil), s.tailEntries...)
	offset := s.tailStart
	return TranscriptWindowSnapshot{
		Snapshot:     ChatSnapshot{Entries: entries},
		TotalEntries: s.totalEntries,
		Offset:       offset,
	}
}

func (s *PersistedTranscriptScan) LastCommittedAssistantFinalAnswer() string {
	if s == nil || !s.lastCommittedAssistantAnswerValid {
		return ""
	}
	return s.lastCommittedAssistantFinalAnswer
}

func (s *PersistedTranscriptScan) appendVisibleMessageEntries(msg llm.Message) {
	switch msg.Role {
	case llm.RoleUser:
		content := strings.TrimSpace(msg.Content)
		if content != "" && msg.MessageType != llm.MessageTypeCompactionSummary {
			s.appendEntry(ChatEntry{Role: "user", Text: msg.Content})
		}
	case llm.RoleAssistant:
		if strings.TrimSpace(msg.Content) != "" {
			s.appendEntry(ChatEntry{Role: "assistant", Text: msg.Content, Phase: msg.Phase})
		}
		for _, call := range msg.ToolCalls {
			s.appendEntry(formatPersistedToolCall(call))
		}
	case llm.RoleTool:
		callID := strings.TrimSpace(msg.ToolCallID)
		result := tools.Result{
			CallID: callID,
			Name:   tools.ID(strings.TrimSpace(msg.Name)),
			Output: json.RawMessage(msg.Content),
		}
		if completion, ok := s.toolCompletions[callID]; ok {
			if result.Name == "" {
				result.Name = completion.Name
			}
			if strings.TrimSpace(msg.Content) == "" && len(completion.Output) > 0 {
				result.Output = completion.Output
			}
			result.IsError = completion.IsError
		}
		if result.Name == "" {
			result.Name = tools.ID("tool")
		}
		role := "tool_result_ok"
		if result.IsError {
			role = "tool_result_error"
		}
		s.appendEntry(ChatEntry{Role: role, Text: formatToolResult(result), ToolCallID: callID})
	case llm.RoleDeveloper:
		if entry, ok := visibleDeveloperChatEntry(msg); ok {
			s.appendEntry(entry)
		}
	}
}

func (s *PersistedTranscriptScan) trackAssistantFinalAnswer(msg llm.Message) {
	if shouldSkipTrailingAssistantHandoffMessage(msg) {
		return
	}
	if msg.Role == llm.RoleAssistant && msg.Phase == llm.MessagePhaseFinal && strings.TrimSpace(msg.Content) != "" {
		s.lastCommittedAssistantFinalAnswer = msg.Content
		s.lastCommittedAssistantAnswerValid = true
		return
	}
	s.lastCommittedAssistantAnswerValid = false
}

func (s *PersistedTranscriptScan) appendEntry(entry ChatEntry) {
	entryIndex := s.totalEntries
	if s.request.Limit > 0 && entryIndex >= s.request.Offset && entryIndex < s.request.Offset+s.request.Limit {
		s.pageEntries = append(s.pageEntries, clonePersistedChatEntry(entry))
	}
	s.totalEntries++
	if !s.request.TrackOngoingTail {
		return
	}
	tailLimit := s.request.TailLimit
	if tailLimit <= 0 {
		return
	}
	startLastN := s.totalEntries - tailLimit
	if startLastN < 0 {
		startLastN = 0
	}
	start := startLastN
	if s.hasCompactionCheckpoint && s.compactionEntryStart >= 0 && s.compactionEntryStart < start {
		start = s.compactionEntryStart
	}
	if start > s.tailStart {
		drop := start - s.tailStart
		if drop >= len(s.tailEntries) {
			s.tailEntries = nil
		} else {
			s.tailEntries = append([]ChatEntry(nil), s.tailEntries[drop:]...)
		}
		s.tailStart = start
	}
	if s.tailEntries == nil {
		s.tailStart = start
	}
	s.tailEntries = append(s.tailEntries, clonePersistedChatEntry(entry))
}

func clonePersistedChatEntry(entry ChatEntry) ChatEntry {
	copyEntry := entry
	copyEntry.ToolCall = clonePersistedToolCallMeta(entry.ToolCall)
	return copyEntry
}

func clonePersistedToolCallMeta(meta *transcript.ToolCallMeta) *transcript.ToolCallMeta {
	if meta == nil {
		return nil
	}
	copyMeta := *meta
	if len(meta.Suggestions) > 0 {
		copyMeta.Suggestions = append([]string(nil), meta.Suggestions...)
	}
	if meta.RenderHint != nil {
		renderHint := *meta.RenderHint
		copyMeta.RenderHint = &renderHint
	}
	return &copyMeta
}

func persistedLocalChatEntry(entry storedLocalEntry) *ChatEntry {
	if strings.TrimSpace(entry.Role) == "" || strings.TrimSpace(entry.Text) == "" {
		return nil
	}
	return &ChatEntry{
		Role:        strings.TrimSpace(entry.Role),
		Text:        entry.Text,
		OngoingText: strings.TrimSpace(entry.OngoingText),
	}
}

func formatPersistedToolCall(call llm.ToolCall) ChatEntry {
	meta := decodeToolCallMeta(call)
	text := "tool call"
	if meta != nil {
		text = strings.TrimSpace(meta.Command)
	}
	if text == "" {
		text = "tool call"
	}
	return ChatEntry{
		Role:       "tool_call",
		Text:       text,
		ToolCallID: strings.TrimSpace(call.ID),
		ToolCall:   meta,
	}
}

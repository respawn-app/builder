package runtime

import (
	"builder/internal/llm"
	"builder/internal/tools"
	"builder/internal/transcript"
	"builder/internal/transcript/toolcodec"
	"builder/prompts"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
)

const (
	defaultShellTimeoutSecond = toolcodec.DefaultShellTimeoutSecs
)

type ChatEntry struct {
	Role        string
	Text        string
	OngoingText string
	Phase       llm.MessagePhase
	ToolCallID  string
	ToolCall    *transcript.ToolCallMeta
}

type ChatSnapshot struct {
	Entries      []ChatEntry
	Ongoing      string
	OngoingError string
}

type storedToolCompletion struct {
	CallID  string          `json:"call_id"`
	Name    string          `json:"name"`
	IsError bool            `json:"is_error"`
	Output  json.RawMessage `json:"output"`
}

type chatStore struct {
	mu sync.RWMutex

	messages []llm.Message
	items    []llm.ResponseItem
	compact  *compactionCheckpoint
	local    []localChatEntry

	toolCompletions map[string]tools.Result
	ongoing         string
	ongoingError    string
	cwd             string

	providerTokenEstimate      int
	providerTokenEstimateDirty bool
}

type localChatEntry struct {
	Entry             ChatEntry
	AfterMessageCount int
}

type compactionCheckpoint struct {
	CutoffItemCount int
	Items           []llm.ResponseItem
}

func newChatStore() *chatStore {
	cwd, _ := os.Getwd()
	return &chatStore{
		toolCompletions:            make(map[string]tools.Result, 16),
		cwd:                        cwd,
		providerTokenEstimateDirty: true,
	}
}

func (s *chatStore) appendMessage(msg llm.Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if msg.Role == llm.RoleAssistant && strings.TrimSpace(msg.Content) != "" {
		s.ongoing = ""
		s.ongoingError = ""
	}
	s.messages = append(s.messages, msg)
	s.items = append(s.items, llm.ItemsFromMessages([]llm.Message{msg})...)
	s.providerTokenEstimateDirty = true
}
func (s *chatStore) replaceHistory(items []llm.ResponseItem) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.compact = &compactionCheckpoint{
		CutoffItemCount: len(s.items),
		Items:           llm.CloneResponseItems(items),
	}
	s.providerTokenEstimateDirty = true
}

func (s *chatStore) restoreMessagesFromItems(items []llm.ResponseItem) {
	s.mu.Lock()
	defer s.mu.Unlock()
	restored := llm.CloneResponseItems(items)
	s.messages = llm.MessagesFromItems(restored)
	s.items = restored
	s.compact = nil
	s.providerTokenEstimateDirty = true
}

func (s *chatStore) estimatedProviderTokens() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.providerTokenEstimateDirty {
		return s.providerTokenEstimate
	}
	total := 0
	if s.compact == nil {
		total = estimateItemsTokens(s.items)
	} else {
		total = estimateItemsTokens(s.compact.Items)
		tailStart := s.compact.CutoffItemCount
		if tailStart < 0 {
			tailStart = 0
		}
		if tailStart < len(s.items) {
			total += estimateItemsTokens(s.items[tailStart:])
		}
	}
	if total < 0 {
		total = 0
	}
	s.providerTokenEstimate = total
	s.providerTokenEstimateDirty = false
	return total
}

func (s *chatStore) snapshotItems() []llm.ResponseItem {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snapshotProviderItemsLocked()
}

func (s *chatStore) restoreToolCompletionPayload(payload []byte) error {
	var completion storedToolCompletion
	if err := json.Unmarshal(payload, &completion); err != nil {
		return fmt.Errorf("decode tool_completed event: %w", err)
	}
	s.recordToolCompletion(tools.Result{
		CallID:  completion.CallID,
		Name:    tools.ID(completion.Name),
		IsError: completion.IsError,
		Output:  completion.Output,
	})
	return nil
}

func (s *chatStore) recordToolCompletion(res tools.Result) {
	callID := strings.TrimSpace(res.CallID)
	if callID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.toolCompletions[callID] = res
}

func (s *chatStore) appendOngoingDelta(delta string) {
	if delta == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ongoing += delta
}

func (s *chatStore) clearOngoing() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ongoing = ""
}

func (s *chatStore) setOngoingError(text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ongoingError = strings.TrimSpace(text)
}

func (s *chatStore) clearOngoingError() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ongoingError = ""
}

func (s *chatStore) appendLocalEntry(role, text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.local = append(s.local, localChatEntry{
		Entry:             ChatEntry{Role: role, Text: text},
		AfterMessageCount: len(s.messages),
	})
}

func (s *chatStore) snapshotMessages() []llm.Message {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return llm.MessagesFromItems(s.snapshotProviderItemsLocked())
}

func (s *chatStore) snapshotProviderItemsLocked() []llm.ResponseItem {
	if s.compact == nil {
		return llm.CloneResponseItems(s.items)
	}
	base := llm.CloneResponseItems(s.compact.Items)
	tailStart := s.compact.CutoffItemCount
	if tailStart < 0 {
		tailStart = 0
	}
	if tailStart >= len(s.items) {
		return base
	}
	tail := llm.CloneResponseItems(s.items[tailStart:])
	out := make([]llm.ResponseItem, 0, len(base)+len(tail))
	out = append(out, base...)
	out = append(out, tail...)
	return out
}

func (s *chatStore) snapshot() ChatSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entries := make([]ChatEntry, 0, len(s.messages)+len(s.local))
	localIndex := 0
	appendLocalEntries := func(processedMessages int) {
		for localIndex < len(s.local) {
			if s.local[localIndex].AfterMessageCount > processedMessages {
				break
			}
			entries = append(entries, s.local[localIndex].Entry)
			localIndex++
		}
	}
	appendLocalEntries(0)
	processedMessages := 0
	for _, msg := range s.messages {
		switch msg.Role {
		case llm.RoleUser:
			content := strings.TrimSpace(msg.Content)
			if content != "" &&
				!strings.HasPrefix(content, prompts.CompactionSummaryPrefix+"\n") {
				entries = append(entries, ChatEntry{Role: "user", Text: msg.Content})
			}
		case llm.RoleAssistant:
			if strings.TrimSpace(msg.Content) != "" {
				entries = append(entries, ChatEntry{Role: "assistant", Text: msg.Content, Phase: msg.Phase})
			}
			if len(msg.ToolCalls) > 0 {
				for _, call := range msg.ToolCalls {
					entries = append(entries, s.formatToolCall(call))
				}
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
			entries = append(entries, ChatEntry{Role: role, Text: formatToolResult(result), ToolCallID: callID})
		case llm.RoleDeveloper:
			if entry, ok := visibleDeveloperChatEntry(msg); ok {
				entries = append(entries, entry)
			}
		}
		processedMessages++
		appendLocalEntries(processedMessages)
	}
	appendLocalEntries(len(s.messages))
	return ChatSnapshot{
		Entries:      entries,
		Ongoing:      s.ongoing,
		OngoingError: s.ongoingError,
	}
}

func visibleDeveloperChatEntry(msg llm.Message) (ChatEntry, bool) {
	if strings.TrimSpace(msg.Content) == "" {
		return ChatEntry{}, false
	}
	switch msg.MessageType {
	case llm.MessageTypeErrorFeedback:
		return ChatEntry{Role: "error", Text: msg.Content}, true
	case llm.MessageTypeBackgroundNotice:
		return ChatEntry{Role: "system", Text: msg.Content, OngoingText: msg.CompactContent}, true
	default:
		return ChatEntry{}, false
	}
}

func (s *chatStore) formatToolCall(call llm.ToolCall) ChatEntry {
	toolName := strings.TrimSpace(call.Name)
	isShellTool := toolName == string(tools.ToolShell) || toolName == string(tools.ToolExecCommand) || toolName == string(tools.ToolWriteStdin)
	meta := &transcript.ToolCallMeta{
		ToolName: toolName,
		IsShell:  isShellTool,
	}
	if meta.IsShell {
		meta.UserInitiated = parseShellToolCallUserInitiated(call.Input)
	}
	if toolName == string(tools.ToolAskQuestion) {
		if question, suggestions, ok := formatAskQuestionToolCall(call.Input); ok {
			meta.Command = question
			meta.Question = question
			meta.Suggestions = append([]string(nil), suggestions...)
			return ChatEntry{
				Role:       "tool_call",
				Text:       question,
				ToolCallID: strings.TrimSpace(call.ID),
				ToolCall:   meta,
			}
		}
	}
	if toolName == string(tools.ToolWebSearch) {
		if query, ok := formatWebSearchToolCall(call.Input); ok {
			meta.Command = query
			return ChatEntry{
				Role:       "tool_call",
				Text:       query,
				ToolCallID: strings.TrimSpace(call.ID),
				ToolCall:   meta,
			}
		}
	}
	if toolName == string(tools.ToolPatch) {
		if summary, detail, ok := s.formatPatchToolCall(call.Input); ok {
			meta.PatchSummary = summary
			meta.PatchDetail = detail
			meta.RenderHint = &transcript.ToolRenderHint{Kind: transcript.ToolRenderKindDiff}
			return ChatEntry{
				Role:       "tool_call",
				Text:       summary,
				ToolCallID: strings.TrimSpace(call.ID),
				ToolCall:   meta,
			}
		}
	}
	command, timeoutLabel := toolcodec.FormatInput(toolName, call.Input, defaultShellTimeoutSecond)
	command = strings.TrimSpace(command)
	if command == "" {
		command = "tool call"
	}
	meta.Command = command
	meta.TimeoutLabel = timeoutLabel
	if meta.IsShell {
		meta.RenderHint = detectShellRenderHint(command)
	}
	return ChatEntry{
		Role:       "tool_call",
		Text:       command,
		ToolCallID: strings.TrimSpace(call.ID),
		ToolCall:   meta,
	}
}

func parseShellToolCallUserInitiated(raw json.RawMessage) bool {
	var in struct {
		UserInitiated bool `json:"user_initiated"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return false
	}
	return in.UserInitiated
}

func formatAskQuestionToolCall(raw json.RawMessage) (string, []string, bool) {
	var in struct {
		Question    string   `json:"question"`
		Suggestions []string `json:"suggestions,omitempty"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return "", nil, false
	}
	question := strings.TrimSpace(in.Question)
	if question == "" {
		return "", nil, false
	}
	suggestions := make([]string, 0, len(in.Suggestions))
	for _, suggestion := range in.Suggestions {
		trimmed := strings.TrimSpace(suggestion)
		if trimmed == "" {
			continue
		}
		suggestions = append(suggestions, trimmed)
	}
	return question, suggestions, true
}

func formatToolResult(result tools.Result) string {
	if result.Name == tools.ToolPatch && !result.IsError {
		return ""
	}
	if result.Name == tools.ToolWebSearch {
		formatted := strings.TrimSpace(formatRawToolJSON(result.Output))
		if formatted != "" {
			return formatted
		}
	}
	output := strings.TrimSpace(toolcodec.FormatOutputForTool(string(result.Name), result.Output))
	if output == "" {
		if result.IsError {
			output = "tool failed"
		} else {
			output = "done"
		}
	}
	return output
}

func formatWebSearchToolCall(raw json.RawMessage) (string, bool) {
	var in struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return "", false
	}
	query := strings.TrimSpace(in.Query)
	if query == "" {
		return "", false
	}
	return query, true
}

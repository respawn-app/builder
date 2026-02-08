package runtime

import (
	"builder/internal/llm"
	"builder/internal/tools"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

const agentsInjectedPrefix = "# AGENTS.md auto-injection"

type ChatEntry struct {
	Role string
	Text string
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
	local    []ChatEntry

	toolCompletions map[string]tools.Result
	ongoing         string
	ongoingError    string
}

func newChatStore() *chatStore {
	return &chatStore{
		toolCompletions: make(map[string]tools.Result, 16),
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
	s.local = append(s.local, ChatEntry{Role: role, Text: text})
}

func (s *chatStore) snapshotMessages() []llm.Message {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]llm.Message, len(s.messages))
	copy(out, s.messages)
	return out
}

func (s *chatStore) snapshot() ChatSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entries := make([]ChatEntry, 0, len(s.messages)+len(s.local))
	for _, msg := range s.messages {
		switch msg.Role {
		case llm.RoleUser:
			content := strings.TrimSpace(msg.Content)
			if content == "" || strings.HasPrefix(content, agentsInjectedPrefix) {
				continue
			}
			entries = append(entries, ChatEntry{Role: "user", Text: msg.Content})
		case llm.RoleAssistant:
			if len(msg.ToolCalls) > 0 {
				for _, call := range msg.ToolCalls {
					entries = append(entries, ChatEntry{Role: "tool_call", Text: formatToolCall(call)})
				}
				continue
			}
			if strings.TrimSpace(msg.Content) != "" {
				entries = append(entries, ChatEntry{Role: "assistant", Text: msg.Content})
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
			entries = append(entries, ChatEntry{Role: "tool_result", Text: formatToolResult(result)})
		}
	}
	entries = append(entries, s.local...)
	return ChatSnapshot{
		Entries:      entries,
		Ongoing:      s.ongoing,
		OngoingError: s.ongoingError,
	}
}

func formatToolCall(call llm.ToolCall) string {
	return fmt.Sprintf("id=%s name=%s\ninput:\n%s", call.ID, call.Name, string(call.Input))
}

func formatToolResult(result tools.Result) string {
	return fmt.Sprintf("id=%s name=%s error=%t\noutput:\n%s", result.CallID, result.Name, result.IsError, string(result.Output))
}

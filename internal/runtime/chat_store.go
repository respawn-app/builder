package runtime

import (
	"builder/internal/llm"
	"builder/internal/tools"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	agentsInjectedPrefix      = "# AGENTS.md auto-injection"
	toolInlineMetaSeparator   = "\x1f"
	defaultShellTimeoutSecond = 300
)

var outputLineNumberPrefix = regexp.MustCompile(`^\s*\d+(?:\t|\s{2,})`)

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
			if strings.TrimSpace(msg.Content) != "" {
				entries = append(entries, ChatEntry{Role: "assistant", Text: msg.Content})
			}
			if len(msg.ToolCalls) > 0 {
				for _, call := range msg.ToolCalls {
					entries = append(entries, ChatEntry{Role: "tool_call", Text: formatToolCall(call)})
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
			entries = append(entries, ChatEntry{Role: role, Text: formatToolResult(result)})
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
	command, timeoutLabel := formatToolInput(call)
	command = strings.TrimSpace(command)
	if command == "" {
		return "tool call"
	}
	if timeoutLabel == "" {
		return command
	}
	return command + toolInlineMetaSeparator + timeoutLabel
}

func formatToolResult(result tools.Result) string {
	output := strings.TrimSpace(formatToolOutput(result.Output))
	if output == "" {
		if result.IsError {
			output = "tool failed"
		} else {
			output = "done"
		}
	}
	return output
}

func formatToolInput(call llm.ToolCall) (string, string) {
	var payload any
	if err := json.Unmarshal(call.Input, &payload); err != nil {
		return strings.TrimSpace(string(call.Input)), ""
	}
	obj, ok := payload.(map[string]any)
	if !ok {
		return renderPlain(payload), ""
	}
	if cmd, ok := asString(obj["command"]); ok {
		timeout := ""
		if secs, ok := asInt(obj["timeout_seconds"]); ok && secs > 0 {
			timeout = "timeout: " + formatDurationShort(time.Duration(secs)*time.Second)
		} else if strings.TrimSpace(call.Name) == string(tools.ToolShell) {
			timeout = "timeout: " + formatDurationShort(time.Duration(defaultShellTimeoutSecond)*time.Second)
		}
		return cmd, timeout
	}
	return renderPlain(payload), ""
}

func formatToolOutput(raw json.RawMessage) string {
	var payload any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return strings.TrimSpace(string(raw))
	}
	obj, ok := payload.(map[string]any)
	if !ok {
		return renderPlain(payload)
	}

	if msg, ok := asString(obj["error"]); ok {
		return msg
	}
	if out, ok := asString(obj["output"]); ok {
		out = stripLineNumbersWhenLikely(out)
		var notes []string
		if code, ok := asInt(obj["exit_code"]); ok && code != 0 {
			notes = append(notes, fmt.Sprintf("exit code %d", code))
		}
		if truncated, ok := obj["truncated"].(bool); ok && truncated {
			if removed, ok := asInt(obj["truncation_bytes"]); ok && removed > 0 {
				notes = append(notes, fmt.Sprintf("truncated %d bytes", removed))
			} else {
				notes = append(notes, "truncated")
			}
		}
		if len(notes) == 0 {
			return out
		}
		if strings.TrimSpace(out) == "" {
			return strings.Join(notes, ", ")
		}
		return out + "\n" + strings.Join(notes, ", ")
	}
	if answer, ok := asString(obj["answer"]); ok {
		return answer
	}
	return renderPlain(payload)
}

func stripLineNumbersWhenLikely(text string) string {
	lines := strings.Split(text, "\n")
	nonEmpty := 0
	numbered := 0
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		nonEmpty++
		if outputLineNumberPrefix.MatchString(line) {
			numbered++
		}
	}
	if nonEmpty == 0 || numbered == 0 || numbered*2 < nonEmpty {
		return text
	}
	for i, line := range lines {
		lines[i] = outputLineNumberPrefix.ReplaceAllString(line, "")
	}
	return strings.Join(lines, "\n")
}

func formatDurationShort(d time.Duration) string {
	if d <= 0 {
		return "0s"
	}
	total := int(d.Seconds())
	hours := total / 3600
	minutes := (total % 3600) / 60
	seconds := total % 60

	parts := make([]string, 0, 3)
	if hours > 0 {
		parts = append(parts, fmt.Sprintf("%dh", hours))
	}
	if minutes > 0 {
		parts = append(parts, fmt.Sprintf("%dm", minutes))
	}
	if seconds > 0 {
		parts = append(parts, fmt.Sprintf("%ds", seconds))
	}
	if len(parts) == 0 {
		return "0s"
	}
	return strings.Join(parts, "")
}

func renderPlain(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case bool:
		return strconv.FormatBool(x)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case []any:
		if len(x) == 0 {
			return "[]"
		}
		lines := make([]string, 0, len(x))
		for _, item := range x {
			rendered := strings.TrimSpace(renderPlain(item))
			if rendered == "" {
				continue
			}
			itemLines := strings.Split(rendered, "\n")
			lines = append(lines, "- "+itemLines[0])
			for _, line := range itemLines[1:] {
				lines = append(lines, "  "+line)
			}
		}
		return strings.Join(lines, "\n")
	case map[string]any:
		if len(x) == 0 {
			return "{}"
		}
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		lines := make([]string, 0, len(keys))
		for _, k := range keys {
			rendered := strings.TrimSpace(renderPlain(x[k]))
			if rendered == "" {
				lines = append(lines, k+":")
				continue
			}
			valueLines := strings.Split(rendered, "\n")
			lines = append(lines, k+": "+valueLines[0])
			for _, line := range valueLines[1:] {
				lines = append(lines, "  "+line)
			}
		}
		return strings.Join(lines, "\n")
	default:
		return fmt.Sprintf("%v", x)
	}
}

func asString(v any) (string, bool) {
	s, ok := v.(string)
	if !ok {
		return "", false
	}
	return strings.TrimSpace(s), true
}

func asInt(v any) (int, bool) {
	switch x := v.(type) {
	case float64:
		return int(x), true
	case int:
		return x, true
	default:
		return 0, false
	}
}

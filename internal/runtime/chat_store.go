package runtime

import (
	"builder/internal/llm"
	"builder/internal/tools"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
	toolShellCallPrefix       = "\x1eshell_call\x1e"
	defaultShellTimeoutSecond = 300
	toolPatchPayloadPrefix    = "\x1epatch_payload\x1e"
	toolPatchPayloadSeparator = "\x1epatch_sep\x1e"
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
	cwd             string
}

func newChatStore() *chatStore {
	cwd, _ := os.Getwd()
	return &chatStore{
		toolCompletions: make(map[string]tools.Result, 16),
		cwd:             cwd,
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
					entries = append(entries, ChatEntry{Role: "tool_call", Text: s.formatToolCall(call)})
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

func (s *chatStore) formatToolCall(call llm.ToolCall) string {
	if strings.TrimSpace(call.Name) == string(tools.ToolPatch) {
		if payload := s.formatPatchToolCall(call.Input); payload != "" {
			return payload
		}
	}
	isShellCall := strings.TrimSpace(call.Name) == string(tools.ToolShell)
	command, timeoutLabel := formatToolInput(call)
	command = strings.TrimSpace(command)
	if command == "" {
		return "tool call"
	}
	if isShellCall {
		command = toolShellCallPrefix + command
	}
	if timeoutLabel == "" {
		return command
	}
	return command + toolInlineMetaSeparator + timeoutLabel
}

func formatToolResult(result tools.Result) string {
	if result.Name == tools.ToolPatch && !result.IsError {
		return ""
	}
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

type patchFileView struct {
	AbsPath string
	RelPath string
	Added   int
	Removed int
	Diff    []string
}

func (s *chatStore) formatPatchToolCall(raw json.RawMessage) string {
	var input map[string]json.RawMessage
	if err := json.Unmarshal(raw, &input); err != nil {
		return ""
	}
	patchRaw, ok := input["patch"]
	if !ok {
		return ""
	}
	var patchText string
	if err := json.Unmarshal(patchRaw, &patchText); err != nil {
		return ""
	}
	files := parsePatchFileViews(patchText, s.cwd)
	if len(files) == 0 {
		return ""
	}

	summaryLines := []string{"Edited:"}
	detailLines := []string{"Edited:"}
	for _, file := range files {
		line := file.RelPath
		if line == "" {
			line = file.AbsPath
		}
		if file.Added > 0 {
			line += fmt.Sprintf(" +%d", file.Added)
		}
		if file.Removed > 0 {
			line += fmt.Sprintf(" -%d", file.Removed)
		}
		summaryLines = append(summaryLines, line)

		detailLines = append(detailLines, file.AbsPath)
		detailLines = append(detailLines, file.Diff...)
	}

	return toolPatchPayloadPrefix +
		strings.Join(summaryLines, "\n") +
		toolPatchPayloadSeparator +
		strings.Join(detailLines, "\n")
}

func parsePatchFileViews(patchText, cwd string) []patchFileView {
	lines := splitRawLines(patchText)
	files := make([]patchFileView, 0, 8)
	byAbs := make(map[string]int, 8)

	resolve := func(path string) (string, string) {
		p := strings.TrimSpace(path)
		if p == "" {
			return "", ""
		}
		var abs string
		if filepath.IsAbs(p) {
			abs = filepath.Clean(p)
		} else if cwd != "" {
			abs = filepath.Clean(filepath.Join(cwd, p))
		} else {
			abs = filepath.Clean(p)
		}
		abs = filepath.ToSlash(abs)
		if cwd == "" {
			return abs, "./" + filepath.ToSlash(strings.TrimPrefix(p, "./"))
		}
		rel, err := filepath.Rel(cwd, filepath.FromSlash(abs))
		if err != nil {
			return abs, filepath.ToSlash(p)
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return abs, "./"
		}
		if strings.HasPrefix(rel, "../") || rel == ".." {
			return abs, filepath.ToSlash(p)
		}
		return abs, "./" + rel
	}

	getFile := func(path string) *patchFileView {
		abs, rel := resolve(path)
		if abs == "" {
			return nil
		}
		if idx, ok := byAbs[abs]; ok {
			return &files[idx]
		}
		files = append(files, patchFileView{AbsPath: abs, RelPath: rel, Diff: make([]string, 0, 32)})
		idx := len(files) - 1
		byAbs[abs] = idx
		return &files[idx]
	}

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		switch {
		case strings.HasPrefix(line, "*** Add File: "):
			file := getFile(strings.TrimPrefix(line, "*** Add File: "))
			for i+1 < len(lines) && !strings.HasPrefix(lines[i+1], "*** ") {
				i++
				row := lines[i]
				if file == nil {
					continue
				}
				if row == "" {
					file.Diff = append(file.Diff, "")
					continue
				}
				if strings.HasPrefix(row, "+") {
					file.Added++
				}
				file.Diff = append(file.Diff, row)
			}
		case strings.HasPrefix(line, "*** Update File: "):
			path := strings.TrimPrefix(line, "*** Update File: ")
			if i+1 < len(lines) && strings.HasPrefix(lines[i+1], "*** Move to: ") {
				i++
				path = strings.TrimPrefix(lines[i], "*** Move to: ")
			}
			file := getFile(path)
			for i+1 < len(lines) && !strings.HasPrefix(lines[i+1], "*** ") {
				i++
				row := lines[i]
				if file == nil {
					continue
				}
				if row == "" {
					file.Diff = append(file.Diff, "")
					continue
				}
				switch row[0] {
				case '+':
					file.Added++
				case '-':
					file.Removed++
				}
				file.Diff = append(file.Diff, row)
			}
		case strings.HasPrefix(line, "*** Delete File: "):
			file := getFile(strings.TrimPrefix(line, "*** Delete File: "))
			if file != nil {
				file.Removed++
				file.Diff = append(file.Diff, "-<deleted file>")
			}
		}
	}

	return files
}

func splitRawLines(v string) []string {
	v = strings.ReplaceAll(v, "\r\n", "\n")
	return strings.Split(v, "\n")
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

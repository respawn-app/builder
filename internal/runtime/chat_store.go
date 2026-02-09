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
	"path/filepath"
	"strings"
	"sync"
)

const (
	agentsInjectedPrefix      = "# AGENTS.md auto-injection"
	defaultShellTimeoutSecond = toolcodec.DefaultShellTimeoutSecs
)

type ChatEntry struct {
	Role       string
	Text       string
	ToolCallID string
	ToolCall   *transcript.ToolCallMeta
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
	s.items = append(s.items, llm.ItemsFromMessages([]llm.Message{msg})...)
}

func (s *chatStore) replaceHistory(items []llm.ResponseItem) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items = llm.CloneResponseItems(items)
	s.messages = llm.MessagesFromItems(items)
}

func (s *chatStore) snapshotItems() []llm.ResponseItem {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return llm.CloneResponseItems(s.items)
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
			if strings.HasPrefix(content, prompts.CompactionSummaryPrefix+"\n") {
				continue
			}
			entries = append(entries, ChatEntry{Role: "user", Text: msg.Content})
		case llm.RoleAssistant:
			if strings.TrimSpace(msg.Content) != "" {
				entries = append(entries, ChatEntry{Role: "assistant", Text: msg.Content})
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
		}
	}
	entries = append(entries, s.local...)
	return ChatSnapshot{
		Entries:      entries,
		Ongoing:      s.ongoing,
		OngoingError: s.ongoingError,
	}
}

func (s *chatStore) formatToolCall(call llm.ToolCall) ChatEntry {
	meta := &transcript.ToolCallMeta{
		ToolName: strings.TrimSpace(call.Name),
		IsShell:  strings.TrimSpace(call.Name) == string(tools.ToolShell),
	}
	if strings.TrimSpace(call.Name) == string(tools.ToolPatch) {
		if summary, detail, ok := s.formatPatchToolCall(call.Input); ok {
			meta.PatchSummary = summary
			meta.PatchDetail = detail
			return ChatEntry{
				Role:       "tool_call",
				Text:       summary,
				ToolCallID: strings.TrimSpace(call.ID),
				ToolCall:   meta,
			}
		}
	}
	command, timeoutLabel := toolcodec.FormatInput(strings.TrimSpace(call.Name), call.Input, defaultShellTimeoutSecond)
	command = strings.TrimSpace(command)
	if command == "" {
		command = "tool call"
	}
	meta.Command = command
	meta.TimeoutLabel = timeoutLabel
	return ChatEntry{
		Role:       "tool_call",
		Text:       command,
		ToolCallID: strings.TrimSpace(call.ID),
		ToolCall:   meta,
	}
}

func formatToolResult(result tools.Result) string {
	if result.Name == tools.ToolPatch && !result.IsError {
		return ""
	}
	output := strings.TrimSpace(toolcodec.FormatOutput(result.Output))
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

func (s *chatStore) formatPatchToolCall(raw json.RawMessage) (summary, detail string, ok bool) {
	var input map[string]json.RawMessage
	if err := json.Unmarshal(raw, &input); err != nil {
		return "", "", false
	}
	patchRaw, ok := input["patch"]
	if !ok {
		return "", "", false
	}
	var patchText string
	if err := json.Unmarshal(patchRaw, &patchText); err != nil {
		return "", "", false
	}
	files := parsePatchFileViews(patchText, s.cwd)
	if len(files) == 0 {
		return "", "", false
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

	return strings.Join(summaryLines, "\n"), strings.Join(detailLines, "\n"), true
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

func formatToolOutput(raw json.RawMessage) string {
	return toolcodec.FormatOutput(raw)
}

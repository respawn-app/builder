package toolcodec

import (
	"builder/internal/tools"
	"encoding/json"
	"strings"
)

const (
	InlineMetaSeparator     = "\x1f"
	DefaultShellTimeoutSecs = tools.DefaultShellTimeoutSeconds
	defaultToolCallFallback = "tool call"
)

func SplitInlineMeta(line string) (string, string) {
	parts := strings.SplitN(line, InlineMetaSeparator, 2)
	command := strings.TrimSpace(parts[0])
	if len(parts) == 1 {
		return command, ""
	}
	return command, strings.TrimSpace(parts[1])
}

func CompactCallText(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return defaultToolCallFallback
	}
	parts := strings.SplitN(trimmed, "\n", 2)
	first := strings.TrimSpace(parts[0])
	if first == "" {
		return defaultToolCallFallback
	}
	command, _ := SplitInlineMeta(first)
	if command == "" {
		return defaultToolCallFallback
	}
	return command
}

func FormatInput(toolName string, raw json.RawMessage, shellTimeoutSeconds int) (string, string) {
	id, ok := tools.ParseID(strings.TrimSpace(toolName))
	if !ok {
		return strings.TrimSpace(string(raw)), ""
	}
	def, ok := tools.DefinitionFor(id)
	if !ok {
		return strings.TrimSpace(string(raw)), ""
	}
	meta := def.BuildToolCallMeta(tools.ToolCallContext{DefaultShellTimeoutSeconds: shellTimeoutSeconds}, raw)
	return strings.TrimSpace(meta.Command), strings.TrimSpace(meta.InlineMeta)
}

func FormatOutput(raw json.RawMessage) string {
	return tools.FormatGenericOutput(raw)
}

func FormatOutputForTool(toolName string, raw json.RawMessage) string {
	id, ok := tools.ParseID(strings.TrimSpace(toolName))
	if !ok {
		return tools.FormatGenericOutput(raw)
	}
	def, ok := tools.DefinitionFor(id)
	if !ok {
		return tools.FormatGenericOutput(raw)
	}
	return def.FormatToolResult(tools.Result{Name: id, Output: raw})
}

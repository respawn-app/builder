package toolcodec

import (
	"builder/internal/tools"
	"encoding/json"
	"strings"
)

const (
	InlineMetaSeparator     = tools.InlineMetaSeparator
	DefaultShellTimeoutSecs = tools.DefaultShellTimeoutSeconds
)

func SplitInlineMeta(line string) (string, string) {
	return tools.SplitInlineMeta(line)
}

func CompactCallText(text string) string {
	return tools.CompactToolCallText(nil, text)
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
	return def.FormatToolInput(tools.ToolCallContext{DefaultShellTimeoutSeconds: shellTimeoutSeconds}, raw)
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

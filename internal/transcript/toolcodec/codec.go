package toolcodec

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	InlineMetaSeparator     = "\x1f"
	ShellCallPrefix         = "\x1eshell_call\x1e"
	PatchPayloadPrefix      = "\x1epatch_payload\x1e"
	PatchPayloadSeparator   = "\x1epatch_sep\x1e"
	DefaultShellTimeoutSecs = 300
	defaultToolCallFallback = "tool call"
	shellToolNameNormalized = "shell"
)

func EncodeInlineCall(command, timeoutLabel string, isShell bool) string {
	command = strings.TrimSpace(command)
	if command == "" {
		return ""
	}
	if isShell {
		command = ShellCallPrefix + command
	}
	timeoutLabel = strings.TrimSpace(timeoutLabel)
	if timeoutLabel == "" {
		return command
	}
	return command + InlineMetaSeparator + timeoutLabel
}

func EncodePatchPayload(summary, detail string) string {
	summary = strings.TrimSpace(summary)
	detail = strings.TrimSpace(detail)
	if summary == "" || detail == "" {
		return ""
	}
	return PatchPayloadPrefix + summary + PatchPayloadSeparator + detail
}

func DecodePatchPayload(text string) (string, string, bool) {
	if !strings.HasPrefix(text, PatchPayloadPrefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(text, PatchPayloadPrefix)
	parts := strings.SplitN(rest, PatchPayloadSeparator, 2)
	if len(parts) != 2 {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func StripShellCallPrefix(text string) (string, bool) {
	if !strings.HasPrefix(text, ShellCallPrefix) {
		return text, false
	}
	return strings.TrimPrefix(text, ShellCallPrefix), true
}

func IsShellToolCall(text string) bool {
	_, ok := StripShellCallPrefix(text)
	return ok
}

func SplitInlineMeta(line string) (string, string) {
	parts := strings.SplitN(line, InlineMetaSeparator, 2)
	command := strings.TrimSpace(parts[0])
	if stripped, ok := StripShellCallPrefix(command); ok {
		command = stripped
	}
	if len(parts) == 1 {
		return command, ""
	}
	return command, strings.TrimSpace(parts[1])
}

func CompactCallText(text string) string {
	if summary, _, ok := DecodePatchPayload(text); ok {
		return strings.TrimSpace(summary)
	}
	if shellText, ok := StripShellCallPrefix(text); ok {
		text = shellText
	}
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
	var payload any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return strings.TrimSpace(string(raw)), ""
	}
	obj, ok := payload.(map[string]any)
	if !ok {
		return renderPlain(payload), ""
	}
	if cmd, ok := asString(obj["command"]); ok {
		timeout := ""
		if secs, ok := asInt(obj["timeout_seconds"]); ok && secs > 0 {
			timeout = "timeout: " + formatDurationShort(time.Duration(secs)*time.Second)
		} else if strings.TrimSpace(toolName) == shellToolNameNormalized {
			if shellTimeoutSeconds <= 0 {
				shellTimeoutSeconds = DefaultShellTimeoutSecs
			}
			timeout = "timeout: " + formatDurationShort(time.Duration(shellTimeoutSeconds)*time.Second)
		}
		return cmd, timeout
	}
	return renderPlain(payload), ""
}

func FormatOutput(raw json.RawMessage) string {
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

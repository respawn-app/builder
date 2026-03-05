package tui

import (
	"builder/internal/llm"
	"builder/internal/transcript"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

func isToolResultRole(role string) bool {
	switch strings.TrimSpace(role) {
	case "tool_result", "tool_result_ok", "tool_result_error":
		return true
	default:
		return false
	}
}

func findMatchingToolResultIndex(entries []TranscriptEntry, callIdx int, consumed map[int]struct{}) int {
	if callIdx < 0 || callIdx >= len(entries) {
		return -1
	}
	callID := strings.TrimSpace(entries[callIdx].ToolCallID)
	nextIdx := callIdx + 1
	if nextIdx < len(entries) {
		if _, used := consumed[nextIdx]; !used && isToolResultRole(entries[nextIdx].Role) {
			nextCallID := strings.TrimSpace(entries[nextIdx].ToolCallID)
			if callID == nextCallID {
				return nextIdx
			}
		}
	}
	if callID == "" {
		return -1
	}
	for i := callIdx + 1; i < len(entries); i++ {
		if _, used := consumed[i]; used || !isToolResultRole(entries[i].Role) {
			continue
		}
		if strings.TrimSpace(entries[i].ToolCallID) == callID {
			return i
		}
	}
	return -1
}

func toolBlockRoleFromResult(role, baseRole string) string {
	if strings.TrimSpace(role) == "tool_result_error" {
		if baseRole == "tool_question" {
			return "tool_question_error"
		}
		if baseRole == "tool_shell" {
			return "tool_shell_error"
		}
		return "tool_error"
	}
	if isToolResultRole(role) {
		if baseRole == "tool_question" {
			return "tool_question"
		}
		if baseRole == "tool_shell" {
			return "tool_shell_success"
		}
		return "tool_success"
	}
	if baseRole == "tool_shell" {
		return "tool_shell"
	}
	return "tool"
}

func (m Model) roleSymbol(role string) string {
	prefix := rolePrefix(role)
	if prefix == "" {
		return ""
	}
	switch role {
	case "tool", "tool_success", "tool_error", "tool_shell", "tool_shell_success", "tool_shell_error", "tool_question", "tool_question_error":
		return styleForRole(role, m.palette()).Render(prefix)
	case "error":
		return styleForRole(role, m.palette()).Render(prefix)
	case "compaction_notice", "compaction_summary", "reviewer_status", "reviewer_suggestions":
		return styleForRole(role, m.palette()).Render(prefix)
	default:
		return prefix
	}
}

func rolePrefix(role string) string {
	switch role {
	case "user":
		return "❯"
	case "assistant", "assistant_commentary":
		return "❮"
	case "tool", "tool_success", "tool_error":
		return "•"
	case "tool_shell", "tool_shell_success", "tool_shell_error":
		return "$"
	case "tool_question", "tool_question_error":
		return "?"
	case "compaction_notice", "compaction_summary":
		return "@"
	case "reviewer_status", "reviewer_suggestions":
		return "@"
	case "error":
		return "!"
	default:
		return ""
	}
}

func isThinkingRole(role string) bool {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "thinking", "thinking_trace", "reasoning":
		return true
	default:
		return false
	}
}

func styleForRole(role string, p palette) lipgloss.Style {
	switch role {
	case "user":
		return p.user
	case "assistant":
		return p.model
	case "assistant_commentary":
		return p.model.Faint(true)
	case "tool_call", "tool_result":
		return p.tool
	case "tool_success", "tool_result_ok":
		return p.toolSuccess
	case "tool_error", "tool_result_error":
		return p.toolError
	case "tool_shell":
		return p.tool
	case "tool_shell_success":
		return p.toolSuccess
	case "tool_shell_error":
		return p.toolError
	case "tool_question":
		return p.user
	case "tool_question_error":
		return p.toolError
	case "system":
		return p.system
	case "reasoning", "thinking_trace":
		return p.system
	case "error":
		return p.error
	case "compaction_notice", "compaction_summary", "reviewer_status", "reviewer_suggestions":
		return p.compaction
	default:
		return p.preview
	}
}

func (m Model) entryRole(entry TranscriptEntry) string {
	role := strings.TrimSpace(entry.Role)
	if role == "assistant" && entry.Phase == llm.MessagePhaseCommentary {
		return "assistant_commentary"
	}
	return role
}

type palette struct {
	preview     lipgloss.Style
	user        lipgloss.Style
	model       lipgloss.Style
	tool        lipgloss.Style
	toolSuccess lipgloss.Style
	toolError   lipgloss.Style
	system      lipgloss.Style
	error       lipgloss.Style
	compaction  lipgloss.Style

	diffAddBackgroundLight    string
	diffRemoveBackgroundLight string
	diffAddBackgroundDark     string
	diffRemoveBackgroundDark  string
}

func (m Model) palette() palette {
	base := lipgloss.AdaptiveColor{Light: "#5C6370", Dark: "#7F848E"}
	user := lipgloss.AdaptiveColor{Light: "#005CC5", Dark: "#61AFEF"}
	model := lipgloss.AdaptiveColor{Light: "#22863A", Dark: "#98C379"}
	tool := lipgloss.AdaptiveColor{Light: "#8A63D2", Dark: "#C678DD"}
	toolSuccess := lipgloss.AdaptiveColor{Light: "#22863A", Dark: "#98C379"}
	toolError := lipgloss.AdaptiveColor{Light: "#D73A49", Dark: "#E06C75"}
	system := lipgloss.AdaptiveColor{Light: "#6A737D", Dark: "#ABB2BF"}
	err := lipgloss.AdaptiveColor{Light: "#D73A49", Dark: "#E06C75"}
	compaction := lipgloss.AdaptiveColor{Light: "#8A5A00", Dark: "#E5C07B"}
	if m.theme == "light" {
		base = lipgloss.AdaptiveColor{Light: "#5C6370", Dark: "#5C6370"}
	}
	return palette{
		preview:     lipgloss.NewStyle().Foreground(base),
		user:        lipgloss.NewStyle().Foreground(user),
		model:       lipgloss.NewStyle().Foreground(model),
		tool:        lipgloss.NewStyle().Foreground(tool),
		toolSuccess: lipgloss.NewStyle().Foreground(toolSuccess),
		toolError:   lipgloss.NewStyle().Foreground(toolError),
		system:      lipgloss.NewStyle().Foreground(system).Faint(true),
		error:       lipgloss.NewStyle().Foreground(err),
		compaction:  lipgloss.NewStyle().Foreground(compaction),

		diffAddBackgroundLight:    "#E6FFED",
		diffRemoveBackgroundLight: "#FFECEF",
		diffAddBackgroundDark:     "#1F2A22",
		diffRemoveBackgroundDark:  "#2B1F22",
	}
}

func normalizeTheme(theme string) string {
	if strings.EqualFold(strings.TrimSpace(theme), "light") {
		return "light"
	}
	return "dark"
}

func splitLines(v string) []string {
	if v == "" {
		return []string{""}
	}
	return strings.Split(v, "\n")
}

func clamp(value, minValue, maxValue int) int {
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

func cloneToolCallMeta(in *transcript.ToolCallMeta) *transcript.ToolCallMeta {
	if in == nil {
		return nil
	}
	out := *in
	if in.RenderHint != nil {
		hint := *in.RenderHint
		out.RenderHint = &hint
	}
	if len(in.Suggestions) > 0 {
		out.Suggestions = append([]string(nil), in.Suggestions...)
	}
	return &out
}

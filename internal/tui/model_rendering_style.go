package tui

import (
	"builder/internal/llm"
	"builder/internal/theme"
	"builder/internal/transcript"
	"fmt"
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

type toolResultIndex struct {
	results map[string][]int
	cursors map[string]int
}

func buildToolResultIndex(entries []TranscriptEntry) toolResultIndex {
	index := toolResultIndex{
		results: make(map[string][]int),
		cursors: make(map[string]int),
	}
	for idx, entry := range entries {
		if !isToolResultRole(entry.Role) {
			continue
		}
		callID := strings.TrimSpace(entry.ToolCallID)
		if callID == "" {
			continue
		}
		index.results[callID] = append(index.results[callID], idx)
	}
	return index
}

func (index toolResultIndex) findMatchingToolResultIndex(entries []TranscriptEntry, callIdx int, consumed map[int]struct{}) int {
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
	results := index.results[callID]
	for cursor := index.cursors[callID]; cursor < len(results); cursor++ {
		resultIdx := results[cursor]
		if resultIdx <= callIdx {
			index.cursors[callID] = cursor + 1
			continue
		}
		if _, used := consumed[resultIdx]; used {
			index.cursors[callID] = cursor + 1
			continue
		}
		index.cursors[callID] = cursor
		return resultIdx
	}
	index.cursors[callID] = len(results)
	return -1
}

func toolBlockRoleFromResult(role, baseRole string) string {
	if strings.TrimSpace(role) == "tool_result_error" {
		if baseRole == "tool_question" {
			return "tool_question_error"
		}
		if baseRole == "tool_web_search" {
			return "tool_web_search_error"
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
		if baseRole == "tool_web_search" {
			return "tool_web_search_success"
		}
		if baseRole == "tool_shell" {
			return "tool_shell_success"
		}
		return "tool_success"
	}
	if baseRole == "tool_shell" {
		return "tool_shell"
	}
	if baseRole == "tool_web_search" {
		return "tool_web_search"
	}
	return "tool"
}

func (m Model) roleSymbol(role string) string {
	prefix := rolePrefix(role)
	if prefix == "" {
		return ""
	}
	switch role {
	case "tool", "tool_success", "tool_error", "tool_shell", "tool_shell_success", "tool_shell_error", "tool_question", "tool_question_error", "tool_web_search", "tool_web_search_success", "tool_web_search_error":
		return styleForRole(role, m.palette()).Render(prefix)
	case "error", "warning":
		return styleForRole(role, m.palette()).Render(prefix)
	case "reviewer_status", "reviewer_suggestions":
		return styleForRole(role, m.palette()).Render(prefix)
	default:
		if isCompactionRole(role) {
			return styleForRole(role, m.palette()).Render(prefix)
		}
		return prefix
	}
}

func rolePrefix(role string) string {
	if isCompactionRole(role) {
		return "@"
	}
	switch role {
	case "user":
		return "❯"
	case "assistant", "assistant_commentary":
		return "❮"
	case "tool", "tool_success", "tool_error":
		return "•"
	case "tool_web_search", "tool_web_search_success", "tool_web_search_error":
		return "@"
	case "tool_shell", "tool_shell_success", "tool_shell_error":
		return "$"
	case "tool_question", "tool_question_error":
		return "?"
	case "reviewer_status", "reviewer_suggestions":
		return "§"
	case "error":
		return "!"
	case "warning":
		return "⚠"
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
	if isCompactionRole(role) {
		return p.compaction
	}
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
	case "tool_web_search":
		return p.tool
	case "tool_web_search_success":
		return p.toolSuccess
	case "tool_web_search_error":
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
	case "warning":
		return p.warning
	case "reviewer_status", "reviewer_suggestions":
		return p.success
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
	foregroundColor rgbColor
	preview         lipgloss.Style
	previewColor    rgbColor
	successColor    rgbColor
	warningColor    rgbColor
	errorColor      rgbColor
	user            lipgloss.Style
	model           lipgloss.Style
	tool            lipgloss.Style
	toolSuccess     lipgloss.Style
	toolError       lipgloss.Style
	system          lipgloss.Style
	error           lipgloss.Style
	warning         lipgloss.Style
	success         lipgloss.Style
	compaction      lipgloss.Style

	diffAddBackgroundLight    string
	diffRemoveBackgroundLight string
	diffAddBackgroundDark     string
	diffRemoveBackgroundDark  string
}

func (m Model) palette() palette {
	const foregroundLight = "#383A42"
	const foregroundDark = "#ABB2BF"
	const previewLight = "#5C6370"
	const previewDark = "#7F848E"
	base := lipgloss.AdaptiveColor{Light: previewLight, Dark: previewDark}
	foregroundColor := rgbColorFromHex(foregroundDark)
	previewColor := rgbColorFromHex(previewDark)
	user := lipgloss.AdaptiveColor{Light: "#005CC5", Dark: "#61AFEF"}
	model := lipgloss.AdaptiveColor{Light: "#22863A", Dark: "#98C379"}
	tool := lipgloss.AdaptiveColor{Light: "#4078F2", Dark: "#61AFEF"}
	toolSuccess := lipgloss.AdaptiveColor{Light: "#22863A", Dark: "#98C379"}
	toolError := lipgloss.AdaptiveColor{Light: "#D73A49", Dark: "#E06C75"}
	system := lipgloss.AdaptiveColor{Light: "#6A737D", Dark: "#ABB2BF"}
	err := lipgloss.AdaptiveColor{Light: "#D73A49", Dark: "#E06C75"}
	warning := lipgloss.AdaptiveColor{Light: "#8A5A00", Dark: "#E5C07B"}
	success := lipgloss.AdaptiveColor{Light: "#22863A", Dark: "#98C379"}
	compaction := lipgloss.AdaptiveColor{Light: "#8A5A00", Dark: "#E5C07B"}
	if m.theme == "light" {
		base = lipgloss.AdaptiveColor{Light: previewLight, Dark: previewLight}
		foregroundColor = rgbColorFromHex(foregroundLight)
		previewColor = rgbColorFromHex(previewLight)
	}
	return palette{
		foregroundColor: foregroundColor,
		preview:         lipgloss.NewStyle().Foreground(base),
		previewColor:    previewColor,
		successColor:    adaptiveColorRGB(m.theme, success),
		warningColor:    adaptiveColorRGB(m.theme, warning),
		errorColor:      adaptiveColorRGB(m.theme, err),
		user:            lipgloss.NewStyle().Foreground(user),
		model:           lipgloss.NewStyle().Foreground(model),
		tool:            lipgloss.NewStyle().Foreground(tool),
		toolSuccess:     lipgloss.NewStyle().Foreground(toolSuccess),
		toolError:       lipgloss.NewStyle().Foreground(toolError),
		system:          lipgloss.NewStyle().Foreground(system).Faint(true),
		error:           lipgloss.NewStyle().Foreground(err),
		warning:         lipgloss.NewStyle().Foreground(warning),
		success:         lipgloss.NewStyle().Foreground(success),
		compaction:      lipgloss.NewStyle().Foreground(compaction),

		diffAddBackgroundLight:    "#E6FFED",
		diffRemoveBackgroundLight: "#FFECEF",
		diffAddBackgroundDark:     "#1F2A22",
		diffRemoveBackgroundDark:  "#2B1F22",
	}
}

func adaptiveColorRGB(theme string, color lipgloss.AdaptiveColor) rgbColor {
	if strings.EqualFold(strings.TrimSpace(theme), "light") {
		return rgbColorFromHex(color.Light)
	}
	return rgbColorFromHex(color.Dark)
}

func rgbColorFromHex(hex string) rgbColor {
	r, g, b, ok := parseHexColor(hex)
	if !ok {
		return rgbColor{}
	}
	return rgbColor{r: r, g: g, b: b}
}

func themeForegroundColor(theme string) rgbColor {
	if strings.EqualFold(strings.TrimSpace(theme), "light") {
		return rgbColorFromHex("#383A42")
	}
	return rgbColorFromHex("#ABB2BF")
}

func themePreviewColor(theme string) rgbColor {
	if strings.EqualFold(strings.TrimSpace(theme), "light") {
		return rgbColorFromHex("#5C6370")
	}
	return rgbColorFromHex("#7F848E")
}

func themeSuccessColor(theme string) rgbColor {
	if strings.EqualFold(strings.TrimSpace(theme), "light") {
		return rgbColorFromHex("#22863A")
	}
	return rgbColorFromHex("#98C379")
}

func themeWarningColor(theme string) rgbColor {
	if strings.EqualFold(strings.TrimSpace(theme), "light") {
		return rgbColorFromHex("#8A5A00")
	}
	return rgbColorFromHex("#E5C07B")
}

func themeErrorColor(theme string) rgbColor {
	if strings.EqualFold(strings.TrimSpace(theme), "light") {
		return rgbColorFromHex("#D73A49")
	}
	return rgbColorFromHex("#E06C75")
}

func (c rgbColor) hexString() string {
	return fmt.Sprintf("#%02X%02X%02X", c.r, c.g, c.b)
}

func normalizeTheme(themeName string) string {
	return theme.Resolve(themeName)
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
	out = transcript.NormalizeToolCallMeta(out)
	if in.RenderHint != nil {
		hint := *in.RenderHint
		out.RenderHint = &hint
	}
	if len(in.Suggestions) > 0 {
		out.Suggestions = append([]string(nil), in.Suggestions...)
	}
	return &out
}

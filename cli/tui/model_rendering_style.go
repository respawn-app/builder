package tui

import (
	"builder/server/llm"
	"builder/shared/theme"
	"builder/shared/transcript"
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
	p := m.palette()
	switch role {
	case "tool", "tool_success", "tool_error", "tool_shell", "tool_shell_success", "tool_shell_error", "tool_question", "tool_question_error", "tool_web_search", "tool_web_search_success", "tool_web_search_error":
		return renderRoleSymbol(prefix, roleSymbolStyle(role, p))
	case "error", "warning", "cache_warning", roleDeveloperFeedback, roleInterruption:
		return renderRoleSymbol(prefix, roleSymbolStyle(role, p))
	case "reviewer_status", "reviewer_suggestions":
		return renderRoleSymbol(prefix, roleSymbolStyle(role, p))
	default:
		if isCompactionRole(role) {
			return renderRoleSymbol(prefix, roleSymbolStyle(role, p))
		}
		return prefix
	}
}

type roleSymbolColorStyle struct {
	color rgbColor
	faint bool
}

func roleSymbolStyle(role string, p palette) roleSymbolColorStyle {
	if isCompactionRole(role) {
		return roleSymbolColorStyle{color: p.compactionColor}
	}
	switch role {
	case "tool_success", "tool_shell_success", "tool_web_search_success":
		return roleSymbolColorStyle{color: p.toolSuccessColor}
	case "reviewer_status", "reviewer_suggestions":
		return roleSymbolColorStyle{color: p.successColor}
	case "tool_error", "tool_shell_error", "tool_web_search_error":
		return roleSymbolColorStyle{color: p.toolErrorColor}
	case "tool_question":
		return roleSymbolColorStyle{color: p.userColor}
	case "tool_question_error", "error", "cache_warning", roleDeveloperFeedback, roleInterruption:
		return roleSymbolColorStyle{color: p.errorColor}
	case "warning":
		return roleSymbolColorStyle{color: p.warningColor}
	case "tool", "tool_shell", "tool_web_search":
		return roleSymbolColorStyle{color: p.toolColor}
	default:
		return roleSymbolColorStyle{color: p.foregroundColor}
	}
}

func renderRoleSymbol(prefix string, style roleSymbolColorStyle) string {
	transform := ansiStyleTransform{DefaultForeground: &style.color, ForceFaint: style.faint}
	return styleEscape(transform, false) + prefix + "\x1b[0m"
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
	case "cache_warning":
		return "⚠"
	case roleDeveloperFeedback:
		return "!"
	case roleInterruption:
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
	case roleDeveloperContext:
		return p.preview
	case roleDeveloperFeedback:
		return p.warning
	case roleInterruption:
		return p.warning
	case "reasoning", "thinking_trace":
		return p.system
	case "error":
		return p.error
	case "cache_warning":
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
	foregroundColor  rgbColor
	preview          lipgloss.Style
	previewColor     rgbColor
	userColor        rgbColor
	modelColor       rgbColor
	toolColor        rgbColor
	toolSuccessColor rgbColor
	toolErrorColor   rgbColor
	successColor     rgbColor
	warningColor     rgbColor
	errorColor       rgbColor
	compactionColor  rgbColor
	user             lipgloss.Style
	model            lipgloss.Style
	tool             lipgloss.Style
	toolSuccess      lipgloss.Style
	toolError        lipgloss.Style
	system           lipgloss.Style
	error            lipgloss.Style
	warning          lipgloss.Style
	success          lipgloss.Style
	compaction       lipgloss.Style
	selection        lipgloss.Style

	diffAddBackground    string
	diffRemoveBackground string
}

func (m Model) palette() palette {
	tokens := theme.ResolvePalette(m.theme)
	return palette{
		foregroundColor:  rgbColorFromHex(tokens.Transcript.Foreground.TrueColor),
		preview:          lipgloss.NewStyle().Foreground(tokens.Transcript.Subdued.Lipgloss()),
		previewColor:     rgbColorFromHex(tokens.Transcript.Subdued.TrueColor),
		userColor:        rgbColorFromHex(tokens.Transcript.User.TrueColor),
		modelColor:       rgbColorFromHex(tokens.Transcript.Assistant.TrueColor),
		toolColor:        rgbColorFromHex(tokens.Transcript.Tool.TrueColor),
		toolSuccessColor: rgbColorFromHex(tokens.Transcript.ToolSuccess.TrueColor),
		toolErrorColor:   rgbColorFromHex(tokens.Transcript.ToolError.TrueColor),
		successColor:     rgbColorFromHex(tokens.Transcript.Success.TrueColor),
		warningColor:     rgbColorFromHex(tokens.Transcript.Warning.TrueColor),
		errorColor:       rgbColorFromHex(tokens.Transcript.Error.TrueColor),
		compactionColor:  rgbColorFromHex(tokens.Transcript.Compaction.TrueColor),
		user:             lipgloss.NewStyle().Foreground(tokens.Transcript.User.Lipgloss()),
		model:            lipgloss.NewStyle().Foreground(tokens.Transcript.Assistant.Lipgloss()),
		tool:             lipgloss.NewStyle().Foreground(tokens.Transcript.Tool.Lipgloss()),
		toolSuccess:      lipgloss.NewStyle().Foreground(tokens.Transcript.ToolSuccess.Lipgloss()),
		toolError:        lipgloss.NewStyle().Foreground(tokens.Transcript.ToolError.Lipgloss()),
		system:           lipgloss.NewStyle().Foreground(tokens.Transcript.System.Lipgloss()).Faint(true),
		error:            lipgloss.NewStyle().Foreground(tokens.Transcript.Error.Lipgloss()),
		warning:          lipgloss.NewStyle().Foreground(tokens.Transcript.Warning.Lipgloss()),
		success:          lipgloss.NewStyle().Foreground(tokens.Transcript.Success.Lipgloss()),
		compaction:       lipgloss.NewStyle().Foreground(tokens.Transcript.Compaction.Lipgloss()),
		selection: lipgloss.NewStyle().
			Background(tokens.Transcript.SelectionBackground.Lipgloss()).
			Foreground(tokens.Transcript.SelectionForeground.Lipgloss()),

		diffAddBackground:    tokens.Transcript.DiffAddBackground.TrueColor,
		diffRemoveBackground: tokens.Transcript.DiffRemoveBackground.TrueColor,
	}
}

func rgbColorFromHex(hex string) rgbColor {
	r, g, b, ok := parseHexColor(hex)
	if !ok {
		return rgbColor{}
	}
	return rgbColor{r: r, g: g, b: b}
}

func themeForegroundColor(themeName string) rgbColor {
	return rgbColorFromHex(theme.ResolvePalette(themeName).Transcript.Foreground.TrueColor)
}

func themePreviewColor(themeName string) rgbColor {
	return rgbColorFromHex(theme.ResolvePalette(themeName).Transcript.Subdued.TrueColor)
}

func themeSuccessColor(themeName string) rgbColor {
	return rgbColorFromHex(theme.ResolvePalette(themeName).Transcript.Success.TrueColor)
}

func themeWarningColor(themeName string) rgbColor {
	return rgbColorFromHex(theme.ResolvePalette(themeName).Transcript.Warning.TrueColor)
}

func themeErrorColor(themeName string) rgbColor {
	return rgbColorFromHex(theme.ResolvePalette(themeName).Transcript.Error.TrueColor)
}

func (m Model) ansiIntentPalette() ansiIntentPalette {
	colors := m.palette()
	return ansiIntentPalette{
		ThemeForeground:   colors.foregroundColor,
		SubduedForeground: colors.previewColor,
		SuccessForeground: colors.successColor,
		WarningForeground: colors.warningColor,
		ErrorForeground:   colors.errorColor,
	}
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

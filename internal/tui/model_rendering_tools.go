package tui

import (
	"builder/internal/shared/textutil"
	"builder/internal/tools"
	"builder/internal/transcript"
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	xansi "github.com/charmbracelet/x/ansi"
)

func detailDivider() string {
	return TranscriptDivider
}

func ongoingDividerGroup(role string) string {
	trimmed := strings.TrimSpace(role)
	if isToolHeadlineRole(trimmed) {
		return "tool"
	}
	return strings.ToLower(trimmed)
}

func skipInOngoing(role string) bool {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "thinking", "thinking_trace", "reasoning", "compaction_summary", "reviewer_suggestions", "error":
		return true
	default:
		return false
	}
}

func compactToolCallText(meta *transcript.ToolCallMeta, text string) string {
	return tools.CompactToolCallText(meta, text)
}

func compactOngoingShellPreviewText(command string) string {
	normalized := textutil.NormalizeCRLF(command)
	if !strings.Contains(normalized, "\n") {
		return command
	}
	for _, line := range strings.Split(normalized, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		return trimmed + "\n…"
	}
	return "…"
}

func shellPreviewShouldCollapse(command string) bool {
	return strings.Contains(textutil.NormalizeCRLF(command), "\n")
}

func compactReviewerStatusForOngoing(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}
	for _, line := range strings.Split(trimmed, "\n") {
		candidate := strings.TrimSpace(line)
		if candidate != "" {
			return candidate
		}
	}
	return trimmed
}

func isReviewerCacheHitLine(text string) bool {
	line := strings.ToLower(strings.TrimSpace(xansi.Strip(text)))
	if line == "" {
		return false
	}
	if !strings.HasSuffix(line, "cache hit") {
		return false
	}
	prefix := strings.TrimSpace(strings.TrimSuffix(line, "cache hit"))
	if !strings.HasSuffix(prefix, "%") {
		return false
	}
	digits := strings.TrimSpace(strings.TrimSuffix(prefix, "%"))
	if digits == "" {
		return false
	}
	for _, r := range digits {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func askQuestionDisplay(meta *transcript.ToolCallMeta, text string) (string, []string) {
	question := ""
	suggestions := make([]string, 0)
	if meta != nil {
		question = normalizeAskQuestionQuestion(meta.Question)
		if question == "" {
			question = normalizeAskQuestionQuestion(meta.Command)
		}
		for _, suggestion := range meta.Suggestions {
			trimmed := normalizeAskQuestionSuggestion(suggestion)
			if trimmed == "" {
				continue
			}
			suggestions = append(suggestions, trimmed)
		}
	}
	if question == "" {
		question = normalizeAskQuestionQuestion(text)
	}
	if question == "" {
		question = "ask_question"
	}
	return question, suggestions
}

func normalizeAskQuestionQuestion(question string) string {
	trimmed := strings.TrimSpace(question)
	if trimmed == "" {
		return ""
	}
	if strings.EqualFold(trimmed, "ask_question") {
		return ""
	}
	if strings.HasPrefix(strings.ToLower(trimmed), "question:") {
		trimmed = strings.TrimSpace(trimmed[len("question:"):])
	}
	return trimmed
}

func normalizeAskQuestionSuggestion(suggestion string) string {
	trimmed := strings.TrimSpace(suggestion)
	trimmed = strings.TrimPrefix(trimmed, "-")
	return strings.TrimSpace(trimmed)
}

func (m Model) flattenAskQuestionEntry(role, question string, suggestions []string, answer string, includeSuggestions bool) []string {
	renderWidth := m.viewportWidth
	if rolePrefix(role) != "" {
		renderWidth -= 2
	}
	if renderWidth < 1 {
		renderWidth = 1
	}

	type askQuestionLine struct {
		text string
		kind string
	}

	lines := make([]askQuestionLine, 0, len(suggestions)+4)
	question = strings.TrimSpace(question)
	if question == "" {
		question = "ask question"
	}
	for _, line := range splitLines(wrapTextForViewport(question, renderWidth)) {
		lines = append(lines, askQuestionLine{text: line, kind: "question"})
	}
	if includeSuggestions {
		for _, suggestion := range suggestions {
			suggestion = normalizeAskQuestionSuggestion(suggestion)
			if suggestion == "" {
				continue
			}
			wrapped := splitLines(wrapTextForViewport(suggestion, max(1, renderWidth-2)))
			for idx, line := range wrapped {
				if idx == 0 {
					lines = append(lines, askQuestionLine{text: "- " + line, kind: "suggestion"})
					continue
				}
				lines = append(lines, askQuestionLine{text: "  " + line, kind: "suggestion"})
			}
		}
	}
	answer = strings.TrimSpace(answer)
	if answer != "" {
		for _, line := range splitLines(wrapTextForViewport(answer, renderWidth)) {
			lines = append(lines, askQuestionLine{text: line, kind: "answer"})
		}
	}
	if len(lines) == 0 {
		lines = append(lines, askQuestionLine{text: "", kind: "question"})
	}

	symbol := m.roleSymbol(role)
	out := make([]string, 0, len(lines))
	for idx, line := range lines {
		display := line.text
		switch line.kind {
		case "suggestion":
			display = m.palette().preview.Faint(true).Render(display)
		case "answer":
			if role == "tool_question_error" {
				display = styleForRole(role, m.palette()).Render(display)
			} else {
				display = m.palette().user.Render(display)
			}
		}
		if idx == 0 {
			if symbol == "" {
				out = append(out, display)
				continue
			}
			out = append(out, fmt.Sprintf("%s %s", symbol, display))
			continue
		}
		if strings.TrimSpace(display) == "" {
			out = append(out, "")
			continue
		}
		out = append(out, "  "+display)
	}
	return out
}

func toolCallDisplayText(meta *transcript.ToolCallMeta, text string) string {
	command := strings.TrimSpace(text)
	inlineMeta := ""
	if meta != nil && strings.TrimSpace(meta.Command) != "" {
		command = strings.TrimSpace(meta.Command)
	}
	if meta != nil && strings.TrimSpace(meta.PatchDetail) != "" && strings.TrimSpace(command) == "" {
		command = strings.TrimSpace(meta.PatchDetail)
	}
	if command == "" {
		command = tools.CompactToolCallText(meta, text)
	}
	if meta != nil && meta.Presentation == transcript.ToolPresentationShell && meta.UserInitiated {
		command = "User ran: " + command
	}
	if meta != nil {
		inlineMeta = strings.TrimSpace(meta.InlineMeta)
		if inlineMeta == "" {
			inlineMeta = strings.TrimSpace(meta.TimeoutLabel)
		}
	}
	if inlineMeta == "" {
		return command
	}
	return command + tools.InlineMetaSeparator + inlineMeta
}

func isShellToolCall(meta *transcript.ToolCallMeta, text string) bool {
	if meta != nil {
		return meta.Presentation == transcript.ToolPresentationShell
	}
	_ = text
	return false
}

func isAskQuestionToolCall(meta *transcript.ToolCallMeta) bool {
	if meta == nil {
		return false
	}
	return meta.Presentation == transcript.ToolPresentationAskQuestion
}

func isToolHeadlineRole(role string) bool {
	switch strings.TrimSpace(role) {
	case "tool", "tool_success", "tool_error", "tool_shell", "tool_shell_success", "tool_shell_error", "tool_question", "tool_question_error":
		return true
	default:
		return false
	}
}

func isShellPreviewRole(role string) bool {
	switch strings.TrimSpace(role) {
	case "tool_shell", "tool_shell_success", "tool_shell_error":
		return true
	default:
		return false
	}
}

func splitToolInlineMeta(line string) (string, string) {
	return tools.SplitInlineMeta(line)
}

func (m Model) renderToolHeadline(line string, width int) string {
	command, meta := splitToolInlineMeta(line)
	if meta == "" {
		return command
	}
	metaText := m.palette().preview.Faint(true).Render(meta)
	if command == "" {
		return metaText
	}
	space := width - lipgloss.Width(command) - lipgloss.Width(metaText)
	if space < 1 {
		space = 1
	}
	return command + strings.Repeat(" ", space) + metaText
}

func (m Model) tintToolDiffLine(line, kind string) string {
	if strings.TrimSpace(line) == "" {
		return line
	}
	if width := m.viewportWidth; width > 0 {
		lineWidth := lipgloss.Width(line)
		if lineWidth < width {
			line += strings.Repeat(" ", width-lineWidth)
		}
	}
	addBg, removeBg := m.diffLineBackgroundEscapes()
	if kind == "add" {
		return applyBackgroundTint(line, addBg)
	}
	if kind == "remove" {
		return applyBackgroundTint(line, removeBg)
	}
	return line
}

func (m Model) diffLineBackgroundEscapes() (string, string) {
	p := m.palette()
	if m.theme == "light" {
		return bgEscape(p.diffAddBackgroundLight), bgEscape(p.diffRemoveBackgroundLight)
	}
	return bgEscape(p.diffAddBackgroundDark), bgEscape(p.diffRemoveBackgroundDark)
}

func (m Model) styleToolLine(line string) string {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return line
	}
	if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
		return m.palette().toolSuccess.Render("+") + line[1:]
	}
	if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
		return m.palette().toolError.Render("-") + line[1:]
	}
	successCountStyle := m.palette().toolSuccess
	errorCountStyle := m.palette().toolError
	if strings.HasPrefix(trimmed, "Edited:") {
		return patchCountTokenPattern.ReplaceAllStringFunc(line, func(token string) string {
			if strings.HasPrefix(token, "+") {
				return successCountStyle.Render(token)
			}
			if strings.HasPrefix(token, "-") {
				return errorCountStyle.Render(token)
			}
			return token
		})
	}
	if !strings.HasPrefix(trimmed, "./") {
		return line
	}
	return patchCountTokenPattern.ReplaceAllStringFunc(line, func(token string) string {
		if strings.HasPrefix(token, "+") {
			return successCountStyle.Render(token)
		}
		if strings.HasPrefix(token, "-") {
			return errorCountStyle.Render(token)
		}
		return token
	})
}

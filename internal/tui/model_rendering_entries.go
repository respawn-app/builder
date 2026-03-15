package tui

import (
	"builder/internal/transcript"
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	xansi "github.com/charmbracelet/x/ansi"
)

func (m Model) flattenEntry(role, text string) []string {
	return m.flattenEntryWithMeta(role, text, false, nil)
}

func (m Model) flattenEntryWithMutedText(role, text string, muteText bool) []string {
	return m.flattenEntryWithMeta(role, text, muteText, nil)
}

func (m Model) flattenEntryWithMeta(role, text string, muteText bool, toolMeta *transcript.ToolCallMeta) []string {
	renderWidth := m.viewportWidth
	if rolePrefix(role) != "" {
		renderWidth -= 2
	}
	if isThinkingRole(role) {
		return m.flattenThinkingEntry(role, text, renderWidth)
	}
	type lineWithKind struct {
		text string
		kind string
	}
	rendered := ""
	lines := make([]lineWithKind, 0, 8)
	if !muteText {
		if diffLines, ok := m.renderDiffToolLines(text, renderWidth, toolMeta); ok {
			for _, line := range diffLines {
				item := lineWithKind{text: line.Text}
				switch line.Kind {
				case diffRenderAdd:
					item.kind = "add"
				case diffRenderRemove:
					item.kind = "remove"
				}
				lines = append(lines, item)
			}
		} else {
			rendered = m.renderEntryText(role, text, renderWidth, toolMeta, muteText)
			for _, chunk := range splitLines(rendered) {
				lines = append(lines, lineWithKind{text: chunk})
			}
		}
	} else {
		rendered = m.renderEntryText(role, text, renderWidth, toolMeta, muteText)
		for _, chunk := range splitLines(rendered) {
			lines = append(lines, lineWithKind{text: chunk})
		}
	}
	if len(lines) == 0 {
		lines = []lineWithKind{{text: ""}}
	}
	plainLines := make([]string, 0, len(lines))
	for _, line := range lines {
		plainLines = append(plainLines, line.text)
	}
	isEditedBlock := isEditedToolBlock(plainLines)
	symbol := m.roleSymbol(role)
	out := make([]string, 0, len(lines))
	for i, line := range lines {
		displayChunk := line.text
		diffKind := line.kind
		if isToolHeadlineRole(role) {
			if i == 0 {
				displayChunk = m.renderToolHeadline(displayChunk, renderWidth)
			}
			displayChunk = m.styleToolLine(displayChunk)
		}
		if muteText && strings.TrimSpace(displayChunk) != "" && !isEditedBlock {
			displayChunk = m.palette().preview.Faint(true).Render(displayChunk)
		} else if role == "reviewer_status" && isReviewerCacheHitLine(displayChunk) {
			displayChunk = m.palette().preview.Faint(true).Render(displayChunk)
		} else if isThinkingRole(role) {
			displayChunk = styleForRole(role, m.palette()).Render(displayChunk)
		} else if role == "compaction_notice" || role == "compaction_summary" || role == "reviewer_status" || role == "reviewer_suggestions" || role == "error" {
			displayChunk = styleForRole(role, m.palette()).Render(displayChunk)
		}
		if i == 0 {
			formattedLine := ""
			if symbol == "" {
				formattedLine = displayChunk
			} else {
				formattedLine = fmt.Sprintf("%s %s", symbol, displayChunk)
			}
			if diffKind != "" {
				formattedLine = m.tintToolDiffLine(formattedLine, diffKind)
			}
			out = append(out, formattedLine)
			continue
		}
		if strings.TrimSpace(displayChunk) == "" {
			out = append(out, "")
			continue
		}
		formattedLine := "  " + displayChunk
		if diffKind != "" {
			formattedLine = m.tintToolDiffLine(formattedLine, diffKind)
		}
		out = append(out, formattedLine)
	}
	if muteText && isShellPreviewRole(role) && shellPreviewShouldCollapse(text) {
		ellipsis := "  " + m.palette().preview.Faint(true).Render("…")
		if len(out) == 0 {
			return []string{"", ellipsis}
		}
		return []string{out[0], ellipsis}
	}
	return out
}

func (m Model) flattenThinkingEntry(role, text string, renderWidth int) []string {
	if renderWidth < 1 {
		renderWidth = 1
	}
	chunks := splitLines(wrapTextForViewport(text, renderWidth))
	if len(chunks) == 0 {
		chunks = []string{""}
	}
	style := styleForRole(role, m.palette())
	out := make([]string, 0, len(chunks))
	for i, chunk := range chunks {
		display := style.Render(chunk)
		if i == 0 {
			out = append(out, display)
			continue
		}
		if strings.TrimSpace(chunk) == "" {
			out = append(out, "")
			continue
		}
		out = append(out, "  "+display)
	}
	return out
}

func isEditedToolBlock(lines []string) bool {
	for _, line := range lines {
		trimmed := strings.TrimSpace(xansi.Strip(line))
		if trimmed == "" {
			continue
		}
		return strings.HasPrefix(trimmed, "Edited:")
	}
	return false
}

func (m Model) renderDiffToolLines(text string, width int, toolMeta *transcript.ToolCallMeta) ([]diffRenderedLine, bool) {
	if toolMeta == nil || !toolMeta.HasRenderHint() || m.code == nil {
		return nil, false
	}
	hint := toolMeta.RenderHint
	if hint == nil || hint.Kind != transcript.ToolRenderKindDiff {
		return nil, false
	}
	highlightTarget := text
	prefix := ""
	if hint.ResultOnly {
		parts := strings.SplitN(text, "\n", 2)
		if len(parts) != 2 || strings.TrimSpace(parts[1]) == "" {
			return nil, false
		}
		prefix = parts[0]
		highlightTarget = parts[1]
	}
	lines, ok := m.code.renderDiffLines(highlightTarget, width)
	if !ok {
		return nil, false
	}
	if strings.TrimSpace(prefix) == "" {
		return lines, true
	}
	wrappedPrefix := splitLines(wrapTextForViewport(prefix, width))
	combined := make([]diffRenderedLine, 0, len(wrappedPrefix)+len(lines))
	for _, line := range wrappedPrefix {
		combined = append(combined, diffRenderedLine{Kind: diffRenderMeta, Text: line})
	}
	combined = append(combined, lines...)
	return combined, true
}

func (m Model) flattenEntryPlain(role, text string) []string {
	renderWidth := m.viewportWidth
	if rolePrefix(role) != "" {
		renderWidth -= 2
	}
	chunks := splitLines(wrapTextForViewport(text, renderWidth))
	if len(chunks) == 0 {
		chunks = []string{""}
	}
	symbol := m.roleSymbol(role)
	out := make([]string, 0, len(chunks))
	for i, chunk := range chunks {
		if i == 0 {
			if symbol == "" {
				out = append(out, chunk)
				continue
			}
			out = append(out, fmt.Sprintf("%s %s", symbol, chunk))
			continue
		}
		if strings.TrimSpace(chunk) == "" {
			out = append(out, "")
			continue
		}
		out = append(out, "  "+chunk)
	}
	return out
}

func (m Model) maybeSelectedUserBlock(entryIndex int, role string, lines []string) []string {
	if !m.selectedTranscriptActive {
		return lines
	}
	if entryIndex != m.selectedTranscriptEntry {
		return lines
	}
	if strings.TrimSpace(role) != "user" {
		return lines
	}
	style := lipgloss.NewStyle().Background(lipgloss.Color("15")).Foreground(lipgloss.Color("0"))
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		out = append(out, style.Render(line))
	}
	return out
}

func (m Model) renderEntryText(role, text string, width int, toolMeta *transcript.ToolCallMeta, muteText bool) string {
	if strings.TrimSpace(text) == "" {
		return text
	}
	if isThinkingRole(role) {
		return wrapTextForViewport(text, width)
	}
	if !muteText {
		if highlighted, ok := m.renderToolTextWithHighlight(role, text, width, toolMeta); ok {
			return highlighted
		}
	}
	if !isMarkdownRole(role) {
		return wrapTextForViewport(text, width)
	}
	if m.md == nil {
		return wrapTextForViewport(text, width)
	}
	rendered, err := m.md.render(role, text, width)
	if err != nil {
		return wrapTextForViewport(text, width)
	}
	return rendered
}

func (m Model) renderToolTextWithHighlight(role, text string, width int, toolMeta *transcript.ToolCallMeta) (string, bool) {
	if !isToolHeadlineRole(role) || toolMeta == nil || !toolMeta.HasRenderHint() || m.code == nil {
		return "", false
	}
	hint := toolMeta.RenderHint
	if hint.Kind == transcript.ToolRenderKindDiff {
		return "", false
	}
	highlightTarget := text
	prefix := ""
	if hint.ResultOnly {
		parts := strings.SplitN(text, "\n", 2)
		if len(parts) != 2 || strings.TrimSpace(parts[1]) == "" {
			return "", false
		}
		prefix = parts[0]
		highlightTarget = parts[1]
	}
	rendered, ok := m.code.render(hint, highlightTarget)
	if !ok {
		return "", false
	}
	if prefix != "" {
		rendered = prefix + "\n" + rendered
	}
	return wrapTextForViewport(rendered, width), true
}

func wrapTextForViewport(text string, width int) string {
	if width < 1 {
		width = 1
	}
	wrapped := xansi.Wordwrap(text, width, " ,.;-+|")
	return strings.TrimRight(wrapped, "\n")
}

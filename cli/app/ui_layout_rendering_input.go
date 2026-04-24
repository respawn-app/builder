package app

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

type wrappedAskPromptLine struct {
	Text string
	Line askPromptLine
}

func (l uiViewLayout) renderInputLines(width int, style uiStyles) []string {
	m := l.model
	inputState := m.inputModeState()
	if inputState.Mode == uiInputModeProcessList {
		return []string{padRight("", width)}
	}
	if inputState.Mode == uiInputModeWorktree {
		return []string{padRight("", width)}
	}
	if inputState.Mode == uiInputModeRollbackSelection {
		return nil
	}
	if width < 1 {
		return []string{padRight("", width)}
	}
	if inputState.ShowsAskInput {
		return l.renderAskInputLines(width, style)
	}

	lineStyle := style.input
	if m.isInputLocked() {
		lineStyle = style.inputDisabled
	}
	return renderFramedEditableInputLines(width, inputContentLineLimit(l.effectiveHeight()), l.mainInputRenderSpec(), lineStyle, l.inputBorderStyle())
}

func (l uiViewLayout) renderAskInputLines(width int, style uiStyles) []string {
	if width < 1 {
		return []string{padRight("", width)}
	}
	wrapped := l.visibleAskPromptLines(width)
	selectedStyle := lipgloss.NewStyle().Foreground(uiPalette(l.model.theme).primary).Bold(true)
	recommendedStyle := lipgloss.NewStyle().Foreground(uiPalette(l.model.theme).secondary)
	recommendedNoteStyle := style.meta.Faint(true)
	rendered := make([]string, 0, len(wrapped))
	for _, line := range wrapped {
		switch {
		case line.Line.Kind == askPromptLineKindHint:
			rendered = append(rendered, style.meta.Render(padANSIRight(line.Text, width)))
		case line.Line.Disabled:
			rendered = append(rendered, style.inputDisabled.Render(padANSIRight(line.Text, width)))
		case line.Line.Selected:
			rendered = append(rendered, selectedStyle.Render(padANSIRight(line.Text, width)))
		case line.Line.Recommended:
			rendered = append(rendered, renderRecommendedAskLine(line.Text, line.Line.MutedSuffix, width, recommendedStyle, recommendedNoteStyle))
		default:
			rendered = append(rendered, style.input.Render(padANSIRight(line.Text, width)))
		}
	}
	return l.renderInputFrame(width, rendered)
}

func renderRecommendedAskLine(text string, mutedSuffix string, width int, recommendedStyle lipgloss.Style, noteStyle lipgloss.Style) string {
	body := text
	suffix := ""
	if mutedSuffix != "" && strings.HasSuffix(body, mutedSuffix) {
		body = strings.TrimSuffix(body, mutedSuffix)
		suffix = mutedSuffix
	}
	if strings.HasPrefix(body, "★ ") {
		body = strings.TrimPrefix(body, "★ ")
		body = "★ " + body
	}
	rendered := recommendedStyle.Render(body)
	if suffix != "" {
		rendered += noteStyle.Render(suffix)
	}
	return padANSIRight(rendered, width)
}

func (l uiViewLayout) mainInputPrefix() string {
	if l.model.isInputLocked() {
		return "⨯ "
	}
	return "› "
}

func (l uiViewLayout) mainInputRenderSpec() uiEditableInputRenderSpec {
	return uiEditableInputRenderSpec{
		Prefix:       l.mainInputPrefix(),
		Text:         l.model.input,
		CursorIndex:  l.model.inputCursor,
		RenderCursor: l.shouldRenderSoftCursor(),
	}
}

func (l uiViewLayout) wrappedMainInputLines(width int) []string {
	return wrappedEditableInputLines(width, l.mainInputRenderSpec())
}

func (l uiViewLayout) visibleMainInputLines(width int) []string {
	return visibleEditableInputLines(width, inputContentLineLimit(l.effectiveHeight()), l.mainInputRenderSpec())
}

func (l uiViewLayout) wrappedAskPromptLines(width int) ([]wrappedAskPromptLine, int) {
	promptLines := l.model.renderAskPromptLines()
	if len(promptLines) == 0 {
		promptLines = []askPromptLine{{Text: "", Kind: askPromptLineKindQuestion}}
	}
	out := make([]wrappedAskPromptLine, 0, len(promptLines)*2)
	cursorLineIndex := -1
	for _, line := range promptLines {
		parts := wrapLine(line.Text, width)
		if line.Kind == askPromptLineKindInput {
			spec := uiEditableInputRenderSpec{Prefix: line.InputPrefix, Text: line.InputText, CursorIndex: line.InputCursor, RenderCursor: line.ShowsCursor}
			parts = wrappedEditableInputLines(width, spec)
			if line.ShowsCursor {
				cursorLine, cursorCol := inputCursorDisplayPosition(spec.Prefix, spec.Text, spec.CursorIndex, width)
				if cursorLine >= 0 && cursorLine < len(parts) {
					parts[cursorLine] = overlayCursorOnLine(parts[cursorLine], cursorCol, width, lipgloss.NewStyle().Reverse(true))
					cursorLineIndex = len(out) + cursorLine
				}
			}
		}
		if len(parts) == 0 {
			parts = []string{""}
		}
		for _, part := range parts {
			wrappedLine := line
			if wrappedLine.MutedSuffix != "" && !strings.HasSuffix(part, wrappedLine.MutedSuffix) {
				wrappedLine.MutedSuffix = ""
			}
			out = append(out, wrappedAskPromptLine{Text: part, Line: wrappedLine})
		}
	}
	if len(out) == 0 {
		return []wrappedAskPromptLine{{Text: "", Line: askPromptLine{Kind: askPromptLineKindQuestion}}}, -1
	}
	return out, cursorLineIndex
}

func (l uiViewLayout) visibleAskPromptLines(width int) []wrappedAskPromptLine {
	wrapped, cursorLine := l.wrappedAskPromptLines(width)
	maxContentLines := inputContentLineLimit(l.effectiveHeight())
	if len(wrapped) > maxContentLines {
		visibleStart := visibleWrappedLineStart(len(wrapped), maxContentLines, cursorLine, cursorLine >= 0)
		wrapped = wrapped[visibleStart : visibleStart+maxContentLines]
	}
	return wrapped
}

func wrapPlainLines(lines []string, width int) []string {
	wrapped := make([]string, 0, len(lines))
	for _, line := range lines {
		wrapped = append(wrapped, wrapLine(line, width)...)
	}
	if len(wrapped) == 0 {
		return []string{""}
	}
	return wrapped
}

func inputContentLineLimit(height int) int {
	maxContentLines := height - 4
	if maxContentLines < 1 {
		return 1
	}
	return maxContentLines
}

func (l uiViewLayout) inputPanelLineCount(width, height int) int {
	inputState := l.model.inputModeState()
	if inputState.Mode == uiInputModeRollbackSelection {
		return 0
	}
	contentLines := len(l.wrappedMainInputLines(width))
	if inputState.ShowsAskInput {
		wrappedAskLines, _ := l.wrappedAskPromptLines(width)
		contentLines = len(wrappedAskLines)
	}
	if contentLines < 1 {
		contentLines = 1
	}
	maxContentLines := inputContentLineLimit(height)
	if contentLines > maxContentLines {
		contentLines = maxContentLines
	}
	return contentLines + 2
}

func (l uiViewLayout) renderInputFrame(width int, lines []string) []string {
	return renderFramedLines(width, lines, l.inputBorderStyle())
}

func (l uiViewLayout) inputBorderStyle() lipgloss.Style {
	borderColor := uiPalette(l.model.theme).primary
	if l.model.busy {
		borderColor = uiPalette(l.model.theme).muted
	}
	return lipgloss.NewStyle().Foreground(borderColor)
}

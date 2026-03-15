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
	if inputState.Mode == uiInputModeRollbackSelection {
		return nil
	}
	if width < 1 {
		return []string{padRight("", width)}
	}
	if inputState.ShowsAskInput {
		return l.renderAskInputLines(width, style)
	}

	wrapped, visibleStart := l.visibleMainInputLines(width)
	if l.shouldRenderSoftCursor() {
		cursorStyle := lipgloss.NewStyle().Reverse(true)
		cursorLine, cursorCol := inputCursorDisplayPosition(l.mainInputPrefix(), m.input, m.inputCursor, width)
		visibleCursorLine := cursorLine - visibleStart
		if visibleCursorLine >= 0 && visibleCursorLine < len(wrapped) {
			wrapped[visibleCursorLine] = overlayCursorOnLine(wrapped[visibleCursorLine], cursorCol, width, cursorStyle)
		}
	}

	lineStyle := style.input
	if m.isInputLocked() {
		lineStyle = style.inputDisabled
	}
	rendered := make([]string, 0, len(wrapped))
	for _, line := range wrapped {
		rendered = append(rendered, lineStyle.Render(padANSIRight(line, width)))
	}
	return l.renderInputFrame(width, rendered)
}

func (l uiViewLayout) renderAskInputLines(width int, style uiStyles) []string {
	if width < 1 {
		return []string{padRight("", width)}
	}
	wrapped := l.visibleAskPromptLines(width)
	selectedStyle := lipgloss.NewStyle().Foreground(uiPalette(l.model.theme).primary).Bold(true)
	rendered := make([]string, 0, len(wrapped))
	for _, line := range wrapped {
		padded := padANSIRight(line.Text, width)
		switch {
		case line.Line.Kind == askPromptLineKindHint:
			rendered = append(rendered, style.meta.Render(padded))
		case line.Line.Selected:
			rendered = append(rendered, selectedStyle.Render(padded))
		default:
			rendered = append(rendered, style.input.Render(padded))
		}
	}
	return l.renderInputFrame(width, rendered)
}

func (l uiViewLayout) mainInputPrefix() string {
	if l.model.isInputLocked() {
		return "⨯ "
	}
	return "› "
}

func (l uiViewLayout) wrappedMainInputLines(width int) []string {
	return wrapPlainLines(splitPlainLines(l.mainInputPrefix()+l.model.input), width)
}

func (l uiViewLayout) visibleMainInputLines(width int) ([]string, int) {
	wrapped := l.wrappedMainInputLines(width)
	visibleStart := 0
	maxContentLines := inputContentLineLimit(l.effectiveHeight())
	if len(wrapped) > maxContentLines {
		visibleStart = len(wrapped) - maxContentLines
		wrapped = wrapped[visibleStart:]
	}
	return wrapped, visibleStart
}

func (l uiViewLayout) wrappedAskPromptLines(width int) []wrappedAskPromptLine {
	promptLines := l.model.renderAskPromptLines()
	if len(promptLines) == 0 {
		promptLines = []askPromptLine{{Text: "", Kind: askPromptLineKindQuestion}}
	}
	out := make([]wrappedAskPromptLine, 0, len(promptLines)*2)
	for _, line := range promptLines {
		parts := wrapLine(line.Text, width)
		if len(parts) == 0 {
			parts = []string{""}
		}
		for _, part := range parts {
			out = append(out, wrappedAskPromptLine{Text: part, Line: line})
		}
	}
	if len(out) == 0 {
		return []wrappedAskPromptLine{{Text: "", Line: askPromptLine{Kind: askPromptLineKindQuestion}}}
	}
	return out
}

func (l uiViewLayout) visibleAskPromptLines(width int) []wrappedAskPromptLine {
	wrapped := l.wrappedAskPromptLines(width)
	maxContentLines := inputContentLineLimit(l.effectiveHeight())
	if len(wrapped) > maxContentLines {
		wrapped = wrapped[len(wrapped)-maxContentLines:]
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
		contentLines = len(l.wrappedAskPromptLines(width))
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
	borderStyle := l.inputBorderStyle()
	border := borderStyle.Render(strings.Repeat("─", width))
	out := make([]string, 0, len(lines)+2)
	out = append(out, border)
	out = append(out, lines...)
	out = append(out, border)
	return out
}

func (l uiViewLayout) inputBorderStyle() lipgloss.Style {
	borderColor := uiPalette(l.model.theme).primary
	if l.model.busy {
		borderColor = uiPalette(l.model.theme).muted
	}
	return lipgloss.NewStyle().Foreground(borderColor)
}

package app

import (
	"strings"

	"builder/shared/theme"
	"github.com/charmbracelet/lipgloss"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"
)

func (m *uiModel) renderStatusLine(width int, style uiStyles) string {
	return m.layout().renderStatusLine(width, style)
}

func (m *uiModel) renderChatPanel(width, height int, style uiStyles) []string {
	return m.layout().renderChatPanel(width, height, style)
}

func (m *uiModel) renderInputLines(width int, style uiStyles) []string {
	return m.layout().renderInputLines(width, style)
}

func (m *uiModel) renderSlashCommandPicker(width int) []string {
	return m.layout().renderActivePicker(width)
}

func (m *uiModel) renderQueuedMessagesPane(width int) []string {
	return m.layout().renderQueuedMessagesPane(width)
}

func (m *uiModel) effectiveWidth() int {
	return m.layout().effectiveWidth()
}

func (m *uiModel) effectiveHeight() int {
	return m.layout().effectiveHeight()
}

func (m *uiModel) calcChatLines() int {
	return m.layout().calcChatLines()
}

func (m *uiModel) syncViewport() {
	m.layout().syncViewport()
}

func (m *uiModel) shouldRenderSoftCursor() bool {
	return m.layout().shouldRenderSoftCursor()
}

type uiEditableInputRenderSpec struct {
	Prefix       string
	Text         string
	CursorIndex  int
	RenderCursor bool
}

func renderFramedEditableInputLines(width, maxContentLines int, spec uiEditableInputRenderSpec, lineStyle lipgloss.Style, borderStyle lipgloss.Style) []string {
	if width < 1 {
		return []string{padRight("", width)}
	}
	contentLines, cursorLine, cursorCol := visibleEditableInputViewport(width, maxContentLines, spec)
	rendered := make([]string, 0, len(contentLines))
	for index, line := range contentLines {
		if spec.RenderCursor && index == cursorLine {
			rendered = append(rendered, renderEditableInputLineWithCursor(line, width, cursorCol, lineStyle))
			continue
		}
		rendered = append(rendered, lineStyle.Render(padANSIRight(line, width)))
	}
	return renderFramedLines(width, rendered, borderStyle)
}

func renderFramedLines(width int, lines []string, borderStyle lipgloss.Style) []string {
	border := borderStyle.Render(strings.Repeat("─", width))
	out := make([]string, 0, len(lines)+2)
	out = append(out, border)
	out = append(out, lines...)
	out = append(out, border)
	return out
}

func wrappedEditableInputLines(width int, spec uiEditableInputRenderSpec) []string {
	return wrapPlainLines(splitPlainLines(spec.Prefix+spec.Text), width)
}

func visibleEditableInputLines(width, maxContentLines int, spec uiEditableInputRenderSpec) []string {
	visible, _, _ := visibleEditableInputViewport(width, maxContentLines, spec)
	return visible
}

func visibleEditableInputViewport(width, maxContentLines int, spec uiEditableInputRenderSpec) ([]string, int, int) {
	wrapped := wrappedEditableInputLines(width, spec)
	if len(wrapped) == 0 {
		wrapped = []string{""}
	}
	cursorLine := -1
	cursorCol := 0
	if spec.RenderCursor {
		cursorLine, cursorCol = inputCursorDisplayPosition(spec.Prefix, spec.Text, spec.CursorIndex, width)
	}
	visibleStart := visibleWrappedLineStart(len(wrapped), maxContentLines, cursorLine, spec.RenderCursor)
	end := visibleStart + maxContentLines
	if end > len(wrapped) {
		end = len(wrapped)
	}
	visible := append([]string(nil), wrapped[visibleStart:end]...)
	visibleCursorLine := cursorLine - visibleStart
	if visibleCursorLine < 0 || visibleCursorLine >= len(visible) {
		return visible, -1, 0
	}
	return visible, visibleCursorLine, cursorCol
}

func renderEditableInputLineWithCursor(line string, width int, cursorCol int, lineStyle lipgloss.Style) string {
	if width < 1 {
		return lineStyle.Render("")
	}
	runes := []rune(line)
	displayCol := 0
	for index, r := range runes {
		rw := runewidth.RuneWidth(r)
		if rw < 1 {
			rw = 1
		}
		if cursorCol < displayCol+rw {
			prefix := string(runes[:index])
			suffix := string(runes[index+1:])
			return lineStyle.Render(prefix) + lineStyle.Reverse(true).Render(string(r)) + lineStyle.Render(padANSIRight(suffix, width-displayCol-rw))
		}
		displayCol += rw
	}
	if displayCol < width {
		return lineStyle.Render(line) + lineStyle.Reverse(true).Render(" ") + lineStyle.Render(strings.Repeat(" ", max(0, width-displayCol-1)))
	}
	if len(runes) == 0 {
		return lineStyle.Reverse(true).Render(" ")
	}
	last := len(runes) - 1
	prefix := string(runes[:last])
	return lineStyle.Render(prefix) + lineStyle.Reverse(true).Render(string(runes[last]))
}

func editableInputCursorLine(width int, spec uiEditableInputRenderSpec) int {
	if !spec.RenderCursor {
		return -1
	}
	line, _ := inputCursorDisplayPosition(spec.Prefix, spec.Text, spec.CursorIndex, width)
	return line
}

func visibleWrappedLineStart(totalLines, maxContentLines, cursorLine int, trackCursor bool) int {
	if maxContentLines < 1 || totalLines <= maxContentLines {
		return 0
	}
	maxStart := totalLines - maxContentLines
	if !trackCursor || cursorLine < 0 {
		return maxStart
	}
	start := cursorLine - maxContentLines + 1
	if start < 0 {
		return 0
	}
	if start > maxStart {
		return maxStart
	}
	return start
}

func inputCursorDisplayPosition(prefix, text string, cursorIndex, width int) (line, col int) {
	textRunes := []rune(text)
	cursor := clampCursor(cursorIndex, len(textRunes))
	return wrappedCursorPosition(append([]rune(prefix), textRunes[:cursor]...), width)
}

func overlayCursorOnLine(line string, cursorCol, width int, cursorStyle lipgloss.Style) string {
	if width < 1 {
		return line
	}

	runes := []rune(line)
	displayCol := 0
	for i, r := range runes {
		rw := runewidth.RuneWidth(r)
		if rw < 1 {
			rw = 1
		}
		if cursorCol < displayCol+rw {
			return string(runes[:i]) + cursorStyle.Render(string(r)) + string(runes[i+1:])
		}
		displayCol += rw
	}

	if displayCol < width {
		return line + cursorStyle.Render(" ")
	}

	if len(runes) == 0 {
		return cursorStyle.Render(" ")
	}

	last := len(runes) - 1
	return string(runes[:last]) + cursorStyle.Render(string(runes[last]))
}

func wrappedCursorPosition(text []rune, width int) (line int, col int) {
	if width < 1 {
		return 0, 0
	}
	line = 0
	col = 0
	for i, r := range text {
		if r == '\n' {
			line++
			col = 0
			continue
		}
		rw := runewidth.RuneWidth(r)
		if rw < 1 {
			rw = 1
		}
		if col+rw > width {
			line++
			col = 0
		}
		col += rw
		if col == width && i < len(text)-1 {
			line++
			col = 0
		}
	}
	return line, col
}

func splitPlainLines(v string) []string {
	if strings.TrimSpace(v) == "" {
		return []string{""}
	}
	return strings.Split(v, "\n")
}

func wrapLine(line string, width int) []string {
	if width <= 0 {
		return []string{line}
	}
	if runewidth.StringWidth(line) <= width {
		return []string{line}
	}
	parts := make([]string, 0, 4)
	remaining := []rune(line)
	for len(remaining) > 0 {
		w := 0
		cut := 0
		for i, r := range remaining {
			rw := runewidth.RuneWidth(r)
			if w+rw > width {
				break
			}
			w += rw
			cut = i + 1
		}
		if cut == 0 {
			cut = 1
		}
		parts = append(parts, string(remaining[:cut]))
		remaining = remaining[cut:]
	}
	return parts
}

func truncateQueuedMessageLine(message string, width int) string {
	if width < 1 {
		return ""
	}
	firstLine := message
	hasMoreContent := false
	if idx := strings.IndexRune(message, '\n'); idx >= 0 {
		firstLine = message[:idx]
		hasMoreContent = true
	}
	if !hasMoreContent && runewidth.StringWidth(firstLine) <= width {
		return firstLine
	}
	if width == 1 {
		return "…"
	}
	maxWidth := width - 1
	runes := []rune(firstLine)
	cut := 0
	w := 0
	for i, r := range runes {
		rw := runewidth.RuneWidth(r)
		if rw < 1 {
			rw = 1
		}
		if w+rw > maxWidth {
			break
		}
		w += rw
		cut = i + 1
	}
	if cut == 0 {
		return "…"
	}
	return string(runes[:cut]) + "…"
}

func padRight(line string, width int) string {
	if width <= 0 {
		return ""
	}
	current := runewidth.StringWidth(line)
	if current == width {
		return line
	}
	if current > width {
		return line
	}
	return line + strings.Repeat(" ", width-current)
}

func padANSIRight(line string, width int) string {
	if width <= 0 {
		return ""
	}
	current := lipgloss.Width(line)
	if current >= width {
		return line
	}
	return line + strings.Repeat(" ", width-current)
}

func truncateANSIRight(line string, width int) string {
	if width <= 0 {
		return ""
	}
	if line == "" || lipgloss.Width(line) <= width {
		return line
	}
	if width == 1 {
		return "…"
	}
	parser := xansi.GetParser()
	defer xansi.PutParser(parser)

	visibleLimit := width - 1
	if visibleLimit < 0 {
		visibleLimit = 0
	}
	hasANSI := strings.Contains(line, "\x1b[")
	state := byte(0)
	input := line
	consumedWidth := 0
	var out strings.Builder
	for len(input) > 0 {
		seq, seqWidth, n, newState := xansi.GraphemeWidth.DecodeSequenceInString(input, state, parser)
		if n <= 0 {
			break
		}
		state = newState
		if seqWidth == 0 {
			out.WriteString(seq)
			input = input[n:]
			continue
		}
		if consumedWidth+seqWidth > visibleLimit {
			break
		}
		out.WriteString(seq)
		consumedWidth += seqWidth
		input = input[n:]
	}
	out.WriteString("…")
	if hasANSI {
		out.WriteString("\x1b[0m")
	}
	return out.String()
}

type uiStyles struct {
	brand         lipgloss.Style
	modeChip      lipgloss.Style
	panel         lipgloss.Style
	chat          lipgloss.Style
	input         lipgloss.Style
	inputDisabled lipgloss.Style
	meta          lipgloss.Style
	ask           lipgloss.Style
}

func uiThemeStyles(theme string) uiStyles {
	p := uiPalette(theme)
	return uiStyles{
		brand: lipgloss.NewStyle().Foreground(p.primary).Bold(true),
		modeChip: lipgloss.NewStyle().
			Foreground(p.modeText).
			Background(p.modeBg).
			Padding(0, 1).
			Bold(true),
		panel: lipgloss.NewStyle().
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(p.border).
			Padding(0, 1),
		chat: lipgloss.NewStyle().
			Foreground(p.foreground),
		input: lipgloss.NewStyle().
			Foreground(p.foreground),
		inputDisabled: lipgloss.NewStyle().
			Foreground(p.muted).
			Faint(true),
		meta: lipgloss.NewStyle().Foreground(p.muted).Faint(true),
		ask: lipgloss.NewStyle().
			BorderStyle(lipgloss.ThickBorder()).
			BorderForeground(p.secondary).
			Foreground(p.foreground).
			Padding(0, 1),
	}
}

type uiColors struct {
	primary    lipgloss.TerminalColor
	secondary  lipgloss.TerminalColor
	foreground lipgloss.TerminalColor
	muted      lipgloss.TerminalColor
	border     lipgloss.TerminalColor
	modeBg     lipgloss.TerminalColor
	modeText   lipgloss.TerminalColor
	chatBg     lipgloss.TerminalColor
	inputBg    lipgloss.TerminalColor
}

func uiPalette(themeName string) uiColors {
	palette := theme.ResolvePalette(themeName).App
	return uiColors{
		primary:    palette.Primary.Lipgloss(),
		secondary:  palette.Secondary.Lipgloss(),
		foreground: palette.Foreground.Lipgloss(),
		muted:      palette.Muted.Lipgloss(),
		border:     palette.Border.Lipgloss(),
		modeBg:     palette.ModeBg.Lipgloss(),
		modeText:   palette.ModeText.Lipgloss(),
		chatBg:     palette.ChatBg.Lipgloss(),
		inputBg:    palette.InputBg.Lipgloss(),
	}
}

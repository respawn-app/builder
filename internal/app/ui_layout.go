package app

import (
	"fmt"
	"math"
	"strings"

	"builder/internal/shared/textutil"
	"builder/internal/tui"

	bubbleprogress "github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

const (
	ansiHideCursor        = "\x1b[?25l"
	ansiClearLine         = "\x1b[2K"
	statusContextBarWidth = 10
	queuedMessagesLimit   = 5
)

type uiViewLayout struct {
	model *uiModel
}

func (m *uiModel) View() string {
	return m.layout().render()
}

func (l uiViewLayout) render() string {
	m := l.model
	if m.usesNativeScrollback() && m.view.Mode() == tui.ModeOngoing {
		return l.renderNativeOngoing()
	}
	style := uiThemeStyles(m.theme)
	width := l.effectiveWidth()
	height := l.effectiveHeight()
	if width <= 0 || height <= 0 {
		return ""
	}

	inputLines := l.renderInputLines(width, style)
	queuedLines := l.renderQueuedMessagesPane(width)
	pickerLines := l.renderSlashCommandPicker(width)
	if m.view.Mode() == tui.ModeDetail {
		inputLines = nil
		queuedLines = nil
		pickerLines = nil
	}
	statusLine := l.renderStatusLine(width, style)
	statusLines := 1
	chatLines := height - len(inputLines) - len(queuedLines) - len(pickerLines) - statusLines
	if chatLines < 1 {
		chatLines = 1
	}
	chatPanel := l.renderChatPanel(width, chatLines, style)
	allLines := make([]string, 0, height)
	allLines = append(allLines, chatPanel...)
	allLines = append(allLines, pickerLines...)
	allLines = append(allLines, queuedLines...)
	allLines = append(allLines, inputLines...)
	allLines = append(allLines, statusLine)
	for len(allLines) < height {
		allLines = append(allLines, padRight("", width))
	}
	if len(allLines) > height {
		allLines = allLines[len(allLines)-height:]
	}
	rendered := strings.Join(allLines, "\n")
	return rendered + ansiHideCursor
}

func (l uiViewLayout) renderNativeOngoing() string {
	m := l.model
	if !m.windowSizeKnown {
		return l.renderNativeOngoingPreSize()
	}
	return l.renderNativeOngoingSized()
}

func (l uiViewLayout) renderNativeOngoingPreSize() string {
	m := l.model
	style := uiThemeStyles(m.theme)
	width := l.effectiveWidth()
	if width <= 0 {
		return ""
	}
	inputLines := l.renderInputLines(width, style)
	queuedLines := l.renderQueuedMessagesPane(width)
	pickerLines := l.renderSlashCommandPicker(width)
	streamingLines := l.renderNativeStreamingLines(width, 12, style)
	statusLine := l.renderStatusLine(width, style)
	lines := make([]string, 0, len(inputLines)+len(queuedLines)+len(pickerLines)+len(streamingLines)+1)
	lines = append(lines, pickerLines...)
	lines = append(lines, queuedLines...)
	lines = append(lines, streamingLines...)
	lines = append(lines, inputLines...)
	lines = append(lines, statusLine)
	return strings.Join(lines, "\n") + ansiHideCursor
}

func (l uiViewLayout) renderNativeOngoingSized() string {
	m := l.model
	style := uiThemeStyles(m.theme)
	width := l.effectiveWidth()
	height := l.effectiveHeight()
	if width <= 0 {
		return ""
	}
	if height <= 0 {
		return l.renderNativeOngoingPreSize()
	}
	inputLines := l.renderInputLines(width, style)
	queuedLines := l.renderQueuedMessagesPane(width)
	pickerLines := l.renderSlashCommandPicker(width)
	availableStreamingLines := height - len(pickerLines) - len(queuedLines) - len(inputLines) - 1
	if availableStreamingLines < 0 {
		availableStreamingLines = 0
	}
	streamingLines := l.renderNativeStreamingLines(width, availableStreamingLines, style)
	statusLine := l.renderStatusLine(width, style)
	liveLines := make([]string, 0, len(pickerLines)+len(queuedLines)+len(streamingLines)+len(inputLines)+1)
	if m.nativeLiveRegionPad > 0 {
		for i := 0; i < m.nativeLiveRegionPad; i++ {
			liveLines = append(liveLines, padRight("", width))
		}
	}
	liveLines = append(liveLines, pickerLines...)
	liveLines = append(liveLines, queuedLines...)
	liveLines = append(liveLines, streamingLines...)
	liveLines = append(liveLines, inputLines...)
	liveLines = append(liveLines, statusLine)
	if len(liveLines) > height {
		liveLines = liveLines[len(liveLines)-height:]
	}
	return strings.Join(liveLines, "\n") + ansiHideCursor
}

func (l uiViewLayout) nativeOngoingLineCount() int {
	m := l.model
	style := uiThemeStyles(m.theme)
	width := l.effectiveWidth()
	if width <= 0 {
		return 0
	}
	inputLines := l.renderInputLines(width, style)
	queuedLines := l.renderQueuedMessagesPane(width)
	pickerLines := l.renderSlashCommandPicker(width)
	height := l.effectiveHeight()
	availableStreamingLines := height - len(pickerLines) - len(queuedLines) - len(inputLines) - 1
	if availableStreamingLines < 0 {
		availableStreamingLines = 0
	}
	streamingLines := l.renderNativeStreamingLines(width, availableStreamingLines, style)
	return len(inputLines) + len(queuedLines) + len(pickerLines) + len(streamingLines) + 1
}

func (l uiViewLayout) renderNativeStreamingLines(width, maxLines int, style uiStyles) []string {
	if width <= 0 || maxLines <= 0 {
		return nil
	}
	if !l.model.busy && !l.model.sawAssistantDelta {
		return nil
	}
	streamText := l.model.view.OngoingStreamingText()
	errText := l.model.view.OngoingErrorText()
	if strings.TrimSpace(streamText) == "" && strings.TrimSpace(errText) == "" {
		return nil
	}
	lines := make([]string, 0, maxLines)
	includeDivider := len(l.model.transcriptEntries) > 0
	if includeDivider {
		lines = append(lines, style.meta.Render(strings.Repeat("─", width)))
	}
	if strings.TrimSpace(streamText) != "" {
		streamLines := splitPlainLines(streamText)
		if len(streamLines) > 0 && strings.TrimSpace(streamLines[len(streamLines)-1]) == "" {
			streamLines = streamLines[:len(streamLines)-1]
		}
		for lineIndex, line := range streamLines {
			for _, wrapped := range wrapLine(line, width) {
				prefix := "  "
				if lineIndex == 0 {
					prefix = "❮ "
				}
				rendered := prefix + wrapped
				lines = append(lines, style.chat.Render(padRight(rendered, width)))
			}
		}
	}
	if strings.TrimSpace(errText) != "" {
		for _, line := range splitPlainLines(errText) {
			for _, wrapped := range wrapLine(line, width) {
				lines = append(lines, style.meta.Render(padRight("  "+wrapped, width)))
			}
		}
	}
	if len(lines) <= maxLines {
		return lines
	}
	if includeDivider && maxLines > 1 {
		content := lines[1:]
		result := []string{lines[0], content[0]}
		remaining := maxLines - len(result)
		if remaining <= 0 {
			return result[:maxLines]
		}
		if len(content) <= 1 {
			return result
		}
		tail := content[1:]
		if len(tail) > remaining {
			tail = tail[len(tail)-remaining:]
		}
		result = append(result, tail...)
		return result
	}
	return lines[len(lines)-maxLines:]
}

func (l uiViewLayout) syncNativeLiveRegionState() {
	m := l.model
	if !m.usesNativeScrollback() || m.view.Mode() != tui.ModeOngoing {
		m.nativeLiveRegionPad = 0
		m.nativeStreamingActive = false
		return
	}
	streamingActiveNow := strings.TrimSpace(m.view.OngoingStreamingText()) != "" || strings.TrimSpace(m.view.OngoingErrorText()) != ""
	current := l.nativeOngoingLineCount()
	if !streamingActiveNow {
		m.nativeLiveRegionPad = 0
		m.nativeLiveRegionLines = current
		m.nativeStreamingActive = false
		return
	}
	m.nativeLiveRegionPad = 0
	m.nativeLiveRegionLines = current
	m.nativeStreamingActive = true
	return
}

func (l uiViewLayout) renderStatusLine(width int, style uiStyles) string {
	m := l.model
	spin := renderStatusDot(m.theme, m.activity, m.spinnerFrame)
	if m.reviewerRunning {
		spin = renderReviewerStatus()
	} else if m.compacting {
		spin = renderCompactionStatus()
	}
	transientStyle := lipgloss.NewStyle().Foreground(statusContextZoneColor(100)).Bold(true)
	segments := []string{
		spin,
		style.meta.Render(string(m.view.Mode())),
		style.meta.Render(textutil.FirstNonEmpty(m.modelName, "gpt-5")),
	}
	if cacheSection := l.renderCacheHitSection(style); cacheSection != "" {
		segments = append(segments, cacheSection)
	}
	if text := strings.TrimSpace(m.transientStatus); text != "" {
		segments = append(segments, transientStyle.Render(text))
	}
	left := strings.Join(segments, style.meta.Render(" | "))
	right := l.renderContextUsage(style)
	if right == "" {
		return padANSIRight(left, width)
	}
	gap := width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	return padANSIRight(left+strings.Repeat(" ", gap)+right, width)
}

func (l uiViewLayout) renderCacheHitSection(style uiStyles) string {
	m := l.model
	if m.engine == nil {
		return ""
	}
	usage := m.engine.ContextUsage()
	if !usage.HasCacheHitPercentage {
		return style.meta.Render("cache --")
	}
	return style.meta.Render(fmt.Sprintf("cache %d%%", usage.CacheHitPercent))
}

func (l uiViewLayout) renderContextUsage(style uiStyles) string {
	m := l.model
	if m.engine == nil {
		return ""
	}
	usage := m.engine.ContextUsage()
	if usage.WindowTokens <= 0 {
		return ""
	}
	used := usage.UsedTokens
	if used < 0 {
		used = 0
	}
	rawPercent := int(math.Round((float64(used) * 100) / float64(usage.WindowTokens)))
	barPercent := rawPercent
	if barPercent < 0 {
		barPercent = 0
	}
	if barPercent > 100 {
		barPercent = 100
	}
	filled := int(math.Round((float64(barPercent) / 100) * statusContextBarWidth))
	if filled < 0 {
		filled = 0
	}
	if filled > statusContextBarWidth {
		filled = statusContextBarWidth
	}
	barProgress := bubbleprogress.New(
		bubbleprogress.WithWidth(statusContextBarWidth),
		bubbleprogress.WithoutPercentage(),
		bubbleprogress.WithSolidFill(statusContextZoneHex(m.theme, rawPercent)),
		bubbleprogress.WithFillCharacters('▮', '▯'),
	)
	barProgress.EmptyColor = statusContextEmptyHex(m.theme)
	bar := barProgress.ViewAs(float64(barPercent) / 100.0)
	label := style.meta.Render(fmt.Sprintf("%d%%", rawPercent))
	return label + " " + bar
}

func statusContextZoneHex(theme string, percent int) string {
	if strings.EqualFold(strings.TrimSpace(theme), "light") {
		if percent < 50 {
			return "#22863A"
		}
		if percent < 80 {
			return "#9A6700"
		}
		return "#CB2431"
	}
	if percent < 50 {
		return "#98C379"
	}
	if percent < 80 {
		return "#E5C07B"
	}
	return "#F97583"
}

func statusContextEmptyHex(theme string) string {
	if strings.EqualFold(strings.TrimSpace(theme), "light") {
		return "#A0A1A7"
	}
	return "#5C6370"
}

func statusContextZoneColor(percent int) lipgloss.TerminalColor {
	green := lipgloss.CompleteAdaptiveColor{
		Light: lipgloss.CompleteColor{ANSI: "2", ANSI256: "34", TrueColor: "#22863A"},
		Dark:  lipgloss.CompleteColor{ANSI: "2", ANSI256: "114", TrueColor: "#98C379"},
	}
	amber := lipgloss.CompleteAdaptiveColor{
		Light: lipgloss.CompleteColor{ANSI: "3", ANSI256: "136", TrueColor: "#9A6700"},
		Dark:  lipgloss.CompleteColor{ANSI: "3", ANSI256: "180", TrueColor: "#E5C07B"},
	}
	red := lipgloss.CompleteAdaptiveColor{
		Light: lipgloss.CompleteColor{ANSI: "1", ANSI256: "160", TrueColor: "#CB2431"},
		Dark:  lipgloss.CompleteColor{ANSI: "1", ANSI256: "203", TrueColor: "#F97583"},
	}
	if percent < 50 {
		return green
	}
	if percent < 80 {
		return amber
	}
	return red
}

func renderStatusDot(theme string, activity uiActivity, frame int) string {
	green := lipgloss.CompleteAdaptiveColor{
		Light: lipgloss.CompleteColor{ANSI: "2", ANSI256: "34", TrueColor: "#22863A"},
		Dark:  lipgloss.CompleteColor{ANSI: "2", ANSI256: "114", TrueColor: "#98C379"},
	}
	red := lipgloss.CompleteAdaptiveColor{
		Light: lipgloss.CompleteColor{ANSI: "1", ANSI256: "160", TrueColor: "#CB2431"},
		Dark:  lipgloss.CompleteColor{ANSI: "1", ANSI256: "203", TrueColor: "#F97583"},
	}
	amber := lipgloss.CompleteAdaptiveColor{
		Light: lipgloss.CompleteColor{ANSI: "3", ANSI256: "136", TrueColor: "#9A6700"},
		Dark:  lipgloss.CompleteColor{ANSI: "3", ANSI256: "180", TrueColor: "#E5C07B"},
	}
	palette := uiPalette(theme)
	switch activity {
	case uiActivityRunning:
		// Slow blink by 3x vs the base spinner tick cadence.
		if (frame/3)%2 == 1 {
			return " "
		}
		return lipgloss.NewStyle().Foreground(palette.muted).Render("●")
	case uiActivityQueued:
		return lipgloss.NewStyle().Foreground(amber).Render("●")
	case uiActivityQuestion:
		return lipgloss.NewStyle().Foreground(palette.primary).Render("●")
	case uiActivityInterrupted:
		return lipgloss.NewStyle().Foreground(amber).Faint(true).Render("●")
	case uiActivityError:
		return lipgloss.NewStyle().Foreground(red).Render("●")
	default:
		return lipgloss.NewStyle().Foreground(green).Render("●")
	}
}

func renderCompactionStatus() string {
	amber := lipgloss.CompleteAdaptiveColor{
		Light: lipgloss.CompleteColor{ANSI: "3", ANSI256: "136", TrueColor: "#9A6700"},
		Dark:  lipgloss.CompleteColor{ANSI: "3", ANSI256: "180", TrueColor: "#E5C07B"},
	}
	return lipgloss.NewStyle().Foreground(amber).Render("⚠ compacting")
}

func renderReviewerStatus() string {
	green := lipgloss.CompleteAdaptiveColor{
		Light: lipgloss.CompleteColor{ANSI: "2", ANSI256: "34", TrueColor: "#22863A"},
		Dark:  lipgloss.CompleteColor{ANSI: "2", ANSI256: "114", TrueColor: "#98C379"},
	}
	keyword := lipgloss.NewStyle().Foreground(green).Bold(true).Render("reviewing")
	return "● " + keyword
}

func (l uiViewLayout) renderChatPanel(width, height int, style uiStyles) []string {
	if width < 1 {
		return []string{padRight("", width)}
	}
	rawLines := splitPlainLines(l.model.view.View())
	contentLines := append([]string(nil), rawLines...)
	if len(contentLines) < height {
		for len(contentLines) < height {
			contentLines = append(contentLines, "")
		}
	} else if len(contentLines) > height {
		end := len(contentLines)
		for end > 0 && strings.TrimSpace(contentLines[end-1]) == "" {
			end--
		}
		if end < height {
			end = height
		}
		start := end - height
		if start < 0 {
			start = 0
		}
		contentLines = contentLines[start:end]
	}
	return l.renderChatContentLines(contentLines, width, style)
}

func (l uiViewLayout) renderChatContentLines(rawLines []string, width int, style uiStyles) []string {
	contentWidth := width
	if contentWidth < 1 {
		contentWidth = 1
	}
	out := make([]string, 0, len(rawLines))
	for _, line := range rawLines {
		if line == tui.TranscriptDivider {
			out = append(out, style.meta.Render(strings.Repeat("─", contentWidth)))
			continue
		}
		out = append(out, style.chat.Render(padANSIRight(line, contentWidth)))
	}
	return out
}

func (l uiViewLayout) renderInputLines(width int, style uiStyles) []string {
	m := l.model
	if m.rollbackMode {
		return nil
	}
	if width < 1 {
		return []string{padRight("", width)}
	}
	contentWidth := width
	if m.activeAsk != nil {
		return l.renderAskInputLines(width, style)
	}
	var raw []string
	{
		text := m.input
		prefix := "› "
		if m.isInputLocked() {
			prefix = "⨯ "
		}
		if m.reviewerBlocking {
			text = "Review in progress."
		}
		raw = splitPlainLines(prefix + text)
	}
	wrapped := make([]string, 0, len(raw))
	for _, line := range raw {
		wrapped = append(wrapped, wrapLine(line, contentWidth)...)
	}
	if len(wrapped) == 0 {
		wrapped = []string{""}
	}
	maxContentLines := l.effectiveHeight() - 4
	if maxContentLines < 1 {
		maxContentLines = 1
	}
	visibleStart := 0
	if len(wrapped) > maxContentLines {
		visibleStart = len(wrapped) - maxContentLines
		wrapped = wrapped[visibleStart:]
	}

	if l.shouldRenderSoftCursor() && m.activeAsk == nil {
		cursorStyle := lipgloss.NewStyle().Reverse(true)
		cursorLine, cursorCol := inputCursorDisplayPosition("› ", m.input, m.inputCursor, contentWidth)
		visibleCursorLine := cursorLine - visibleStart
		if visibleCursorLine >= 0 && visibleCursorLine < len(wrapped) {
			wrapped[visibleCursorLine] = overlayCursorOnLine(wrapped[visibleCursorLine], cursorCol, contentWidth, cursorStyle)
		}
	}

	borderColor := uiPalette(m.theme).primary
	if m.busy {
		borderColor = uiPalette(m.theme).muted
	}
	borderStyle := lipgloss.NewStyle().Foreground(borderColor)
	top := borderStyle.Render(strings.Repeat("─", width))
	bottom := borderStyle.Render(strings.Repeat("─", width))

	out := make([]string, 0, len(wrapped)+2)
	out = append(out, top)
	lineStyle := style.input
	if m.isInputLocked() {
		lineStyle = style.inputDisabled
	}
	for _, line := range wrapped {
		out = append(out, lineStyle.Render(padANSIRight(line, contentWidth)))
	}
	out = append(out, bottom)
	return out
}

func (l uiViewLayout) renderAskInputLines(width int, style uiStyles) []string {
	m := l.model
	if width < 1 {
		return []string{padRight("", width)}
	}
	contentWidth := width
	promptLines := m.renderAskPromptLines()
	if len(promptLines) == 0 {
		promptLines = []askPromptLine{{Text: "", Kind: askPromptLineKindQuestion}}
	}
	wrapped := make([]struct {
		Text string
		Line askPromptLine
	}, 0, len(promptLines)*2)
	for _, line := range promptLines {
		parts := wrapLine(line.Text, contentWidth)
		if len(parts) == 0 {
			parts = []string{""}
		}
		for _, part := range parts {
			wrapped = append(wrapped, struct {
				Text string
				Line askPromptLine
			}{Text: part, Line: line})
		}
	}
	if len(wrapped) == 0 {
		wrapped = append(wrapped, struct {
			Text string
			Line askPromptLine
		}{Text: "", Line: askPromptLine{Kind: askPromptLineKindQuestion}})
	}
	maxContentLines := l.effectiveHeight() - 4
	if maxContentLines < 1 {
		maxContentLines = 1
	}
	if len(wrapped) > maxContentLines {
		wrapped = wrapped[len(wrapped)-maxContentLines:]
	}

	borderColor := uiPalette(m.theme).primary
	if m.busy {
		borderColor = uiPalette(m.theme).muted
	}
	borderStyle := lipgloss.NewStyle().Foreground(borderColor)
	top := borderStyle.Render(strings.Repeat("─", width))
	bottom := borderStyle.Render(strings.Repeat("─", width))

	selectedStyle := lipgloss.NewStyle().Foreground(uiPalette(m.theme).primary).Bold(true)
	out := make([]string, 0, len(wrapped)+2)
	out = append(out, top)
	for _, line := range wrapped {
		padded := padANSIRight(line.Text, contentWidth)
		rendered := style.input.Render(padded)
		switch line.Line.Kind {
		case askPromptLineKindHint:
			rendered = style.meta.Render(padded)
		default:
			if line.Line.Selected {
				rendered = selectedStyle.Render(padded)
			}
		}
		out = append(out, rendered)
	}
	out = append(out, bottom)
	return out
}

func (l uiViewLayout) renderSlashCommandPicker(width int) []string {
	m := l.model
	state := m.slashCommandPicker()
	if !state.visible || width < 1 {
		return nil
	}
	palette := uiPalette(m.theme)
	selectedCommandStyle := lipgloss.NewStyle().Foreground(palette.muted).Bold(true)
	unselectedCommandStyle := lipgloss.NewStyle().Bold(true)
	descriptionStyle := lipgloss.NewStyle().Foreground(palette.muted).Faint(true)
	out := make([]string, 0, slashCommandPickerLines)
	for row := 0; row < slashCommandPickerLines; row++ {
		idx := state.start + row
		line := ""
		if idx < len(state.matches) {
			commandStyle := unselectedCommandStyle
			if idx == state.selection {
				commandStyle = selectedCommandStyle
			}
			line = commandStyle.Render("/" + state.matches[idx].Name)
			description := strings.TrimSpace(state.matches[idx].Description)
			if description != "" {
				line += " - " + descriptionStyle.Render(description)
			}
		} else if len(state.matches) == 0 && row == 0 {
			line = descriptionStyle.Render("No matching commands")
		}
		out = append(out, padANSIRight(line, width))
	}
	return out
}

func (l uiViewLayout) renderQueuedMessagesPane(width int) []string {
	m := l.model
	if width < 1 || len(m.queued) == 0 {
		return nil
	}
	lines := l.queuedMessagePaneLines(width)
	if len(lines) == 0 {
		return nil
	}
	palette := uiPalette(m.theme)
	queueStyle := lipgloss.NewStyle().Foreground(palette.secondary).Faint(true)
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		out = append(out, queueStyle.Render(padANSIRight(line, width)))
	}
	return out
}

func (l uiViewLayout) queuedPaneLineCount() int {
	total := len(l.model.queued)
	if total == 0 {
		return 0
	}
	visible := total
	if visible > queuedMessagesLimit {
		visible = queuedMessagesLimit
	}
	hidden := total - visible
	if hidden > 0 {
		return visible + 1
	}
	return visible
}

func (l uiViewLayout) queuedMessagePaneLines(width int) []string {
	m := l.model
	total := len(m.queued)
	if width < 1 || total == 0 {
		return nil
	}
	start := 0
	if total > queuedMessagesLimit {
		start = total - queuedMessagesLimit
	}
	visible := m.queued[start:]
	out := make([]string, 0, len(visible)+1)
	hidden := total - len(visible)
	if hidden > 0 {
		out = append(out, fmt.Sprintf("%d more messages", hidden))
	}
	for _, message := range visible {
		out = append(out, truncateQueuedMessageLine(message, width))
	}
	return out
}

func (l uiViewLayout) effectiveWidth() int {
	m := l.model
	if m.termWidth > 0 {
		return m.termWidth
	}
	return 120
}

func (l uiViewLayout) effectiveHeight() int {
	m := l.model
	if m.termHeight > 0 {
		return m.termHeight
	}
	return 32
}

func (l uiViewLayout) calcChatLines() int {
	m := l.model
	height := l.effectiveHeight()
	if m.view.Mode() == tui.ModeDetail {
		chat := height - 1 // keep status line
		if chat < 1 {
			return 1
		}
		return chat
	}
	width := l.effectiveWidth()
	contentWidth := width
	if contentWidth < 1 {
		contentWidth = 1
	}

	inputLines := 0
	if !m.rollbackMode {
		inputContentLines := 1
		if m.activeAsk != nil {
			lines := splitPlainLines(m.renderAskPrompt())
			inputContentLines = 0
			for _, line := range lines {
				inputContentLines += len(wrapLine(line, contentWidth))
			}
		} else {
			text := m.input
			if m.reviewerBlocking {
				text = "Review in progress."
			}
			wrapped := wrapLine("› "+text, contentWidth)
			inputContentLines = len(wrapped)
		}
		if inputContentLines < 1 {
			inputContentLines = 1
		}
		maxContentLines := height - 4
		if maxContentLines < 1 {
			maxContentLines = 1
		}
		if inputContentLines > maxContentLines {
			inputContentLines = maxContentLines
		}
		inputLines = inputContentLines + 2
	}
	queuedLines := l.queuedPaneLineCount()
	pickerLines := 0
	if m.slashCommandPicker().visible {
		pickerLines = slashCommandPickerLines
	}
	chat := height - inputLines - queuedLines - pickerLines - 1
	if chat < 1 {
		return 1
	}
	return chat
}

func (l uiViewLayout) syncViewport() {
	m := l.model
	l.syncNativeLiveRegionState()
	m.nativeReplayWidth = l.effectiveWidth()
	m.forwardToView(tui.SetViewportSizeMsg{
		Lines: l.calcChatLines(),
		Width: l.effectiveWidth(),
	})
}

func (l uiViewLayout) shouldRenderSoftCursor() bool {
	m := l.model
	return !m.isInputLocked() && m.activeAsk == nil && !m.rollbackMode
}

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
	return m.layout().renderSlashCommandPicker(width)
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

func uiPalette(theme string) uiColors {
	theme = strings.ToLower(strings.TrimSpace(theme))
	if theme == "light" {
		return uiColors{
			primary:    lipgloss.CompleteAdaptiveColor{Light: lipgloss.CompleteColor{ANSI: "4", ANSI256: "33", TrueColor: "#4078F2"}, Dark: lipgloss.CompleteColor{ANSI: "4", ANSI256: "33", TrueColor: "#61AFEF"}},
			secondary:  lipgloss.CompleteAdaptiveColor{Light: lipgloss.CompleteColor{ANSI: "6", ANSI256: "36", TrueColor: "#2AA876"}, Dark: lipgloss.CompleteColor{ANSI: "6", ANSI256: "79", TrueColor: "#7FDBA6"}},
			foreground: lipgloss.CompleteAdaptiveColor{Light: lipgloss.CompleteColor{ANSI: "0", ANSI256: "235", TrueColor: "#383A42"}, Dark: lipgloss.CompleteColor{ANSI: "7", ANSI256: "252", TrueColor: "#ABB2BF"}},
			muted:      lipgloss.CompleteAdaptiveColor{Light: lipgloss.CompleteColor{ANSI: "8", ANSI256: "245", TrueColor: "#A0A1A7"}, Dark: lipgloss.CompleteColor{ANSI: "8", ANSI256: "243", TrueColor: "#5C6370"}},
			border:     lipgloss.CompleteAdaptiveColor{Light: lipgloss.CompleteColor{ANSI: "7", ANSI256: "250", TrueColor: "#D0D0D0"}, Dark: lipgloss.CompleteColor{ANSI: "8", ANSI256: "240", TrueColor: "#3D434F"}},
			modeBg:     lipgloss.CompleteAdaptiveColor{Light: lipgloss.CompleteColor{ANSI: "7", ANSI256: "252", TrueColor: "#EAEAEB"}, Dark: lipgloss.CompleteColor{ANSI: "8", ANSI256: "238", TrueColor: "#353B45"}},
			modeText:   lipgloss.CompleteAdaptiveColor{Light: lipgloss.CompleteColor{ANSI: "0", ANSI256: "235", TrueColor: "#383A42"}, Dark: lipgloss.CompleteColor{ANSI: "7", ANSI256: "252", TrueColor: "#ABB2BF"}},
			chatBg:     lipgloss.CompleteAdaptiveColor{Light: lipgloss.CompleteColor{ANSI: "7", ANSI256: "255", TrueColor: "#F8F8F8"}, Dark: lipgloss.CompleteColor{ANSI: "0", ANSI256: "235", TrueColor: "#1E222A"}},
			inputBg:    lipgloss.CompleteAdaptiveColor{Light: lipgloss.CompleteColor{ANSI: "7", ANSI256: "254", TrueColor: "#F2F3F5"}, Dark: lipgloss.CompleteColor{ANSI: "0", ANSI256: "236", TrueColor: "#2A2F37"}},
		}
	}
	return uiColors{
		primary:    lipgloss.CompleteAdaptiveColor{Light: lipgloss.CompleteColor{ANSI: "4", ANSI256: "33", TrueColor: "#4078F2"}, Dark: lipgloss.CompleteColor{ANSI: "4", ANSI256: "75", TrueColor: "#61AFEF"}},
		secondary:  lipgloss.CompleteAdaptiveColor{Light: lipgloss.CompleteColor{ANSI: "6", ANSI256: "36", TrueColor: "#2AA876"}, Dark: lipgloss.CompleteColor{ANSI: "6", ANSI256: "79", TrueColor: "#7FDBA6"}},
		foreground: lipgloss.CompleteAdaptiveColor{Light: lipgloss.CompleteColor{ANSI: "0", ANSI256: "235", TrueColor: "#383A42"}, Dark: lipgloss.CompleteColor{ANSI: "7", ANSI256: "252", TrueColor: "#ABB2BF"}},
		muted:      lipgloss.CompleteAdaptiveColor{Light: lipgloss.CompleteColor{ANSI: "8", ANSI256: "245", TrueColor: "#A0A1A7"}, Dark: lipgloss.CompleteColor{ANSI: "8", ANSI256: "243", TrueColor: "#5C6370"}},
		border:     lipgloss.CompleteAdaptiveColor{Light: lipgloss.CompleteColor{ANSI: "7", ANSI256: "250", TrueColor: "#D0D0D0"}, Dark: lipgloss.CompleteColor{ANSI: "8", ANSI256: "240", TrueColor: "#3D434F"}},
		modeBg:     lipgloss.CompleteAdaptiveColor{Light: lipgloss.CompleteColor{ANSI: "7", ANSI256: "252", TrueColor: "#EAEAEB"}, Dark: lipgloss.CompleteColor{ANSI: "8", ANSI256: "238", TrueColor: "#353B45"}},
		modeText:   lipgloss.CompleteAdaptiveColor{Light: lipgloss.CompleteColor{ANSI: "0", ANSI256: "235", TrueColor: "#383A42"}, Dark: lipgloss.CompleteColor{ANSI: "7", ANSI256: "252", TrueColor: "#ABB2BF"}},
		chatBg:     lipgloss.CompleteAdaptiveColor{Light: lipgloss.CompleteColor{ANSI: "7", ANSI256: "255", TrueColor: "#F8F8F8"}, Dark: lipgloss.CompleteColor{ANSI: "0", ANSI256: "235", TrueColor: "#1E222A"}},
		inputBg:    lipgloss.CompleteAdaptiveColor{Light: lipgloss.CompleteColor{ANSI: "7", ANSI256: "254", TrueColor: "#F2F3F5"}, Dark: lipgloss.CompleteColor{ANSI: "0", ANSI256: "236", TrueColor: "#2A2F37"}},
	}
}

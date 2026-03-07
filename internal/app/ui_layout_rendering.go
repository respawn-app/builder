package app

import (
	"fmt"
	"math"
	"strings"

	"builder/internal/llm"
	"builder/internal/tui"

	bubbleprogress "github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/lipgloss"
)

func (l uiViewLayout) renderStatusLine(width int, style uiStyles) string {
	m := l.model
	spin := renderStatusDot(m.theme, m.activity, m.spinnerFrame)
	if m.reviewerRunning {
		spin = renderReviewerStatus()
	} else if m.compacting {
		spin = renderCompactionStatus()
	}
	segments := []string{
		spin,
		style.meta.Render(string(m.view.Mode())),
		style.meta.Render(l.statusModelLabel()),
	}
	if label := processCountLabel(m.backgroundManager); label != "" {
		segments = append(segments, style.meta.Render(label))
	}
	if cacheSection := l.renderCacheHitSection(style); cacheSection != "" {
		segments = append(segments, cacheSection)
	}
	left := strings.Join(segments, style.meta.Render(" | "))
	right := l.renderStatusLineRight(width, left, style)
	if right == "" {
		return padANSIRight(left, width)
	}
	gap := width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	return padANSIRight(left+strings.Repeat(" ", gap)+right, width)
}

func (l uiViewLayout) renderStatusLineRight(width int, left string, style uiStyles) string {
	context := l.renderContextUsage(style)
	notice := l.renderStatusNotice(width, left, context, style)
	segments := make([]string, 0, 2)
	if notice != "" {
		segments = append(segments, notice)
	}
	if context != "" {
		segments = append(segments, context)
	}
	return strings.Join(segments, style.meta.Render(" | "))
}

func (l uiViewLayout) renderStatusNotice(width int, left string, context string, style uiStyles) string {
	m := l.model
	text := strings.TrimSpace(m.transientStatus)
	if text == "" {
		return ""
	}
	separatorWidth := 0
	if context != "" {
		separatorWidth = lipgloss.Width(style.meta.Render(" | "))
	}
	available := width - lipgloss.Width(left) - lipgloss.Width(context) - separatorWidth - 1
	if available <= 0 {
		return ""
	}
	text = truncateQueuedMessageLine(text, available)
	return statusNoticeStyle(m.theme, m.transientStatusKind).Render(text)
}

func statusNoticeStyle(theme string, kind uiStatusNoticeKind) lipgloss.Style {
	palette := uiPalette(theme)
	color := palette.primary
	switch kind {
	case uiStatusNoticeSuccess:
		color = palette.secondary
	case uiStatusNoticeError:
		color = statusContextZoneColor(100)
	}
	return lipgloss.NewStyle().Foreground(color).Bold(true)
}

func (l uiViewLayout) statusModelLabel() string {
	m := l.model
	label := llm.ModelDisplayLabel(m.modelName, m.thinkingLevel)
	if !m.modelContractLocked {
		return label
	}
	return label + " (model locked)"
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
	if l.model.psVisible {
		return l.renderProcessList(width, height, style)
	}
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
	if m.psVisible {
		return []string{padRight("", width)}
	}
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
	selectedCommandStyle := lipgloss.NewStyle().Foreground(palette.primary).Bold(true)
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
			wrapped := wrapLine("› "+m.input, contentWidth)
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

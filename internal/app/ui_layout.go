package app

import (
	"strings"

	"builder/internal/tui"
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

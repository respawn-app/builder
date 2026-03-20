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

type uiRenderFrame struct {
	width       int
	height      int
	chatPanel   []string
	pickerPane  []string
	queuePane   []string
	inputPane   []string
	statusLine  string
	padToHeight bool
	tailOnly    bool
}

type nativeLiveRegionState struct {
	pad             int
	lines           int
	streamingActive bool
}

func (m *uiModel) View() string {
	return m.layout().render()
}

func (l uiViewLayout) render() string {
	if l.model.view.Mode() == tui.ModeOngoing {
		return l.renderNativeOngoing()
	}
	style := uiThemeStyles(l.model.theme)
	frame, ok := l.composeStandardFrame(style)
	if !ok {
		return ""
	}
	return frame.render()
}

func (l uiViewLayout) composeStandardFrame(style uiStyles) (uiRenderFrame, bool) {
	m := l.model
	width := l.effectiveWidth()
	height := l.effectiveHeight()
	if width <= 0 || height <= 0 {
		return uiRenderFrame{}, false
	}
	frame := uiRenderFrame{width: width, height: height, statusLine: l.renderStatusLine(width, style), padToHeight: true, tailOnly: true}
	if m.view.Mode() != tui.ModeDetail {
		frame.inputPane = l.renderInputLines(width, style)
		frame.queuePane = l.renderQueuedMessagesPane(width)
		frame.pickerPane = l.renderSlashCommandPicker(width)
	}
	chatLines := height - len(frame.inputPane) - len(frame.queuePane) - len(frame.pickerPane) - 1
	if chatLines < 1 {
		chatLines = 1
	}
	frame.chatPanel = l.renderChatPanel(width, chatLines, style)
	return frame, true
}

func (l uiViewLayout) renderNativeOngoing() string {
	if !l.model.windowSizeKnown {
		return ""
	}
	return l.renderNativeOngoingSized()
}

func (l uiViewLayout) renderNativeOngoingSized() string {
	m := l.model
	style := uiThemeStyles(m.theme)
	frame, status := l.composeNativeSizedFrame(style)
	if status == nativeFrameInvalid {
		return ""
	}
	return frame.render()
}

func (f uiRenderFrame) render() string {
	allLines := make([]string, 0, f.height)
	allLines = append(allLines, f.chatPanel...)
	allLines = append(allLines, f.pickerPane...)
	allLines = append(allLines, f.queuePane...)
	allLines = append(allLines, f.inputPane...)
	if strings.TrimSpace(f.statusLine) != "" || f.height > 0 {
		allLines = append(allLines, f.statusLine)
	}
	if f.padToHeight {
		for len(allLines) < f.height {
			allLines = append(allLines, padRight("", f.width))
		}
	}
	if len(allLines) > f.height {
		if f.tailOnly {
			allLines = allLines[len(allLines)-f.height:]
		} else {
			allLines = allLines[:f.height]
		}
	}
	return strings.Join(allLines, "\n") + ansiHideCursor
}

type nativeFrameStatus uint8

const (
	nativeFrameInvalid nativeFrameStatus = iota
	nativeFrameReady
)

func (l uiViewLayout) composeNativeSizedFrame(style uiStyles) (uiRenderFrame, nativeFrameStatus) {
	m := l.model
	width := l.effectiveWidth()
	height := l.effectiveHeight()
	if width <= 0 {
		return uiRenderFrame{}, nativeFrameInvalid
	}
	if height <= 0 {
		return uiRenderFrame{}, nativeFrameInvalid
	}
	frame := uiRenderFrame{
		width:       width,
		height:      height,
		pickerPane:  l.renderSlashCommandPicker(width),
		queuePane:   l.renderQueuedMessagesPane(width),
		inputPane:   l.renderInputLines(width, style),
		statusLine:  l.renderStatusLine(width, style),
		tailOnly:    true,
		padToHeight: false,
	}
	availableStreamingLines := height - len(frame.pickerPane) - len(frame.queuePane) - len(frame.inputPane) - 1
	if availableStreamingLines < 0 {
		availableStreamingLines = 0
	}
	frame.chatPanel = l.renderNativeStreamingLines(width, availableStreamingLines, style)
	if m.nativeLiveRegionPad > 0 {
		pad := make([]string, 0, m.nativeLiveRegionPad+len(frame.chatPanel))
		for i := 0; i < m.nativeLiveRegionPad; i++ {
			pad = append(pad, padRight("", width))
		}
		frame.chatPanel = append(pad, frame.chatPanel...)
	}
	return frame, nativeFrameReady
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
	pendingLines := l.renderNativePendingLines(width)
	hasStreaming := l.model.busy || l.model.sawAssistantDelta
	if !hasStreaming && len(pendingLines) == 0 {
		return nil
	}
	streamText := ""
	if hasStreaming {
		streamText = l.model.view.OngoingStreamingText()
	}
	errText := l.model.view.OngoingErrorText()
	if len(pendingLines) == 0 && strings.TrimSpace(streamText) == "" && strings.TrimSpace(errText) == "" {
		return nil
	}
	lines := make([]string, 0, maxLines)
	includeDivider := len(nativeCommittedEntries(l.model.transcriptEntries)) > 0
	if includeDivider {
		lines = append(lines, style.meta.Render(strings.Repeat("─", width)))
	}
	lines = append(lines, pendingLines...)
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

func (l uiViewLayout) renderNativePendingLines(width int) []string {
	rendered := renderNativePendingToolSnapshot(l.model.transcriptEntries, l.model.theme, width)
	if strings.TrimSpace(rendered) == "" {
		return nil
	}
	return strings.Split(rendered, "\n")
}

func (l uiViewLayout) syncNativeLiveRegionState() {
	state := l.computeNativeLiveRegionState()
	m := l.model
	m.nativeLiveRegionPad = state.pad
	m.nativeLiveRegionLines = state.lines
	m.nativeStreamingActive = state.streamingActive
}

func (l uiViewLayout) computeNativeLiveRegionState() nativeLiveRegionState {
	m := l.model
	if m.view.Mode() != tui.ModeOngoing {
		return nativeLiveRegionState{}
	}
	streamingActiveNow := strings.TrimSpace(m.view.OngoingStreamingText()) != "" || strings.TrimSpace(m.view.OngoingErrorText()) != ""
	current := l.nativeOngoingLineCount()
	if !streamingActiveNow {
		return nativeLiveRegionState{lines: current}
	}
	return nativeLiveRegionState{lines: current, streamingActive: true}
}

package app

import (
	"strings"

	"builder/internal/config"
	"builder/internal/tui"

	tea "github.com/charmbracelet/bubbletea"
)

func (m *uiModel) usesNativeScrollback() bool {
	return m.tuiScrollMode == config.TUIScrollModeNative
}

func (m *uiModel) syncNativeHistoryFromTranscript() tea.Cmd {
	if !m.usesNativeScrollback() {
		return nil
	}
	if !m.windowSizeKnown {
		return nil
	}
	if len(m.transcriptEntries) == 0 {
		m.nativeFlushedEntryCount = 0
		m.nativeHistoryReplayed = false
		return nil
	}

	start := 0
	if m.nativeHistoryReplayed {
		if m.nativeFlushedEntryCount < 0 || m.nativeFlushedEntryCount > len(m.transcriptEntries) {
			start = 0
		} else {
			start = m.nativeFlushedEntryCount
		}
	}
	if start >= len(m.transcriptEntries) {
		return nil
	}

	renderWidth := m.nativeReplayRenderWidth()
	rendered := renderNativeScrollbackSnapshot(m.transcriptEntries[start:], m.theme, renderWidth)
	chunks := splitNativeScrollbackChunks(rendered, 64*1024)
	m.nativeFlushedEntryCount = len(m.transcriptEntries)
	m.nativeHistoryReplayed = true
	if len(chunks) == 0 {
		return nil
	}
	cmds := make([]tea.Cmd, 0, len(chunks))
	for _, chunk := range chunks {
		if cmd := emitNativeHistoryFlush(chunk); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	if len(cmds) == 0 {
		return nil
	}
	return tea.Sequence(cmds...)
}

func (m *uiModel) nativeReplayRenderWidth() int {
	if m.termWidth > 0 {
		return m.termWidth
	}
	if m.nativeReplayWidth > 0 {
		return m.nativeReplayWidth
	}
	return 120
}

func emitNativeHistoryFlush(text string) tea.Cmd {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	return func() tea.Msg {
		return nativeHistoryFlushMsg{Text: ensureNativeFlushNewline(text)}
	}
}

func ensureNativeFlushNewline(text string) string {
	payload := text
	if !strings.HasSuffix(payload, "\n") {
		payload += "\n"
	}
	return payload
}

func splitNativeScrollbackChunks(rendered string, maxBytes int) []string {
	if strings.TrimSpace(rendered) == "" {
		return nil
	}
	if maxBytes <= 0 {
		maxBytes = 64 * 1024
	}
	lines := strings.Split(rendered, "\n")
	capacity := len(lines) / 32
	if capacity < 1 {
		capacity = 1
	}
	chunks := make([]string, 0, capacity)
	var current strings.Builder
	for _, line := range lines {
		if current.Len() == 0 {
			current.WriteString(line)
			continue
		}
		if current.Len()+1+len(line) > maxBytes {
			chunks = append(chunks, current.String())
			current.Reset()
			current.WriteString(line)
			continue
		}
		current.WriteByte('\n')
		current.WriteString(line)
	}
	if current.Len() > 0 {
		chunks = append(chunks, current.String())
	}
	return chunks
}

func renderNativeScrollbackSnapshot(entries []tui.TranscriptEntry, theme string, width int) string {
	if len(entries) == 0 {
		return ""
	}
	if width <= 0 {
		width = 120
	}
	tuiModel := tui.NewModel(tui.WithTheme(theme), tui.WithPreviewLines(200000))
	next, _ := tuiModel.Update(tui.SetViewportSizeMsg{Lines: 200000, Width: width})
	if casted, ok := next.(tui.Model); ok {
		tuiModel = casted
	}
	for _, entry := range entries {
		if strings.TrimSpace(entry.Text) == "" {
			continue
		}
		next, _ = tuiModel.Update(tui.AppendTranscriptMsg{
			Role:       entry.Role,
			Text:       entry.Text,
			Phase:      entry.Phase,
			ToolCallID: entry.ToolCallID,
			ToolCall:   entry.ToolCall,
		})
		if casted, ok := next.(tui.Model); ok {
			tuiModel = casted
		}
	}
	return styleNativeReplayDividers(tuiModel.OngoingCommittedSnapshot(), theme, width)
}

func styleNativeReplayDividers(snapshot, theme string, width int) string {
	if strings.TrimSpace(snapshot) == "" {
		return ""
	}
	if width <= 0 {
		width = 120
	}
	style := uiThemeStyles(theme)
	divider := style.meta.Render(strings.Repeat("─", width))
	lines := strings.Split(snapshot, "\n")
	for idx, line := range lines {
		if line == tui.TranscriptDivider {
			lines[idx] = divider
		}
	}
	return strings.Join(lines, "\n")
}

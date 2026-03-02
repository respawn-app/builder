package app

import (
	"os"
	"strings"
	"sync"

	"builder/internal/config"
	"builder/internal/tui"

	tea "github.com/charmbracelet/bubbletea"
)

const (
	nativePendingStreamMaxRunes = 20000
	nativeStreamLineMaxRunes    = 8000
)

var nativeOutputMu sync.Mutex

var writeNativeOutput = func(text string) {
	nativeOutputMu.Lock()
	defer nativeOutputMu.Unlock()
	_, _ = os.Stdout.WriteString(text)
}

func emitNativeDirectWrite(text string) tea.Cmd {
	return func() tea.Msg {
		writeNativeOutput(text)
		return nil
	}
}

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
		m.resetNativeFormatterState()
		return nil
	}

	if m.nativeFlushedEntryCount < 0 || m.nativeFlushedEntryCount > len(m.transcriptEntries) {
		if m.nativeFormatterReady {
			m.rebaseNativeFormatterSnapshot()
			return nil
		}
		m.resetNativeFormatterState()
	}

	if !m.nativeFormatterReady {
		m.initNativeFormatterModel()
		filtered := nonEmptyNativeEntries(m.transcriptEntries)
		next, _ := m.nativeFormatter.Update(tui.SetConversationMsg{Entries: filtered})
		if casted, ok := next.(tui.Model); ok {
			m.nativeFormatter = casted
		}
		rawSnapshot := m.nativeFormatter.OngoingCommittedSnapshot()
		m.nativeFormatterSnapshot = ensureNativeFlushNewline(rawSnapshot)
		m.nativeFormatterEntries = cloneNativeEntries(filtered)
		m.nativeFlushedEntryCount = len(m.transcriptEntries)
		m.nativeHistoryReplayed = true
		m.nativePendingStreamText = ""
		return m.emitNativeRenderedText(styleNativeReplayDividers(rawSnapshot, m.theme, m.nativeFormatterWidth))
	}

	filteredAll := nonEmptyNativeEntries(m.transcriptEntries)
	if !nativeEntriesPrefixEqual(filteredAll, m.nativeFormatterEntries) {
		m.rebaseNativeFormatterSnapshot()
		return nil
	}

	start := m.nativeFlushedEntryCount
	if start >= len(m.transcriptEntries) {
		return nil
	}

	for _, entry := range m.transcriptEntries[start:] {
		if strings.TrimSpace(entry.Text) == "" {
			continue
		}
		if entry.Role == "assistant" && m.nativePendingStreamText != "" {
			remainingPending, remainingCommitted := consumeNativeStreamPrefix(m.nativePendingStreamText, entry.Text)
			if remainingPending == m.nativePendingStreamText && remainingCommitted == entry.Text {
				m.nativePendingStreamText = ""
			} else {
				m.nativePendingStreamText = remainingPending
				entry.Text = remainingCommitted
			}
			if strings.TrimSpace(entry.Text) == "" {
				continue
			}
		}
		next, _ := m.nativeFormatter.Update(tui.AppendTranscriptMsg{
			Role:       entry.Role,
			Text:       entry.Text,
			Phase:      entry.Phase,
			ToolCallID: entry.ToolCallID,
			ToolCall:   entry.ToolCall,
		})
		if casted, ok := next.(tui.Model); ok {
			m.nativeFormatter = casted
		}
	}

	rawSnapshot := m.nativeFormatter.OngoingCommittedSnapshot()
	normalizedSnapshot := ensureNativeFlushNewline(rawSnapshot)
	previous := m.nativeFormatterSnapshot
	m.nativeFormatterSnapshot = normalizedSnapshot
	m.nativeFormatterEntries = cloneNativeEntries(filteredAll)
	m.nativeFlushedEntryCount = len(m.transcriptEntries)
	m.nativeHistoryReplayed = true
	if previous == "" {
		return m.emitNativeRenderedText(styleNativeReplayDividers(rawSnapshot, m.theme, m.nativeFormatterWidth))
	}
	if !strings.HasPrefix(normalizedSnapshot, previous) {
		m.rebaseNativeFormatterSnapshot()
		return nil
	}
	if !strings.HasSuffix(previous, "\n") {
		m.rebaseNativeFormatterSnapshot()
		return nil
	}
	delta := normalizedSnapshot[len(previous):]
	if len(delta) == 0 {
		return nil
	}
	return m.emitNativeRenderedText(styleNativeReplayDividers(delta, m.theme, m.nativeFormatterWidth))
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

func (m *uiModel) initNativeFormatterModel() {
	width := m.nativeReplayRenderWidth()
	formatter := tui.NewModel(tui.WithTheme(m.theme), tui.WithPreviewLines(200000))
	next, _ := formatter.Update(tui.SetViewportSizeMsg{Lines: 200000, Width: width})
	if casted, ok := next.(tui.Model); ok {
		formatter = casted
	}
	m.nativeFormatter = formatter
	m.nativeFormatterReady = true
	m.nativeFormatterWidth = width
	m.nativeFormatterSnapshot = ""
}

func (m *uiModel) resetNativeFormatterState() {
	m.nativeFlushedEntryCount = 0
	m.nativeHistoryReplayed = false
	m.nativeFormatterReady = false
	m.nativeFormatterWidth = 0
	m.nativeFormatterSnapshot = ""
	m.nativeFormatterEntries = nil
	m.nativePendingStreamText = ""
	m.nativeStreamLineBuffer = ""
	m.nativeFormatter = tui.Model{}
}

func (m *uiModel) rebaseNativeFormatterSnapshot() {
	if !m.nativeFormatterReady {
		return
	}
	m.initNativeFormatterModel()
	filtered := nonEmptyNativeEntries(m.transcriptEntries)
	next, _ := m.nativeFormatter.Update(tui.SetConversationMsg{Entries: filtered})
	if casted, ok := next.(tui.Model); ok {
		m.nativeFormatter = casted
	}
	m.nativeFormatterSnapshot = m.nativeFormatter.OngoingCommittedSnapshot()
	m.nativeFormatterSnapshot = ensureNativeFlushNewline(m.nativeFormatterSnapshot)
	m.nativeFormatterEntries = cloneNativeEntries(filtered)
	m.nativeFlushedEntryCount = len(m.transcriptEntries)
	m.nativeHistoryReplayed = true
	m.nativePendingStreamText = ""
	m.nativeStreamLineBuffer = ""
}

func nonEmptyNativeEntries(entries []tui.TranscriptEntry) []tui.TranscriptEntry {
	filtered := make([]tui.TranscriptEntry, 0, len(entries))
	for _, entry := range entries {
		if strings.TrimSpace(entry.Text) == "" {
			continue
		}
		filtered = append(filtered, entry)
	}
	return filtered
}

func cloneNativeEntries(entries []tui.TranscriptEntry) []tui.TranscriptEntry {
	cloned := make([]tui.TranscriptEntry, len(entries))
	copy(cloned, entries)
	return cloned
}

func nativeEntriesPrefixEqual(entries []tui.TranscriptEntry, prefix []tui.TranscriptEntry) bool {
	if len(prefix) > len(entries) {
		return false
	}
	for index := range prefix {
		if !nativeEntryEqual(entries[index], prefix[index]) {
			return false
		}
	}
	return true
}

func nativeEntryEqual(left tui.TranscriptEntry, right tui.TranscriptEntry) bool {
	if left.Role != right.Role || left.Text != right.Text || left.Phase != right.Phase || left.ToolCallID != right.ToolCallID {
		return false
	}
	if left.ToolCall == nil || right.ToolCall == nil {
		return left.ToolCall == nil && right.ToolCall == nil
	}
	if left.ToolCall.ToolName != right.ToolCall.ToolName ||
		left.ToolCall.Command != right.ToolCall.Command ||
		left.ToolCall.Question != right.ToolCall.Question ||
		left.ToolCall.PatchSummary != right.ToolCall.PatchSummary ||
		left.ToolCall.PatchDetail != right.ToolCall.PatchDetail ||
		left.ToolCall.IsShell != right.ToolCall.IsShell ||
		left.ToolCall.UserInitiated != right.ToolCall.UserInitiated {
		return false
	}
	if len(left.ToolCall.Suggestions) != len(right.ToolCall.Suggestions) {
		return false
	}
	for index := range left.ToolCall.Suggestions {
		if left.ToolCall.Suggestions[index] != right.ToolCall.Suggestions[index] {
			return false
		}
	}
	if left.ToolCall.RenderHint == nil || right.ToolCall.RenderHint == nil {
		return left.ToolCall.RenderHint == nil && right.ToolCall.RenderHint == nil
	}
	return left.ToolCall.RenderHint.Kind == right.ToolCall.RenderHint.Kind &&
		left.ToolCall.RenderHint.Path == right.ToolCall.RenderHint.Path &&
		left.ToolCall.RenderHint.ResultOnly == right.ToolCall.RenderHint.ResultOnly
}

func consumeNativeStreamPrefix(pending, committed string) (string, string) {
	if pending == "" || committed == "" {
		return pending, committed
	}
	pendingRunes := []rune(pending)
	committedRunes := []rune(committed)
	match := 0
	limit := len(pendingRunes)
	if len(committedRunes) < limit {
		limit = len(committedRunes)
	}
	for match < limit && pendingRunes[match] == committedRunes[match] {
		match++
	}
	return string(pendingRunes[match:]), string(committedRunes[match:])
}

func appendBoundedPendingStream(existing, delta string) string {
	if delta == "" {
		return existing
	}
	combined := existing + delta
	runes := []rune(combined)
	if len(runes) <= nativePendingStreamMaxRunes {
		return combined
	}
	return string(runes[len(runes)-nativePendingStreamMaxRunes:])
}

func appendBoundedStreamLine(existing, delta string) string {
	if delta == "" {
		return existing
	}
	combined := existing + delta
	runes := []rune(combined)
	if len(runes) <= nativeStreamLineMaxRunes {
		return combined
	}
	return string(runes[len(runes)-nativeStreamLineMaxRunes:])
}

func tailRunes(value string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= maxRunes {
		return value
	}
	return string(runes[len(runes)-maxRunes:])
}

func splitCompleteLines(value string) (string, string) {
	if value == "" {
		return "", ""
	}
	idx := strings.LastIndex(value, "\n")
	if idx < 0 {
		return "", value
	}
	return value[:idx+1], value[idx+1:]
}

func (m *uiModel) emitNativeRenderedText(rendered string) tea.Cmd {
	chunks := splitNativeScrollbackChunks(rendered, 64*1024)
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
	filtered := nonEmptyNativeEntries(entries)
	if len(filtered) == 0 {
		return ""
	}
	next, _ = tuiModel.Update(tui.SetConversationMsg{Entries: filtered})
	if casted, ok := next.(tui.Model); ok {
		tuiModel = casted
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

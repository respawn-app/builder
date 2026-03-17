package app

import (
	"strings"

	patchformat "builder/internal/tools/patch/format"
	"builder/internal/tui"

	tea "github.com/charmbracelet/bubbletea"
)

func (m *uiModel) syncNativeHistoryFromTranscript() tea.Cmd {
	if !m.windowSizeKnown {
		return nil
	}
	if len(m.transcriptEntries) == 0 {
		m.resetNativeFormatterState()
		return nil
	}

	committedEntries := nativeCommittedEntries(m.transcriptEntries)

	if m.nativeFlushedEntryCount < 0 || m.nativeFlushedEntryCount > len(committedEntries) {
		if m.nativeFormatterReady {
			m.rebaseNativeFormatterSnapshot()
			return nil
		}
		m.resetNativeFormatterState()
	}

	if !m.nativeFormatterReady {
		if len(committedEntries) == 0 {
			m.nativeFormatterEntries = nil
			m.nativeFormatterSnapshot = ""
			m.nativeFlushedEntryCount = 0
			m.nativeHistoryReplayed = true
			return nil
		}
		m.initNativeFormatterModel()
		next, _ := m.nativeFormatter.Update(tui.SetConversationMsg{Entries: committedEntries})
		if casted, ok := next.(tui.Model); ok {
			m.nativeFormatter = casted
		}
		rawSnapshot := m.nativeFormatter.OngoingCommittedSnapshot()
		m.nativeFormatterSnapshot = rawSnapshot
		m.nativeFormatterEntries = cloneNativeEntries(committedEntries)
		m.nativeFlushedEntryCount = len(committedEntries)
		m.nativeHistoryReplayed = true
		if !m.shouldEmitNativeHistory() {
			return nil
		}
		return m.emitCurrentNativeHistorySnapshot(false)
	}

	if !nativeEntriesPrefixEqual(committedEntries, m.nativeFormatterEntries) {
		m.rebaseNativeFormatterSnapshot()
		return nil
	}

	start := m.nativeFlushedEntryCount
	if start >= len(committedEntries) {
		return nil
	}

	for _, entry := range committedEntries[start:] {
		if strings.TrimSpace(ongoingTranscriptText(entry)) == "" {
			continue
		}
		next, _ := m.nativeFormatter.Update(tui.AppendTranscriptMsg{
			Role:        entry.Role,
			Text:        entry.Text,
			OngoingText: entry.OngoingText,
			Phase:       entry.Phase,
			ToolCallID:  entry.ToolCallID,
			ToolCall:    entry.ToolCall,
		})
		if casted, ok := next.(tui.Model); ok {
			m.nativeFormatter = casted
		}
	}

	rawSnapshot := m.nativeFormatter.OngoingCommittedSnapshot()
	m.nativeFormatterSnapshot = rawSnapshot
	m.nativeFormatterEntries = cloneNativeEntries(committedEntries)
	m.nativeFlushedEntryCount = len(committedEntries)
	m.nativeHistoryReplayed = true
	if !m.shouldEmitNativeHistory() {
		return nil
	}
	return m.emitCurrentNativeHistorySnapshot(false)
}

func (m *uiModel) shouldEmitNativeHistory() bool {
	return m.windowSizeKnown && m.view.Mode() == tui.ModeOngoing
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
	m.nativeRenderedSnapshot = ""
	m.nativeFormatterEntries = nil
	m.nativeFormatter = tui.Model{}
}

func (m *uiModel) rebaseNativeFormatterSnapshot() {
	if !m.nativeFormatterReady {
		return
	}
	m.initNativeFormatterModel()
	committedEntries := nativeCommittedEntries(m.transcriptEntries)
	if len(committedEntries) == 0 {
		m.nativeFormatterSnapshot = ""
		m.nativeFormatterEntries = nil
		m.nativeFlushedEntryCount = 0
		m.nativeHistoryReplayed = true
		return
	}
	next, _ := m.nativeFormatter.Update(tui.SetConversationMsg{Entries: committedEntries})
	if casted, ok := next.(tui.Model); ok {
		m.nativeFormatter = casted
	}
	m.nativeFormatterSnapshot = m.nativeFormatter.OngoingCommittedSnapshot()
	m.nativeFormatterEntries = cloneNativeEntries(committedEntries)
	m.nativeFlushedEntryCount = len(committedEntries)
	m.nativeHistoryReplayed = true
}

func (m *uiModel) emitCurrentNativeHistorySnapshot(forceFull bool) tea.Cmd {
	rawSnapshot := m.nativeFormatterSnapshot
	if strings.TrimSpace(rawSnapshot) == "" {
		m.nativeRenderedSnapshot = ""
		return nil
	}
	if !forceFull && m.nativeRenderedSnapshot != "" {
		if strings.HasPrefix(rawSnapshot, m.nativeRenderedSnapshot) {
			delta := rawSnapshot[len(m.nativeRenderedSnapshot):]
			m.nativeRenderedSnapshot = rawSnapshot
			if len(delta) == 0 {
				return nil
			}
			return m.emitNativeRenderedText(styleNativeReplayDividers(delta, m.theme, m.nativeFormatterWidth))
		}
		forceFull = true
	}
	m.nativeRenderedSnapshot = rawSnapshot
	styled := styleNativeReplayDividers(rawSnapshot, m.theme, m.nativeFormatterWidth)
	if strings.TrimSpace(styled) == "" {
		return nil
	}
	if forceFull && strings.TrimSpace(m.nativeRenderedSnapshot) != "" {
		return tea.Sequence(tea.ClearScreen, m.emitNativeRenderedText(styled))
	}
	return m.emitNativeRenderedText(styled)
}

func (m *uiModel) replayNativeTranscriptThroughEntry(entryIndex int) tea.Cmd {
	if !m.windowSizeKnown {
		return nil
	}
	if entryIndex < 0 || entryIndex >= len(m.transcriptEntries) {
		return nil
	}
	rawSnapshot := renderNativeCommittedSnapshot(m.transcriptEntries[:entryIndex+1], m.theme, m.nativeReplayRenderWidth())
	m.nativeRenderedSnapshot = rawSnapshot
	if strings.TrimSpace(rawSnapshot) == "" {
		return tea.ClearScreen
	}
	return tea.Sequence(
		tea.ClearScreen,
		m.emitNativeRenderedText(styleNativeReplayDividers(rawSnapshot, m.theme, m.nativeReplayRenderWidth())),
	)
}

func nonEmptyNativeEntries(entries []tui.TranscriptEntry) []tui.TranscriptEntry {
	filtered := make([]tui.TranscriptEntry, 0, len(entries))
	for _, entry := range entries {
		if strings.TrimSpace(entry.Text) == "" {
			if strings.TrimSpace(entry.OngoingText) == "" {
				continue
			}
		}
		filtered = append(filtered, entry)
	}
	return filtered
}

func nativeCommittedEntries(entries []tui.TranscriptEntry) []tui.TranscriptEntry {
	if len(entries) == 0 {
		return nil
	}
	prefixEnd := nativeCommittedPrefixEnd(entries)
	if prefixEnd <= 0 {
		return nil
	}
	return nonEmptyNativeEntries(entries[:prefixEnd])
}

func nativePendingEntries(entries []tui.TranscriptEntry) []tui.TranscriptEntry {
	if len(entries) == 0 {
		return nil
	}
	prefixEnd := nativeCommittedPrefixEnd(entries)
	if prefixEnd >= len(entries) {
		return nil
	}
	return nonEmptyNativeEntries(entries[prefixEnd:])
}

func nativeCommittedPrefixEnd(entries []tui.TranscriptEntry) int {
	consumedResults := make(map[int]struct{})
	for idx, entry := range entries {
		if strings.TrimSpace(entry.Role) != "tool_call" {
			continue
		}
		if strings.TrimSpace(ongoingTranscriptText(entry)) == "" {
			continue
		}
		resultIdx := nativeFindMatchingToolResultIndex(entries, idx, consumedResults)
		if resultIdx < 0 {
			return idx
		}
		consumedResults[resultIdx] = struct{}{}
	}
	return len(entries)
}

func nativeFindMatchingToolResultIndex(entries []tui.TranscriptEntry, callIdx int, consumed map[int]struct{}) int {
	if callIdx < 0 || callIdx >= len(entries) {
		return -1
	}
	callID := strings.TrimSpace(entries[callIdx].ToolCallID)
	nextIdx := callIdx + 1
	if nextIdx < len(entries) {
		if _, used := consumed[nextIdx]; !used && nativeIsToolResultRole(entries[nextIdx].Role) {
			nextCallID := strings.TrimSpace(entries[nextIdx].ToolCallID)
			if callID == nextCallID {
				return nextIdx
			}
		}
	}
	if callID == "" {
		return -1
	}
	for idx := callIdx + 1; idx < len(entries); idx++ {
		if _, used := consumed[idx]; used || !nativeIsToolResultRole(entries[idx].Role) {
			continue
		}
		if strings.TrimSpace(entries[idx].ToolCallID) == callID {
			return idx
		}
	}
	return -1
}

func nativeIsToolResultRole(role string) bool {
	switch strings.TrimSpace(role) {
	case "tool_result", "tool_result_ok", "tool_result_error":
		return true
	default:
		return false
	}
}

func ongoingTranscriptText(entry tui.TranscriptEntry) string {
	if strings.TrimSpace(entry.OngoingText) != "" {
		return entry.OngoingText
	}
	return entry.Text
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
	if left.Role != right.Role || left.Text != right.Text || left.OngoingText != right.OngoingText || left.Phase != right.Phase || left.ToolCallID != right.ToolCallID {
		return false
	}
	if left.ToolCall == nil || right.ToolCall == nil {
		return left.ToolCall == nil && right.ToolCall == nil
	}
	if left.ToolCall.ToolName != right.ToolCall.ToolName ||
		left.ToolCall.Presentation != right.ToolCall.Presentation ||
		left.ToolCall.RenderBehavior != right.ToolCall.RenderBehavior ||
		left.ToolCall.Command != right.ToolCall.Command ||
		left.ToolCall.CompactText != right.ToolCall.CompactText ||
		left.ToolCall.InlineMeta != right.ToolCall.InlineMeta ||
		left.ToolCall.TimeoutLabel != right.ToolCall.TimeoutLabel ||
		left.ToolCall.Question != right.ToolCall.Question ||
		left.ToolCall.PatchSummary != right.ToolCall.PatchSummary ||
		left.ToolCall.PatchDetail != right.ToolCall.PatchDetail ||
		left.ToolCall.IsShell != right.ToolCall.IsShell ||
		left.ToolCall.UserInitiated != right.ToolCall.UserInitiated ||
		left.ToolCall.OmitSuccessfulResult != right.ToolCall.OmitSuccessfulResult {
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
	if !nativePatchRenderEqual(left.ToolCall.PatchRender, right.ToolCall.PatchRender) {
		return false
	}
	if left.ToolCall.RenderHint == nil || right.ToolCall.RenderHint == nil {
		return left.ToolCall.RenderHint == nil && right.ToolCall.RenderHint == nil
	}
	return left.ToolCall.RenderHint.Kind == right.ToolCall.RenderHint.Kind &&
		left.ToolCall.RenderHint.Path == right.ToolCall.RenderHint.Path &&
		left.ToolCall.RenderHint.ResultOnly == right.ToolCall.RenderHint.ResultOnly
}

func nativePatchRenderEqual(left *patchformat.RenderedPatch, right *patchformat.RenderedPatch) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	if len(left.Files) != len(right.Files) || len(left.SummaryLines) != len(right.SummaryLines) || len(left.DetailLines) != len(right.DetailLines) {
		return false
	}
	for index := range left.Files {
		if left.Files[index].AbsPath != right.Files[index].AbsPath ||
			left.Files[index].RelPath != right.Files[index].RelPath ||
			left.Files[index].Added != right.Files[index].Added ||
			left.Files[index].Removed != right.Files[index].Removed ||
			len(left.Files[index].Diff) != len(right.Files[index].Diff) {
			return false
		}
		for diffIndex := range left.Files[index].Diff {
			if left.Files[index].Diff[diffIndex] != right.Files[index].Diff[diffIndex] {
				return false
			}
		}
	}
	for index := range left.SummaryLines {
		if left.SummaryLines[index] != right.SummaryLines[index] {
			return false
		}
	}
	for index := range left.DetailLines {
		if left.DetailLines[index] != right.DetailLines[index] {
			return false
		}
	}
	return true
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
		return nativeHistoryFlushMsg{Text: text}
	}
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
	rawSnapshot := renderNativeCommittedSnapshot(entries, theme, width)
	return styleNativeReplayDividers(rawSnapshot, theme, width)
}

func renderNativeCommittedSnapshot(entries []tui.TranscriptEntry, theme string, width int) string {
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
	return tuiModel.OngoingCommittedSnapshot()
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

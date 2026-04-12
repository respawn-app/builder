package app

import (
	"fmt"
	"strings"

	"builder/cli/tui"

	tea "github.com/charmbracelet/bubbletea"
)

func (m *uiModel) syncNativeHistoryFromTranscript() tea.Cmd {
	if !m.windowSizeKnown {
		return nil
	}
	committedEntries := tui.CommittedOngoingEntries(m.transcriptEntries)
	if len(committedEntries) == 0 {
		hasPendingTransientTail := len(tui.PendingOngoingEntries(m.transcriptEntries)) > 0
		alreadyReplayed := m.nativeHistoryReplayed
		m.resetNativeHistoryState()
		m.nativeHistoryReplayed = true
		if hasPendingTransientTail || alreadyReplayed || !m.shouldEmitNativeHistory() {
			return nil
		}
		return m.emitCurrentNativeScrollbackState(false)
	}

	projection := m.view.CommittedOngoingProjectionForEntries(m.transcriptEntries)
	committedCount := len(committedEntries)
	if m.nativeFlushedEntryCount < 0 || m.nativeFlushedEntryCount > committedCount {
		m.rebaseNativeProjection(projection, committedCount)
		return nil
	}
	if !m.nativeHistoryReplayed || m.nativeProjection.Empty() {
		m.rebaseNativeProjection(projection, committedCount)
		if !m.shouldEmitNativeHistory() {
			return nil
		}
		return m.emitCurrentNativeScrollbackState(false)
	}

	previousProjection := m.nativeRenderedProjection
	if previousProjection.Empty() {
		previousProjection = m.nativeProjection
	}
	previousBlockCount := len(previousProjection.Blocks)
	delta, ok := projection.RenderAppendDeltaFrom(previousProjection, tui.TranscriptDivider)
	m.rebaseNativeProjection(projection, committedCount)
	if !m.shouldEmitNativeHistory() {
		return nil
	}
	if !ok {
		return m.emitNonContiguousNativeProjectionRecovery(projection, previousProjection)
	}
	if strings.TrimSpace(delta) == "" {
		return nil
	}
	m.nativeRenderedProjection = projection
	m.nativeRenderedSnapshot = projection.Render(tui.TranscriptDivider)
	return m.emitNativeRenderedText(renderStyledNativeProjectionLines(projection.LinesFromBlock(previousBlockCount, tui.TranscriptDivider), m.theme, m.nativeReplayRenderWidth()))
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

func (m *uiModel) resetNativeHistoryState() {
	m.nativeFlushedEntryCount = 0
	m.nativeHistoryReplayed = false
	m.nativeProjection = tui.TranscriptProjection{}
	m.nativeRenderedProjection = tui.TranscriptProjection{}
	m.nativeRenderedSnapshot = ""
	m.waitRuntimeEventAfterFlushSequence = 0
	m.discardPendingNativeHistoryFlushes()
}

func (m *uiModel) rebaseNativeProjection(projection tui.TranscriptProjection, committedCount int) {
	m.nativeProjection = projection
	m.nativeFlushedEntryCount = committedCount
	m.nativeHistoryReplayed = true
}

func (m *uiModel) emitCurrentNativeScrollbackState(forceFull bool) tea.Cmd {
	if !m.nativeProjection.Empty() {
		return m.emitCurrentNativeHistorySnapshot(forceFull)
	}
	return m.emitEmptyNativeScrollbackSpacer(forceFull)
}

func (m *uiModel) emitEmptyNativeScrollbackSpacer(forceFull bool) tea.Cmd {
	spacer := m.nativeEmptyScrollbackSpacerText()
	if spacer == "" {
		if forceFull {
			return tea.ClearScreen
		}
		return nil
	}
	flush := m.emitNativeHistoryFlush(spacer, true)
	if !forceFull {
		return flush
	}
	return tea.Sequence(tea.ClearScreen, flush)
}

func (m *uiModel) nativeEmptyScrollbackSpacerText() string {
	if !m.windowSizeKnown || m.termHeight <= 0 {
		return ""
	}
	return strings.Repeat("\n", m.termHeight)
}

func (m *uiModel) emitCurrentNativeHistorySnapshot(forceFull bool) tea.Cmd {
	rawSnapshot := m.nativeProjection.Render(tui.TranscriptDivider)
	if strings.TrimSpace(rawSnapshot) == "" {
		return nil
	}
	if forceFull {
		styled := renderStyledNativeProjection(m.nativeProjection, m.theme, m.nativeReplayRenderWidth())
		if strings.TrimSpace(styled) == "" {
			return nil
		}
		m.nativeRenderedProjection = m.nativeProjection
		m.nativeRenderedSnapshot = rawSnapshot
		return tea.Sequence(tea.ClearScreen, m.emitNativeRenderedText(styled))
	}
	rewriteRenderedHistory := m.view.Mode() == tui.ModeOngoing && !m.nativeRenderedProjection.Empty()
	if !m.nativeRenderedProjection.Empty() {
		previousBlockCount := len(m.nativeRenderedProjection.Blocks)
		delta, ok := m.nativeProjection.RenderAppendDeltaFrom(m.nativeRenderedProjection, tui.TranscriptDivider)
		delta = strings.TrimPrefix(delta, "\n")
		if ok && strings.TrimSpace(delta) != "" {
			styledDelta := renderStyledNativeProjectionLines(m.nativeProjection.LinesFromBlock(previousBlockCount, tui.TranscriptDivider), m.theme, m.nativeReplayRenderWidth())
			m.nativeRenderedProjection = m.nativeProjection
			m.nativeRenderedSnapshot = rawSnapshot
			if strings.TrimSpace(styledDelta) != "" {
				return m.emitNativeRenderedText(styledDelta)
			}
		}
		if ok && strings.TrimSpace(delta) == "" {
			m.nativeRenderedProjection = m.nativeProjection
			m.nativeRenderedSnapshot = rawSnapshot
			return nil
		}
		if rewriteRenderedHistory {
			return m.emitNonContiguousNativeProjectionRecovery(m.nativeProjection, m.nativeRenderedProjection)
		}
		forceFull = true
	}
	if !forceFull {
		if deltaRaw, ok := nativeRenderedDelta(m.nativeRenderedSnapshot, rawSnapshot); ok {
			styledDelta := styleNativeReplayDividers(deltaRaw, m.theme, m.nativeReplayRenderWidth())
			m.nativeRenderedProjection = m.nativeProjection
			m.nativeRenderedSnapshot = rawSnapshot
			if strings.TrimSpace(styledDelta) == "" {
				return nil
			}
			return m.emitNativeRenderedText(styledDelta)
		}
	}
	if rewriteRenderedHistory {
		return nil
	}
	styled := renderStyledNativeProjection(m.nativeProjection, m.theme, m.nativeReplayRenderWidth())
	if strings.TrimSpace(styled) == "" {
		return nil
	}
	m.nativeRenderedProjection = m.nativeProjection
	m.nativeRenderedSnapshot = rawSnapshot
	if forceFull {
		return tea.Sequence(tea.ClearScreen, m.emitNativeRenderedText(styled))
	}
	return m.emitNativeRenderedText(styled)
}

func (m *uiModel) emitNonContiguousNativeProjectionRecovery(current tui.TranscriptProjection, rendered tui.TranscriptProjection) tea.Cmd {
	if current.Empty() {
		return nil
	}
	if m.debugMode {
		panic(fmt.Sprintf("non-contiguous committed transcript recovery requires rebuild: rendered_blocks=%d current_blocks=%d", len(rendered.Blocks), len(current.Blocks)))
	}
	m.logf("ui.native_history.rebuild_required rendered_blocks=%d current_blocks=%d", len(rendered.Blocks), len(current.Blocks))
	return m.emitForcedNativeProjectionReplay(current)
}

func (m *uiModel) emitForcedNativeProjectionReplay(projection tui.TranscriptProjection) tea.Cmd {
	rawSnapshot := projection.Render(tui.TranscriptDivider)
	m.nativeRenderedProjection = projection
	m.nativeRenderedSnapshot = rawSnapshot
	if strings.TrimSpace(rawSnapshot) == "" {
		return tea.ClearScreen
	}
	styled := renderStyledNativeProjection(projection, m.theme, m.nativeReplayRenderWidth())
	if strings.TrimSpace(styled) == "" {
		return tea.ClearScreen
	}
	return tea.Sequence(tea.ClearScreen, m.emitNativeRenderedText(styled))
}

func nativeRenderedDelta(previous, current string) (string, bool) {
	if strings.TrimSpace(previous) == "" || previous == current {
		return "", false
	}
	if !strings.HasPrefix(current, previous) {
		return "", false
	}
	delta := strings.TrimPrefix(current, previous)
	delta = strings.TrimPrefix(delta, "\n")
	return delta, true
}

func (m *uiModel) replayNativeTranscriptThroughEntry(entryIndex int) tea.Cmd {
	if !m.windowSizeKnown {
		return nil
	}
	if entryIndex < 0 || entryIndex >= len(m.transcriptEntries) {
		return nil
	}
	projection := nativeCommittedProjection(m.transcriptEntries[:entryIndex+1], m.theme, m.nativeReplayRenderWidth())
	rawSnapshot := renderNativeCommittedSnapshot(m.transcriptEntries[:entryIndex+1], m.theme, m.nativeReplayRenderWidth())
	m.nativeRenderedProjection = projection
	m.nativeRenderedSnapshot = rawSnapshot
	if strings.TrimSpace(rawSnapshot) == "" {
		return tea.ClearScreen
	}
	return tea.Sequence(
		tea.ClearScreen,
		m.emitNativeRenderedText(renderStyledNativeProjection(projection, m.theme, m.nativeReplayRenderWidth())),
	)
}

func nativeCommittedEntries(entries []tui.TranscriptEntry) []tui.TranscriptEntry {
	return tui.CommittedOngoingEntries(entries)
}

func nativePendingEntries(entries []tui.TranscriptEntry) []tui.TranscriptEntry {
	return tui.PendingOngoingEntries(entries)
}

func (m *uiModel) emitNativeRenderedText(rendered string) tea.Cmd {
	if len(rendered) <= 64*1024 {
		return m.emitNativeHistoryFlush(rendered, false)
	}
	chunks := splitNativeScrollbackChunks(rendered, 64*1024)
	if len(chunks) == 0 {
		return nil
	}
	cmds := make([]tea.Cmd, 0, len(chunks))
	for _, chunk := range chunks {
		if cmd := m.emitNativeHistoryFlush(chunk, false); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	if len(cmds) == 0 {
		return nil
	}
	if len(cmds) == 1 {
		return cmds[0]
	}
	return tea.Sequence(cmds...)
}

func (m *uiModel) emitNativeHistoryFlush(text string, allowBlank bool) tea.Cmd {
	if text == "" {
		return nil
	}
	if !allowBlank && strings.TrimSpace(text) == "" {
		return nil
	}
	m.nativeFlushSequence++
	msg := nativeHistoryFlushMsg{Text: text, AllowBlank: allowBlank, Sequence: m.nativeFlushSequence}
	return func() tea.Msg {
		return msg
	}
}

func (m *uiModel) discardPendingNativeHistoryFlushes() {
	m.nativeFlushedSequence = m.nativeFlushSequence
	if len(m.nativePendingFlushes) == 0 {
		return
	}
	clear(m.nativePendingFlushes)
}

func (m *uiModel) handleNativeHistoryFlush(msg nativeHistoryFlushMsg) tea.Cmd {
	if msg.Sequence == 0 {
		if !msg.AllowBlank && strings.TrimSpace(msg.Text) == "" {
			if m.waitRuntimeEventAfterFlushSequence != 0 && m.nativeFlushedSequence >= m.waitRuntimeEventAfterFlushSequence {
				m.waitRuntimeEventAfterFlushSequence = 0
				return m.waitRuntimeEventCmd()
			}
			return nil
		}
		cmds := []tea.Cmd{tea.Printf("%s", msg.Text)}
		if m.waitRuntimeEventAfterFlushSequence != 0 && m.nativeFlushedSequence >= m.waitRuntimeEventAfterFlushSequence {
			m.waitRuntimeEventAfterFlushSequence = 0
			cmds = append(cmds, m.waitRuntimeEventCmd())
		}
		return sequenceCmds(cmds...)
	}
	if msg.Sequence <= m.nativeFlushedSequence {
		return nil
	}
	if msg.Sequence > m.nativeFlushedSequence+1 {
		if m.nativePendingFlushes == nil {
			m.nativePendingFlushes = make(map[uint64]nativeHistoryFlushMsg)
		}
		m.nativePendingFlushes[msg.Sequence] = msg
		return nil
	}
	cmds := make([]tea.Cmd, 0, 1)
	current := msg
	for {
		m.nativeFlushedSequence = current.Sequence
		if current.AllowBlank || strings.TrimSpace(current.Text) != "" {
			cmds = append(cmds, tea.Printf("%s", current.Text))
		}
		next, ok := m.nativePendingFlushes[m.nativeFlushedSequence+1]
		if !ok {
			break
		}
		delete(m.nativePendingFlushes, next.Sequence)
		current = next
	}
	if m.waitRuntimeEventAfterFlushSequence != 0 && m.nativeFlushedSequence >= m.waitRuntimeEventAfterFlushSequence {
		m.waitRuntimeEventAfterFlushSequence = 0
		cmds = append(cmds, m.waitRuntimeEventCmd())
	}
	return sequenceCmds(cmds...)
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
	if width <= 0 {
		width = 120
	}
	model := tui.NewModel(tui.WithTheme(theme), tui.WithPreviewLines(200000))
	next, _ := model.Update(tui.SetViewportSizeMsg{Lines: 200000, Width: width})
	if casted, ok := next.(tui.Model); ok {
		model = casted
	}
	next, _ = model.Update(tui.SetConversationMsg{Entries: entries})
	if casted, ok := next.(tui.Model); ok {
		model = casted
	}
	return renderStyledNativeProjection(model.CommittedOngoingProjection(), theme, width)
}

func renderNativeCommittedSnapshot(entries []tui.TranscriptEntry, theme string, width int) string {
	return tui.RenderCommittedOngoingSnapshot(entries, theme, width)
}

func nativeCommittedProjection(entries []tui.TranscriptEntry, theme string, width int) tui.TranscriptProjection {
	if width <= 0 {
		width = 120
	}
	model := tui.NewModel(tui.WithTheme(theme), tui.WithPreviewLines(200000))
	next, _ := model.Update(tui.SetViewportSizeMsg{Lines: 200000, Width: width})
	if casted, ok := next.(tui.Model); ok {
		model = casted
	}
	next, _ = model.Update(tui.SetConversationMsg{Entries: entries})
	if casted, ok := next.(tui.Model); ok {
		model = casted
	}
	return model.CommittedOngoingProjection()
}

func renderStyledNativeProjection(projection tui.TranscriptProjection, theme string, width int) string {
	return renderStyledNativeProjectionLines(projection.Lines(tui.TranscriptDivider), theme, width)
}

func styleNativeReplayDividers(rendered, theme string, width int) string {
	if strings.TrimSpace(rendered) == "" {
		return rendered
	}
	rawLines := splitPlainLines(rendered)
	lines := make([]tui.TranscriptProjectionLine, 0, len(rawLines))
	for _, line := range rawLines {
		lines = append(lines, tui.TranscriptProjectionLine{Kind: tui.VisibleLineContent, Text: line})
	}
	return renderStyledNativeProjectionLines(lines, theme, width)
}

func renderStyledNativeProjectionLines(lines []tui.TranscriptProjectionLine, theme string, width int) string {
	if len(lines) == 0 {
		return ""
	}
	if width <= 0 {
		width = 120
	}
	style := uiThemeStyles(theme)
	divider := style.meta.Render(strings.Repeat("─", width))
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if line.Kind == tui.VisibleLineDivider {
			out = append(out, divider)
			continue
		}
		out = append(out, line.Text)
	}
	return strings.Join(out, "\n")
}

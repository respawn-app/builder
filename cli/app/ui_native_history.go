package app

import (
	"fmt"
	"strings"

	"builder/cli/tui"

	tea "github.com/charmbracelet/bubbletea"
)

const nativeHistoryDivergenceStatusMessage = "Transcript sync bug: ongoing scrollback may be stale until reconnect"

func (m *uiModel) syncNativeHistoryFromTranscript() tea.Cmd {
	if !m.windowSizeKnown {
		return nil
	}
	committedEntries := committedTranscriptEntriesForApp(m.transcriptEntries)
	if len(committedEntries) == 0 {
		hasPendingTransientTail := len(tui.PendingOngoingEntries(m.transcriptEntries)) > 0
		alreadyReplayed := m.nativeHistoryReplayed
		m.resetNativeHistoryState()
		m.nativeHistoryReplayed = true
		if hasPendingTransientTail || alreadyReplayed || !m.shouldEmitNativeHistory() {
			return m.sequenceNativeStreamingScrollback(nil)
		}
		return m.sequenceNativeStreamingScrollback(m.emitCurrentNativeScrollbackState(false))
	}

	projection := committedTranscriptProjectionForApp(m.view, m.transcriptEntries)
	committedCount := len(committedEntries)
	if m.nativeFlushedEntryCount < 0 || m.nativeFlushedEntryCount > committedCount {
		m.rebaseNativeProjection(projection, m.transcriptBaseOffset, committedCount)
		return m.sequenceNativeStreamingScrollback(nil)
	}
	if !m.shouldEmitNativeHistory() && m.canFinalizeNativeStreamingCommit(committedEntries, committedCount) {
		return nil
	}
	if cmd, handled := m.finalizeNativeStreamingCommit(projection, committedEntries, committedCount); handled {
		return cmd
	}
	if !m.nativeHistoryReplayed || m.nativeProjection.Empty() {
		m.rebaseNativeProjection(projection, m.transcriptBaseOffset, committedCount)
		if !m.shouldEmitNativeHistory() {
			return nil
		}
		return m.sequenceNativeStreamingScrollback(m.emitCurrentNativeScrollbackState(false))
	}
	previousProjection := m.nativeRenderedProjection
	previousBaseOffset := m.nativeRenderedBaseOffset
	if previousProjection.Empty() {
		previousProjection = m.nativeProjection
		previousBaseOffset = m.nativeProjectionBaseOffset
	}
	previousBlockCount := len(previousProjection.Blocks)
	delta, ok := projection.RenderAppendDeltaFrom(previousProjection, tui.TranscriptDivider)
	m.rebaseNativeProjection(projection, m.transcriptBaseOffset, committedCount)
	if !m.shouldEmitNativeHistory() {
		return nil
	}
	replayPermit := m.consumeNativeHistoryReplayPermit()
	if !ok {
		if appendCmd, appended := m.emitNativeSlidingWindowAppend(projection, previousProjection, m.transcriptBaseOffset, previousBaseOffset); appended {
			return m.sequenceNativeStreamingScrollback(appendCmd)
		}
		if appendCmd, appended := m.emitNativePostRewriteVisibleAppend(projection, previousProjection); appended {
			return m.sequenceNativeStreamingScrollback(appendCmd)
		}
		if replayPermit == nativeHistoryReplayPermitContinuityRecovery {
			return m.sequenceNativeStreamingScrollback(m.emitNonContiguousNativeProjectionRecovery(projection, previousProjection))
		}
		if replayPermit == nativeHistoryReplayPermitModeRestore {
			m.acceptNativeProjectionWithoutReplay(projection)
			return m.sequenceNativeStreamingScrollback(nil)
		}
		if replayPermit == nativeHistoryReplayPermitAuthoritativeHydrate {
			m.acceptNativeProjectionWithoutReplay(projection)
			return m.sequenceNativeStreamingScrollback(m.setTransientStatusWithKind(nativeHistoryDivergenceStatusMessage, uiStatusNoticeError))
		}
		m.acceptNativeProjectionWithoutReplay(projection)
		return m.sequenceNativeStreamingScrollback(m.reportNativeProjectionDivergence(projection, previousProjection))
	}
	if strings.TrimSpace(delta) == "" {
		return m.sequenceNativeStreamingScrollback(nil)
	}
	m.nativeRenderedProjection = projection
	m.nativeRenderedSnapshot = projection.Render(tui.TranscriptDivider)
	return m.sequenceNativeStreamingScrollback(m.emitNativeRenderedText(renderStyledNativeProjectionLines(projection.LinesFromBlock(previousBlockCount, tui.TranscriptDivider), m.theme, m.nativeReplayRenderWidth())))
}

func (m *uiModel) canFinalizeNativeStreamingCommit(committedEntries []tui.TranscriptEntry, committedCount int) bool {
	if m == nil {
		return false
	}
	if strings.TrimSpace(m.view.OngoingStreamingText()) != "" {
		return false
	}
	if !m.nativeStreamingDividerFlushed && m.nativeStreamingFlushedLineCount == 0 {
		return false
	}
	if strings.TrimSpace(m.nativeStreamingText) == "" {
		return false
	}
	previousCommittedCount := m.nativeFlushedEntryCount
	if previousCommittedCount < 0 || previousCommittedCount > committedCount {
		return false
	}
	newEntries := committedEntries[previousCommittedCount:]
	return len(newEntries) > 0 && strings.TrimSpace(newEntries[0].Role) == "assistant" && strings.TrimSpace(newEntries[0].Text) == strings.TrimSpace(m.nativeStreamingText)
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
	m.nativeProjectionBaseOffset = 0
	m.nativeRenderedProjection = tui.TranscriptProjection{}
	m.nativeRenderedBaseOffset = 0
	m.nativeRenderedSnapshot = ""
	m.nativeHistoryReplayPermit = nativeHistoryReplayPermitNone
	m.waitRuntimeEventAfterFlushSequence = 0
	m.resetNativeStreamingState()
	m.discardPendingNativeHistoryFlushes()
}

func (m *uiModel) resetNativeStreamingState() {
	m.nativeStreamingText = ""
	m.nativeStreamingWidth = 0
	m.nativeStreamingFlushedLineCount = 0
	m.nativeStreamingDividerFlushed = false
}

func (m *uiModel) sequenceNativeStreamingScrollback(cmd tea.Cmd) tea.Cmd {
	return sequenceCmds(cmd, m.syncNativeStreamingScrollback())
}

func (m *uiModel) syncNativeStreamingScrollback() tea.Cmd {
	if m == nil || !m.shouldEmitNativeHistory() {
		return nil
	}
	streamText, ok := m.activeNativeStreamingText()
	if !ok {
		m.resetNativeStreamingState()
		return nil
	}
	width := m.nativeReplayRenderWidth()
	m.reconcileNativeStreamingState(streamText, width)
	assistantLines := renderNativeStreamingAssistantLines(streamText, m.theme, width)
	if len(assistantLines) == 0 {
		return nil
	}
	overflowCount := len(assistantLines) - m.nativeStreamingAssistantLiveBudget(width)
	if overflowCount <= 0 {
		return nil
	}
	if overflowCount <= m.nativeStreamingFlushedLineCount {
		return nil
	}
	newAssistantLines := assistantLines[m.nativeStreamingFlushedLineCount:overflowCount]
	if len(newAssistantLines) == 0 {
		return nil
	}
	lines := make([]tui.TranscriptProjectionLine, 0, len(newAssistantLines)+1)
	if len(committedTranscriptEntriesForApp(m.transcriptEntries)) > 0 && !m.nativeStreamingDividerFlushed {
		lines = append(lines, tui.TranscriptProjectionLine{Kind: tui.VisibleLineDivider, Text: tui.TranscriptDivider})
		m.nativeStreamingDividerFlushed = true
	}
	for _, line := range newAssistantLines {
		lines = append(lines, tui.TranscriptProjectionLine{Kind: tui.VisibleLineContent, Text: line})
	}
	m.nativeStreamingFlushedLineCount = overflowCount
	m.nativeStreamingText = streamText
	m.nativeStreamingWidth = width
	return m.emitNativeRenderedText(renderStyledNativeProjectionLines(lines, m.theme, width))
}

func (m *uiModel) activeNativeStreamingText() (string, bool) {
	if m == nil {
		return "", false
	}
	streamText := m.view.OngoingStreamingText()
	if strings.TrimSpace(streamText) == "" {
		return "", false
	}
	// Authoritative ongoing-tail hydrates can populate streaming text before the
	// reviewer/run-state flags or live assistant delta marker are set.
	return streamText, true
}

func (m *uiModel) reconcileNativeStreamingState(streamText string, width int) {
	if m == nil {
		return
	}
	if strings.TrimSpace(streamText) == "" {
		m.resetNativeStreamingState()
		return
	}
	if m.nativeStreamingText == "" {
		m.nativeStreamingText = streamText
		m.nativeStreamingWidth = width
		return
	}
	if width != m.nativeStreamingWidth {
		// Resize keeps old spill immutable in scrollback. Restart visual spill tracking at
		// the new width so future overflow remains unbounded without replaying history.
		m.nativeStreamingWidth = width
		m.nativeStreamingFlushedLineCount = 0
		m.nativeStreamingText = streamText
		return
	}
	if !strings.HasPrefix(streamText, m.nativeStreamingText) {
		m.nativeStreamingFlushedLineCount = 0
		m.nativeStreamingDividerFlushed = false
	}
	m.nativeStreamingText = streamText
	m.nativeStreamingWidth = width
}

func (m *uiModel) nativeStreamingAssistantLiveBudget(width int) int {
	if m == nil || width <= 0 {
		return 0
	}
	style := uiThemeStyles(m.theme)
	budget := m.layout().nativeStreamingViewportLineBudget(width, style)
	if budget <= 0 {
		return 0
	}
	budget -= len(m.layout().renderNativePendingLines(width))
	if !m.nativeStreamingDividerFlushed && len(committedTranscriptEntriesForApp(m.transcriptEntries)) > 0 {
		budget--
	}
	errLines := 0
	for _, line := range splitPlainLines(m.view.OngoingErrorText()) {
		errLines += len(wrapLine(line, width))
	}
	budget -= errLines
	if budget < 0 {
		return 0
	}
	return budget
}

func (m *uiModel) finalizeNativeStreamingCommit(projection tui.TranscriptProjection, committedEntries []tui.TranscriptEntry, committedCount int) (tea.Cmd, bool) {
	if !m.canFinalizeNativeStreamingCommit(committedEntries, committedCount) {
		if m != nil && strings.TrimSpace(m.nativeStreamingText) == "" {
			m.resetNativeStreamingState()
		}
		return nil, false
	}
	previousCommittedCount := m.nativeFlushedEntryCount
	newEntries := committedEntries[previousCommittedCount:]
	if len(newEntries) == 0 {
		m.resetNativeStreamingState()
		return nil, false
	}
	hadCommittedHistory := previousCommittedCount > 0
	flushTail := m.emitNativeRenderedText(renderStyledNativeProjectionLines(m.nativeStreamingPendingTailLines(m.nativeReplayRenderWidth(), hadCommittedHistory), m.theme, m.nativeReplayRenderWidth()))
	postAssistant := m.emitNativeProjectionLinesAfterEntry(projection, previousCommittedCount)
	m.consumeNativeHistoryReplayPermit()
	m.rebaseNativeProjection(projection, m.transcriptBaseOffset, committedCount)
	m.acceptNativeProjectionWithoutReplay(projection)
	m.resetNativeStreamingState()
	return sequenceCmds(flushTail, postAssistant), true
}

func (m *uiModel) nativeStreamingPendingTailLines(width int, hadCommittedHistory bool) []tui.TranscriptProjectionLine {
	if m == nil {
		return nil
	}
	assistantLines := renderNativeStreamingAssistantLines(m.nativeStreamingText, m.theme, width)
	if len(assistantLines) == 0 {
		return nil
	}
	start := m.nativeStreamingFlushedLineCount
	if start < 0 {
		start = 0
	}
	if start > len(assistantLines) {
		start = len(assistantLines)
	}
	lines := make([]tui.TranscriptProjectionLine, 0, len(assistantLines)-start+1)
	if hadCommittedHistory && !m.nativeStreamingDividerFlushed {
		lines = append(lines, tui.TranscriptProjectionLine{Kind: tui.VisibleLineDivider, Text: tui.TranscriptDivider})
	}
	for _, line := range assistantLines[start:] {
		lines = append(lines, tui.TranscriptProjectionLine{Kind: tui.VisibleLineContent, Text: line})
	}
	return lines
}

func (m *uiModel) emitNativeProjectionLinesAfterEntry(projection tui.TranscriptProjection, entryIndex int) tea.Cmd {
	if entryIndex < 0 {
		entryIndex = 0
	}
	startBlock := -1
	for idx, block := range projection.Blocks {
		if block.EntryIndex >= entryIndex+1 {
			startBlock = idx
			break
		}
	}
	if startBlock < 0 {
		return nil
	}
	styled := renderStyledNativeProjectionLines(projection.LinesFromBlock(startBlock, tui.TranscriptDivider), m.theme, m.nativeReplayRenderWidth())
	if strings.TrimSpace(styled) == "" {
		return nil
	}
	return m.emitNativeRenderedText(styled)
}

func (m *uiModel) armNativeHistoryReplayPermit(permit nativeHistoryReplayPermit) {
	if m == nil || permit == nativeHistoryReplayPermitNone {
		return
	}
	if permit == nativeHistoryReplayPermitContinuityRecovery {
		m.nativeHistoryReplayPermit = permit
		return
	}
	if m.nativeHistoryReplayPermit == nativeHistoryReplayPermitContinuityRecovery {
		return
	}
	if permit == nativeHistoryReplayPermitModeRestore {
		m.nativeHistoryReplayPermit = permit
		return
	}
	if m.nativeHistoryReplayPermit == nativeHistoryReplayPermitModeRestore {
		return
	}
	m.nativeHistoryReplayPermit = permit
}

func (m *uiModel) consumeNativeHistoryReplayPermit() nativeHistoryReplayPermit {
	if m == nil {
		return nativeHistoryReplayPermitNone
	}
	permit := m.nativeHistoryReplayPermit
	m.nativeHistoryReplayPermit = nativeHistoryReplayPermitNone
	return permit
}

func (m *uiModel) acceptNativeProjectionWithoutReplay(projection tui.TranscriptProjection) {
	m.nativeRenderedProjection = projection
	m.nativeRenderedBaseOffset = m.nativeProjectionBaseOffset
	m.nativeRenderedSnapshot = projection.Render(tui.TranscriptDivider)
}

func (m *uiModel) reportNativeProjectionDivergence(current tui.TranscriptProjection, rendered tui.TranscriptProjection) tea.Cmd {
	if m.debugMode {
		panic(fmt.Sprintf("same-session committed transcript divergence requires root-cause fix: rendered_blocks=%d current_blocks=%d", len(rendered.Blocks), len(current.Blocks)))
	}
	m.logf("ui.native_history.divergence_detected rendered_blocks=%d current_blocks=%d", len(rendered.Blocks), len(current.Blocks))
	return m.setTransientStatusWithKind(nativeHistoryDivergenceStatusMessage, uiStatusNoticeError)
}

func (m *uiModel) rebaseNativeProjection(projection tui.TranscriptProjection, baseOffset int, committedCount int) {
	m.nativeProjection = projection
	m.nativeProjectionBaseOffset = baseOffset
	m.nativeFlushedEntryCount = committedCount
	m.nativeHistoryReplayed = true
}

func (m *uiModel) emitCurrentNativeScrollbackState(forceFull bool) tea.Cmd {
	replayPermit := m.consumeNativeHistoryReplayPermit()
	if !m.nativeProjection.Empty() {
		return m.emitCurrentNativeHistorySnapshot(forceFull, replayPermit)
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

func (m *uiModel) emitCurrentNativeHistorySnapshot(forceFull bool, replayPermit nativeHistoryReplayPermit) tea.Cmd {
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
			m.nativeRenderedBaseOffset = m.nativeProjectionBaseOffset
			m.nativeRenderedSnapshot = rawSnapshot
			return nil
		}
		if appendCmd, appended := m.emitNativeSlidingWindowAppend(m.nativeProjection, m.nativeRenderedProjection, m.nativeProjectionBaseOffset, m.nativeRenderedBaseOffset); appended {
			return appendCmd
		}
		if appendCmd, appended := m.emitNativePostRewriteVisibleAppend(m.nativeProjection, m.nativeRenderedProjection); appended {
			return appendCmd
		}
		if rewriteRenderedHistory {
			if replayPermit == nativeHistoryReplayPermitContinuityRecovery {
				return m.emitNonContiguousNativeProjectionRecovery(m.nativeProjection, m.nativeRenderedProjection)
			}
			if replayPermit == nativeHistoryReplayPermitModeRestore {
				m.acceptNativeProjectionWithoutReplay(m.nativeProjection)
				return nil
			}
			if replayPermit == nativeHistoryReplayPermitAuthoritativeHydrate {
				m.acceptNativeProjectionWithoutReplay(m.nativeProjection)
				return m.setTransientStatusWithKind(nativeHistoryDivergenceStatusMessage, uiStatusNoticeError)
			}
			m.acceptNativeProjectionWithoutReplay(m.nativeProjection)
			return m.reportNativeProjectionDivergence(m.nativeProjection, m.nativeRenderedProjection)
		}
		forceFull = true
	}
	if !forceFull {
		if deltaRaw, ok := nativeRenderedDelta(m.nativeRenderedSnapshot, rawSnapshot); ok {
			styledDelta := styleNativeReplayDividers(deltaRaw, m.theme, m.nativeReplayRenderWidth())
			m.nativeRenderedProjection = m.nativeProjection
			m.nativeRenderedBaseOffset = m.nativeProjectionBaseOffset
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
	m.nativeRenderedBaseOffset = m.nativeProjectionBaseOffset
	m.nativeRenderedSnapshot = rawSnapshot
	if forceFull {
		return tea.Sequence(tea.ClearScreen, m.emitNativeRenderedText(styled))
	}
	return m.emitNativeRenderedText(styled)
}

func (m *uiModel) emitNativeSlidingWindowAppend(current tui.TranscriptProjection, rendered tui.TranscriptProjection, currentBaseOffset int, renderedBaseOffset int) (tea.Cmd, bool) {
	if current.Empty() || rendered.Empty() {
		return nil, false
	}
	if currentBaseOffset <= renderedBaseOffset {
		return nil, false
	}
	overlapBlocks := current.SharedSuffixPrefixBlockCount(rendered)
	if overlapBlocks <= 0 {
		return nil, false
	}
	m.nativeRenderedProjection = current
	m.nativeRenderedBaseOffset = currentBaseOffset
	m.nativeRenderedSnapshot = current.Render(tui.TranscriptDivider)
	if overlapBlocks >= len(current.Blocks) {
		return nil, true
	}
	styledDelta := renderStyledNativeProjectionLines(current.LinesFromBlock(overlapBlocks, tui.TranscriptDivider), m.theme, m.nativeReplayRenderWidth())
	if strings.TrimSpace(styledDelta) == "" {
		return nil, true
	}
	return m.emitNativeRenderedText(styledDelta), true
}

func (m *uiModel) emitNativePostRewriteVisibleAppend(current tui.TranscriptProjection, rendered tui.TranscriptProjection) (tea.Cmd, bool) {
	if current.Empty() || rendered.Empty() {
		return nil, false
	}
	renderedFrontier, ok := nativeProjectionRenderedFrontier(rendered)
	if !ok {
		return nil, false
	}
	if !nativeProjectionOverlapMatchesRendered(current, rendered, renderedFrontier) {
		return nil, false
	}
	startBlock := nativeProjectionFirstBlockAfterEntry(current, renderedFrontier)
	if startBlock < 0 {
		return nil, false
	}
	m.nativeRenderedProjection = current
	m.nativeRenderedBaseOffset = m.nativeProjectionBaseOffset
	m.nativeRenderedSnapshot = current.Render(tui.TranscriptDivider)
	styledDelta := renderStyledNativeProjectionLines(current.LinesFromBlock(startBlock, tui.TranscriptDivider), m.theme, m.nativeReplayRenderWidth())
	if strings.TrimSpace(styledDelta) == "" {
		return nil, true
	}
	return m.emitNativeRenderedText(styledDelta), true
}

func nativeProjectionRenderedFrontier(projection tui.TranscriptProjection) (int, bool) {
	if len(projection.Blocks) == 0 {
		return 0, false
	}
	frontier := projection.Blocks[len(projection.Blocks)-1].EntryEnd
	if frontier < 0 {
		frontier = projection.Blocks[len(projection.Blocks)-1].EntryIndex
	}
	return frontier, frontier >= 0
}

func nativeProjectionFirstBlockAfterEntry(projection tui.TranscriptProjection, frontier int) int {
	for idx, block := range projection.Blocks {
		if block.EntryIndex > frontier {
			return idx
		}
	}
	return -1
}

func nativeProjectionOverlapMatchesRendered(current tui.TranscriptProjection, rendered tui.TranscriptProjection, frontier int) bool {
	if frontier < 0 {
		return false
	}
	renderedByRange := make(map[[2]int]tui.TranscriptProjectionBlock, len(rendered.Blocks))
	for _, block := range rendered.Blocks {
		renderedByRange[[2]int{block.EntryIndex, block.EntryEnd}] = block
	}
	for _, block := range current.Blocks {
		if block.EntryEnd > frontier {
			continue
		}
		renderedBlock, ok := renderedByRange[[2]int{block.EntryIndex, block.EntryEnd}]
		if !ok || !nativeProjectionBlocksEqual(block, renderedBlock) {
			return false
		}
	}
	return true
}

func nativeProjectionBlocksEqual(left tui.TranscriptProjectionBlock, right tui.TranscriptProjectionBlock) bool {
	if left.Role != right.Role || left.DividerGroup != right.DividerGroup || len(left.Lines) != len(right.Lines) {
		return false
	}
	for idx := range left.Lines {
		if left.Lines[idx] != right.Lines[idx] {
			return false
		}
	}
	return true
}

func (m *uiModel) emitNonContiguousNativeProjectionRecovery(current tui.TranscriptProjection, rendered tui.TranscriptProjection) tea.Cmd {
	if current.Empty() {
		return nil
	}
	m.logf("ui.native_history.rebuild_required rendered_blocks=%d current_blocks=%d", len(rendered.Blocks), len(current.Blocks))
	return m.emitForcedNativeProjectionReplay(current)
}

func (m *uiModel) emitForcedNativeProjectionReplay(projection tui.TranscriptProjection) tea.Cmd {
	rawSnapshot := projection.Render(tui.TranscriptDivider)
	m.nativeRenderedProjection = projection
	m.nativeRenderedBaseOffset = m.nativeProjectionBaseOffset
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
	localIndex := entryIndex - m.transcriptBaseOffset
	if localIndex < 0 || localIndex >= len(m.transcriptEntries) {
		return nil
	}
	entries := m.transcriptEntries[:localIndex+1]
	projection := nativeCommittedProjection(entries, m.theme, m.nativeReplayRenderWidth())
	rawSnapshot := renderNativeCommittedSnapshot(entries, m.theme, m.nativeReplayRenderWidth())
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
	return committedTranscriptEntriesForApp(entries)
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

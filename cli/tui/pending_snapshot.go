package tui

import (
	"strings"
)

func RenderPendingToolSnapshot(entries []TranscriptEntry, theme string, width int, spinner string) string {
	return renderPendingToolSnapshotProjection(entries, theme, width, spinner).Render(TranscriptDivider)
}

func RenderPendingOngoingSnapshot(entries []TranscriptEntry, theme string, width int, spinner string) string {
	return renderPendingOngoingSnapshotProjection(entries, theme, width, spinner).Render(TranscriptDivider)
}

func RenderPendingToolSnapshotLines(entries []TranscriptEntry, theme string, width int, spinner string) []TranscriptProjectionLine {
	return renderPendingToolSnapshotProjection(entries, theme, width, spinner).Lines(TranscriptDivider)
}

func RenderPendingOngoingSnapshotLines(entries []TranscriptEntry, theme string, width int, spinner string) []TranscriptProjectionLine {
	return renderPendingOngoingSnapshotProjection(entries, theme, width, spinner).Lines(TranscriptDivider)
}

func renderPendingToolSnapshotProjection(entries []TranscriptEntry, theme string, width int, spinner string) TranscriptProjection {
	pending := PendingToolEntries(entries)
	if len(pending) == 0 {
		return TranscriptProjection{}
	}
	return renderPendingOngoingSnapshotProjection(pending, theme, width, spinner)
}

func renderPendingOngoingSnapshotProjection(entries []TranscriptEntry, theme string, width int, spinner string) TranscriptProjection {
	if len(entries) == 0 {
		return TranscriptProjection{}
	}
	if width <= 0 {
		width = 120
	}
	model := NewModel(WithTheme(theme), WithPreviewLines(200000))
	next, _ := model.Update(SetViewportSizeMsg{Lines: 200000, Width: width})
	if casted, ok := next.(Model); ok {
		model = casted
	}
	next, _ = model.Update(SetConversationMsg{Entries: entries})
	if casted, ok := next.(Model); ok {
		model = casted
	}
	blocks := model.buildOngoingBlocks(false)
	blocks = model.applyPendingSpinner(blocks, entries, spinner)
	if len(blocks) == 0 {
		return TranscriptProjection{}
	}
	return projectionFromOngoingBlocks(blocks)
}

func (m Model) applyPendingSpinner(blocks []ongoingBlock, entries []TranscriptEntry, spinner string) []ongoingBlock {
	if strings.TrimSpace(spinner) == "" {
		return blocks
	}
	consumedResults := make(map[int]struct{})
	resultIndex := buildToolResultIndex(entries)
	out := make([]ongoingBlock, 0, len(blocks))
	for _, block := range blocks {
		if !m.shouldRenderPendingSpinner(block, entries, consumedResults, resultIndex) {
			out = append(out, block)
			continue
		}
		spinnerSymbol := styleForRole(block.role, m.palette()).Render(spinner)
		rebuilt, ok := m.renderPendingSpinnerBlock(block, entries, spinnerSymbol)
		if !ok {
			out = append(out, block)
			continue
		}
		out = append(out, rebuilt)
	}
	return out
}

func (m Model) renderPendingSpinnerBlock(block ongoingBlock, entries []TranscriptEntry, spinnerSymbol string) (ongoingBlock, bool) {
	if block.entryIndex < 0 || block.entryIndex >= len(entries) {
		return ongoingBlock{}, false
	}
	entry := entries[block.entryIndex]
	if strings.TrimSpace(entry.Role) != "tool_call" {
		return ongoingBlock{}, false
	}
	lines := block.lines
	if isAskQuestionToolCall(entry.ToolCall) {
		question, suggestions, recommendedOptionIndex := askQuestionDisplay(entry.ToolCall, entry.Text)
		lines = m.flattenAskQuestionEntryWithSymbol(block.role, question, suggestions, recommendedOptionIndex, "", false, spinnerSymbol)
	} else {
		combined := m.toolCallDisplayText(entry, block.role, transcriptBlockOptions{mode: transcriptBlockModeOngoing})
		lines = m.flattenEntryWithMetaAndSymbol(block.role, combined, true, entry.ToolCall, spinnerSymbol)
	}
	return ongoingBlock{role: block.role, lines: lines, entryIndex: block.entryIndex, entryEnd: block.entryEnd}, true
}

func (m Model) shouldRenderPendingSpinner(block ongoingBlock, entries []TranscriptEntry, consumedResults map[int]struct{}, resultIndex toolResultIndex) bool {
	if !isToolHeadlineRole(block.role) || len(block.lines) == 0 {
		return false
	}
	if block.entryIndex < 0 || block.entryIndex >= len(entries) {
		return false
	}
	entry := entries[block.entryIndex]
	if strings.TrimSpace(entry.Role) != "tool_call" {
		return false
	}
	resultIdx := resultIndex.findMatchingToolResultIndex(entries, block.entryIndex, consumedResults)
	if resultIdx < 0 {
		return true
	}
	consumedResults[resultIdx] = struct{}{}
	return false
}

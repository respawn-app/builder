package tui

import (
	"strings"

	xansi "github.com/charmbracelet/x/ansi"
)

func RenderPendingToolSnapshot(entries []TranscriptEntry, theme string, width int, spinner string) string {
	if len(entries) == 0 {
		return ""
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
		return ""
	}
	lines := make([]string, 0, len(blocks)*2)
	for idx, block := range blocks {
		if idx > 0 && ongoingDividerGroup(blocks[idx-1].role) != ongoingDividerGroup(block.role) {
			lines = append(lines, detailDivider())
		}
		lines = append(lines, block.lines...)
	}
	return strings.Join(lines, "\n")
}

func (m Model) applyPendingSpinner(blocks []ongoingBlock, entries []TranscriptEntry, spinner string) []ongoingBlock {
	trimmedSpinner := strings.TrimSpace(spinner)
	if trimmedSpinner == "" {
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
		symbol := m.roleSymbol(block.role)
		if symbol == "" {
			out = append(out, block)
			continue
		}
		plainPrefix := rolePrefix(block.role) + " "
		plainFirstLine := xansi.Strip(block.lines[0])
		if !strings.HasPrefix(plainFirstLine, plainPrefix) {
			out = append(out, block)
			continue
		}
		lines := append([]string(nil), block.lines...)
		spinnerSymbol := styleForRole(block.role, m.palette()).Render(trimmedSpinner)
		body := strings.TrimPrefix(plainFirstLine, plainPrefix)
		body = m.palette().preview.Faint(true).Render(body)
		lines[0] = spinnerSymbol + " " + body
		out = append(out, ongoingBlock{role: block.role, lines: lines, entryIndex: block.entryIndex})
	}
	return out
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

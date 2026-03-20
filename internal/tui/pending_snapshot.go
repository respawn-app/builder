package tui

import "strings"

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
	blocks = model.applyPendingSpinner(blocks, spinner)
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

func (m Model) applyPendingSpinner(blocks []ongoingBlock, spinner string) []ongoingBlock {
	trimmedSpinner := strings.TrimSpace(spinner)
	if trimmedSpinner == "" {
		return blocks
	}
	out := make([]ongoingBlock, 0, len(blocks))
	for _, block := range blocks {
		if !isToolHeadlineRole(block.role) || len(block.lines) == 0 {
			out = append(out, block)
			continue
		}
		symbol := m.roleSymbol(block.role)
		if symbol == "" {
			out = append(out, block)
			continue
		}
		prefix := symbol + " "
		if !strings.HasPrefix(block.lines[0], prefix) {
			out = append(out, block)
			continue
		}
		lines := append([]string(nil), block.lines...)
		spinnerSymbol := styleForRole(block.role, m.palette()).Render(trimmedSpinner)
		lines[0] = spinnerSymbol + " " + strings.TrimPrefix(lines[0], prefix)
		out = append(out, ongoingBlock{role: block.role, lines: lines, entryIndex: block.entryIndex})
	}
	return out
}

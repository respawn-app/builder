package tui

import (
	"builder/internal/transcript"
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	xansi "github.com/charmbracelet/x/ansi"
)

func (m Model) renderFlatDetailTranscript() string {
	blocks := m.buildDetailBlocks(true, true)
	if len(blocks) == 0 {
		return ""
	}
	lines := make([]string, 0, len(blocks)*2)
	for idx, block := range blocks {
		if idx > 0 {
			lines = append(lines, detailDivider())
		}
		lines = append(lines, block.lines...)
	}
	return strings.Join(lines, "\n")
}

func (m Model) buildDetailBlocks(includeStreaming bool, applySelection bool) []ongoingBlock {
	blocks := make([]ongoingBlock, 0, len(m.transcript)+1)
	consumedResults := make(map[int]struct{})
	for i := 0; i < len(m.transcript); i++ {
		if _, consumed := consumedResults[i]; consumed {
			continue
		}
		if thinkingBlock, ok := m.trailingThinkingBlockBeforeEntry(m.transcript, i, consumedResults); ok {
			blocks = append(blocks, ongoingBlock{role: "reasoning", lines: thinkingBlock, entryIndex: -1})
		}
		entry := m.transcript[i]
		role := m.entryRole(entry)
		switch role {
		case "tool_call":
			blockRole := "tool"
			if isAskQuestionToolCall(entry.ToolCall) {
				blockRole = "tool_question"
				question, suggestions := askQuestionDisplay(entry.ToolCall, entry.Text)
				answer := ""
				if resultIdx := findMatchingToolResultIndex(m.transcript, i, consumedResults); resultIdx >= 0 {
					nextRole := strings.TrimSpace(m.transcript[resultIdx].Role)
					if isToolResultRole(nextRole) {
						answer = strings.TrimSpace(m.transcript[resultIdx].Text)
						blockRole = toolBlockRoleFromResult(nextRole, blockRole)
						consumedResults[resultIdx] = struct{}{}
					}
				}
				blocks = append(blocks, ongoingBlock{
					role:       blockRole,
					lines:      m.flattenAskQuestionEntry(blockRole, question, suggestions, answer, true),
					entryIndex: i,
				})
				continue
			} else if isShellToolCall(entry.ToolCall, entry.Text) {
				blockRole = "tool_shell"
			}
			_, patchDetail, hasPatchPayload := extractPatchPayload(entry.ToolCall, entry.Text)
			combined := toolCallDisplayText(entry.ToolCall, entry.Text)
			if hasPatchPayload {
				combined = patchDetail
			}
			if resultIdx := findMatchingToolResultIndex(m.transcript, i, consumedResults); resultIdx >= 0 {
				nextRole := strings.TrimSpace(m.transcript[resultIdx].Role)
				resultText := m.transcript[resultIdx].Text
				if strings.TrimSpace(resultText) != "" {
					if !(hasPatchPayload && nextRole != "tool_result_error") {
						combined = combined + "\n" + resultText
					}
				}
				blockRole = toolBlockRoleFromResult(nextRole, blockRole)
				consumedResults[resultIdx] = struct{}{}
			}
			blocks = append(blocks, ongoingBlock{
				role:       blockRole,
				lines:      m.flattenEntryWithMeta(blockRole, combined, false, entry.ToolCall),
				entryIndex: i,
			})
		case "tool_result", "tool_result_ok", "tool_result_error":
			blocks = append(blocks, ongoingBlock{
				role:       toolBlockRoleFromResult(role, "tool"),
				lines:      m.flattenEntry(toolBlockRoleFromResult(role, "tool"), entry.Text),
				entryIndex: i,
			})
		default:
			if isThinkingRole(role) {
				combined := strings.TrimSpace(entry.Text)
				for j := i + 1; j < len(m.transcript); j++ {
					nextRole := strings.TrimSpace(m.transcript[j].Role)
					if !isThinkingRole(nextRole) {
						break
					}
					nextText := strings.TrimSpace(m.transcript[j].Text)
					if nextText != "" {
						if combined == "" {
							combined = nextText
						} else {
							combined += "\n" + nextText
						}
					}
					consumedResults[j] = struct{}{}
				}
				blocks = append(blocks, ongoingBlock{
					role:       role,
					lines:      m.flattenEntry(role, combined),
					entryIndex: i,
				})
				continue
			}
			block := m.flattenEntry(role, entry.Text)
			if applySelection {
				block = m.maybeSelectedUserBlock(i, role, block)
			}
			blocks = append(blocks, ongoingBlock{
				role:       role,
				lines:      block,
				entryIndex: i,
			})
		}
	}
	if includeStreaming {
		if lines := m.streamingReasoningLines(); len(lines) > 0 {
			blocks = append(blocks, ongoingBlock{role: "reasoning", lines: lines, entryIndex: -1})
		}
	}
	if includeStreaming && m.ongoing != "" {
		blocks = append(blocks, ongoingBlock{
			role:       "assistant",
			lines:      m.flattenEntry("assistant", m.ongoing),
			entryIndex: -1,
		})
	}
	return blocks
}

func (m Model) streamingReasoningLines() []string {
	if len(m.streamingReasoning) == 0 {
		return nil
	}
	parts := make([]string, 0, len(m.streamingReasoning))
	for _, entry := range m.streamingReasoning {
		text := strings.TrimSpace(entry.Text)
		if text == "" {
			continue
		}
		parts = append(parts, text)
	}
	if len(parts) == 0 {
		return nil
	}
	return m.flattenEntry("reasoning", strings.Join(parts, "\n"))
}

func (m Model) trailingThinkingBlockBeforeEntry(entries []TranscriptEntry, idx int, consumed map[int]struct{}) ([]string, bool) {
	if idx < 0 || idx >= len(entries) {
		return nil, false
	}
	role := m.entryRole(entries[idx])
	if role != "assistant" && role != "assistant_commentary" && role != "tool_call" {
		return nil, false
	}
	actionEnd := idx
	for actionEnd+1 < len(entries) {
		next := actionEnd + 1
		if _, used := consumed[next]; used {
			break
		}
		if strings.TrimSpace(entries[next].Role) != "tool_call" {
			break
		}
		actionEnd = next
	}
	thinkingStart := actionEnd + 1
	if thinkingStart >= len(entries) {
		return nil, false
	}
	if _, used := consumed[thinkingStart]; used {
		return nil, false
	}
	if !isThinkingRole(strings.TrimSpace(entries[thinkingStart].Role)) {
		return nil, false
	}

	combined := strings.TrimSpace(entries[thinkingStart].Text)
	consumed[thinkingStart] = struct{}{}
	for j := thinkingStart + 1; j < len(entries); j++ {
		if _, used := consumed[j]; used {
			break
		}
		if !isThinkingRole(strings.TrimSpace(entries[j].Role)) {
			break
		}
		nextText := strings.TrimSpace(entries[j].Text)
		if nextText != "" {
			if combined == "" {
				combined = nextText
			} else {
				combined += "\n" + nextText
			}
		}
		consumed[j] = struct{}{}
	}

	if combined == "" {
		return nil, false
	}
	return m.flattenEntry("reasoning", combined), true
}

func (m Model) renderFlatOngoingTranscript() string {
	return m.renderFlatOngoingTranscriptWithStreaming(true)
}

func (m Model) renderFlatCommittedOngoingTranscript() string {
	return m.renderFlatOngoingTranscriptWithStreaming(false)
}

func (m Model) renderFlatOngoingTranscriptWithStreaming(includeStreaming bool) string {
	blocks := m.buildOngoingBlocks(includeStreaming)
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

func (m Model) buildOngoingBlocks(includeStreaming bool) []ongoingBlock {
	blocks := make([]ongoingBlock, 0, len(m.transcript)+1)
	consumedResults := make(map[int]struct{})
	for i := 0; i < len(m.transcript); i++ {
		if _, consumed := consumedResults[i]; consumed {
			continue
		}
		entry := m.transcript[i]
		role := m.entryRole(entry)
		if skipInOngoing(role) {
			continue
		}
		switch role {
		case "tool_call":
			blockRole := "tool"
			if isAskQuestionToolCall(entry.ToolCall) {
				blockRole = "tool_question"
				question, suggestions := askQuestionDisplay(entry.ToolCall, entry.Text)
				answer := ""
				if resultIdx := findMatchingToolResultIndex(m.transcript, i, consumedResults); resultIdx >= 0 {
					nextRole := strings.TrimSpace(m.transcript[resultIdx].Role)
					if isToolResultRole(nextRole) {
						answer = strings.TrimSpace(m.transcript[resultIdx].Text)
						blockRole = toolBlockRoleFromResult(nextRole, blockRole)
						consumedResults[resultIdx] = struct{}{}
					}
				}
				blocks = append(blocks, ongoingBlock{
					role:       blockRole,
					lines:      m.flattenAskQuestionEntry(blockRole, question, suggestions, answer, false),
					entryIndex: i,
				})
				continue
			} else if isShellToolCall(entry.ToolCall, entry.Text) {
				blockRole = "tool_shell"
			}
			patchSummary, _, hasPatchPayload := extractPatchPayload(entry.ToolCall, entry.Text)
			combined := compactToolCallText(entry.ToolCall, entry.Text)
			if blockRole == "tool_shell" {
				combined = compactOngoingShellPreviewText(combined)
			}
			if hasPatchPayload {
				combined = strings.TrimSpace(patchSummary)
			}
			if resultIdx := findMatchingToolResultIndex(m.transcript, i, consumedResults); resultIdx >= 0 {
				nextRole := strings.TrimSpace(m.transcript[resultIdx].Role)
				if isToolResultRole(nextRole) {
					blockRole = toolBlockRoleFromResult(nextRole, blockRole)
					consumedResults[resultIdx] = struct{}{}
				}
			}
			blocks = append(blocks, ongoingBlock{
				role:       blockRole,
				lines:      m.flattenEntryWithMeta(blockRole, combined, true, entry.ToolCall),
				entryIndex: i,
			})
		case "tool_result", "tool_result_ok", "tool_result_error":
			continue
		default:
			text := entry.Text
			if role == "reviewer_status" {
				text = compactReviewerStatusForOngoing(text)
			}
			lines := m.flattenEntry(role, text)
			blocks = append(blocks, ongoingBlock{
				role:       role,
				lines:      m.maybeSelectedUserBlock(i, role, lines),
				entryIndex: i,
			})
		}
	}
	if includeStreaming && m.ongoing != "" {
		blocks = append(blocks, ongoingBlock{
			role:       "assistant",
			lines:      m.flattenEntryPlain("assistant", m.ongoing),
			entryIndex: -1,
		})
	}
	return blocks
}

func (m Model) ongoingLineRangeForEntry(entryIndex int) (int, int, bool) {
	if entryIndex < 0 {
		return 0, 0, false
	}
	blocks := m.buildOngoingBlocks(true)
	lineOffset := 0
	for idx, block := range blocks {
		if idx > 0 && ongoingDividerGroup(blocks[idx-1].role) != ongoingDividerGroup(block.role) {
			lineOffset++
		}
		start := lineOffset
		end := lineOffset + len(block.lines) - 1
		if block.entryIndex == entryIndex {
			return start, end, true
		}
		lineOffset += len(block.lines)
	}
	return 0, 0, false
}

func (m Model) detailLineRangeForEntry(entryIndex int) (int, int, bool) {
	if entryIndex < 0 {
		return 0, 0, false
	}
	if entryIndex < len(m.detailEntryLineRanges) {
		rangeForEntry := m.detailEntryLineRanges[entryIndex]
		if rangeForEntry.Start >= 0 && rangeForEntry.End >= rangeForEntry.Start {
			return rangeForEntry.Start, rangeForEntry.End, true
		}
		return 0, 0, false
	}
	blocks := m.buildDetailBlocks(true, false)
	lineOffset := 0
	for idx, block := range blocks {
		if idx > 0 {
			lineOffset++
		}
		start := lineOffset
		end := lineOffset + len(block.lines) - 1
		if block.entryIndex == entryIndex {
			return start, end, true
		}
		lineOffset += len(block.lines)
	}
	return 0, 0, false
}

func (m Model) flattenEntry(role, text string) []string {
	return m.flattenEntryWithMeta(role, text, false, nil)
}

func (m Model) flattenEntryWithMutedText(role, text string, muteText bool) []string {
	return m.flattenEntryWithMeta(role, text, muteText, nil)
}

func (m Model) flattenEntryWithMeta(role, text string, muteText bool, toolMeta *transcript.ToolCallMeta) []string {
	renderWidth := m.viewportWidth
	if rolePrefix(role) != "" {
		renderWidth -= 2
	}
	if isThinkingRole(role) {
		return m.flattenThinkingEntry(role, text, renderWidth)
	}
	type lineWithKind struct {
		text string
		kind string
	}
	rendered := ""
	lines := make([]lineWithKind, 0, 8)
	if !muteText {
		if diffLines, ok := m.renderDiffToolLines(text, renderWidth, toolMeta); ok {
			for _, line := range diffLines {
				item := lineWithKind{text: line.Text}
				switch line.Kind {
				case diffRenderAdd:
					item.kind = "add"
				case diffRenderRemove:
					item.kind = "remove"
				}
				lines = append(lines, item)
			}
		} else {
			rendered = m.renderEntryText(role, text, renderWidth, toolMeta, muteText)
			for _, chunk := range splitLines(rendered) {
				lines = append(lines, lineWithKind{text: chunk})
			}
		}
	} else {
		rendered = m.renderEntryText(role, text, renderWidth, toolMeta, muteText)
		for _, chunk := range splitLines(rendered) {
			lines = append(lines, lineWithKind{text: chunk})
		}
	}
	if len(lines) == 0 {
		lines = []lineWithKind{{text: ""}}
	}
	plainLines := make([]string, 0, len(lines))
	for _, line := range lines {
		plainLines = append(plainLines, line.text)
	}
	isEditedBlock := isEditedToolBlock(plainLines)
	symbol := m.roleSymbol(role)
	out := make([]string, 0, len(lines))
	for i, line := range lines {
		displayChunk := line.text
		diffKind := line.kind
		if isToolHeadlineRole(role) {
			if i == 0 {
				displayChunk = m.renderToolHeadline(displayChunk, renderWidth)
			}
			displayChunk = m.styleToolLine(displayChunk)
		}
		if muteText && strings.TrimSpace(displayChunk) != "" && !isEditedBlock {
			displayChunk = m.palette().preview.Faint(true).Render(displayChunk)
		} else if role == "reviewer_status" && isReviewerCacheHitLine(displayChunk) {
			displayChunk = m.palette().preview.Faint(true).Render(displayChunk)
		} else if isThinkingRole(role) {
			displayChunk = styleForRole(role, m.palette()).Render(displayChunk)
		} else if role == "compaction_notice" || role == "compaction_summary" || role == "reviewer_status" || role == "reviewer_suggestions" || role == "error" {
			displayChunk = styleForRole(role, m.palette()).Render(displayChunk)
		}
		if i == 0 {
			line := ""
			if symbol == "" {
				line = displayChunk
			} else {
				line = fmt.Sprintf("%s %s", symbol, displayChunk)
			}
			if diffKind != "" {
				line = m.tintToolDiffLine(line, diffKind)
			}
			out = append(out, line)
			continue
		}
		if strings.TrimSpace(displayChunk) == "" {
			out = append(out, "")
			continue
		}
		line := "  " + displayChunk
		if diffKind != "" {
			line = m.tintToolDiffLine(line, diffKind)
		}
		out = append(out, line)
	}
	if muteText && isShellPreviewRole(role) && shellPreviewShouldCollapse(text) {
		ellipsis := "  " + m.palette().preview.Faint(true).Render("…")
		if len(out) == 0 {
			return []string{"", ellipsis}
		}
		return []string{out[0], ellipsis}
	}
	return out
}

func (m Model) flattenThinkingEntry(role, text string, renderWidth int) []string {
	if renderWidth < 1 {
		renderWidth = 1
	}
	chunks := splitLines(wrapTextForViewport(text, renderWidth))
	if len(chunks) == 0 {
		chunks = []string{""}
	}
	style := styleForRole(role, m.palette())
	out := make([]string, 0, len(chunks))
	for i, chunk := range chunks {
		display := style.Render(chunk)
		if i == 0 {
			out = append(out, display)
			continue
		}
		if strings.TrimSpace(chunk) == "" {
			out = append(out, "")
			continue
		}
		out = append(out, "  "+display)
	}
	return out
}

func isEditedToolBlock(lines []string) bool {
	for _, line := range lines {
		trimmed := strings.TrimSpace(xansi.Strip(line))
		if trimmed == "" {
			continue
		}
		return strings.HasPrefix(trimmed, "Edited:")
	}
	return false
}

func (m Model) renderDiffToolLines(text string, width int, toolMeta *transcript.ToolCallMeta) ([]diffRenderedLine, bool) {
	if toolMeta == nil || !toolMeta.HasRenderHint() || m.code == nil {
		return nil, false
	}
	hint := toolMeta.RenderHint
	if hint == nil || hint.Kind != transcript.ToolRenderKindDiff {
		return nil, false
	}
	highlightTarget := text
	prefix := ""
	if hint.ResultOnly {
		parts := strings.SplitN(text, "\n", 2)
		if len(parts) != 2 || strings.TrimSpace(parts[1]) == "" {
			return nil, false
		}
		prefix = parts[0]
		highlightTarget = parts[1]
	}
	lines, ok := m.code.renderDiffLines(highlightTarget, width)
	if !ok {
		return nil, false
	}
	if strings.TrimSpace(prefix) == "" {
		return lines, true
	}
	wrappedPrefix := splitLines(wrapTextForViewport(prefix, width))
	combined := make([]diffRenderedLine, 0, len(wrappedPrefix)+len(lines))
	for _, line := range wrappedPrefix {
		combined = append(combined, diffRenderedLine{Kind: diffRenderMeta, Text: line})
	}
	combined = append(combined, lines...)
	return combined, true
}

func (m Model) flattenEntryPlain(role, text string) []string {
	renderWidth := m.viewportWidth
	if rolePrefix(role) != "" {
		renderWidth -= 2
	}
	chunks := splitLines(wrapTextForViewport(text, renderWidth))
	if len(chunks) == 0 {
		chunks = []string{""}
	}
	symbol := m.roleSymbol(role)
	out := make([]string, 0, len(chunks))
	for i, chunk := range chunks {
		if i == 0 {
			if symbol == "" {
				out = append(out, chunk)
				continue
			}
			out = append(out, fmt.Sprintf("%s %s", symbol, chunk))
			continue
		}
		if strings.TrimSpace(chunk) == "" {
			out = append(out, "")
			continue
		}
		out = append(out, "  "+chunk)
	}
	return out
}

func (m Model) maybeSelectedUserBlock(entryIndex int, role string, lines []string) []string {
	if !m.selectedTranscriptActive {
		return lines
	}
	if entryIndex != m.selectedTranscriptEntry {
		return lines
	}
	if strings.TrimSpace(role) != "user" {
		return lines
	}
	style := lipgloss.NewStyle().Background(lipgloss.Color("15")).Foreground(lipgloss.Color("0"))
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		out = append(out, style.Render(line))
	}
	return out
}

func (m Model) renderEntryText(role, text string, width int, toolMeta *transcript.ToolCallMeta, muteText bool) string {
	if strings.TrimSpace(text) == "" {
		return text
	}
	if isThinkingRole(role) {
		return wrapTextForViewport(text, width)
	}
	if !muteText {
		if highlighted, ok := m.renderToolTextWithHighlight(role, text, width, toolMeta); ok {
			return highlighted
		}
	}
	if !isMarkdownRole(role) {
		return wrapTextForViewport(text, width)
	}
	if m.md == nil {
		return wrapTextForViewport(text, width)
	}
	rendered, err := m.md.render(role, text, width)
	if err != nil {
		return wrapTextForViewport(text, width)
	}
	return rendered
}

func (m Model) renderToolTextWithHighlight(role, text string, width int, toolMeta *transcript.ToolCallMeta) (string, bool) {
	if !isToolHeadlineRole(role) || toolMeta == nil || !toolMeta.HasRenderHint() || m.code == nil {
		return "", false
	}
	hint := toolMeta.RenderHint
	if hint.Kind == transcript.ToolRenderKindDiff {
		return "", false
	}
	highlightTarget := text
	prefix := ""
	if hint.ResultOnly {
		parts := strings.SplitN(text, "\n", 2)
		if len(parts) != 2 || strings.TrimSpace(parts[1]) == "" {
			return "", false
		}
		prefix = parts[0]
		highlightTarget = parts[1]
	}
	rendered, ok := m.code.render(hint, highlightTarget)
	if !ok {
		return "", false
	}
	if prefix != "" {
		rendered = prefix + "\n" + rendered
	}
	return wrapTextForViewport(rendered, width), true
}

func wrapTextForViewport(text string, width int) string {
	if width < 1 {
		width = 1
	}
	wrapped := xansi.Wordwrap(text, width, " ,.;-+|")
	return strings.TrimRight(wrapped, "\n")
}

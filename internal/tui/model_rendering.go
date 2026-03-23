package tui

import (
	"builder/internal/transcript"
	"strings"
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
	return m.buildTranscriptBlocks(transcriptBlockOptions{
		mode:             transcriptBlockModeDetail,
		includeStreaming: includeStreaming,
		applySelection:   applySelection,
	})
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
	return m.buildTranscriptBlocks(transcriptBlockOptions{
		mode:             transcriptBlockModeOngoing,
		includeStreaming: includeStreaming,
	})
}

type transcriptBlockMode int

const (
	transcriptBlockModeDetail transcriptBlockMode = iota
	transcriptBlockModeOngoing
)

type transcriptBlockOptions struct {
	mode             transcriptBlockMode
	includeStreaming bool
	applySelection   bool
}

func (m Model) buildTranscriptBlocks(opts transcriptBlockOptions) []ongoingBlock {
	blocks := make([]ongoingBlock, 0, len(m.transcript)+1)
	consumedResults := make(map[int]struct{})
	resultIndex := buildToolResultIndex(m.transcript)
	for idx := 0; idx < len(m.transcript); idx++ {
		if _, consumed := consumedResults[idx]; consumed {
			continue
		}
		if reasoningBlock, ok := m.prefixedReasoningBlock(idx, consumedResults, opts); ok {
			blocks = append(blocks, reasoningBlock)
		}
		entry := m.transcript[idx]
		role := m.entryRole(entry)
		if opts.mode == transcriptBlockModeOngoing && skipInOngoing(role) {
			continue
		}
		block, ok := m.entryBlock(idx, entry, role, consumedResults, resultIndex, opts)
		if !ok {
			continue
		}
		blocks = append(blocks, block)
	}
	return m.appendStreamingBlocks(blocks, opts)
}

func (m Model) prefixedReasoningBlock(entryIndex int, consumed map[int]struct{}, opts transcriptBlockOptions) (ongoingBlock, bool) {
	if opts.mode != transcriptBlockModeDetail {
		return ongoingBlock{}, false
	}
	thinkingBlock, ok := m.trailingThinkingBlockBeforeEntry(m.transcript, entryIndex, consumed)
	if !ok {
		return ongoingBlock{}, false
	}
	return ongoingBlock{role: "reasoning", lines: thinkingBlock, entryIndex: -1}, true
}

func (m Model) entryBlock(entryIndex int, entry TranscriptEntry, role string, consumed map[int]struct{}, resultIndex toolResultIndex, opts transcriptBlockOptions) (ongoingBlock, bool) {
	switch role {
	case "tool_call":
		return m.toolCallBlock(entryIndex, entry, consumed, resultIndex, opts), true
	case "tool_result", "tool_result_ok", "tool_result_error":
		if opts.mode == transcriptBlockModeOngoing {
			return ongoingBlock{}, false
		}
		blockRole := toolBlockRoleFromResult(role, "tool")
		return ongoingBlock{
			role:       blockRole,
			lines:      m.flattenEntry(blockRole, entry.Text),
			entryIndex: entryIndex,
		}, true
	default:
		return m.standardEntryBlock(entryIndex, entry, role, consumed, opts), true
	}
}

func (m Model) toolCallBlock(entryIndex int, entry TranscriptEntry, consumed map[int]struct{}, resultIndex toolResultIndex, opts transcriptBlockOptions) ongoingBlock {
	blockRole := "tool"
	if isAskQuestionToolCall(entry.ToolCall) {
		return m.askQuestionBlock(entryIndex, entry, consumed, resultIndex, opts, blockRole)
	}
	if isWebSearchToolCall(entry.ToolCall) {
		blockRole = "tool_web_search"
	} else if isShellToolCall(entry.ToolCall, entry.Text) {
		blockRole = "tool_shell"
	}
	combined := m.toolCallDisplayText(entry, blockRole, opts)
	blockRole, combined = m.applyToolResult(entryIndex, entry.ToolCall, blockRole, combined, consumed, resultIndex, opts)
	return ongoingBlock{
		role:       blockRole,
		lines:      m.flattenEntryWithMeta(blockRole, combined, opts.mode == transcriptBlockModeOngoing, entry.ToolCall),
		entryIndex: entryIndex,
	}
}

func (m Model) askQuestionBlock(entryIndex int, entry TranscriptEntry, consumed map[int]struct{}, resultIndex toolResultIndex, opts transcriptBlockOptions, defaultRole string) ongoingBlock {
	blockRole := "tool_question"
	question, suggestions := askQuestionDisplay(entry.ToolCall, entry.Text)
	answer := ""
	if resultIdx := resultIndex.findMatchingToolResultIndex(m.transcript, entryIndex, consumed); resultIdx >= 0 {
		nextRole := strings.TrimSpace(m.transcript[resultIdx].Role)
		if isToolResultRole(nextRole) {
			answer = strings.TrimSpace(m.transcript[resultIdx].Text)
			blockRole = toolBlockRoleFromResult(nextRole, blockRole)
			consumed[resultIdx] = struct{}{}
		}
	}
	return ongoingBlock{
		role:       blockRole,
		lines:      m.flattenAskQuestionEntry(blockRole, question, suggestions, answer, opts.mode == transcriptBlockModeDetail),
		entryIndex: entryIndex,
	}
}

func (m Model) toolCallDisplayText(entry TranscriptEntry, blockRole string, opts transcriptBlockOptions) string {
	if opts.mode == transcriptBlockModeDetail {
		return toolCallDisplayText(entry.ToolCall, entry.Text)
	}
	combined := compactToolCallText(entry.ToolCall, entry.Text)
	if blockRole == "tool_shell" {
		combined = compactOngoingShellPreviewText(combined)
	}
	return combined
}

func (m Model) applyToolResult(entryIndex int, meta *transcript.ToolCallMeta, blockRole string, combined string, consumed map[int]struct{}, resultIndex toolResultIndex, opts transcriptBlockOptions) (string, string) {
	resultIdx := resultIndex.findMatchingToolResultIndex(m.transcript, entryIndex, consumed)
	if resultIdx < 0 {
		return blockRole, combined
	}
	nextRole := strings.TrimSpace(m.transcript[resultIdx].Role)
	if opts.mode == transcriptBlockModeDetail {
		resultText := m.transcript[resultIdx].Text
		omitSuccessfulResult := meta != nil && meta.OmitSuccessfulResult && nextRole != "tool_result_error"
		if strings.TrimSpace(resultText) != "" && !omitSuccessfulResult {
			combined += "\n" + resultText
		}
	}
	if isToolResultRole(nextRole) {
		blockRole = toolBlockRoleFromResult(nextRole, blockRole)
		consumed[resultIdx] = struct{}{}
	}
	return blockRole, combined
}

func (m Model) standardEntryBlock(entryIndex int, entry TranscriptEntry, role string, consumed map[int]struct{}, opts transcriptBlockOptions) ongoingBlock {
	if opts.mode == transcriptBlockModeDetail && isThinkingRole(role) {
		return ongoingBlock{
			role:       role,
			lines:      m.flattenEntry(role, m.combinedThinkingText(entryIndex, consumed)),
			entryIndex: entryIndex,
		}
	}
	text := entry.Text
	if opts.mode == transcriptBlockModeOngoing {
		text = m.ongoingEntryText(entry)
		if role == "reviewer_status" {
			text = compactReviewerStatusForOngoing(text)
		} else if role == "reviewer_suggestions" {
			text = compactReviewerSuggestionsForOngoing(text)
		}
	}
	lines := m.flattenEntry(role, text)
	if opts.applySelection {
		lines = m.maybeSelectedUserBlock(entryIndex, role, lines)
	}
	return ongoingBlock{role: role, lines: lines, entryIndex: entryIndex}
}

func (m Model) combinedThinkingText(entryIndex int, consumed map[int]struct{}) string {
	combined := strings.TrimSpace(m.transcript[entryIndex].Text)
	for idx := entryIndex + 1; idx < len(m.transcript); idx++ {
		if _, used := consumed[idx]; used {
			break
		}
		if !isThinkingRole(strings.TrimSpace(m.transcript[idx].Role)) {
			break
		}
		nextText := strings.TrimSpace(m.transcript[idx].Text)
		if nextText != "" {
			if combined == "" {
				combined = nextText
			} else {
				combined += "\n" + nextText
			}
		}
		consumed[idx] = struct{}{}
	}
	return combined
}

func (m Model) appendStreamingBlocks(blocks []ongoingBlock, opts transcriptBlockOptions) []ongoingBlock {
	if opts.includeStreaming && opts.mode == transcriptBlockModeDetail {
		if lines := m.streamingReasoningLines(); len(lines) > 0 {
			blocks = append(blocks, ongoingBlock{role: "reasoning", lines: lines, entryIndex: -1})
		}
	}
	if !opts.includeStreaming || m.ongoing == "" {
		return blocks
	}
	lines := m.flattenEntry("assistant", m.ongoing)
	if opts.mode == transcriptBlockModeOngoing {
		lines = m.flattenEntryPlain("assistant", m.ongoing)
	}
	return append(blocks, ongoingBlock{role: "assistant", lines: lines, entryIndex: -1})
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

func (m Model) ongoingEntryText(entry TranscriptEntry) string {
	if strings.TrimSpace(entry.OngoingText) != "" {
		return entry.OngoingText
	}
	return entry.Text
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

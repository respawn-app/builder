package tui

import "strings"

type TranscriptProjection struct {
	Blocks []TranscriptProjectionBlock
}

type TranscriptProjectionLine struct {
	Kind VisibleLineKind
	Text string
}

type TranscriptProjectionBlock struct {
	Role         string
	DividerGroup string
	EntryIndex   int
	EntryEnd     int
	Lines        []string
}

func (p TranscriptProjection) Empty() bool {
	return len(p.Blocks) == 0
}

func (p TranscriptProjection) Lines(dividerText string) []TranscriptProjectionLine {
	if len(p.Blocks) == 0 {
		return nil
	}
	lines := make([]TranscriptProjectionLine, 0, len(p.Blocks)*2)
	for idx, block := range p.Blocks {
		if idx > 0 && p.Blocks[idx-1].DividerGroup != block.DividerGroup {
			lines = append(lines, TranscriptProjectionLine{Kind: VisibleLineDivider, Text: dividerText})
		}
		for _, line := range block.Lines {
			lines = append(lines, TranscriptProjectionLine{Kind: VisibleLineContent, Text: line})
		}
	}
	return lines
}

func (p TranscriptProjection) Render(divider string) string {
	lines := p.Lines(divider)
	if len(lines) == 0 {
		return ""
	}
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		out = append(out, line.Text)
	}
	return strings.Join(out, "\n")
}

func (p TranscriptProjection) RenderWithBlockSeparator(separator string) string {
	if len(p.Blocks) == 0 {
		return ""
	}
	lines := make([]string, 0, len(p.Blocks)*2)
	for idx, block := range p.Blocks {
		if idx > 0 {
			lines = append(lines, separator)
		}
		lines = append(lines, block.Lines...)
	}
	return strings.Join(lines, "\n")
}

func (p TranscriptProjection) RenderAppendDeltaFrom(previous TranscriptProjection, divider string) (string, bool) {
	if len(previous.Blocks) == 0 {
		return p.Render(divider), true
	}
	if len(previous.Blocks) > len(p.Blocks) {
		return "", false
	}
	for idx, prior := range previous.Blocks {
		if !prior.equal(p.Blocks[idx]) {
			return "", false
		}
	}
	if len(previous.Blocks) == len(p.Blocks) {
		return "", true
	}
	return p.renderFromBlock(len(previous.Blocks), divider), true
}

func (p TranscriptProjection) SharedPrefixBlockCount(other TranscriptProjection) int {
	limit := min(len(p.Blocks), len(other.Blocks))
	for idx := 0; idx < limit; idx++ {
		if !p.Blocks[idx].equal(other.Blocks[idx]) {
			return idx
		}
	}
	return limit
}

func (p TranscriptProjection) SharedSuffixPrefixBlockCount(previous TranscriptProjection) int {
	limit := min(len(p.Blocks), len(previous.Blocks))
	for overlap := limit; overlap > 0; overlap-- {
		start := len(previous.Blocks) - overlap
		matches := true
		for idx := 0; idx < overlap; idx++ {
			if !p.Blocks[idx].equal(previous.Blocks[start+idx]) {
				matches = false
				break
			}
		}
		if matches {
			return overlap
		}
	}
	return 0
}

func (p TranscriptProjection) LinesFromBlock(start int, dividerText string) []TranscriptProjectionLine {
	if start < 0 {
		start = 0
	}
	if start >= len(p.Blocks) {
		return nil
	}
	lines := make([]TranscriptProjectionLine, 0, (len(p.Blocks)-start)*2)
	for idx := start; idx < len(p.Blocks); idx++ {
		if idx > 0 && p.Blocks[idx-1].DividerGroup != p.Blocks[idx].DividerGroup {
			lines = append(lines, TranscriptProjectionLine{Kind: VisibleLineDivider, Text: dividerText})
		}
		for _, line := range p.Blocks[idx].Lines {
			lines = append(lines, TranscriptProjectionLine{Kind: VisibleLineContent, Text: line})
		}
	}
	return lines
}

func (p TranscriptProjection) renderFromBlock(start int, divider string) string {
	if start < 0 {
		start = 0
	}
	if start >= len(p.Blocks) {
		return ""
	}
	lines := make([]string, 0, (len(p.Blocks)-start)*2)
	for idx := start; idx < len(p.Blocks); idx++ {
		if idx > 0 && p.Blocks[idx-1].DividerGroup != p.Blocks[idx].DividerGroup {
			lines = append(lines, divider)
		}
		lines = append(lines, p.Blocks[idx].Lines...)
	}
	return strings.Join(lines, "\n")
}

func (b TranscriptProjectionBlock) equal(other TranscriptProjectionBlock) bool {
	if b.Role != other.Role || b.DividerGroup != other.DividerGroup || len(b.Lines) != len(other.Lines) {
		return false
	}
	for idx := range b.Lines {
		if b.Lines[idx] != other.Lines[idx] {
			return false
		}
	}
	return true
}

func (m Model) OngoingProjection(includeStreaming bool) TranscriptProjection {
	return projectionFromOngoingBlocks(m.buildOngoingBlocks(includeStreaming))
}

func (m Model) CommittedOngoingProjection() TranscriptProjection {
	return m.CommittedOngoingProjectionForEntries(m.transcript)
}

func (m Model) CommittedOngoingProjectionForEntries(entries []TranscriptEntry) TranscriptProjection {
	committed := CommittedOngoingEntries(m.transcript)
	if len(entries) > 0 {
		committed = CommittedOngoingEntries(entries)
	}
	if len(committed) == 0 {
		return TranscriptProjection{}
	}
	clone := m
	clone.transcript = append([]TranscriptEntry(nil), committed...)
	clone.ongoing = ""
	clone.streamingReasoning = nil
	return projectionFromOngoingBlocks(clone.buildOngoingBlocks(false))
}

func (m Model) DetailProjection(includeStreaming bool, applySelection bool) TranscriptProjection {
	return projectionFromDetailBlocks(m.buildDetailBlocks(includeStreaming, applySelection))
}

func projectionFromOngoingBlocks(blocks []ongoingBlock) TranscriptProjection {
	projection := TranscriptProjection{Blocks: make([]TranscriptProjectionBlock, 0, len(blocks))}
	for _, block := range blocks {
		projection.Blocks = append(projection.Blocks, TranscriptProjectionBlock{
			Role:         block.role,
			DividerGroup: ongoingDividerGroup(block.role),
			EntryIndex:   block.entryIndex,
			EntryEnd:     block.entryEnd,
			Lines:        append([]string(nil), block.lines...),
		})
	}
	return projection
}

func projectionFromDetailBlocks(blocks []ongoingBlock) TranscriptProjection {
	projection := TranscriptProjection{Blocks: make([]TranscriptProjectionBlock, 0, len(blocks))}
	for _, block := range blocks {
		projection.Blocks = append(projection.Blocks, TranscriptProjectionBlock{
			Role:         block.role,
			DividerGroup: "detail",
			EntryIndex:   block.entryIndex,
			EntryEnd:     block.entryEnd,
			Lines:        append([]string(nil), block.lines...),
		})
	}
	return projection
}

func CommittedOngoingEntries(entries []TranscriptEntry) []TranscriptEntry {
	if len(entries) == 0 {
		return nil
	}
	prefixEnd := committedOngoingPrefixEnd(entries)
	if prefixEnd <= 0 {
		return nil
	}
	return nonEmptyTranscriptEntries(entries[:prefixEnd])
}

func PendingOngoingEntries(entries []TranscriptEntry) []TranscriptEntry {
	if len(entries) == 0 {
		return nil
	}
	prefixEnd := committedOngoingPrefixEnd(entries)
	if prefixEnd >= len(entries) {
		return nil
	}
	return nonEmptyTranscriptEntries(entries[prefixEnd:])
}

func PendingToolEntries(entries []TranscriptEntry) []TranscriptEntry {
	if len(entries) == 0 {
		return nil
	}
	start := committedOngoingPrefixEnd(entries)
	if start >= len(entries) {
		return nil
	}
	tail := entries[start:]
	include := make(map[int]struct{})
	consumedResults := make(map[int]struct{})
	resultIndex := buildToolResultIndex(tail)
	for idx, entry := range tail {
		if strings.TrimSpace(entry.Role) != "tool_call" {
			continue
		}
		if strings.TrimSpace(ongoingTranscriptText(entry)) == "" {
			continue
		}
		include[idx] = struct{}{}
		resultIdx := resultIndex.findMatchingToolResultIndex(tail, idx, consumedResults)
		if resultIdx < 0 {
			continue
		}
		include[resultIdx] = struct{}{}
		consumedResults[resultIdx] = struct{}{}
	}
	pending := make([]TranscriptEntry, 0, len(include))
	for idx, entry := range tail {
		if _, ok := include[idx]; !ok {
			continue
		}
		pending = append(pending, entry)
	}
	return pending
}

func RenderCommittedOngoingSnapshot(entries []TranscriptEntry, theme string, width int) string {
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
	return model.CommittedOngoingProjection().Render(TranscriptDivider)
}

func nonEmptyTranscriptEntries(entries []TranscriptEntry) []TranscriptEntry {
	filtered := make([]TranscriptEntry, 0, len(entries))
	for _, entry := range entries {
		if isToolResultRole(strings.TrimSpace(entry.Role)) &&
			strings.TrimSpace(entry.Text) == "" &&
			strings.TrimSpace(entry.OngoingText) == "" {
			// Successful patch/edit calls intentionally emit an empty tool_result
			// body. Preserve the entry as a structural status marker so merged
			// tool blocks can still resolve to their final success/error role.
			filtered = append(filtered, entry)
			continue
		}
		if strings.TrimSpace(entry.Text) == "" && strings.TrimSpace(entry.OngoingText) == "" {
			continue
		}
		filtered = append(filtered, entry)
	}
	return filtered
}

func committedOngoingPrefixEnd(entries []TranscriptEntry) int {
	consumedResults := make(map[int]struct{})
	resultIndex := buildToolResultIndex(entries)
	for idx, entry := range entries {
		if entry.Transient {
			return committedOngoingPrefixEndBefore(entries, idx, resultIndex)
		}
		if strings.TrimSpace(entry.Role) != "tool_call" {
			continue
		}
		if strings.TrimSpace(ongoingTranscriptText(entry)) == "" {
			continue
		}
		resultIdx := resultIndex.findMatchingToolResultIndex(entries, idx, consumedResults)
		if resultIdx < 0 || entries[resultIdx].Transient {
			return idx
		}
		consumedResults[resultIdx] = struct{}{}
	}
	return len(entries)
}

func committedOngoingPrefixEndBefore(entries []TranscriptEntry, boundary int, resultIndex toolResultIndex) int {
	consumedResults := make(map[int]struct{})
	for idx := boundary - 1; idx >= 0; idx-- {
		entry := entries[idx]
		if strings.TrimSpace(entry.Role) != "tool_call" {
			continue
		}
		if strings.TrimSpace(ongoingTranscriptText(entry)) == "" {
			continue
		}
		resultIdx := resultIndex.findMatchingToolResultIndex(entries, idx, consumedResults)
		if resultIdx < 0 || resultIdx >= boundary || entries[resultIdx].Transient {
			return idx
		}
		consumedResults[resultIdx] = struct{}{}
	}
	return boundary
}

func ongoingTranscriptText(entry TranscriptEntry) string {
	if strings.TrimSpace(entry.OngoingText) != "" {
		return entry.OngoingText
	}
	return entry.Text
}

package tui

import "strings"

type TranscriptProjection struct {
	Blocks []TranscriptProjectionBlock
}

// CommittedOngoingProjectionKey identifies the render-affecting inputs for the
// committed ongoing transcript projection. Revision must advance when transcript
// content changes; revisionless keys intentionally skip projection caching.
type CommittedOngoingProjectionKey struct {
	Revision   int64
	Width      int
	Theme      string
	BaseOffset int
	EntryCount int
}

// CommittedOngoingProjector reuses the ongoing transcript renderer and caches
// committed projections by transcript revision and terminal width.
type CommittedOngoingProjector struct {
	key           CommittedOngoingProjectionKey
	projection    TranscriptProjection
	projectionSet bool
	renderer      Model
	rendererSet   bool
	rendererTheme string
	rendererWidth int
}

type TranscriptProjectionLine struct {
	Kind VisibleLineKind
	Text string
}

type TranscriptProjectionBlock struct {
	Role         RenderIntent
	DividerGroup string
	EntryIndex   int
	EntryEnd     int
	Lines        []string
}

func (p TranscriptProjection) Empty() bool {
	return len(p.Blocks) == 0
}

func (p TranscriptProjection) Clone() TranscriptProjection {
	if len(p.Blocks) == 0 {
		return TranscriptProjection{}
	}
	blocks := make([]TranscriptProjectionBlock, 0, len(p.Blocks))
	for _, block := range p.Blocks {
		blocks = append(blocks, TranscriptProjectionBlock{
			Role:         block.Role,
			DividerGroup: block.DividerGroup,
			EntryIndex:   block.EntryIndex,
			EntryEnd:     block.EntryEnd,
			Lines:        append([]string(nil), block.Lines...),
		})
	}
	return TranscriptProjection{Blocks: blocks}
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
	target := m.transcript
	if len(entries) > 0 {
		target = entries
	}
	return projectCommittedOngoingTranscriptWithRenderer(m, target)
}

// ProjectCommittedOngoingTranscript renders committed ongoing transcript entries
// without requiring callers to construct a throwaway tui.Model.
func ProjectCommittedOngoingTranscript(entries []TranscriptEntry, theme string, width int) TranscriptProjection {
	var projector CommittedOngoingProjector
	return projector.Project(entries, CommittedOngoingProjectionKey{
		Theme:      theme,
		Width:      width,
		EntryCount: len(entries),
	})
}

// Project returns the committed ongoing projection for entries, reusing a cached
// projection when the key still matches.
func (p *CommittedOngoingProjector) Project(entries []TranscriptEntry, key CommittedOngoingProjectionKey) TranscriptProjection {
	key = normalizeCommittedOngoingProjectionKey(key, len(entries))
	cacheable := key.Revision > 0
	if cacheable && p != nil && p.projectionSet && p.key == key {
		return p.projection.Clone()
	}
	renderer := committedOngoingProjectionRenderer(key.Theme, key.Width, key.BaseOffset)
	if p != nil {
		renderer = p.rendererFor(key.Theme, key.Width, key.BaseOffset)
	}
	projection := projectCommittedOngoingTranscriptWithRenderer(renderer, entries)
	if cacheable && p != nil {
		p.key = key
		p.projection = projection.Clone()
		p.projectionSet = true
	}
	return projection
}

func normalizeCommittedOngoingProjectionKey(key CommittedOngoingProjectionKey, entryCount int) CommittedOngoingProjectionKey {
	key.Theme = normalizeTheme(key.Theme)
	if key.Width <= 0 {
		key.Width = 120
	}
	if key.EntryCount != entryCount {
		key.EntryCount = entryCount
	}
	return key
}

func (p *CommittedOngoingProjector) rendererFor(theme string, width int, baseOffset int) Model {
	theme = normalizeTheme(theme)
	if width <= 0 {
		width = 120
	}
	if !p.rendererSet || p.rendererTheme != theme || p.rendererWidth != width {
		p.renderer = committedOngoingProjectionRenderer(theme, width, baseOffset)
		p.rendererSet = true
		p.rendererTheme = theme
		p.rendererWidth = width
	}
	p.renderer.transcriptBaseOffset = baseOffset
	return p.renderer
}

func committedOngoingProjectionRenderer(theme string, width int, baseOffset int) Model {
	return transcriptProjectionRenderer(theme, width, baseOffset)
}

func transcriptProjectionRenderer(theme string, width int, baseOffset int) Model {
	model := NewModel(WithTheme(theme))
	model.viewportWidth = width
	model.transcriptBaseOffset = baseOffset
	return model
}

func projectCommittedOngoingTranscriptWithRenderer(renderer Model, entries []TranscriptEntry) TranscriptProjection {
	committed := CommittedOngoingEntries(entries)
	if len(committed) == 0 {
		return TranscriptProjection{}
	}
	renderer.transcript = append([]TranscriptEntry(nil), committed...)
	renderer.ongoing = ""
	renderer.streamingReasoning = nil
	return projectionFromOngoingBlocks(renderer.buildOngoingBlocks(false))
}

func (m Model) DetailProjection(includeStreaming bool, applySelection bool) TranscriptProjection {
	m.mode = ModeDetail
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
			DividerGroup: ongoingDividerGroup(block.role),
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
		if roleFromEntry(entry) != TranscriptRoleToolCall {
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
	return ProjectCommittedOngoingTranscript(entries, theme, width).Render(TranscriptDivider)
}

func nonEmptyTranscriptEntries(entries []TranscriptEntry) []TranscriptEntry {
	filtered := make([]TranscriptEntry, 0, len(entries))
	for _, entry := range entries {
		if roleFromEntry(entry).IsToolResult() &&
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
		if roleFromEntry(entry) != TranscriptRoleToolCall {
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
		if roleFromEntry(entry) != TranscriptRoleToolCall {
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

package tui

import (
	"builder/internal/llm"
	"builder/internal/transcript"
	"fmt"
	"regexp"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

type Mode string

const (
	ModeOngoing Mode = "ongoing"
	ModeDetail  Mode = "detail"

	DefaultPreviewLines = 8
	TranscriptDivider   = "────────────────────────"
)

var patchCountTokenPattern = regexp.MustCompile(`([+-]\d+)\b`)

type TranscriptEntry struct {
	Role        string
	Text        string
	OngoingText string
	Phase       llm.MessagePhase
	ToolCallID  string
	ToolCall    *transcript.ToolCallMeta
}

type VisibleLineKind uint8

const (
	VisibleLineContent VisibleLineKind = iota
	VisibleLineDivider
)

type StreamingReasoningEntry struct {
	Key  string
	Role string
	Text string
}

type ToggleModeMsg struct {
	SkipDetailWarmup bool
}

type SetModeMsg struct {
	Mode             Mode
	SkipDetailWarmup bool
}

type ScrollOngoingMsg struct {
	Delta int
}

type SetViewportLinesMsg struct {
	Lines int
}

type SetViewportSizeMsg struct {
	Lines int
	Width int
}

type AppendTranscriptMsg struct {
	Role        string
	Text        string
	OngoingText string
	Phase       llm.MessagePhase
	ToolCallID  string
	ToolCall    *transcript.ToolCallMeta
}

type SetConversationMsg struct {
	Entries      []TranscriptEntry
	Ongoing      string
	OngoingError string
}

type SetSelectedTranscriptEntryMsg struct {
	EntryIndex            int
	Active                bool
	RefreshDetailSnapshot bool
}

type FocusTranscriptEntryMsg struct {
	EntryIndex int
	Center     bool
	Bottom     bool
}

type SetOngoingScrollMsg struct {
	Scroll int
}

type StreamAssistantMsg struct {
	Delta string
}

type ClearOngoingAssistantMsg struct{}

type UpsertStreamingReasoningMsg struct {
	Key  string
	Role string
	Text string
}

type ClearStreamingReasoningMsg struct{}

type CommitAssistantMsg struct{}

type SetOngoingErrorMsg struct {
	Err error
}

type ClearOngoingErrorMsg struct{}

type Option func(*Model)

type RenderDiagnosticSeverity string

const (
	RenderDiagnosticSeverityInfo  RenderDiagnosticSeverity = "info"
	RenderDiagnosticSeverityWarn  RenderDiagnosticSeverity = "warn"
	RenderDiagnosticSeverityError RenderDiagnosticSeverity = "error"
	RenderDiagnosticSeverityFatal RenderDiagnosticSeverity = "fatal"
)

type RenderDiagnostic struct {
	Component string
	Message   string
	Err       error
	Severity  RenderDiagnosticSeverity
}

type RenderDiagnosticHandler func(RenderDiagnostic)

func WithPreviewLines(lines int) Option {
	return func(m *Model) {
		if lines > 0 {
			m.viewportLines = lines
		}
	}
}

func WithTheme(theme string) Option {
	return func(m *Model) {
		m.theme = normalizeTheme(theme)
	}
}

func WithRenderDiagnosticHandler(handler RenderDiagnosticHandler) Option {
	return func(m *Model) {
		m.renderDiagnosticHandler = handler
	}
}

type Model struct {
	mode Mode

	viewportLines               int
	viewportWidth               int
	ongoingScroll               int
	detailScroll                int
	snapOngoingOnViewportResize bool

	transcript         []TranscriptEntry
	ongoing            string
	streamingReasoning []StreamingReasoningEntry

	selectedTranscriptEntry  int
	selectedTranscriptActive bool

	detailSnapshot          string
	detailLines             []string
	detailLineKinds         []VisibleLineKind
	detailLineEntryIndices  []int
	detailEntryLineRanges   []lineRange
	detailDirty             bool
	ongoingSnapshot         string
	ongoingLineCache        []string
	ongoingLineKinds        []VisibleLineKind
	ongoingBaseLines        []string
	ongoingBaseLineKinds    []VisibleLineKind
	ongoingBaseLastGroup    string
	ongoingStreamingLines   []string
	ongoingStreamingKinds   []VisibleLineKind
	ongoingStreamingDivider bool
	ongoingBaseDirty        bool
	ongoingDirty            bool
	ongoingError            string
	theme                   string
	md                      *markdownRenderer
	code                    *codeRenderer
	renderDiagnosticHandler RenderDiagnosticHandler
}

type ongoingBlock struct {
	role       string
	lines      []string
	entryIndex int
	entryEnd   int
}

type lineRange struct {
	Start int
	End   int
}

func NewModel(opts ...Option) Model {
	m := Model{
		mode:             ModeOngoing,
		viewportLines:    DefaultPreviewLines,
		viewportWidth:    120,
		theme:            normalizeTheme(""),
		ongoingBaseDirty: true,
		ongoingDirty:     true,
		detailDirty:      true,
	}
	for _, opt := range opts {
		opt(&m)
	}
	m.md = newMarkdownRenderer(m.theme, m.reportRenderDiagnostic)
	m.code = newCodeRenderer(m.theme)
	return m
}

func (m Model) reportRenderDiagnostic(diag RenderDiagnostic) {
	if strings.TrimSpace(diag.Message) == "" && diag.Err != nil {
		diag.Message = diag.Err.Error()
	}
	if strings.TrimSpace(diag.Component) == "" {
		diag.Component = "render"
	}
	if strings.TrimSpace(string(diag.Severity)) == "" {
		diag.Severity = RenderDiagnosticSeverityWarn
	}
	if m.renderDiagnosticHandler != nil {
		m.renderDiagnosticHandler(diag)
	}
}

func (m Model) Init() tea.Cmd {
	return nil
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	m.reduce(msg)
	return m, nil
}

func (m Model) View() string {
	if m.mode == ModeDetail {
		return m.renderDetailSnapshot()
	}
	return m.renderOngoing()
}

func (m *Model) VisibleLineKinds() []VisibleLineKind {
	if m == nil {
		return nil
	}
	if m.mode == ModeDetail {
		if m.detailDirty && len(m.detailLines) == 0 {
			m.rebuildDetailSnapshot()
		}
		kinds := m.detailLineKinds
		if len(kinds) == 0 {
			kinds = make([]VisibleLineKind, len(m.detailSnapshotLines()))
		}
		return sliceVisibleLineKinds(kinds, m.detailScroll, m.maxDetailScroll(), m.viewportLines)
	}
	if m.ongoingDirty {
		m.rebuildOngoingSnapshot()
	}
	return m.visibleOngoingLineKinds()
}

func sliceVisibleLineKinds(kinds []VisibleLineKind, scroll, maxScroll, viewportLines int) []VisibleLineKind {
	if viewportLines <= 0 {
		return nil
	}
	if len(kinds) == 0 {
		return append(make([]VisibleLineKind, 0, viewportLines), VisibleLineContent)
	}
	start := clamp(scroll, 0, maxScroll)
	end := start + viewportLines
	if end > len(kinds) {
		end = len(kinds)
	}
	out := append([]VisibleLineKind(nil), kinds[start:end]...)
	for len(out) < viewportLines {
		out = append(out, VisibleLineContent)
	}
	return out
}

func (m Model) Mode() Mode {
	return m.mode
}

func (m Model) OngoingScroll() int {
	return m.ongoingScroll
}

func (m Model) OngoingSnapshot() string {
	if m.ongoingDirty {
		return m.renderFlatOngoingTranscript()
	}
	if m.ongoingSnapshot != "" {
		return m.ongoingSnapshot
	}
	return strings.Join(m.ongoingLines(), "\n")
}

func (m Model) OngoingCommittedSnapshot() string {
	return m.renderFlatCommittedOngoingTranscript()
}

func (m Model) OngoingStreamingText() string {
	return m.ongoing
}

func (m Model) OngoingErrorText() string {
	return m.ongoingError
}

func FormatOngoingError(err error) string {
	if err == nil {
		return ""
	}
	msg := strings.TrimSpace(err.Error())
	if msg == "" {
		return "error"
	}
	return fmt.Sprintf("error: %s", msg)
}

func (m Model) toggleMode(skipDetailWarmup bool) Model {
	target := ModeDetail
	if m.mode == ModeDetail {
		target = ModeOngoing
	}
	return m.transitionMode(target, skipDetailWarmup)
}

func (m Model) transitionMode(target Mode, skipDetailWarmup bool) Model {
	if target == "" || target == m.mode {
		return m
	}
	switch target {
	case ModeDetail:
		m.mode = ModeDetail
		m.snapOngoingOnViewportResize = false
		if !skipDetailWarmup && (m.detailDirty || len(m.detailLines) == 0) {
			m.rebuildDetailSnapshot()
		}
		m.detailScroll = m.maxDetailScroll()
	case ModeOngoing:
		m.mode = ModeOngoing
		// Ongoing mode is the live tail view, so exiting detail always snaps to
		// the latest visible transcript content.
		m.ongoingScroll = m.maxOngoingScroll()
		// App-level layout shrinks the viewport when returning to ongoing. Re-snap
		// on the next viewport resize so we stay on the true latest tail.
		m.snapOngoingOnViewportResize = true
	}
	return m
}

func (m Model) scrollOngoing(delta int) Model {
	m.ongoingScroll = clamp(m.ongoingScroll+delta, 0, m.maxOngoingScroll())
	return m
}

func (m Model) scrollDetail(delta int) Model {
	m.detailScroll = clamp(m.detailScroll+delta, 0, m.maxDetailScroll())
	return m
}

func (m Model) maxOngoingScroll() int {
	lineCount := m.ongoingRenderedLineCount()
	if lineCount <= m.viewportLines {
		return 0
	}
	return lineCount - m.viewportLines
}

func (m Model) maxDetailScroll() int {
	lines := m.detailSnapshotLines()
	if len(lines) <= m.viewportLines {
		return 0
	}
	return len(lines) - m.viewportLines
}

func (m Model) isOngoingAtBottom() bool {
	return m.ongoingScroll >= m.maxOngoingScroll()
}

func (m Model) renderOngoing() string {
	lineCount := m.ongoingRenderedLineCount()
	start := clamp(m.ongoingScroll, 0, m.maxOngoingScroll())
	end := start + m.viewportLines
	if end > lineCount {
		end = lineCount
	}

	out := make([]string, 0, m.viewportLines+1)
	for i := start; i < end; i++ {
		out = append(out, m.ongoingLineAt(i))
	}
	for len(out) < m.viewportLines {
		out = append(out, "")
	}
	return strings.Join(out, "\n")
}

func (m Model) ongoingLines() []string {
	if m.ongoingDirty {
		return splitLines(m.renderFlatOngoingTranscript())
	}
	if len(m.ongoingLineCache) > 0 {
		return m.ongoingLineCache
	}
	lineCount := m.ongoingRenderedLineCount()
	lines := make([]string, 0, lineCount)
	for idx := 0; idx < lineCount; idx++ {
		lines = append(lines, m.ongoingLineAt(idx))
	}
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}

func (m *Model) invalidateOngoingSnapshot() {
	m.ongoingBaseDirty = true
	m.ongoingDirty = true
	m.ongoingSnapshot = ""
	m.ongoingLineCache = nil
	m.ongoingLineKinds = nil
}

func (m *Model) invalidateOngoingStreamingSnapshot() {
	m.ongoingDirty = true
	m.ongoingSnapshot = ""
	m.ongoingLineCache = nil
	m.ongoingLineKinds = nil
}

func (m *Model) rebuildOngoingSnapshot() {
	if m.ongoingBaseDirty {
		projection := m.OngoingProjection(false)
		lines := projection.Lines(detailDivider())
		m.ongoingBaseLines = m.ongoingBaseLines[:0]
		m.ongoingBaseLineKinds = m.ongoingBaseLineKinds[:0]
		m.ongoingBaseLastGroup = ""
		for _, line := range lines {
			m.ongoingBaseLines = append(m.ongoingBaseLines, line.Text)
			m.ongoingBaseLineKinds = append(m.ongoingBaseLineKinds, line.Kind)
		}
		if blockCount := len(projection.Blocks); blockCount > 0 {
			m.ongoingBaseLastGroup = projection.Blocks[blockCount-1].DividerGroup
		}
		m.ongoingBaseDirty = false
	}
	m.ongoingStreamingLines = m.ongoingStreamingLines[:0]
	m.ongoingStreamingKinds = m.ongoingStreamingKinds[:0]
	m.ongoingStreamingDivider = false
	if strings.TrimSpace(m.ongoing) != "" {
		m.ongoingStreamingLines = append(m.ongoingStreamingLines, m.flattenEntryPlain("assistant", m.ongoing)...)
		if len(m.ongoingStreamingLines) > 0 {
			m.ongoingStreamingKinds = make([]VisibleLineKind, len(m.ongoingStreamingLines))
			if len(m.ongoingBaseLines) > 0 && m.ongoingBaseLastGroup != ongoingDividerGroup("assistant") {
				m.ongoingStreamingDivider = true
			}
		}
	}
	m.ongoingDirty = false
}

func (m Model) ongoingRenderedLineCount() int {
	total := len(m.ongoingBaseLines) + len(m.ongoingStreamingLines)
	if m.ongoingStreamingDivider {
		total++
	}
	if total == 0 {
		return 1
	}
	return total
}

func (m Model) ongoingLineAt(index int) string {
	if index < 0 || index >= m.ongoingRenderedLineCount() {
		return ""
	}
	if len(m.ongoingBaseLines) == 0 && !m.ongoingStreamingDivider && len(m.ongoingStreamingLines) == 0 {
		return ""
	}
	if index < len(m.ongoingBaseLines) {
		return m.ongoingBaseLines[index]
	}
	index -= len(m.ongoingBaseLines)
	if m.ongoingStreamingDivider {
		if index == 0 {
			return detailDivider()
		}
		index--
	}
	if index >= 0 && index < len(m.ongoingStreamingLines) {
		return m.ongoingStreamingLines[index]
	}
	return ""
}

func (m Model) ongoingLineKindAt(index int) VisibleLineKind {
	if index < 0 || index >= m.ongoingRenderedLineCount() {
		return VisibleLineContent
	}
	if len(m.ongoingBaseLines) == 0 && !m.ongoingStreamingDivider && len(m.ongoingStreamingLines) == 0 {
		return VisibleLineContent
	}
	if index < len(m.ongoingBaseLineKinds) {
		return m.ongoingBaseLineKinds[index]
	}
	index -= len(m.ongoingBaseLineKinds)
	if m.ongoingStreamingDivider {
		if index == 0 {
			return VisibleLineDivider
		}
		index--
	}
	if index >= 0 && index < len(m.ongoingStreamingKinds) {
		return m.ongoingStreamingKinds[index]
	}
	return VisibleLineContent
}

func (m Model) visibleOngoingLineKinds() []VisibleLineKind {
	if m.viewportLines <= 0 {
		return nil
	}
	start := clamp(m.ongoingScroll, 0, m.maxOngoingScroll())
	end := start + m.viewportLines
	if total := m.ongoingRenderedLineCount(); end > total {
		end = total
	}
	out := make([]VisibleLineKind, 0, m.viewportLines)
	for idx := start; idx < end; idx++ {
		out = append(out, m.ongoingLineKindAt(idx))
	}
	for len(out) < m.viewportLines {
		out = append(out, VisibleLineContent)
	}
	return out
}

func (m Model) renderDetailSnapshot() string {
	if m.detailDirty && len(m.detailLines) == 0 {
		m.rebuildDetailSnapshot()
	}
	lines := m.detailSnapshotLines()
	if len(lines) == 0 {
		lines = []string{""}
	}
	start := clamp(m.detailScroll, 0, m.maxDetailScroll())
	end := start + m.viewportLines
	if end > len(lines) {
		end = len(lines)
	}

	selectedEntry := -1
	if m.selectedTranscriptActive && m.selectedTranscriptEntry >= 0 && m.selectedTranscriptEntry < len(m.transcript) {
		if strings.TrimSpace(m.transcript[m.selectedTranscriptEntry].Role) == "user" {
			selectedEntry = m.selectedTranscriptEntry
		}
	}
	selectedStyle := m.palette().selection
	out := make([]string, 0, m.viewportLines)
	for i := start; i < end; i++ {
		line := lines[i]
		if selectedEntry >= 0 && i < len(m.detailLineEntryIndices) && m.detailLineEntryIndices[i] == selectedEntry {
			line = selectedStyle.Render(line)
		}
		out = append(out, line)
	}
	for len(out) < m.viewportLines {
		out = append(out, "")
	}
	return strings.Join(out, "\n")
}

func (m Model) detailSnapshotLines() []string {
	if len(m.detailLines) > 0 {
		return m.detailLines
	}
	return splitLines(m.detailSnapshot)
}

func (m *Model) invalidateDetailSnapshot() {
	m.detailDirty = true
}

func (m *Model) rebuildDetailSnapshot() {
	blocks := m.buildDetailBlocks(true, false)
	if len(blocks) == 0 {
		m.detailSnapshot = ""
		m.detailLines = []string{""}
		m.detailLineKinds = []VisibleLineKind{VisibleLineContent}
		m.detailLineEntryIndices = []int{-1}
		m.detailEntryLineRanges = nil
		m.detailDirty = false
		return
	}
	lines := make([]string, 0, len(blocks)*2)
	lineKinds := make([]VisibleLineKind, 0, len(blocks)*2)
	lineOwners := make([]int, 0, len(blocks)*2)
	ranges := make([]lineRange, len(m.transcript))
	for i := range ranges {
		ranges[i] = lineRange{Start: -1, End: -1}
	}
	for idx, block := range blocks {
		if idx > 0 {
			lines = append(lines, detailDivider())
			lineKinds = append(lineKinds, VisibleLineDivider)
			lineOwners = append(lineOwners, -1)
		}
		start := len(lines)
		lines = append(lines, block.lines...)
		for range block.lines {
			lineKinds = append(lineKinds, VisibleLineContent)
		}
		for range block.lines {
			lineOwners = append(lineOwners, block.entryIndex)
		}
		if block.entryIndex < 0 || block.entryIndex >= len(ranges) {
			continue
		}
		if ranges[block.entryIndex].Start < 0 {
			ranges[block.entryIndex] = lineRange{Start: start, End: len(lines) - 1}
			continue
		}
		ranges[block.entryIndex] = lineRange{Start: ranges[block.entryIndex].Start, End: len(lines) - 1}
	}
	m.detailSnapshot = strings.Join(lines, "\n")
	m.detailLines = lines
	m.detailLineKinds = lineKinds
	m.detailLineEntryIndices = lineOwners
	m.detailEntryLineRanges = ranges
	m.detailDirty = false
}

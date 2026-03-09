package tui

import (
	"builder/internal/llm"
	"builder/internal/transcript"
	"fmt"
	"regexp"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
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

type StreamingReasoningEntry struct {
	Key  string
	Role string
	Text string
}

type ToggleModeMsg struct{}

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

	detailSnapshot         string
	detailLines            []string
	detailLineEntryIndices []int
	detailEntryLineRanges  []lineRange
	detailDirty            bool
	ongoingSnapshot        string
	ongoingLineCache       []string
	ongoingDirty           bool
	ongoingError           string
	theme                  string
	md                     *markdownRenderer
	code                   *codeRenderer
}

type ongoingBlock struct {
	role       string
	lines      []string
	entryIndex int
}

type lineRange struct {
	Start int
	End   int
}

func NewModel(opts ...Option) Model {
	m := Model{
		mode:          ModeOngoing,
		viewportLines: DefaultPreviewLines,
		viewportWidth: 120,
		theme:         "dark",
		ongoingDirty:  true,
		detailDirty:   true,
	}
	for _, opt := range opts {
		opt(&m)
	}
	m.md = newMarkdownRenderer(m.theme)
	m.code = newCodeRenderer(m.theme)
	return m
}

func (m Model) Init() tea.Cmd {
	return nil
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	wasAtOngoingBottom := false
	if m.mode == ModeOngoing {
		wasAtOngoingBottom = m.isOngoingAtBottom()
	}
	shouldAutoFollowOngoing := false
	viewportChanged := false
	ongoingChanged := false
	detailChanged := false
	forceDetailRefresh := false

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyTab:
			m = m.toggleMode()
		case tea.KeyUp:
			m = m.scrollActive(-1)
		case tea.KeyDown:
			m = m.scrollActive(1)
		case tea.KeyPgUp:
			m = m.scrollActive(-max(1, m.viewportLines-1))
		case tea.KeyPgDown:
			m = m.scrollActive(max(1, m.viewportLines-1))
		}
	case tea.MouseMsg:
		switch msg.Type {
		case tea.MouseWheelUp:
			m = m.scrollActive(-1)
		case tea.MouseWheelDown:
			m = m.scrollActive(1)
		}
	case ToggleModeMsg:
		if m.mode == ModeDetail && m.ongoingDirty {
			m.rebuildOngoingSnapshot()
		}
		m = m.toggleMode()
	case ScrollOngoingMsg:
		m = m.scrollActive(msg.Delta)
	case SetViewportLinesMsg:
		if msg.Lines > 0 {
			m.viewportLines = msg.Lines
			viewportChanged = true
		}
	case SetViewportSizeMsg:
		if msg.Lines > 0 {
			m.viewportLines = msg.Lines
			viewportChanged = true
		}
		if msg.Width > 0 {
			if m.viewportWidth != msg.Width {
				m.viewportWidth = msg.Width
				ongoingChanged = true
				detailChanged = true
				if m.mode == ModeDetail {
					forceDetailRefresh = true
				}
			}
		}
	case AppendTranscriptMsg:
		role := strings.TrimSpace(msg.Role)
		if role == "" {
			role = "unknown"
		}
		m.transcript = append(m.transcript, TranscriptEntry{
			Role:        role,
			Text:        msg.Text,
			OngoingText: msg.OngoingText,
			Phase:       msg.Phase,
			ToolCallID:  strings.TrimSpace(msg.ToolCallID),
			ToolCall:    cloneToolCallMeta(msg.ToolCall),
		})
		shouldAutoFollowOngoing = true
		ongoingChanged = true
		detailChanged = true
	case SetConversationMsg:
		entries := make([]TranscriptEntry, len(msg.Entries))
		copy(entries, msg.Entries)
		for i := range entries {
			entries[i].ToolCallID = strings.TrimSpace(entries[i].ToolCallID)
			entries[i].ToolCall = cloneToolCallMeta(entries[i].ToolCall)
		}
		m.transcript = entries
		m.ongoing = msg.Ongoing
		m.ongoingError = strings.TrimSpace(msg.OngoingError)
		if m.selectedTranscriptEntry < 0 || m.selectedTranscriptEntry >= len(m.transcript) {
			m.selectedTranscriptActive = false
		}
		shouldAutoFollowOngoing = true
		ongoingChanged = true
		detailChanged = true
	case SetSelectedTranscriptEntryMsg:
		m.selectedTranscriptEntry = msg.EntryIndex
		m.selectedTranscriptActive = msg.Active
		ongoingChanged = true
		if m.mode == ModeDetail && msg.RefreshDetailSnapshot {
			detailChanged = true
			forceDetailRefresh = true
		}
	case FocusTranscriptEntryMsg:
		switch m.mode {
		case ModeOngoing:
			if start, end, ok := m.ongoingLineRangeForEntry(msg.EntryIndex); ok {
				target := start
				if msg.Bottom {
					target = end - m.viewportLines + 1
				} else if msg.Center {
					midpoint := (start + end) / 2
					target = midpoint - m.viewportLines/2
				}
				m.ongoingScroll = clamp(target, 0, m.maxOngoingScroll())
			}
		case ModeDetail:
			if m.detailDirty {
				m.rebuildDetailSnapshot()
			}
			if start, end, ok := m.detailLineRangeForEntry(msg.EntryIndex); ok {
				target := start
				if msg.Bottom {
					target = end - m.viewportLines + 1
				} else if msg.Center {
					midpoint := (start + end) / 2
					target = midpoint - m.viewportLines/2
				}
				m.detailScroll = clamp(target, 0, m.maxDetailScroll())
			}
		}
	case SetOngoingScrollMsg:
		m.ongoingScroll = clamp(msg.Scroll, 0, m.maxOngoingScroll())
	case StreamAssistantMsg:
		m.ongoing += msg.Delta
		shouldAutoFollowOngoing = true
		ongoingChanged = true
		detailChanged = true
	case ClearOngoingAssistantMsg:
		m.ongoing = ""
		m.ongoingScroll = 0
		ongoingChanged = true
		detailChanged = true
	case UpsertStreamingReasoningMsg:
		key := strings.TrimSpace(msg.Key)
		role := strings.TrimSpace(msg.Role)
		text := strings.TrimSpace(msg.Text)
		if role == "" {
			role = "reasoning"
		}
		if key != "" {
			updated := false
			for i := range m.streamingReasoning {
				if m.streamingReasoning[i].Key != key {
					continue
				}
				updated = true
				if text == "" {
					m.streamingReasoning = append(m.streamingReasoning[:i], m.streamingReasoning[i+1:]...)
				} else {
					m.streamingReasoning[i].Role = role
					m.streamingReasoning[i].Text = text
				}
				break
			}
			if !updated && text != "" {
				m.streamingReasoning = append(m.streamingReasoning, StreamingReasoningEntry{Key: key, Role: role, Text: text})
			}
			detailChanged = true
			if m.mode == ModeDetail {
				forceDetailRefresh = true
			}
		}
	case ClearStreamingReasoningMsg:
		if len(m.streamingReasoning) > 0 {
			m.streamingReasoning = nil
			detailChanged = true
			if m.mode == ModeDetail {
				forceDetailRefresh = true
			}
		}
	case CommitAssistantMsg:
		if m.ongoing != "" {
			m.transcript = append(m.transcript, TranscriptEntry{
				Role: "assistant",
				Text: m.ongoing,
			})
			m.ongoing = ""
			shouldAutoFollowOngoing = true
			ongoingChanged = true
			detailChanged = true
		}
	case SetOngoingErrorMsg:
		m.ongoingError = FormatOngoingError(msg.Err)
	case ClearOngoingErrorMsg:
		m.ongoingError = ""
	}

	if ongoingChanged {
		m.invalidateOngoingSnapshot()
	}
	if detailChanged {
		m.invalidateDetailSnapshot()
	}
	if forceDetailRefresh {
		m.rebuildDetailSnapshot()
	}
	if m.ongoingDirty && m.mode == ModeOngoing {
		m.rebuildOngoingSnapshot()
	}

	clampOngoing := m.mode == ModeOngoing
	if clampOngoing {
		maxOngoing := m.maxOngoingScroll()
		m.ongoingScroll = clamp(m.ongoingScroll, 0, maxOngoing)
		if m.mode == ModeOngoing && viewportChanged && m.snapOngoingOnViewportResize {
			m.ongoingScroll = maxOngoing
			m.snapOngoingOnViewportResize = false
		}
		if m.mode == ModeOngoing && shouldAutoFollowOngoing && wasAtOngoingBottom {
			m.ongoingScroll = maxOngoing
		}
	}

	if m.mode == ModeDetail || viewportChanged {
		m.detailScroll = clamp(m.detailScroll, 0, m.maxDetailScroll())
	}
	return m, nil
}

func (m Model) View() string {
	if m.mode == ModeDetail {
		return m.renderDetailSnapshot()
	}
	return m.renderOngoing()
}

func (m Model) Mode() Mode {
	return m.mode
}

func (m Model) OngoingScroll() int {
	return m.ongoingScroll
}

func (m Model) OngoingSnapshot() string {
	if m.ongoingSnapshot != "" {
		return m.ongoingSnapshot
	}
	return m.renderFlatOngoingTranscript()
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

func (m Model) toggleMode() Model {
	if m.mode == ModeOngoing {
		m.mode = ModeDetail
		m.snapOngoingOnViewportResize = false
		if m.detailDirty || len(m.detailLines) == 0 {
			m.rebuildDetailSnapshot()
		}
		m.detailScroll = m.maxDetailScroll()
		return m
	}
	m.mode = ModeOngoing
	// Ongoing mode is the live tail view, so exiting detail always snaps to
	// the latest visible transcript content.
	m.ongoingScroll = m.maxOngoingScroll()
	// App-level layout shrinks the viewport when returning to ongoing. Re-snap
	// on the next viewport resize so we stay on the true latest tail.
	m.snapOngoingOnViewportResize = true
	return m
}

func (m Model) scrollActive(delta int) Model {
	if m.mode == ModeDetail {
		m.detailScroll = clamp(m.detailScroll+delta, 0, m.maxDetailScroll())
		return m
	}
	m.ongoingScroll = clamp(m.ongoingScroll+delta, 0, m.maxOngoingScroll())
	return m
}

func (m Model) maxOngoingScroll() int {
	lines := m.ongoingLines()
	if len(lines) <= m.viewportLines {
		return 0
	}
	return len(lines) - m.viewportLines
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
	lines := m.ongoingLines()
	if len(lines) == 0 {
		lines = []string{""}
	}

	start := clamp(m.ongoingScroll, 0, m.maxOngoingScroll())
	end := start + m.viewportLines
	if end > len(lines) {
		end = len(lines)
	}

	out := make([]string, 0, m.viewportLines+1)
	for i := start; i < end; i++ {
		out = append(out, lines[i])
	}
	for len(out) < m.viewportLines {
		out = append(out, "")
	}
	return strings.Join(out, "\n")
}

func (m Model) ongoingLines() []string {
	if len(m.ongoingLineCache) > 0 {
		return m.ongoingLineCache
	}
	return splitLines(m.renderFlatOngoingTranscript())
}

func (m *Model) invalidateOngoingSnapshot() {
	m.ongoingDirty = true
}

func (m *Model) rebuildOngoingSnapshot() {
	snapshot := m.renderFlatOngoingTranscript()
	m.ongoingSnapshot = snapshot
	m.ongoingLineCache = splitLines(snapshot)
	m.ongoingDirty = false
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
	selectedStyle := lipgloss.NewStyle().Background(lipgloss.Color("15")).Foreground(lipgloss.Color("0"))
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
		m.detailLineEntryIndices = []int{-1}
		m.detailEntryLineRanges = nil
		m.detailDirty = false
		return
	}
	lines := make([]string, 0, len(blocks)*2)
	lineOwners := make([]int, 0, len(blocks)*2)
	ranges := make([]lineRange, len(m.transcript))
	for i := range ranges {
		ranges[i] = lineRange{Start: -1, End: -1}
	}
	for idx, block := range blocks {
		if idx > 0 {
			lines = append(lines, detailDivider())
			lineOwners = append(lineOwners, -1)
		}
		start := len(lines)
		lines = append(lines, block.lines...)
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
	m.detailLineEntryIndices = lineOwners
	m.detailEntryLineRanges = ranges
	m.detailDirty = false
}

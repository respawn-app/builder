package tui

import (
	"builder/shared/transcript"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

type modelUpdateResult struct {
	viewportChanged    bool
	ongoingBaseChanged bool
	ongoingChanged     bool
	detailChanged      bool
	detailStale        bool
	forceDetailRefresh bool
	autoFollowOngoing  bool
}

func (m *Model) reduce(msg tea.Msg) {
	wasAtOngoingBottom := false
	if m.mode == ModeOngoing {
		wasAtOngoingBottom = m.isOngoingAtBottom()
	}

	result := modelUpdateResult{}
	switch msg := msg.(type) {
	case tea.KeyMsg:
		m.reduceKeyMsg(msg)
	case tea.MouseMsg:
		m.reduceMouseMsg(msg)
	case ToggleModeMsg:
		m.reduceToggleModeMsg(msg)
	case SetModeMsg:
		m.reduceSetModeMsg(msg)
	case ScrollOngoingMsg:
		m.reduceScrollOngoingMsg(msg)
	case SetViewportLinesMsg:
		result.viewportChanged = m.reduceViewportLinesMsg(msg)
	case SetViewportSizeMsg:
		m.reduceViewportSizeMsg(msg, &result)
	case AppendTranscriptMsg:
		m.reduceAppendTranscriptMsg(msg, &result)
	case SetConversationMsg:
		m.reduceSetConversationMsg(msg, &result)
	case SetSelectedTranscriptEntryMsg:
		m.reduceSetSelectedTranscriptEntryMsg(msg, &result)
	case FocusTranscriptEntryMsg:
		m.reduceFocusTranscriptEntryMsg(msg)
	case SetOngoingScrollMsg:
		m.reduceSetOngoingScrollMsg(msg)
	case StreamAssistantMsg:
		m.reduceStreamAssistantMsg(msg, &result)
	case ClearOngoingAssistantMsg:
		m.reduceClearOngoingAssistantMsg(&result)
	case UpsertStreamingReasoningMsg:
		m.reduceUpsertStreamingReasoningMsg(msg, &result)
	case ClearStreamingReasoningMsg:
		m.reduceClearStreamingReasoningMsg(&result)
	case CommitAssistantMsg:
		m.reduceCommitAssistantMsg(&result)
	case SetOngoingErrorMsg:
		m.ongoingError = FormatOngoingError(msg.Err)
	case ClearOngoingErrorMsg:
		m.ongoingError = ""
	}

	m.applyUpdateResult(result, wasAtOngoingBottom)
}

func (m *Model) reduceKeyMsg(msg tea.KeyMsg) {
	switch m.mode {
	case ModeDetail:
		m.reduceDetailKeyMsg(msg)
	default:
		m.reduceOngoingKeyMsg(msg)
	}
}

func (m *Model) reduceMouseMsg(msg tea.MouseMsg) {
	switch m.mode {
	case ModeDetail:
		m.reduceDetailMouseMsg(msg)
	default:
		m.reduceOngoingMouseMsg(msg)
	}
}

func (m *Model) reduceToggleModeMsg(msg ToggleModeMsg) {
	target := ModeDetail
	if m.mode == ModeDetail {
		target = ModeOngoing
	}
	m.reduceSetModeMsg(SetModeMsg{Mode: target, SkipDetailWarmup: msg.SkipDetailWarmup})
}

func (m *Model) reduceSetModeMsg(msg SetModeMsg) {
	if msg.Mode == "" || msg.Mode == m.mode {
		return
	}
	if m.mode == ModeDetail && msg.Mode == ModeOngoing && m.ongoingDirty {
		m.rebuildOngoingSnapshot()
	}
	*m = m.transitionMode(msg.Mode, msg.SkipDetailWarmup)
}

func (m *Model) reduceScrollOngoingMsg(msg ScrollOngoingMsg) {
	*m = m.scrollOngoing(msg.Delta)
}

func (m *Model) reduceOngoingKeyMsg(msg tea.KeyMsg) {
	switch msg.Type {
	case tea.KeyTab:
		*m = m.transitionMode(ModeDetail, false)
	case tea.KeyUp:
		*m = m.scrollOngoing(-1)
	case tea.KeyDown:
		*m = m.scrollOngoing(1)
	case tea.KeyPgUp:
		*m = m.scrollOngoing(-max(1, m.viewportLines-1))
	case tea.KeyPgDown:
		*m = m.scrollOngoing(max(1, m.viewportLines-1))
	}
}

func (m *Model) reduceDetailKeyMsg(msg tea.KeyMsg) {
	switch msg.Type {
	case tea.KeyTab:
		*m = m.transitionMode(ModeOngoing, false)
	case tea.KeyUp:
		*m = m.scrollDetail(-1)
	case tea.KeyDown:
		*m = m.scrollDetail(1)
	case tea.KeyPgUp:
		*m = m.scrollDetail(-max(1, m.viewportLines-1))
	case tea.KeyPgDown:
		*m = m.scrollDetail(max(1, m.viewportLines-1))
	}
}

func (m *Model) reduceOngoingMouseMsg(msg tea.MouseMsg) {
	switch msg.Type {
	case tea.MouseWheelUp:
		*m = m.scrollOngoing(-1)
	case tea.MouseWheelDown:
		*m = m.scrollOngoing(1)
	}
}

func (m *Model) reduceDetailMouseMsg(msg tea.MouseMsg) {
	switch msg.Type {
	case tea.MouseWheelUp:
		*m = m.scrollDetail(-1)
	case tea.MouseWheelDown:
		*m = m.scrollDetail(1)
	}
}

func (m *Model) reduceViewportLinesMsg(msg SetViewportLinesMsg) bool {
	if msg.Lines <= 0 {
		return false
	}
	if m.viewportLines == msg.Lines {
		return false
	}
	m.viewportLines = msg.Lines
	return true
}

func (m *Model) reduceViewportSizeMsg(msg SetViewportSizeMsg, result *modelUpdateResult) {
	if result == nil {
		return
	}
	if msg.Lines > 0 && m.viewportLines != msg.Lines {
		m.viewportLines = msg.Lines
		result.viewportChanged = true
	}
	if msg.Width <= 0 || m.viewportWidth == msg.Width {
		return
	}
	m.viewportWidth = msg.Width
	result.ongoingBaseChanged = true
	result.ongoingChanged = true
	result.detailChanged = true
	if m.mode == ModeDetail {
		result.forceDetailRefresh = true
	}
}

func (m *Model) reduceAppendTranscriptMsg(msg AppendTranscriptMsg, result *modelUpdateResult) {
	role := strings.TrimSpace(msg.Role)
	if role == "" {
		role = "unknown"
	}
	m.transcript = append(m.transcript, TranscriptEntry{
		Visibility:  transcript.NormalizeEntryVisibility(msg.Visibility),
		Role:        role,
		Text:        msg.Text,
		OngoingText: msg.OngoingText,
		Phase:       msg.Phase,
		ToolCallID:  strings.TrimSpace(msg.ToolCallID),
		ToolCall:    cloneToolCallMeta(msg.ToolCall),
	})
	m.transcriptTotalEntries = max(m.transcriptTotalEntries, m.transcriptBaseOffset+len(m.transcript))
	result.autoFollowOngoing = true
	result.ongoingBaseChanged = true
	result.ongoingChanged = true
	if m.mode == ModeDetail {
		result.detailStale = true
	} else {
		result.detailChanged = true
	}
}

func (m *Model) reduceSetConversationMsg(msg SetConversationMsg, result *modelUpdateResult) {
	anchorEntry, anchorOffset, preserveAnchor := m.detailViewportAnchor()
	entries := make([]TranscriptEntry, len(msg.Entries))
	copy(entries, msg.Entries)
	for i := range entries {
		entries[i].Visibility = transcript.NormalizeEntryVisibility(entries[i].Visibility)
		entries[i].ToolCallID = strings.TrimSpace(entries[i].ToolCallID)
		entries[i].ToolCall = cloneToolCallMeta(entries[i].ToolCall)
	}
	m.transcript = entries
	if msg.BaseOffset < 0 {
		msg.BaseOffset = 0
	}
	m.transcriptBaseOffset = msg.BaseOffset
	totalEntries := msg.TotalEntries
	if totalEntries < m.transcriptBaseOffset+len(entries) {
		totalEntries = m.transcriptBaseOffset + len(entries)
	}
	m.transcriptTotalEntries = totalEntries
	m.ongoing = msg.Ongoing
	m.ongoingError = strings.TrimSpace(msg.OngoingError)
	if _, ok := m.localTranscriptIndex(m.selectedTranscriptEntry); !ok {
		m.selectedTranscriptActive = false
	}
	result.autoFollowOngoing = true
	result.ongoingBaseChanged = true
	result.ongoingChanged = true
	if m.mode != ModeDetail {
		result.detailChanged = true
		return
	}
	m.invalidateDetailSnapshot()
	m.rebuildDetailSnapshot()
	if preserveAnchor {
		if start, _, ok := m.detailLineRangeForEntry(anchorEntry); ok {
			m.detailScroll = clamp(start+anchorOffset, 0, m.maxDetailScroll())
			m.detailBottomAnchor = false
			m.detailBottomOffset = 0
			m.refreshDetailViewport()
		}
	}
}

func (m *Model) detailViewportAnchor() (int, int, bool) {
	if m == nil || m.mode != ModeDetail || m.detailBottomAnchor {
		return 0, 0, false
	}
	if m.detailDirty {
		m.rebuildDetailSnapshot()
	}
	for _, entryIndex := range m.detailLineEntryIndices {
		if entryIndex < 0 {
			continue
		}
		start, _, ok := m.detailLineRangeForEntry(entryIndex)
		if !ok {
			return 0, 0, false
		}
		return entryIndex, max(0, m.detailScroll-start), true
	}
	return 0, 0, false
}

func (m *Model) reduceSetSelectedTranscriptEntryMsg(msg SetSelectedTranscriptEntryMsg, result *modelUpdateResult) {
	m.selectedTranscriptEntry = msg.EntryIndex
	m.selectedTranscriptActive = msg.Active
	result.ongoingChanged = true
	if m.mode == ModeDetail && msg.RefreshDetailSnapshot {
		result.detailChanged = true
		result.forceDetailRefresh = true
	}
}

func (m *Model) reduceFocusTranscriptEntryMsg(msg FocusTranscriptEntryMsg) {
	switch m.mode {
	case ModeOngoing:
		if start, end, ok := m.ongoingLineRangeForEntry(msg.EntryIndex); ok {
			m.ongoingScroll = clamp(focusedScrollTarget(start, end, m.viewportLines, msg), 0, m.maxOngoingScroll())
		}
	case ModeDetail:
		if m.detailDirty {
			m.rebuildDetailSnapshot()
		}
		if start, end, ok := m.detailLineRangeForEntry(msg.EntryIndex); ok {
			if m.detailBottomAnchor || !m.detailMetricsResolved {
				m.ensureDetailMetricsResolved()
			}
			m.detailScroll = clamp(focusedScrollTarget(start, end, m.viewportLines, msg), 0, m.maxDetailScroll())
			m.detailBottomAnchor = false
			m.detailBottomOffset = 0
			m.refreshDetailViewport()
		}
	}
}

func (m *Model) reduceSetOngoingScrollMsg(msg SetOngoingScrollMsg) {
	m.ongoingScroll = clamp(msg.Scroll, 0, m.maxOngoingScroll())
}

func (m *Model) reduceStreamAssistantMsg(msg StreamAssistantMsg, result *modelUpdateResult) {
	m.ongoing += msg.Delta
	result.autoFollowOngoing = true
	result.ongoingChanged = true
	if m.mode == ModeDetail {
		result.detailStale = true
	} else if !m.detailDirty {
		result.detailChanged = true
	}
}

func (m *Model) reduceClearOngoingAssistantMsg(result *modelUpdateResult) {
	m.ongoing = ""
	m.ongoingScroll = 0
	result.ongoingChanged = true
	if m.mode == ModeDetail {
		result.detailStale = true
	} else {
		result.detailChanged = true
	}
}

func (m *Model) reduceUpsertStreamingReasoningMsg(msg UpsertStreamingReasoningMsg, result *modelUpdateResult) {
	key := strings.TrimSpace(msg.Key)
	if key == "" {
		return
	}
	role := strings.TrimSpace(msg.Role)
	if role == "" {
		role = "reasoning"
	}
	text := strings.TrimSpace(msg.Text)
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
	result.detailChanged = true
	if m.mode == ModeDetail {
		result.forceDetailRefresh = true
	}
}

func (m *Model) reduceClearStreamingReasoningMsg(result *modelUpdateResult) {
	if len(m.streamingReasoning) == 0 {
		return
	}
	m.streamingReasoning = nil
	result.detailChanged = true
	if m.mode == ModeDetail {
		result.forceDetailRefresh = true
	}
}

func (m *Model) reduceCommitAssistantMsg(result *modelUpdateResult) {
	if m.ongoing == "" {
		return
	}
	m.transcript = append(m.transcript, TranscriptEntry{Role: "assistant", Text: m.ongoing})
	m.ongoing = ""
	result.autoFollowOngoing = true
	result.ongoingBaseChanged = true
	result.ongoingChanged = true
	if m.mode == ModeDetail {
		result.detailStale = true
	} else {
		result.detailChanged = true
	}
}

func (m *Model) applyUpdateResult(result modelUpdateResult, wasAtOngoingBottom bool) {
	if result.ongoingBaseChanged {
		m.invalidateOngoingBaseSnapshot()
	} else if result.ongoingChanged {
		m.invalidateOngoingSnapshot()
	}
	if result.detailChanged {
		m.invalidateDetailSnapshot()
	}
	if result.detailStale {
		m.detailStale = true
	}
	if result.forceDetailRefresh || (m.mode == ModeDetail && result.detailChanged) {
		m.rebuildDetailSnapshot()
		m.detailStale = false
	}
	if m.ongoingDirty && m.mode == ModeOngoing {
		m.rebuildOngoingSnapshot()
	}

	if m.mode == ModeOngoing {
		maxOngoing := m.maxOngoingScroll()
		m.ongoingScroll = clamp(m.ongoingScroll, 0, maxOngoing)
		if result.viewportChanged && m.snapOngoingOnViewportResize {
			m.ongoingScroll = maxOngoing
			m.snapOngoingOnViewportResize = false
		}
		if result.autoFollowOngoing && wasAtOngoingBottom {
			m.ongoingScroll = maxOngoing
		}
	}

	if m.mode == ModeDetail {
		if !m.detailDirty && !m.detailBottomAnchor {
			m.detailScroll = clamp(m.detailScroll, 0, m.maxDetailScroll())
		}
		if !m.detailDirty {
			m.refreshDetailViewport()
		}
	}
}

func focusedScrollTarget(start, end, viewportLines int, msg FocusTranscriptEntryMsg) int {
	target := start
	if msg.Bottom {
		return end - viewportLines + 1
	}
	if msg.Center {
		midpoint := (start + end) / 2
		return midpoint - viewportLines/2
	}
	return target
}

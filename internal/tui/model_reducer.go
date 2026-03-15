package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

type modelUpdateResult struct {
	viewportChanged    bool
	ongoingChanged     bool
	detailChanged      bool
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
		m.reduceToggleModeMsg()
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
	switch msg.Type {
	case tea.KeyTab:
		*m = m.toggleMode()
	case tea.KeyUp:
		*m = m.scrollActive(-1)
	case tea.KeyDown:
		*m = m.scrollActive(1)
	case tea.KeyPgUp:
		*m = m.scrollActive(-max(1, m.viewportLines-1))
	case tea.KeyPgDown:
		*m = m.scrollActive(max(1, m.viewportLines-1))
	}
}

func (m *Model) reduceMouseMsg(msg tea.MouseMsg) {
	switch msg.Type {
	case tea.MouseWheelUp:
		*m = m.scrollActive(-1)
	case tea.MouseWheelDown:
		*m = m.scrollActive(1)
	}
}

func (m *Model) reduceToggleModeMsg() {
	if m.mode == ModeDetail && m.ongoingDirty {
		m.rebuildOngoingSnapshot()
	}
	*m = m.toggleMode()
}

func (m *Model) reduceScrollOngoingMsg(msg ScrollOngoingMsg) {
	*m = m.scrollActive(msg.Delta)
}

func (m *Model) reduceViewportLinesMsg(msg SetViewportLinesMsg) bool {
	if msg.Lines <= 0 {
		return false
	}
	m.viewportLines = msg.Lines
	return true
}

func (m *Model) reduceViewportSizeMsg(msg SetViewportSizeMsg, result *modelUpdateResult) {
	if result == nil {
		return
	}
	if msg.Lines > 0 {
		m.viewportLines = msg.Lines
		result.viewportChanged = true
	}
	if msg.Width <= 0 || m.viewportWidth == msg.Width {
		return
	}
	m.viewportWidth = msg.Width
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
		Role:        role,
		Text:        msg.Text,
		OngoingText: msg.OngoingText,
		Phase:       msg.Phase,
		ToolCallID:  strings.TrimSpace(msg.ToolCallID),
		ToolCall:    cloneToolCallMeta(msg.ToolCall),
	})
	result.autoFollowOngoing = true
	result.ongoingChanged = true
	result.detailChanged = true
}

func (m *Model) reduceSetConversationMsg(msg SetConversationMsg, result *modelUpdateResult) {
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
	result.autoFollowOngoing = true
	result.ongoingChanged = true
	result.detailChanged = true
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
			m.detailScroll = clamp(focusedScrollTarget(start, end, m.viewportLines, msg), 0, m.maxDetailScroll())
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
	result.detailChanged = true
}

func (m *Model) reduceClearOngoingAssistantMsg(result *modelUpdateResult) {
	m.ongoing = ""
	m.ongoingScroll = 0
	result.ongoingChanged = true
	result.detailChanged = true
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
	result.ongoingChanged = true
	result.detailChanged = true
}

func (m *Model) applyUpdateResult(result modelUpdateResult, wasAtOngoingBottom bool) {
	if result.ongoingChanged {
		m.invalidateOngoingSnapshot()
	}
	if result.detailChanged {
		m.invalidateDetailSnapshot()
	}
	if result.forceDetailRefresh {
		m.rebuildDetailSnapshot()
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

	if m.mode == ModeDetail || result.viewportChanged {
		m.detailScroll = clamp(m.detailScroll, 0, m.maxDetailScroll())
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

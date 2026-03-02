package app

import (
	"strings"

	"builder/internal/runtime"
	"builder/internal/tui"

	tea "github.com/charmbracelet/bubbletea"
)

type uiRuntimeAdapter struct {
	model *uiModel
}

func (a uiRuntimeAdapter) handleRuntimeEvent(evt runtime.Event) tea.Cmd {
	m := a.model
	switch evt.Kind {
	case runtime.EventConversationUpdated:
		return a.syncConversationFromEngine(evt.StepID)
	case runtime.EventAssistantDelta:
		delta := evt.AssistantDelta
		m.sawAssistantDelta = delta != ""
		if delta != "" {
			if m.suppressLateDeltaStepID != "" && evt.StepID == m.suppressLateDeltaStepID && strings.TrimSpace(m.view.OngoingStreamingText()) == "" {
				break
			}
			currentOngoing := m.view.OngoingStreamingText()
			expectedSnapshotLen := m.pendingSnapshotPreviousOngoingLen + len(delta)
			if m.pendingSnapshotDeltaDedup &&
				len(currentOngoing) == m.pendingSnapshotOngoingLen &&
				m.pendingSnapshotOngoingLen == expectedSnapshotLen &&
				strings.HasSuffix(currentOngoing, delta) {
				m.pendingSnapshotDeltaDedup = false
				m.pendingSnapshotOngoingLen = 0
				m.pendingSnapshotPreviousOngoingLen = 0
				break
			}
			m.pendingSnapshotDeltaDedup = false
			m.pendingSnapshotOngoingLen = 0
			m.pendingSnapshotPreviousOngoingLen = 0
			m.forwardToView(tui.StreamAssistantMsg{Delta: delta})
		}
	case runtime.EventAssistantDeltaReset:
		m.sawAssistantDelta = false
		m.pendingSnapshotDeltaDedup = false
		m.pendingSnapshotOngoingLen = 0
		m.pendingSnapshotPreviousOngoingLen = 0
		m.suppressLateDeltaStepID = ""
	case runtime.EventCompactionStarted:
		m.compacting = true
	case runtime.EventCompactionCompleted, runtime.EventCompactionFailed:
		m.compacting = false
	case runtime.EventReviewerStarted:
		m.reviewerRunning = true
		m.reviewerBlocking = true
	case runtime.EventReviewerCompleted:
		m.clearReviewerState()
	case runtime.EventUserMessageFlushed:
		a.onUserMessageFlushed(evt.UserMessage)
	}
	return nil
}

func (a uiRuntimeAdapter) onUserMessageFlushed(text string) {
	m := a.model
	for i, pending := range m.pendingInjected {
		if strings.TrimSpace(pending) != strings.TrimSpace(text) {
			continue
		}
		m.pendingInjected = append(m.pendingInjected[:i], m.pendingInjected[i+1:]...)
		break
	}
	if m.inputSubmitLocked && strings.TrimSpace(m.lockedInjectText) == strings.TrimSpace(text) {
		m.clearInput()
		m.lockedInjectText = ""
		m.inputSubmitLocked = false
	}
}

func (a uiRuntimeAdapter) syncConversationFromEngine(stepID string) tea.Cmd {
	m := a.model
	if m.engine == nil {
		return nil
	}
	return a.applyChatSnapshot(stepID, m.engine.ChatSnapshot())
}

func (a uiRuntimeAdapter) applyChatSnapshot(stepID string, snapshot runtime.ChatSnapshot) tea.Cmd {
	m := a.model
	previousOngoingLen := len(m.view.OngoingStreamingText())
	previousEntryCount := len(m.transcriptEntries)
	entries := make([]tui.TranscriptEntry, 0, len(snapshot.Entries))
	for _, entry := range snapshot.Entries {
		entries = append(entries, tui.TranscriptEntry{
			Role:       entry.Role,
			Text:       entry.Text,
			Phase:      entry.Phase,
			ToolCallID: entry.ToolCallID,
			ToolCall:   entry.ToolCall,
		})
	}
	m.transcriptEntries = append(m.transcriptEntries[:0], entries...)
	m.refreshRollbackCandidates()
	m.forwardToView(tui.SetConversationMsg{
		Entries:      entries,
		Ongoing:      snapshot.Ongoing,
		OngoingError: snapshot.OngoingError,
	})
	assistantCommitted := strings.TrimSpace(snapshot.Ongoing) == "" && len(entries) > previousEntryCount
	if assistantCommitted && strings.TrimSpace(stepID) != "" {
		m.suppressLateDeltaStepID = stepID
	} else if strings.TrimSpace(snapshot.Ongoing) != "" || strings.TrimSpace(stepID) == "" || m.suppressLateDeltaStepID == stepID {
		m.suppressLateDeltaStepID = ""
	}
	if strings.TrimSpace(snapshot.Ongoing) == "" {
		m.pendingSnapshotDeltaDedup = false
		m.pendingSnapshotOngoingLen = 0
		m.pendingSnapshotPreviousOngoingLen = 0
	} else {
		m.pendingSnapshotDeltaDedup = true
		m.pendingSnapshotOngoingLen = len(snapshot.Ongoing)
		m.pendingSnapshotPreviousOngoingLen = previousOngoingLen
	}
	return m.syncNativeHistoryFromTranscript()
}

func waitRuntimeEvent(ch <-chan runtime.Event) tea.Cmd {
	return func() tea.Msg {
		evt, ok := <-ch
		if !ok {
			return nil
		}
		return runtimeEventMsg{event: evt}
	}
}

func waitAskEvent(ch <-chan askEvent) tea.Cmd {
	return func() tea.Msg {
		evt, ok := <-ch
		if !ok {
			return nil
		}
		return askEventMsg{event: evt}
	}
}

func (m *uiModel) handleRuntimeEvent(evt runtime.Event) {
	_ = m.runtimeAdapter().handleRuntimeEvent(evt)
}

func (m *uiModel) onUserMessageFlushed(text string) {
	m.runtimeAdapter().onUserMessageFlushed(text)
}

func (m *uiModel) syncConversationFromEngine() {
	_ = m.runtimeAdapter().syncConversationFromEngine("")
}

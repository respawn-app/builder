package app

import (
	"fmt"
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
		return a.syncConversationFromEngine()
	case runtime.EventAssistantDelta:
		delta := evt.AssistantDelta
		m.sawAssistantDelta = delta != ""
		if delta != "" {
			m.forwardToView(tui.StreamAssistantMsg{Delta: delta})
		}
	case runtime.EventAssistantDeltaReset:
		m.sawAssistantDelta = false
		m.forwardToView(tui.ClearOngoingAssistantMsg{})
	case runtime.EventReasoningDelta:
		if evt.ReasoningDelta != nil {
			if header := extractReasoningStatusHeader(evt.ReasoningDelta.Text); header != "" {
				m.reasoningStatusHeader = header
			}
			m.forwardToView(tui.UpsertStreamingReasoningMsg{Key: evt.ReasoningDelta.Key, Role: evt.ReasoningDelta.Role, Text: evt.ReasoningDelta.Text})
		}
	case runtime.EventReasoningDeltaReset:
		m.forwardToView(tui.ClearStreamingReasoningMsg{})
	case runtime.EventCompactionStarted:
		m.compacting = true
	case runtime.EventCompactionCompleted, runtime.EventCompactionFailed:
		m.compacting = false
	case runtime.EventReviewerStarted:
		m.reviewerRunning = true
		m.reviewerBlocking = true
	case runtime.EventReviewerCompleted:
		m.clearReviewerState()
	case runtime.EventRunStateChanged:
		if evt.RunState != nil {
			m.busy = evt.RunState.Busy
			if evt.RunState.Busy {
				m.activity = uiActivityRunning
			} else {
				if m.activity == uiActivityRunning {
					m.activity = uiActivityIdle
				}
				m.reasoningStatusHeader = ""
				m.forwardToView(tui.ClearStreamingReasoningMsg{})
			}
		}
	case runtime.EventBackgroundUpdated:
		m.refreshProcessEntries()
		if evt.Background != nil && (evt.Background.Type == "completed" || evt.Background.Type == "killed") {
			if evt.Background.NoticeSuppressed {
				return nil
			}
			kind := uiStatusNoticeSuccess
			if evt.Background.Type == "killed" && !evt.Background.UserRequestedKill {
				kind = uiStatusNoticeError
			}
			return m.setTransientStatusWithKind(formatBackgroundTransientStatus(evt.Background, m.busy), kind)
		}
	case runtime.EventUserMessageFlushed:
		a.onUserMessageFlushed(evt.UserMessage)
		if m.usesNativeScrollback() {
			return a.syncConversationFromEngine()
		}
	}
	return nil
}

func formatBackgroundTransientStatus(evt *runtime.BackgroundShellEvent, busy bool) string {
	text := fmt.Sprintf("background shell %s %s", evt.ID, evt.State)
	if !busy {
		return text
	}
	return text + "; transcript notice queued for next turn slot"
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

func (a uiRuntimeAdapter) syncConversationFromEngine() tea.Cmd {
	m := a.model
	if m.engine == nil {
		return nil
	}
	return a.applyChatSnapshot(m.engine.ChatSnapshot())
}

func (a uiRuntimeAdapter) applyChatSnapshot(snapshot runtime.ChatSnapshot) tea.Cmd {
	m := a.model
	entries := make([]tui.TranscriptEntry, 0, len(snapshot.Entries))
	for _, entry := range snapshot.Entries {
		entries = append(entries, tui.TranscriptEntry{
			Role:        entry.Role,
			Text:        entry.Text,
			OngoingText: entry.OngoingText,
			Phase:       entry.Phase,
			ToolCallID:  entry.ToolCallID,
			ToolCall:    entry.ToolCall,
		})
	}
	m.transcriptEntries = append(m.transcriptEntries[:0], entries...)
	m.refreshRollbackCandidates()
	m.forwardToView(tui.ClearStreamingReasoningMsg{})
	m.forwardToView(tui.SetConversationMsg{
		Entries:      entries,
		Ongoing:      snapshot.Ongoing,
		OngoingError: snapshot.OngoingError,
	})
	if strings.TrimSpace(snapshot.Ongoing) == "" {
		m.sawAssistantDelta = false
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
	_ = m.runtimeAdapter().syncConversationFromEngine()
}

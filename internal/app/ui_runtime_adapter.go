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

func (a uiRuntimeAdapter) handleRuntimeEvent(evt runtime.Event) {
	m := a.model
	switch evt.Kind {
	case runtime.EventConversationUpdated:
		a.syncConversationFromEngine()
	case runtime.EventAssistantDelta:
		m.sawAssistantDelta = evt.AssistantDelta != ""
	case runtime.EventAssistantDeltaReset:
		m.sawAssistantDelta = false
	case runtime.EventUserMessageFlushed:
		a.onUserMessageFlushed(evt.UserMessage)
	}
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
		m.input = ""
		m.lockedInjectText = ""
		m.inputSubmitLocked = false
	}
}

func (a uiRuntimeAdapter) syncConversationFromEngine() {
	m := a.model
	if m.engine == nil {
		return
	}
	snapshot := m.engine.ChatSnapshot()
	entries := make([]tui.TranscriptEntry, 0, len(snapshot.Entries))
	for _, entry := range snapshot.Entries {
		entries = append(entries, tui.TranscriptEntry{
			Role:     entry.Role,
			Text:     entry.Text,
			ToolCall: entry.ToolCall,
		})
	}
	m.forwardToView(tui.SetConversationMsg{
		Entries:      entries,
		Ongoing:      snapshot.Ongoing,
		OngoingError: snapshot.OngoingError,
	})
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
	m.runtimeAdapter().handleRuntimeEvent(evt)
}

func (m *uiModel) onUserMessageFlushed(text string) {
	m.runtimeAdapter().onUserMessageFlushed(text)
}

func (m *uiModel) syncConversationFromEngine() {
	m.runtimeAdapter().syncConversationFromEngine()
}

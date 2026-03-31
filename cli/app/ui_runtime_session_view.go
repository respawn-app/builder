package app

import "builder/shared/clientui"

func (m *uiModel) runtimeSessionView() clientui.RuntimeSessionView {
	if m.engine != nil {
		return m.engine.SessionView()
	}
	entries := make([]clientui.ChatEntry, 0, len(m.transcriptEntries))
	for _, entry := range m.transcriptEntries {
		entries = append(entries, clientui.ChatEntry{
			Role:        entry.Role,
			Text:        entry.Text,
			OngoingText: entry.OngoingText,
			Phase:       string(entry.Phase),
			ToolCallID:  entry.ToolCallID,
		})
	}
	return clientui.RuntimeSessionView{
		SessionID:             m.sessionID,
		SessionName:           m.sessionName,
		ConversationFreshness: m.conversationFreshness,
		Chat: clientui.ChatSnapshot{
			Entries: entries,
			Ongoing: m.view.OngoingStreamingText(),
		},
	}
}

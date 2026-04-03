package app

import "builder/shared/clientui"

func (m *uiModel) runtimeMainView() clientui.RuntimeMainView {
	if client := m.runtimeClient(); client != nil {
		return client.MainView()
	}
	return clientui.RuntimeMainView{
		Status:  m.localRuntimeStatus(),
		Session: m.localRuntimeSessionView(),
	}
}

func (m *uiModel) refreshRuntimeMainView() clientui.RuntimeMainView {
	if client := m.runtimeClient(); client != nil {
		view, err := client.RefreshMainView()
		if err == nil {
			return view
		}
		return client.MainView()
	}
	return clientui.RuntimeMainView{
		Status:  m.localRuntimeStatus(),
		Session: m.localRuntimeSessionView(),
	}
}

func (m *uiModel) runtimeStatus() clientui.RuntimeStatus {
	return m.runtimeMainView().Status
}

func (m *uiModel) refreshRuntimeStatus() clientui.RuntimeStatus {
	return m.refreshRuntimeMainView().Status
}

func (m *uiModel) refreshRuntimeSessionView() clientui.RuntimeSessionView {
	return m.refreshRuntimeMainView().Session
}

func (m *uiModel) localRuntimeStatus() clientui.RuntimeStatus {
	return clientui.RuntimeStatus{
		ReviewerFrequency:     m.reviewerMode,
		ReviewerEnabled:       m.reviewerEnabled,
		AutoCompactionEnabled: m.autoCompactionEnabled,
		FastModeAvailable:     m.fastModeAvailable,
		FastModeEnabled:       m.fastModeEnabled,
		ConversationFreshness: m.conversationFreshness,
		ThinkingLevel:         m.thinkingLevel,
	}
}

package app

import (
	"context"

	"builder/shared/clientui"
)

func (m *uiModel) runtimeClient() clientui.RuntimeClient {
	if m == nil {
		return nil
	}
	return m.engine
}

func (m *uiModel) hasRuntimeClient() bool {
	return m.runtimeClient() != nil
}

func (m *uiModel) setRuntimeSessionName(name string) error {
	if client := m.runtimeClient(); client != nil {
		return client.SetSessionName(name)
	}
	return nil
}

func (m *uiModel) setRuntimeThinkingLevel(level string) error {
	if client := m.runtimeClient(); client != nil {
		return client.SetThinkingLevel(level)
	}
	return nil
}

func (m *uiModel) setRuntimeFastModeEnabled(enabled bool) (bool, error) {
	if client := m.runtimeClient(); client != nil {
		return client.SetFastModeEnabled(enabled)
	}
	return false, nil
}

func (m *uiModel) setRuntimeReviewerEnabled(enabled bool) (bool, string, error) {
	if client := m.runtimeClient(); client != nil {
		return client.SetReviewerEnabled(enabled)
	}
	return false, "", nil
}

func (m *uiModel) setRuntimeAutoCompactionEnabled(enabled bool) (bool, bool, error) {
	if client := m.runtimeClient(); client != nil {
		return client.SetAutoCompactionEnabled(enabled)
	}
	return false, false, nil
}

func (m *uiModel) appendRuntimeLocalEntry(role, text string) {
	if client := m.runtimeClient(); client != nil {
		client.AppendLocalEntry(role, text)
	}
}

func (m *uiModel) runtimeShouldCompactBeforeUserMessage(ctx context.Context, text string) (bool, error) {
	if client := m.runtimeClient(); client != nil {
		return client.ShouldCompactBeforeUserMessage(ctx, text)
	}
	return false, nil
}

func (m *uiModel) submitRuntimeUserMessage(ctx context.Context, text string) (string, error) {
	if client := m.runtimeClient(); client != nil {
		return client.SubmitUserMessage(ctx, text)
	}
	return "", nil
}

func (m *uiModel) submitRuntimeUserShellCommand(ctx context.Context, command string) error {
	if client := m.runtimeClient(); client != nil {
		return client.SubmitUserShellCommand(ctx, command)
	}
	return nil
}

func (m *uiModel) compactRuntimeContext(ctx context.Context, args string) error {
	if client := m.runtimeClient(); client != nil {
		return client.CompactContext(ctx, args)
	}
	return nil
}

func (m *uiModel) compactRuntimeContextForPreSubmit(ctx context.Context) error {
	if client := m.runtimeClient(); client != nil {
		return client.CompactContextForPreSubmit(ctx)
	}
	return nil
}

func (m *uiModel) hasQueuedRuntimeUserWork() (bool, error) {
	if client := m.runtimeClient(); client != nil {
		return client.HasQueuedUserWork()
	}
	return false, nil
}

func (m *uiModel) submitQueuedRuntimeUserMessages(ctx context.Context) (string, error) {
	if client := m.runtimeClient(); client != nil {
		return client.SubmitQueuedUserMessages(ctx)
	}
	return "", nil
}

func (m *uiModel) interruptRuntime() error {
	if client := m.runtimeClient(); client != nil {
		return client.Interrupt()
	}
	return nil
}

func (m *uiModel) queueRuntimeUserMessage(text string) {
	if client := m.runtimeClient(); client != nil {
		client.QueueUserMessage(text)
	}
}

func (m *uiModel) discardQueuedRuntimeUserMessagesMatching(text string) int {
	if client := m.runtimeClient(); client != nil {
		return client.DiscardQueuedUserMessagesMatching(text)
	}
	return 0
}

func (m *uiModel) recordRuntimePromptHistory(text string) error {
	if client := m.runtimeClient(); client != nil {
		return client.RecordPromptHistory(text)
	}
	return nil
}

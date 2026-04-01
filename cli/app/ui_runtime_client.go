package app

import (
	"context"

	"builder/server/runtime"
	"builder/server/sessionview"
	"builder/server/runtimecontrol"
	"builder/shared/client"
	"builder/shared/clientui"
	"builder/shared/serverapi"
)

type sessionRuntimeClient struct {
	reads     client.SessionViewClient
	controls  client.RuntimeControlClient
	sessionID string
}

func newRuntimeClient(sessionID string, reads client.SessionViewClient, controls client.RuntimeControlClient) clientui.RuntimeClient {
	return newUIRuntimeClientWithReads(sessionID, reads, controls)
}

func newUIRuntimeClientFromEngine(engine *runtime.Engine) clientui.RuntimeClient {
	if engine == nil {
		return nil
	}
	resolver := sessionview.NewStaticRuntimeResolver(engine)
	reads := client.NewLoopbackSessionViewClient(sessionview.NewService(nil, resolver))
	controls := client.NewLoopbackRuntimeControlClient(runtimecontrol.NewService(resolver, nil))
	return newUIRuntimeClientWithReads(engine.SessionID(), reads, controls)
}

func newUIRuntimeClient(engine *runtime.Engine) clientui.RuntimeClient {
	return newUIRuntimeClientFromEngine(engine)
}

func newUIRuntimeClientWithReads(sessionID string, reads client.SessionViewClient, controls client.RuntimeControlClient) clientui.RuntimeClient {
	if reads == nil || controls == nil {
		return nil
	}
	return sessionRuntimeClient{
		sessionID: sessionID,
		reads:     reads,
		controls:  controls,
	}
}

func (c sessionRuntimeClient) MainView() clientui.RuntimeMainView {
	resp, err := c.reads.GetSessionMainView(context.Background(), serverapi.SessionMainViewRequest{SessionID: c.sessionID})
	if err != nil {
		return clientui.RuntimeMainView{}
	}
	return resp.MainView
}

func (c sessionRuntimeClient) Status() clientui.RuntimeStatus {
	return c.MainView().Status
}

func (c sessionRuntimeClient) SessionView() clientui.RuntimeSessionView {
	return c.MainView().Session
}

func (c sessionRuntimeClient) SetSessionName(name string) error {
	return c.controls.SetSessionName(context.Background(), serverapi.RuntimeSetSessionNameRequest{SessionID: c.sessionID, Name: name})
}
func (c sessionRuntimeClient) SetThinkingLevel(level string) error {
	return c.controls.SetThinkingLevel(context.Background(), serverapi.RuntimeSetThinkingLevelRequest{SessionID: c.sessionID, Level: level})
}
func (c sessionRuntimeClient) SetFastModeEnabled(enabled bool) (bool, error) {
	resp, err := c.controls.SetFastModeEnabled(context.Background(), serverapi.RuntimeSetFastModeEnabledRequest{SessionID: c.sessionID, Enabled: enabled})
	return resp.Changed, err
}
func (c sessionRuntimeClient) SetReviewerEnabled(enabled bool) (bool, string, error) {
	resp, err := c.controls.SetReviewerEnabled(context.Background(), serverapi.RuntimeSetReviewerEnabledRequest{SessionID: c.sessionID, Enabled: enabled})
	return resp.Changed, resp.Mode, err
}
func (c sessionRuntimeClient) SetAutoCompactionEnabled(enabled bool) (bool, bool) {
	resp, err := c.controls.SetAutoCompactionEnabled(context.Background(), serverapi.RuntimeSetAutoCompactionEnabledRequest{SessionID: c.sessionID, Enabled: enabled})
	if err != nil {
		return false, false
	}
	return resp.Changed, resp.Enabled
}
func (c sessionRuntimeClient) AppendLocalEntry(role, text string) {
	_ = c.controls.AppendLocalEntry(context.Background(), serverapi.RuntimeAppendLocalEntryRequest{SessionID: c.sessionID, Role: role, Text: text})
}
func (c sessionRuntimeClient) ShouldCompactBeforeUserMessage(ctx context.Context, text string) (bool, error) {
	resp, err := c.controls.ShouldCompactBeforeUserMessage(ctx, serverapi.RuntimeShouldCompactBeforeUserMessageRequest{SessionID: c.sessionID, Text: text})
	return resp.ShouldCompact, err
}
func (c sessionRuntimeClient) SubmitUserMessage(ctx context.Context, text string) (string, error) {
	resp, err := c.controls.SubmitUserMessage(ctx, serverapi.RuntimeSubmitUserMessageRequest{SessionID: c.sessionID, Text: text})
	return resp.Message, err
}
func (c sessionRuntimeClient) SubmitUserShellCommand(ctx context.Context, command string) error {
	return c.controls.SubmitUserShellCommand(ctx, serverapi.RuntimeSubmitUserShellCommandRequest{SessionID: c.sessionID, Command: command})
}
func (c sessionRuntimeClient) CompactContext(ctx context.Context, args string) error {
	return c.controls.CompactContext(ctx, serverapi.RuntimeCompactContextRequest{SessionID: c.sessionID, Args: args})
}
func (c sessionRuntimeClient) CompactContextForPreSubmit(ctx context.Context) error {
	return c.controls.CompactContextForPreSubmit(ctx, serverapi.RuntimeCompactContextForPreSubmitRequest{SessionID: c.sessionID})
}
func (c sessionRuntimeClient) HasQueuedUserWork() bool {
	resp, err := c.controls.HasQueuedUserWork(context.Background(), serverapi.RuntimeHasQueuedUserWorkRequest{SessionID: c.sessionID})
	if err != nil {
		return false
	}
	return resp.HasQueuedUserWork
}
func (c sessionRuntimeClient) SubmitQueuedUserMessages(ctx context.Context) (string, error) {
	resp, err := c.controls.SubmitQueuedUserMessages(ctx, serverapi.RuntimeSubmitQueuedUserMessagesRequest{SessionID: c.sessionID})
	return resp.Message, err
}
func (c sessionRuntimeClient) Interrupt() error {
	return c.controls.Interrupt(context.Background(), serverapi.RuntimeInterruptRequest{SessionID: c.sessionID})
}

func (c sessionRuntimeClient) QueueUserMessage(text string) {
	_ = c.controls.QueueUserMessage(context.Background(), serverapi.RuntimeQueueUserMessageRequest{SessionID: c.sessionID, Text: text})
}

func (c sessionRuntimeClient) DiscardQueuedUserMessagesMatching(text string) int {
	resp, err := c.controls.DiscardQueuedUserMessagesMatching(context.Background(), serverapi.RuntimeDiscardQueuedUserMessagesMatchingRequest{SessionID: c.sessionID, Text: text})
	if err != nil {
		return 0
	}
	return resp.Discarded
}

func (c sessionRuntimeClient) RecordPromptHistory(text string) error {
	return c.controls.RecordPromptHistory(context.Background(), serverapi.RuntimeRecordPromptHistoryRequest{SessionID: c.sessionID, Text: text})
}

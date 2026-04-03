package app

import (
	"context"
	"time"

	"builder/server/runtime"
	"builder/server/runtimecontrol"
	"builder/server/sessionview"
	"builder/shared/client"
	"builder/shared/clientui"
	"builder/shared/serverapi"
)

const uiRuntimeControlTimeout = 3 * time.Second

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

func (c sessionRuntimeClient) controlContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), uiRuntimeControlTimeout)
}

func (c sessionRuntimeClient) SetSessionName(name string) error {
	ctx, cancel := c.controlContext()
	defer cancel()
	return c.controls.SetSessionName(ctx, serverapi.RuntimeSetSessionNameRequest{SessionID: c.sessionID, Name: name})
}
func (c sessionRuntimeClient) SetThinkingLevel(level string) error {
	ctx, cancel := c.controlContext()
	defer cancel()
	return c.controls.SetThinkingLevel(ctx, serverapi.RuntimeSetThinkingLevelRequest{SessionID: c.sessionID, Level: level})
}
func (c sessionRuntimeClient) SetFastModeEnabled(enabled bool) (bool, error) {
	ctx, cancel := c.controlContext()
	defer cancel()
	resp, err := c.controls.SetFastModeEnabled(ctx, serverapi.RuntimeSetFastModeEnabledRequest{SessionID: c.sessionID, Enabled: enabled})
	return resp.Changed, err
}
func (c sessionRuntimeClient) SetReviewerEnabled(enabled bool) (bool, string, error) {
	ctx, cancel := c.controlContext()
	defer cancel()
	resp, err := c.controls.SetReviewerEnabled(ctx, serverapi.RuntimeSetReviewerEnabledRequest{SessionID: c.sessionID, Enabled: enabled})
	return resp.Changed, resp.Mode, err
}
func (c sessionRuntimeClient) SetAutoCompactionEnabled(enabled bool) (bool, bool, error) {
	ctx, cancel := c.controlContext()
	defer cancel()
	resp, err := c.controls.SetAutoCompactionEnabled(ctx, serverapi.RuntimeSetAutoCompactionEnabledRequest{SessionID: c.sessionID, Enabled: enabled})
	if err != nil {
		return false, false, err
	}
	return resp.Changed, resp.Enabled, nil
}
func (c sessionRuntimeClient) AppendLocalEntry(role, text string) {
	ctx, cancel := c.controlContext()
	defer cancel()
	_ = c.controls.AppendLocalEntry(ctx, serverapi.RuntimeAppendLocalEntryRequest{SessionID: c.sessionID, Role: role, Text: text})
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
func (c sessionRuntimeClient) HasQueuedUserWork() (bool, error) {
	ctx, cancel := c.controlContext()
	defer cancel()
	resp, err := c.controls.HasQueuedUserWork(ctx, serverapi.RuntimeHasQueuedUserWorkRequest{SessionID: c.sessionID})
	if err != nil {
		return false, err
	}
	return resp.HasQueuedUserWork, nil
}
func (c sessionRuntimeClient) SubmitQueuedUserMessages(ctx context.Context) (string, error) {
	resp, err := c.controls.SubmitQueuedUserMessages(ctx, serverapi.RuntimeSubmitQueuedUserMessagesRequest{SessionID: c.sessionID})
	return resp.Message, err
}
func (c sessionRuntimeClient) Interrupt() error {
	ctx, cancel := c.controlContext()
	defer cancel()
	return c.controls.Interrupt(ctx, serverapi.RuntimeInterruptRequest{SessionID: c.sessionID})
}

func (c sessionRuntimeClient) QueueUserMessage(text string) {
	ctx, cancel := c.controlContext()
	defer cancel()
	_ = c.controls.QueueUserMessage(ctx, serverapi.RuntimeQueueUserMessageRequest{SessionID: c.sessionID, Text: text})
}

func (c sessionRuntimeClient) DiscardQueuedUserMessagesMatching(text string) int {
	ctx, cancel := c.controlContext()
	defer cancel()
	resp, err := c.controls.DiscardQueuedUserMessagesMatching(ctx, serverapi.RuntimeDiscardQueuedUserMessagesMatchingRequest{SessionID: c.sessionID, Text: text})
	if err != nil {
		return 0
	}
	return resp.Discarded
}

func (c sessionRuntimeClient) RecordPromptHistory(text string) error {
	ctx, cancel := c.controlContext()
	defer cancel()
	return c.controls.RecordPromptHistory(ctx, serverapi.RuntimeRecordPromptHistoryRequest{SessionID: c.sessionID, Text: text})
}

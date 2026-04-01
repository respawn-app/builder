package app

import (
	"context"

	"builder/server/runtime"
	"builder/server/runtimeview"
	"builder/server/session"
	"builder/server/sessionview"
	"builder/shared/client"
	"builder/shared/clientui"
	"builder/shared/serverapi"
)

type engineUIRuntimeClient struct {
	engine    *runtime.Engine
	reads     client.SessionViewClient
	sessionID string
}

func newUIRuntimeClient(engine *runtime.Engine) clientui.RuntimeClient {
	return newUIRuntimeClientWithReads(engine, nil)
}

func newUIRuntimeClientWithReads(engine *runtime.Engine, reads client.SessionViewClient) clientui.RuntimeClient {
	if engine == nil {
		return nil
	}
	if reads == nil {
		reads = client.NewLoopbackSessionViewClient(sessionview.NewService(nil, sessionview.NewStaticRuntimeResolver(engine)))
	}
	return engineUIRuntimeClient{
		engine:    engine,
		sessionID: engine.SessionID(),
		reads:     reads,
	}
}

func (c engineUIRuntimeClient) MainView() clientui.RuntimeMainView {
	if c.reads == nil {
		return runtimeview.MainViewFromRuntime(c.engine)
	}
	resp, err := c.reads.GetSessionMainView(context.Background(), serverapi.SessionMainViewRequest{SessionID: c.sessionID})
	if err != nil {
		return runtimeview.MainViewFromRuntime(c.engine)
	}
	return resp.MainView
}

func (c engineUIRuntimeClient) Status() clientui.RuntimeStatus {
	return c.MainView().Status
}

func (c engineUIRuntimeClient) SessionView() clientui.RuntimeSessionView {
	return c.MainView().Session
}

func (c engineUIRuntimeClient) SetSessionName(name string) error {
	return c.engine.SetSessionName(name)
}
func (c engineUIRuntimeClient) SetThinkingLevel(level string) error {
	return c.engine.SetThinkingLevel(level)
}
func (c engineUIRuntimeClient) SetFastModeEnabled(enabled bool) (bool, error) {
	return c.engine.SetFastModeEnabled(enabled)
}
func (c engineUIRuntimeClient) SetReviewerEnabled(enabled bool) (bool, string, error) {
	return c.engine.SetReviewerEnabled(enabled)
}
func (c engineUIRuntimeClient) SetAutoCompactionEnabled(enabled bool) (bool, bool) {
	return c.engine.SetAutoCompactionEnabled(enabled)
}
func (c engineUIRuntimeClient) AppendLocalEntry(role, text string) {
	c.engine.AppendLocalEntry(role, text)
}
func (c engineUIRuntimeClient) ShouldCompactBeforeUserMessage(ctx context.Context, text string) (bool, error) {
	return c.engine.ShouldCompactBeforeUserMessage(ctx, text)
}
func (c engineUIRuntimeClient) SubmitUserMessage(ctx context.Context, text string) (string, error) {
	msg, err := c.engine.SubmitUserMessage(ctx, text)
	return msg.Content, err
}
func (c engineUIRuntimeClient) SubmitUserShellCommand(ctx context.Context, command string) error {
	_, err := c.engine.SubmitUserShellCommand(ctx, command)
	return err
}
func (c engineUIRuntimeClient) CompactContext(ctx context.Context, args string) error {
	return c.engine.CompactContext(ctx, args)
}
func (c engineUIRuntimeClient) CompactContextForPreSubmit(ctx context.Context) error {
	return c.engine.CompactContextForPreSubmit(ctx)
}
func (c engineUIRuntimeClient) HasQueuedUserWork() bool { return c.engine.HasQueuedUserWork() }
func (c engineUIRuntimeClient) SubmitQueuedUserMessages(ctx context.Context) (string, error) {
	msg, err := c.engine.SubmitQueuedUserMessages(ctx)
	return msg.Content, err
}
func (c engineUIRuntimeClient) Interrupt() error             { return c.engine.Interrupt() }
func (c engineUIRuntimeClient) QueueUserMessage(text string) { c.engine.QueueUserMessage(text) }
func (c engineUIRuntimeClient) DiscardQueuedUserMessagesMatching(text string) int {
	return c.engine.DiscardQueuedUserMessagesMatching(text)
}
func (c engineUIRuntimeClient) RecordPromptHistory(text string) error {
	return c.engine.RecordPromptHistory(text)
}

func mapConversationFreshness(freshness session.ConversationFreshness) clientui.ConversationFreshness {
	if freshness.IsFresh() {
		return clientui.ConversationFreshnessFresh
	}
	return clientui.ConversationFreshnessEstablished
}

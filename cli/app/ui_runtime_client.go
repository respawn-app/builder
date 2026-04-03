package app

import (
	"context"
	"sync"
	"time"

	"builder/server/runtime"
	"builder/server/runtimecontrol"
	"builder/server/sessionview"
	"builder/shared/client"
	"builder/shared/clientui"
	"builder/shared/serverapi"
)

const uiRuntimeControlTimeout = 3 * time.Second
const uiRuntimeReadTimeout = 300 * time.Millisecond
const uiRuntimeMainViewRefreshInterval = 250 * time.Millisecond

type sessionRuntimeClient struct {
	reads     client.SessionViewClient
	controls  client.RuntimeControlClient
	sessionID string

	mu              sync.RWMutex
	mainView        clientui.RuntimeMainView
	hasMainView     bool
	lastMainViewAt  time.Time
	refreshInFlight bool
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
	return &sessionRuntimeClient{
		sessionID: sessionID,
		reads:     reads,
		controls:  controls,
		mainView:  clientui.RuntimeMainView{Session: clientui.RuntimeSessionView{SessionID: sessionID}},
	}
}

func (c *sessionRuntimeClient) MainView() clientui.RuntimeMainView {
	view, hasView, stale := c.cachedMainView()
	if !hasView {
		refreshed, err := c.RefreshMainView()
		if err == nil {
			return refreshed
		}
		return view
	}
	if stale {
		c.refreshMainViewAsync()
	}
	return view
}

func (c *sessionRuntimeClient) RefreshMainView() (clientui.RuntimeMainView, error) {
	return c.refreshMainViewSync()
}

func (c *sessionRuntimeClient) Status() clientui.RuntimeStatus {
	return c.MainView().Status
}

func (c *sessionRuntimeClient) SessionView() clientui.RuntimeSessionView {
	return c.MainView().Session
}

func (c *sessionRuntimeClient) controlContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), uiRuntimeControlTimeout)
}

func (c *sessionRuntimeClient) readContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), uiRuntimeReadTimeout)
}

func (c *sessionRuntimeClient) cachedMainView() (clientui.RuntimeMainView, bool, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	view := c.mainView
	if !c.hasMainView {
		return view, false, true
	}
	return view, true, time.Since(c.lastMainViewAt) >= uiRuntimeMainViewRefreshInterval
}

func (c *sessionRuntimeClient) storeMainView(view clientui.RuntimeMainView) clientui.RuntimeMainView {
	if view.Session.SessionID == "" {
		view.Session.SessionID = c.sessionID
	}
	c.mu.Lock()
	c.mainView = view
	c.hasMainView = true
	c.lastMainViewAt = time.Now()
	c.mu.Unlock()
	return view
}

func (c *sessionRuntimeClient) patchMainView(apply func(view *clientui.RuntimeMainView)) {
	c.mu.Lock()
	apply(&c.mainView)
	if c.mainView.Session.SessionID == "" {
		c.mainView.Session.SessionID = c.sessionID
	}
	c.hasMainView = true
	c.lastMainViewAt = time.Now()
	c.mu.Unlock()
}

func (c *sessionRuntimeClient) refreshMainViewSync() (clientui.RuntimeMainView, error) {
	ctx, cancel := c.readContext()
	defer cancel()
	resp, err := c.reads.GetSessionMainView(ctx, serverapi.SessionMainViewRequest{SessionID: c.sessionID})
	if err != nil {
		c.mu.Lock()
		if c.mainView.Session.SessionID == "" {
			c.mainView.Session.SessionID = c.sessionID
		}
		c.hasMainView = true
		c.lastMainViewAt = time.Now()
		view := c.mainView
		c.mu.Unlock()
		return view, err
	}
	return c.storeMainView(resp.MainView), nil
}

func (c *sessionRuntimeClient) refreshMainViewAsync() {
	c.mu.Lock()
	if c.refreshInFlight {
		c.mu.Unlock()
		return
	}
	c.refreshInFlight = true
	c.mu.Unlock()
	go func() {
		defer func() {
			c.mu.Lock()
			c.refreshInFlight = false
			c.mu.Unlock()
		}()
		_, _ = c.refreshMainViewSync()
	}()
}

func (c *sessionRuntimeClient) SetSessionName(name string) error {
	ctx, cancel := c.controlContext()
	defer cancel()
	if err := c.controls.SetSessionName(ctx, serverapi.RuntimeSetSessionNameRequest{SessionID: c.sessionID, Name: name}); err != nil {
		return err
	}
	c.patchMainView(func(view *clientui.RuntimeMainView) {
		view.Session.SessionName = name
	})
	return nil
}
func (c *sessionRuntimeClient) SetThinkingLevel(level string) error {
	ctx, cancel := c.controlContext()
	defer cancel()
	if err := c.controls.SetThinkingLevel(ctx, serverapi.RuntimeSetThinkingLevelRequest{SessionID: c.sessionID, Level: level}); err != nil {
		return err
	}
	c.patchMainView(func(view *clientui.RuntimeMainView) {
		view.Status.ThinkingLevel = level
	})
	return nil
}
func (c *sessionRuntimeClient) SetFastModeEnabled(enabled bool) (bool, error) {
	ctx, cancel := c.controlContext()
	defer cancel()
	resp, err := c.controls.SetFastModeEnabled(ctx, serverapi.RuntimeSetFastModeEnabledRequest{SessionID: c.sessionID, Enabled: enabled})
	if err == nil {
		c.patchMainView(func(view *clientui.RuntimeMainView) {
			view.Status.FastModeEnabled = enabled
		})
	}
	return resp.Changed, err
}
func (c *sessionRuntimeClient) SetReviewerEnabled(enabled bool) (bool, string, error) {
	ctx, cancel := c.controlContext()
	defer cancel()
	resp, err := c.controls.SetReviewerEnabled(ctx, serverapi.RuntimeSetReviewerEnabledRequest{SessionID: c.sessionID, Enabled: enabled})
	if err == nil {
		c.patchMainView(func(view *clientui.RuntimeMainView) {
			view.Status.ReviewerFrequency = resp.Mode
			view.Status.ReviewerEnabled = resp.Mode != "" && resp.Mode != "off"
		})
	}
	return resp.Changed, resp.Mode, err
}
func (c *sessionRuntimeClient) SetAutoCompactionEnabled(enabled bool) (bool, bool, error) {
	ctx, cancel := c.controlContext()
	defer cancel()
	resp, err := c.controls.SetAutoCompactionEnabled(ctx, serverapi.RuntimeSetAutoCompactionEnabledRequest{SessionID: c.sessionID, Enabled: enabled})
	if err != nil {
		return false, false, err
	}
	c.patchMainView(func(view *clientui.RuntimeMainView) {
		view.Status.AutoCompactionEnabled = resp.Enabled
	})
	return resp.Changed, resp.Enabled, nil
}
func (c *sessionRuntimeClient) AppendLocalEntry(role, text string) {
	ctx, cancel := c.controlContext()
	defer cancel()
	_ = c.controls.AppendLocalEntry(ctx, serverapi.RuntimeAppendLocalEntryRequest{SessionID: c.sessionID, Role: role, Text: text})
}
func (c *sessionRuntimeClient) ShouldCompactBeforeUserMessage(ctx context.Context, text string) (bool, error) {
	resp, err := c.controls.ShouldCompactBeforeUserMessage(ctx, serverapi.RuntimeShouldCompactBeforeUserMessageRequest{SessionID: c.sessionID, Text: text})
	return resp.ShouldCompact, err
}
func (c *sessionRuntimeClient) SubmitUserMessage(ctx context.Context, text string) (string, error) {
	resp, err := c.controls.SubmitUserMessage(ctx, serverapi.RuntimeSubmitUserMessageRequest{SessionID: c.sessionID, Text: text})
	return resp.Message, err
}
func (c *sessionRuntimeClient) SubmitUserShellCommand(ctx context.Context, command string) error {
	return c.controls.SubmitUserShellCommand(ctx, serverapi.RuntimeSubmitUserShellCommandRequest{SessionID: c.sessionID, Command: command})
}
func (c *sessionRuntimeClient) CompactContext(ctx context.Context, args string) error {
	return c.controls.CompactContext(ctx, serverapi.RuntimeCompactContextRequest{SessionID: c.sessionID, Args: args})
}
func (c *sessionRuntimeClient) CompactContextForPreSubmit(ctx context.Context) error {
	return c.controls.CompactContextForPreSubmit(ctx, serverapi.RuntimeCompactContextForPreSubmitRequest{SessionID: c.sessionID})
}
func (c *sessionRuntimeClient) HasQueuedUserWork() (bool, error) {
	ctx, cancel := c.controlContext()
	defer cancel()
	resp, err := c.controls.HasQueuedUserWork(ctx, serverapi.RuntimeHasQueuedUserWorkRequest{SessionID: c.sessionID})
	if err != nil {
		return false, err
	}
	return resp.HasQueuedUserWork, nil
}
func (c *sessionRuntimeClient) SubmitQueuedUserMessages(ctx context.Context) (string, error) {
	resp, err := c.controls.SubmitQueuedUserMessages(ctx, serverapi.RuntimeSubmitQueuedUserMessagesRequest{SessionID: c.sessionID})
	return resp.Message, err
}
func (c *sessionRuntimeClient) Interrupt() error {
	ctx, cancel := c.controlContext()
	defer cancel()
	return c.controls.Interrupt(ctx, serverapi.RuntimeInterruptRequest{SessionID: c.sessionID})
}

func (c *sessionRuntimeClient) QueueUserMessage(text string) {
	ctx, cancel := c.controlContext()
	defer cancel()
	_ = c.controls.QueueUserMessage(ctx, serverapi.RuntimeQueueUserMessageRequest{SessionID: c.sessionID, Text: text})
}

func (c *sessionRuntimeClient) DiscardQueuedUserMessagesMatching(text string) int {
	ctx, cancel := c.controlContext()
	defer cancel()
	resp, err := c.controls.DiscardQueuedUserMessagesMatching(ctx, serverapi.RuntimeDiscardQueuedUserMessagesMatchingRequest{SessionID: c.sessionID, Text: text})
	if err != nil {
		return 0
	}
	return resp.Discarded
}

func (c *sessionRuntimeClient) RecordPromptHistory(text string) error {
	ctx, cancel := c.controlContext()
	defer cancel()
	return c.controls.RecordPromptHistory(ctx, serverapi.RuntimeRecordPromptHistoryRequest{SessionID: c.sessionID, Text: text})
}

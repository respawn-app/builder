package app

import (
	"context"
	"os"
	"strings"
	"sync"
	"time"

	"builder/server/runtime"
	"builder/server/runtimecontrol"
	"builder/server/runtimeview"
	"builder/server/sessionview"
	"builder/shared/client"
	"builder/shared/clientui"
	"builder/shared/serverapi"
	"builder/shared/transcriptdiag"
)

const uiRuntimeControlTimeout = 3 * time.Second
const uiRuntimeReadTimeout = 300 * time.Millisecond
const uiRuntimeHydrationReadTimeout = 10 * time.Second
const uiRuntimeMainViewRefreshInterval = 250 * time.Millisecond

type sessionRuntimeClient struct {
	reads     client.SessionViewClient
	controls  client.RuntimeControlClient
	sessionID string

	mu                        sync.RWMutex
	mainView                  clientui.RuntimeMainView
	hasMainView               bool
	lastMainViewAt            time.Time
	transcript                clientui.TranscriptPage
	hasTranscript             bool
	lastTranscriptAt          time.Time
	refreshInFlight           bool
	transcriptRefreshInFlight bool
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
	runtimeClient := newUIRuntimeClientWithReads(engine.SessionID(), reads, controls).(*sessionRuntimeClient)
	runtimeClient.storeMainView(runtimeview.MainViewFromRuntime(engine))
	runtimeClient.storeTranscript(runtimeview.TranscriptPageFromRuntime(engine, clientui.TranscriptPageRequest{Window: clientui.TranscriptWindowOngoingTail}))
	return runtimeClient
}

func newUIRuntimeClient(engine *runtime.Engine) clientui.RuntimeClient {
	return newUIRuntimeClientFromEngine(engine)
}

func newUIRuntimeClientWithReads(sessionID string, reads client.SessionViewClient, controls client.RuntimeControlClient) clientui.RuntimeClient {
	if reads == nil || controls == nil {
		return nil
	}
	return &sessionRuntimeClient{
		sessionID:  sessionID,
		reads:      reads,
		controls:   controls,
		mainView:   clientui.RuntimeMainView{Session: clientui.RuntimeSessionView{SessionID: sessionID}},
		transcript: clientui.TranscriptPage{SessionID: sessionID},
	}
}

func (c *sessionRuntimeClient) MainView() clientui.RuntimeMainView {
	view, hasView, stale := c.cachedMainView()
	if !hasView {
		refreshed, err := c.refreshMainViewSync(uiRuntimeReadTimeout)
		if err == nil {
			return refreshed
		}
		c.refreshMainViewAsync()
		return view
	}
	if stale {
		c.refreshMainViewAsync()
	}
	return view
}

func (c *sessionRuntimeClient) RefreshMainView() (clientui.RuntimeMainView, error) {
	return c.refreshMainViewSync(uiRuntimeHydrationReadTimeout)
}

func (c *sessionRuntimeClient) Transcript() clientui.TranscriptPage {
	page, hasPage, stale := c.cachedTranscript()
	if !hasPage {
		c.refreshTranscriptAsync()
		return page
	}
	if stale {
		c.refreshTranscriptAsync()
	}
	return page
}

func (c *sessionRuntimeClient) RefreshTranscript() (clientui.TranscriptPage, error) {
	return c.LoadTranscriptPage(clientui.TranscriptPageRequest{Window: clientui.TranscriptWindowOngoingTail})
}

func (c *sessionRuntimeClient) LoadTranscriptPage(req clientui.TranscriptPageRequest) (clientui.TranscriptPage, error) {
	return c.refreshTranscriptPageSync(req, uiRuntimeHydrationReadTimeout)
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

func (c *sessionRuntimeClient) readContext(timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		timeout = uiRuntimeReadTimeout
	}
	return context.WithTimeout(context.Background(), timeout)
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

func (c *sessionRuntimeClient) cachedTranscript() (clientui.TranscriptPage, bool, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	page := c.transcript
	if !c.hasTranscript {
		return page, false, true
	}
	return page, true, time.Since(c.lastTranscriptAt) >= uiRuntimeMainViewRefreshInterval
}

func (c *sessionRuntimeClient) storeMainView(view clientui.RuntimeMainView) clientui.RuntimeMainView {
	if view.Session.SessionID == "" {
		view.Session.SessionID = c.sessionID
	}
	c.mu.Lock()
	c.mainView = view
	c.hasMainView = true
	c.lastMainViewAt = time.Now()
	storeTranscriptFromSessionViewLocked(c, view.Session)
	c.mu.Unlock()
	return view
}

func (c *sessionRuntimeClient) storeTranscript(page clientui.TranscriptPage) clientui.TranscriptPage {
	if page.SessionID == "" {
		page.SessionID = c.sessionID
	}
	c.mu.Lock()
	storeTranscriptLocked(c, page)
	c.mu.Unlock()
	return page
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

func (c *sessionRuntimeClient) refreshMainViewSync(timeout time.Duration) (clientui.RuntimeMainView, error) {
	ctx, cancel := c.readContext(timeout)
	defer cancel()
	resp, err := c.reads.GetSessionMainView(ctx, serverapi.SessionMainViewRequest{SessionID: c.sessionID})
	if err != nil {
		c.mu.RLock()
		view := c.mainView
		hasView := c.hasMainView
		c.mu.RUnlock()
		if !hasView && view.Session.SessionID == "" {
			view.Session.SessionID = c.sessionID
		}
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
		_, _ = c.refreshMainViewSync(uiRuntimeHydrationReadTimeout)
	}()
}

func (c *sessionRuntimeClient) refreshTranscriptSync(timeout time.Duration) (clientui.TranscriptPage, error) {
	return c.refreshTranscriptPageSync(clientui.TranscriptPageRequest{Window: clientui.TranscriptWindowOngoingTail}, timeout)
}

func (c *sessionRuntimeClient) refreshTranscriptPageSync(req clientui.TranscriptPageRequest, timeout time.Duration) (clientui.TranscriptPage, error) {
	req = normalizeRuntimeTranscriptRequest(req)
	ctx, cancel := c.readContext(timeout)
	defer cancel()
	resp, err := c.reads.GetSessionTranscriptPage(ctx, serverapi.SessionTranscriptPageRequest{
		SessionID: c.sessionID,
		Offset:    req.Offset,
		Limit:     req.Limit,
		Page:      req.Page,
		PageSize:  req.PageSize,
		Window:    req.Window,
	})
	if transcriptdiag.EnabledFromEnv(os.Getenv) {
		fields := map[string]string{"session_id": c.sessionID, "path": "hydrate"}
		for key, value := range transcriptdiag.RequestFields(req) {
			fields[key] = value
		}
		if err != nil {
			fields["err"] = err.Error()
			logRuntimeClientTranscriptDiag(transcriptdiag.FormatLine("transcript.diag.client.hydrate_fetch", fields))
		} else {
			logRuntimeClientTranscriptDiag(transcriptdiag.FormatLine("transcript.diag.client.hydrate_fetch", transcriptdiag.AddPageFields(fields, resp.Transcript)))
		}
	}
	if err != nil {
		c.mu.RLock()
		page := c.transcript
		hasPage := c.hasTranscript
		c.mu.RUnlock()
		if !hasPage && page.SessionID == "" {
			page.SessionID = c.sessionID
		}
		return page, err
	}
	return c.storeTranscript(resp.Transcript), nil
}

var runtimeClientTranscriptDiagLogf = func(format string, args ...any) {}

func logRuntimeClientTranscriptDiag(line string) {
	runtimeClientTranscriptDiagLogf("%s", strings.TrimSpace(line))
}

func normalizeRuntimeTranscriptRequest(req clientui.TranscriptPageRequest) clientui.TranscriptPageRequest {
	if req == (clientui.TranscriptPageRequest{}) {
		return clientui.TranscriptPageRequest{Window: clientui.TranscriptWindowOngoingTail}
	}
	return req
}

func (c *sessionRuntimeClient) refreshTranscriptAsync() {
	c.mu.Lock()
	if c.transcriptRefreshInFlight {
		c.mu.Unlock()
		return
	}
	c.transcriptRefreshInFlight = true
	c.mu.Unlock()
	go func() {
		defer func() {
			c.mu.Lock()
			c.transcriptRefreshInFlight = false
			c.mu.Unlock()
		}()
		_, _ = c.refreshTranscriptSync(uiRuntimeHydrationReadTimeout)
	}()
}

func storeTranscriptFromSessionViewLocked(c *sessionRuntimeClient, view clientui.RuntimeSessionView) {
	page := transcriptPageFromSessionView(view)
	if page.SessionID == "" || (len(page.Entries) == 0 && view.Transcript.CommittedEntryCount > 0) {
		return
	}
	storeTranscriptLocked(c, page)
}

func storeTranscriptLocked(c *sessionRuntimeClient, page clientui.TranscriptPage) {
	if page.SessionID == "" {
		page.SessionID = c.sessionID
	}
	c.transcript = page
	c.hasTranscript = true
	c.lastTranscriptAt = time.Now()
	c.mainView.Session.Transcript = clientui.TranscriptMetadata{
		Revision:            page.Revision,
		CommittedEntryCount: page.TotalEntries,
	}
	if page.Offset == 0 && !page.HasMore {
		c.mainView.Session.Chat = clientui.ChatSnapshot{
			Entries:      cloneTranscriptEntries(page.Entries),
			Ongoing:      page.Ongoing,
			OngoingError: page.OngoingError,
		}
	}
}

func transcriptPageFromSessionView(view clientui.RuntimeSessionView) clientui.TranscriptPage {
	total := view.Transcript.CommittedEntryCount
	if total == 0 {
		total = len(view.Chat.Entries)
	}
	hasMore := total > len(view.Chat.Entries)
	nextOffset := 0
	if hasMore {
		nextOffset = len(view.Chat.Entries)
	}
	return clientui.TranscriptPage{
		SessionID:             view.SessionID,
		SessionName:           view.SessionName,
		ConversationFreshness: view.ConversationFreshness,
		Revision:              view.Transcript.Revision,
		TotalEntries:          total,
		Offset:                0,
		NextOffset:            nextOffset,
		HasMore:               hasMore,
		Entries:               cloneTranscriptEntries(view.Chat.Entries),
		Ongoing:               view.Chat.Ongoing,
		OngoingError:          view.Chat.OngoingError,
	}
}

func cloneTranscriptEntries(entries []clientui.ChatEntry) []clientui.ChatEntry {
	if len(entries) == 0 {
		return nil
	}
	cloned := make([]clientui.ChatEntry, 0, len(entries))
	for _, entry := range entries {
		copyEntry := entry
		if entry.ToolCall != nil {
			copyMeta := *entry.ToolCall
			if len(entry.ToolCall.Suggestions) > 0 {
				copyMeta.Suggestions = append([]string(nil), entry.ToolCall.Suggestions...)
			}
			if entry.ToolCall.RenderHint != nil {
				renderHint := *entry.ToolCall.RenderHint
				copyMeta.RenderHint = &renderHint
			}
			copyEntry.ToolCall = &copyMeta
		}
		cloned = append(cloned, copyEntry)
	}
	return cloned
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

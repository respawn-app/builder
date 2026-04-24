package app

import (
	"context"
	"errors"
	"strconv"
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
	"github.com/google/uuid"
)

const uiRuntimeControlTimeout = 3 * time.Second
const uiRuntimeReadTimeout = 300 * time.Millisecond
const uiRuntimeHydrationReadTimeout = 10 * time.Second
const uiRuntimeMainViewRefreshInterval = 250 * time.Millisecond
const uiRuntimeTranscriptPageCacheMaxEntries = 16
const runtimeLeaseRecoveryWarningText = "Connection was lost, re-acquiring a new lease."

type sessionRuntimeClient struct {
	reads                   client.SessionViewClient
	controls                client.RuntimeControlClient
	sessionID               string
	controllerLease         *controllerLeaseManager
	diagLogf                func(string)
	transcriptDiagnostics   bool
	connectionStateObserver func(error)
	leaseRecoveryWarning    func(string)

	mu                        sync.RWMutex
	mainView                  clientui.RuntimeMainView
	hasMainView               bool
	lastMainViewAt            time.Time
	transcript                clientui.TranscriptPage
	hasTranscript             bool
	lastTranscriptAt          time.Time
	transcriptPages           map[string]cachedTranscriptPage
	transcriptPagesClock      uint64
	refreshInFlight           bool
	transcriptRefreshInFlight bool
}

type cachedTranscriptPage struct {
	page       clientui.TranscriptPage
	loadedAt   time.Time
	hasContent bool
	lastUsed   uint64
}

func newRuntimeClient(sessionID string, reads client.SessionViewClient, controls client.RuntimeControlClient) clientui.RuntimeClient {
	return newUIRuntimeClientWithReads(sessionID, reads, controls)
}

func newUIRuntimeClientFromEngine(engine *runtime.Engine) clientui.RuntimeClient {
	if engine == nil {
		return nil
	}
	resolver := sessionview.NewStaticRuntimeResolver(engine)
	reads := client.NewLoopbackSessionViewClient(sessionview.NewService(nil, resolver, nil))
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
		sessionID:       sessionID,
		controllerLease: newControllerLeaseManager("local-ui-controller"),
		reads:           reads,
		controls:        controls,
		mainView:        clientui.RuntimeMainView{Session: clientui.RuntimeSessionView{SessionID: sessionID}},
		transcript:      clientui.TranscriptPage{SessionID: sessionID},
		transcriptPages: make(map[string]cachedTranscriptPage),
	}
}

func (c *sessionRuntimeClient) SetControllerLeaseManager(manager *controllerLeaseManager) {
	if c == nil || manager == nil {
		return
	}
	c.mu.Lock()
	c.controllerLease = manager
	c.mu.Unlock()
}

func (c *sessionRuntimeClient) SetControllerLeaseID(leaseID string) {
	if c == nil {
		return
	}
	if manager := c.controllerLeaseManager(); manager != nil {
		manager.Set(leaseID)
	}
}

func (c *sessionRuntimeClient) controllerLeaseIDValue() string {
	if manager := c.controllerLeaseManager(); manager != nil {
		return manager.Value()
	}
	return ""
}

func (c *sessionRuntimeClient) controllerLeaseManager() *controllerLeaseManager {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.controllerLease
}

func (c *sessionRuntimeClient) recoverControllerLease(ctx context.Context) error {
	manager := c.controllerLeaseManager()
	if manager == nil {
		return errControllerLeaseRecoveryUnavailable
	}
	leaseID, err := manager.Recover(ctx)
	if err != nil {
		return err
	}
	c.appendLeaseRecoveryWarning(leaseID)
	return nil
}

func (c *sessionRuntimeClient) appendLeaseRecoveryWarning(controllerLeaseID string) {
	if c == nil || c.controls == nil {
		return
	}
	warningCtx, cancel := c.controlContext()
	defer cancel()
	if err := c.controls.AppendLocalEntry(warningCtx, serverapi.RuntimeAppendLocalEntryRequest{
		ClientRequestID:   uuid.NewString(),
		SessionID:         c.sessionID,
		ControllerLeaseID: controllerLeaseID,
		Role:              "warning",
		Text:              runtimeLeaseRecoveryWarningText,
		Visibility:        string(clientui.EntryVisibilityAll),
	}); err != nil {
		c.notifyLeaseRecoveryWarning(runtimeLeaseRecoveryWarningText)
	}
}

func isRecoverableRuntimeControlError(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, serverapi.ErrInvalidControllerLease) || errors.Is(err, serverapi.ErrRuntimeUnavailable)
}

func (c *sessionRuntimeClient) retryControlCallNoResult(ctx context.Context, call func(controllerLeaseID string) error) error {
	_, err := retryRuntimeControlCall(ctx, c.controllerLeaseIDValue, c.recoverControllerLease, func(controllerLeaseID string) (struct{}, error) {
		return struct{}{}, call(controllerLeaseID)
	})
	return err
}

func retryRuntimeControlCall[T any](ctx context.Context, currentLeaseID func() string, recoverLease func(context.Context) error, call func(controllerLeaseID string) (T, error)) (T, error) {
	value, err := call(currentLeaseID())
	if !isRecoverableRuntimeControlError(err) {
		return value, err
	}
	var zero T
	if recoverErr := recoverLease(ctx); recoverErr != nil {
		return zero, recoverErr
	}
	return call(currentLeaseID())
}

func (c *sessionRuntimeClient) SetTranscriptDiagnosticLogger(logf func(string)) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.diagLogf = logf
}

func (c *sessionRuntimeClient) SetTranscriptDiagnosticsEnabled(enabled bool) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.transcriptDiagnostics = enabled
	if enabled {
		return
	}
}

func (c *sessionRuntimeClient) SetConnectionStateObserver(observer func(error)) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.connectionStateObserver = observer
}

func (c *sessionRuntimeClient) SetLeaseRecoveryWarningObserver(observer func(string)) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.leaseRecoveryWarning = observer
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
	page, hasPage, stale := c.cachedTranscript(clientui.TranscriptPageRequest{Window: clientui.TranscriptWindowOngoingTail})
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
	return c.refreshTranscriptPageSync(clientui.TranscriptPageRequest{Window: clientui.TranscriptWindowOngoingTail}, uiRuntimeHydrationReadTimeout)
}

func (c *sessionRuntimeClient) RefreshTranscriptPage(req clientui.TranscriptPageRequest) (clientui.TranscriptPage, error) {
	return c.refreshTranscriptPageSync(req, uiRuntimeHydrationReadTimeout)
}

func (c *sessionRuntimeClient) LoadTranscriptPage(req clientui.TranscriptPageRequest) (clientui.TranscriptPage, error) {
	req = normalizeRuntimeTranscriptRequest(req)
	if page, hasPage, stale := c.cachedTranscriptPage(req); hasPage && !stale {
		return page, nil
	}
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

func (c *sessionRuntimeClient) cachedTranscript(req clientui.TranscriptPageRequest) (clientui.TranscriptPage, bool, bool) {
	req = normalizeRuntimeTranscriptRequest(req)
	key := transcriptRequestCacheKey(req)
	c.mu.Lock()
	entry, hasEntry := c.transcriptPages[key]
	if hasEntry && entry.hasContent {
		entry.lastUsed = c.nextTranscriptPageCacheStampLocked()
		c.transcriptPages[key] = entry
	}
	page := c.transcript
	hasPage := c.hasTranscript
	lastTranscriptAt := c.lastTranscriptAt
	c.mu.Unlock()
	if hasEntry && entry.hasContent {
		return entry.page, true, time.Since(entry.loadedAt) >= uiRuntimeMainViewRefreshInterval
	}
	if !hasPage {
		return page, false, true
	}
	return page, hasPage, time.Since(lastTranscriptAt) >= uiRuntimeMainViewRefreshInterval
}

func (c *sessionRuntimeClient) cachedTranscriptPage(req clientui.TranscriptPageRequest) (clientui.TranscriptPage, bool, bool) {
	req = normalizeRuntimeTranscriptRequest(req)
	key := transcriptRequestCacheKey(req)
	c.mu.Lock()
	entry, hasEntry := c.transcriptPages[key]
	if hasEntry && entry.hasContent {
		entry.lastUsed = c.nextTranscriptPageCacheStampLocked()
		c.transcriptPages[key] = entry
	}
	c.mu.Unlock()
	if !hasEntry || !entry.hasContent {
		return clientui.TranscriptPage{SessionID: c.sessionID}, false, true
	}
	return entry.page, true, time.Since(entry.loadedAt) >= uiRuntimeMainViewRefreshInterval
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
	return c.storeTranscriptForRequest(clientui.TranscriptPageRequest{Window: clientui.TranscriptWindowOngoingTail}, page)
}

func (c *sessionRuntimeClient) storeTranscriptForRequest(req clientui.TranscriptPageRequest, page clientui.TranscriptPage) clientui.TranscriptPage {
	if page.SessionID == "" {
		page.SessionID = c.sessionID
	}
	c.mu.Lock()
	storeTranscriptLocked(c, req, page)
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
	c.notifyConnectionState(err)
	if err != nil {
		c.mu.Lock()
		view := c.mainView
		if view.Session.SessionID == "" {
			view.Session.SessionID = c.sessionID
		}
		c.mainView = view
		c.hasMainView = true
		c.lastMainViewAt = time.Now()
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
		SessionID:                c.sessionID,
		Offset:                   req.Offset,
		Limit:                    req.Limit,
		Page:                     req.Page,
		PageSize:                 req.PageSize,
		Window:                   req.Window,
		KnownRevision:            req.KnownRevision,
		KnownCommittedEntryCount: req.KnownCommittedEntryCount,
	})
	c.notifyConnectionState(err)
	if c.transcriptDiagnosticsEnabled() {
		fields := map[string]string{"session_id": c.sessionID, "path": "hydrate"}
		for key, value := range transcriptdiag.RequestFields(req) {
			fields[key] = value
		}
		if err != nil {
			fields["err"] = err.Error()
			c.logTranscriptDiag(transcriptdiag.FormatLine("transcript.diag.client.hydrate_fetch", fields))
		} else {
			c.logTranscriptDiag(transcriptdiag.FormatLine("transcript.diag.client.hydrate_fetch", transcriptdiag.AddPageFields(fields, resp.Transcript)))
		}
	}
	if err != nil {
		page, hasPage, _ := c.cachedTranscript(req)
		if !hasPage && page.SessionID == "" {
			page.SessionID = c.sessionID
		}
		return page, err
	}
	return c.storeTranscriptForRequest(req, resp.Transcript), nil
}

func (c *sessionRuntimeClient) transcriptDiagnosticsEnabled() bool {
	if c == nil {
		return false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.transcriptDiagnostics || transcriptdiag.EnabledForProcess(false)
}

func (c *sessionRuntimeClient) notifyConnectionState(err error) {
	if c == nil {
		return
	}
	c.mu.RLock()
	observer := c.connectionStateObserver
	c.mu.RUnlock()
	if observer == nil {
		return
	}
	observer(err)
}

func (c *sessionRuntimeClient) notifyLeaseRecoveryWarning(text string) {
	if c == nil || strings.TrimSpace(text) == "" {
		return
	}
	c.mu.RLock()
	observer := c.leaseRecoveryWarning
	c.mu.RUnlock()
	if observer == nil {
		return
	}
	observer(text)
}

func (c *sessionRuntimeClient) logTranscriptDiag(line string) {
	if c == nil {
		return
	}
	c.mu.RLock()
	logf := c.diagLogf
	c.mu.RUnlock()
	if logf == nil {
		return
	}
	logf(strings.TrimSpace(line))
}

func normalizeRuntimeTranscriptRequest(req clientui.TranscriptPageRequest) clientui.TranscriptPageRequest {
	if req == (clientui.TranscriptPageRequest{}) {
		return clientui.TranscriptPageRequest{Window: clientui.TranscriptWindowOngoingTail}
	}
	return req
}

func transcriptRequestCacheKey(req clientui.TranscriptPageRequest) string {
	req = normalizeRuntimeTranscriptRequest(req)
	return strings.Join([]string{
		strconv.Itoa(req.Offset),
		strconv.Itoa(req.Limit),
		strconv.Itoa(req.Page),
		strconv.Itoa(req.PageSize),
		string(req.Window),
	}, ":")
}

func ongoingTailTranscriptRequest() clientui.TranscriptPageRequest {
	return clientui.TranscriptPageRequest{Window: clientui.TranscriptWindowOngoingTail}
}

func ongoingTailTranscriptCacheKey() string {
	return transcriptRequestCacheKey(ongoingTailTranscriptRequest())
}

func (c *sessionRuntimeClient) nextTranscriptPageCacheStampLocked() uint64 {
	c.transcriptPagesClock++
	return c.transcriptPagesClock
}

func (c *sessionRuntimeClient) evictTranscriptPageCacheLocked() {
	if c == nil || len(c.transcriptPages) <= uiRuntimeTranscriptPageCacheMaxEntries {
		return
	}
	defaultKey := ongoingTailTranscriptCacheKey()
	oldestKey := ""
	oldestStamp := uint64(0)
	for key, entry := range c.transcriptPages {
		if key == defaultKey {
			continue
		}
		if oldestKey == "" || entry.lastUsed < oldestStamp {
			oldestKey = key
			oldestStamp = entry.lastUsed
		}
	}
	if oldestKey == "" {
		for key, entry := range c.transcriptPages {
			if oldestKey == "" || entry.lastUsed < oldestStamp {
				oldestKey = key
				oldestStamp = entry.lastUsed
			}
		}
	}
	if oldestKey != "" {
		delete(c.transcriptPages, oldestKey)
	}
	if len(c.transcriptPages) > uiRuntimeTranscriptPageCacheMaxEntries {
		for key := range c.transcriptPages {
			if key == defaultKey {
				continue
			}
			delete(c.transcriptPages, key)
			if len(c.transcriptPages) <= uiRuntimeTranscriptPageCacheMaxEntries {
				break
			}
		}
	}
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
	if c == nil || c.hasTranscript {
		// SessionView.Chat is a bootstrap snapshot. Once the ongoing-tail cache has
		// been populated from a real transcript page, never let main-view refreshes
		// downgrade it back to the weaker summary snapshot.
		return
	}
	page := transcriptPageFromSessionView(view)
	if page.SessionID == "" || (len(page.Entries) == 0 && view.Transcript.CommittedEntryCount > 0) {
		return
	}
	storeTranscriptLocked(c, ongoingTailTranscriptRequest(), page)
}

func storeTranscriptLocked(c *sessionRuntimeClient, req clientui.TranscriptPageRequest, page clientui.TranscriptPage) {
	if page.SessionID == "" {
		page.SessionID = c.sessionID
	}
	req = normalizeRuntimeTranscriptRequest(req)
	if c.transcriptPages == nil {
		c.transcriptPages = make(map[string]cachedTranscriptPage)
	}
	now := time.Now()
	c.transcriptPages[transcriptRequestCacheKey(req)] = cachedTranscriptPage{page: page, loadedAt: now, hasContent: true, lastUsed: c.nextTranscriptPageCacheStampLocked()}
	c.evictTranscriptPageCacheLocked()
	c.mainView.Session.Transcript = clientui.TranscriptMetadata{
		Revision:            page.Revision,
		CommittedEntryCount: page.TotalEntries,
	}
	if req.Window != clientui.TranscriptWindowOngoingTail {
		return
	}
	c.transcript = page
	c.hasTranscript = true
	c.lastTranscriptAt = now
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
	if err := c.retryControlCallNoResult(ctx, func(controllerLeaseID string) error {
		return c.controls.SetSessionName(ctx, serverapi.RuntimeSetSessionNameRequest{ClientRequestID: uuid.NewString(), SessionID: c.sessionID, ControllerLeaseID: controllerLeaseID, Name: name})
	}); err != nil {
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
	if err := c.retryControlCallNoResult(ctx, func(controllerLeaseID string) error {
		return c.controls.SetThinkingLevel(ctx, serverapi.RuntimeSetThinkingLevelRequest{ClientRequestID: uuid.NewString(), SessionID: c.sessionID, ControllerLeaseID: controllerLeaseID, Level: level})
	}); err != nil {
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
	resp, err := retryRuntimeControlCall(ctx, c.controllerLeaseIDValue, c.recoverControllerLease, func(controllerLeaseID string) (serverapi.RuntimeSetFastModeEnabledResponse, error) {
		return c.controls.SetFastModeEnabled(ctx, serverapi.RuntimeSetFastModeEnabledRequest{ClientRequestID: uuid.NewString(), SessionID: c.sessionID, ControllerLeaseID: controllerLeaseID, Enabled: enabled})
	})
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
	resp, err := retryRuntimeControlCall(ctx, c.controllerLeaseIDValue, c.recoverControllerLease, func(controllerLeaseID string) (serverapi.RuntimeSetReviewerEnabledResponse, error) {
		return c.controls.SetReviewerEnabled(ctx, serverapi.RuntimeSetReviewerEnabledRequest{ClientRequestID: uuid.NewString(), SessionID: c.sessionID, ControllerLeaseID: controllerLeaseID, Enabled: enabled})
	})
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
	resp, err := retryRuntimeControlCall(ctx, c.controllerLeaseIDValue, c.recoverControllerLease, func(controllerLeaseID string) (serverapi.RuntimeSetAutoCompactionEnabledResponse, error) {
		return c.controls.SetAutoCompactionEnabled(ctx, serverapi.RuntimeSetAutoCompactionEnabledRequest{ClientRequestID: uuid.NewString(), SessionID: c.sessionID, ControllerLeaseID: controllerLeaseID, Enabled: enabled})
	})
	if err != nil {
		return false, false, err
	}
	c.patchMainView(func(view *clientui.RuntimeMainView) {
		view.Status.AutoCompactionEnabled = resp.Enabled
	})
	return resp.Changed, resp.Enabled, nil
}
func (c *sessionRuntimeClient) AppendLocalEntry(role, text string) error {
	ctx, cancel := c.controlContext()
	defer cancel()
	return c.retryControlCallNoResult(ctx, func(controllerLeaseID string) error {
		return c.controls.AppendLocalEntry(ctx, serverapi.RuntimeAppendLocalEntryRequest{ClientRequestID: uuid.NewString(), SessionID: c.sessionID, ControllerLeaseID: controllerLeaseID, Role: role, Text: text})
	})
}
func (c *sessionRuntimeClient) ShouldCompactBeforeUserMessage(ctx context.Context, text string) (bool, error) {
	resp, err := c.controls.ShouldCompactBeforeUserMessage(ctx, serverapi.RuntimeShouldCompactBeforeUserMessageRequest{SessionID: c.sessionID, Text: text})
	return resp.ShouldCompact, err
}
func (c *sessionRuntimeClient) SubmitUserMessage(ctx context.Context, text string) (string, error) {
	resp, err := retryRuntimeControlCall(ctx, c.controllerLeaseIDValue, c.recoverControllerLease, func(controllerLeaseID string) (serverapi.RuntimeSubmitUserMessageResponse, error) {
		return c.controls.SubmitUserMessage(ctx, serverapi.RuntimeSubmitUserMessageRequest{ClientRequestID: uuid.NewString(), SessionID: c.sessionID, ControllerLeaseID: controllerLeaseID, Text: text})
	})
	return resp.Message, err
}
func (c *sessionRuntimeClient) SubmitUserShellCommand(ctx context.Context, command string) error {
	return c.retryControlCallNoResult(ctx, func(controllerLeaseID string) error {
		return c.controls.SubmitUserShellCommand(ctx, serverapi.RuntimeSubmitUserShellCommandRequest{ClientRequestID: uuid.NewString(), SessionID: c.sessionID, ControllerLeaseID: controllerLeaseID, Command: command})
	})
}
func (c *sessionRuntimeClient) CompactContext(ctx context.Context, args string) error {
	return c.retryControlCallNoResult(ctx, func(controllerLeaseID string) error {
		return c.controls.CompactContext(ctx, serverapi.RuntimeCompactContextRequest{ClientRequestID: uuid.NewString(), SessionID: c.sessionID, ControllerLeaseID: controllerLeaseID, Args: args})
	})
}
func (c *sessionRuntimeClient) CompactContextForPreSubmit(ctx context.Context) error {
	return c.retryControlCallNoResult(ctx, func(controllerLeaseID string) error {
		return c.controls.CompactContextForPreSubmit(ctx, serverapi.RuntimeCompactContextForPreSubmitRequest{ClientRequestID: uuid.NewString(), SessionID: c.sessionID, ControllerLeaseID: controllerLeaseID})
	})
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
	resp, err := retryRuntimeControlCall(ctx, c.controllerLeaseIDValue, c.recoverControllerLease, func(controllerLeaseID string) (serverapi.RuntimeSubmitQueuedUserMessagesResponse, error) {
		return c.controls.SubmitQueuedUserMessages(ctx, serverapi.RuntimeSubmitQueuedUserMessagesRequest{ClientRequestID: uuid.NewString(), SessionID: c.sessionID, ControllerLeaseID: controllerLeaseID})
	})
	return resp.Message, err
}
func (c *sessionRuntimeClient) Interrupt() error {
	ctx, cancel := c.controlContext()
	defer cancel()
	return c.retryControlCallNoResult(ctx, func(controllerLeaseID string) error {
		return c.controls.Interrupt(ctx, serverapi.RuntimeInterruptRequest{ClientRequestID: uuid.NewString(), SessionID: c.sessionID, ControllerLeaseID: controllerLeaseID})
	})
}

func (c *sessionRuntimeClient) QueueUserMessage(text string) {
	ctx, cancel := c.controlContext()
	defer cancel()
	if err := c.retryControlCallNoResult(ctx, func(controllerLeaseID string) error {
		return c.controls.QueueUserMessage(ctx, serverapi.RuntimeQueueUserMessageRequest{ClientRequestID: uuid.NewString(), SessionID: c.sessionID, ControllerLeaseID: controllerLeaseID, Text: text})
	}); err != nil {
		c.notifyConnectionState(err)
	}
}

func (c *sessionRuntimeClient) DiscardQueuedUserMessagesMatching(text string) int {
	ctx, cancel := c.controlContext()
	defer cancel()
	resp, err := retryRuntimeControlCall(ctx, c.controllerLeaseIDValue, c.recoverControllerLease, func(controllerLeaseID string) (serverapi.RuntimeDiscardQueuedUserMessagesMatchingResponse, error) {
		return c.controls.DiscardQueuedUserMessagesMatching(ctx, serverapi.RuntimeDiscardQueuedUserMessagesMatchingRequest{ClientRequestID: uuid.NewString(), SessionID: c.sessionID, ControllerLeaseID: controllerLeaseID, Text: text})
	})
	if err != nil {
		return 0
	}
	return resp.Discarded
}

func (c *sessionRuntimeClient) RecordPromptHistory(text string) error {
	ctx, cancel := c.controlContext()
	defer cancel()
	return c.retryControlCallNoResult(ctx, func(controllerLeaseID string) error {
		return c.controls.RecordPromptHistory(ctx, serverapi.RuntimeRecordPromptHistoryRequest{ClientRequestID: uuid.NewString(), SessionID: c.sessionID, ControllerLeaseID: controllerLeaseID, Text: text})
	})
}

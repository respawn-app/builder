package app

import (
	"context"
	"encoding/json"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"builder/server/llm"
	"builder/server/registry"
	"builder/server/runtime"
	"builder/server/runtimecontrol"
	"builder/server/session"
	"builder/server/sessionview"
	"builder/server/tools"
	sharedclient "builder/shared/client"
	"builder/shared/clientui"
	"builder/shared/serverapi"
)

type countingSessionViewClient struct {
	view              clientui.RuntimeMainView
	page              clientui.TranscriptPage
	pageForRequest    func(serverapi.SessionTranscriptPageRequest) clientui.TranscriptPage
	count             atomic.Int32
	lastTranscriptReq serverapi.SessionTranscriptPageRequest
}

func (c *countingSessionViewClient) GetSessionMainView(context.Context, serverapi.SessionMainViewRequest) (serverapi.SessionMainViewResponse, error) {
	c.count.Add(1)
	return serverapi.SessionMainViewResponse{MainView: c.view}, nil
}

func (c *countingSessionViewClient) GetSessionTranscriptPage(ctx context.Context, req serverapi.SessionTranscriptPageRequest) (serverapi.SessionTranscriptPageResponse, error) {
	_ = ctx
	c.lastTranscriptReq = req
	c.count.Add(1)
	if c.pageForRequest != nil {
		return serverapi.SessionTranscriptPageResponse{Transcript: c.pageForRequest(req)}, nil
	}
	return serverapi.SessionTranscriptPageResponse{Transcript: c.page}, nil
}

func (*countingSessionViewClient) GetRun(context.Context, serverapi.RunGetRequest) (serverapi.RunGetResponse, error) {
	return serverapi.RunGetResponse{}, nil
}

type blockingSessionViewClient struct{}

func (blockingSessionViewClient) GetSessionMainView(ctx context.Context, _ serverapi.SessionMainViewRequest) (serverapi.SessionMainViewResponse, error) {
	<-ctx.Done()
	return serverapi.SessionMainViewResponse{}, ctx.Err()
}

func (blockingSessionViewClient) GetSessionTranscriptPage(ctx context.Context, _ serverapi.SessionTranscriptPageRequest) (serverapi.SessionTranscriptPageResponse, error) {
	<-ctx.Done()
	return serverapi.SessionTranscriptPageResponse{}, ctx.Err()
}

func (blockingSessionViewClient) GetRun(context.Context, serverapi.RunGetRequest) (serverapi.RunGetResponse, error) {
	return serverapi.RunGetResponse{}, nil
}

type blockingCountingSessionViewClient struct {
	count atomic.Int32
}

func (c *blockingCountingSessionViewClient) GetSessionMainView(ctx context.Context, _ serverapi.SessionMainViewRequest) (serverapi.SessionMainViewResponse, error) {
	c.count.Add(1)
	<-ctx.Done()
	return serverapi.SessionMainViewResponse{}, ctx.Err()
}

func (c *blockingCountingSessionViewClient) GetSessionTranscriptPage(ctx context.Context, _ serverapi.SessionTranscriptPageRequest) (serverapi.SessionTranscriptPageResponse, error) {
	<-ctx.Done()
	return serverapi.SessionTranscriptPageResponse{}, ctx.Err()
}

func (*blockingCountingSessionViewClient) GetRun(context.Context, serverapi.RunGetRequest) (serverapi.RunGetResponse, error) {
	return serverapi.RunGetResponse{}, nil
}

type flakySessionViewClient struct {
	mu        sync.Mutex
	responses []serverapi.SessionMainViewResponse
	pages     []serverapi.SessionTranscriptPageResponse
	errs      []error
	count     int
}

func (c *flakySessionViewClient) GetSessionMainView(context.Context, serverapi.SessionMainViewRequest) (serverapi.SessionMainViewResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	idx := c.count
	c.count++
	if idx < len(c.errs) && c.errs[idx] != nil {
		return serverapi.SessionMainViewResponse{}, c.errs[idx]
	}
	if idx < len(c.responses) {
		return c.responses[idx], nil
	}
	if len(c.responses) > 0 {
		return c.responses[len(c.responses)-1], nil
	}
	return serverapi.SessionMainViewResponse{}, nil
}

func (c *flakySessionViewClient) GetSessionTranscriptPage(context.Context, serverapi.SessionTranscriptPageRequest) (serverapi.SessionTranscriptPageResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	idx := c.count
	c.count++
	if idx < len(c.errs) && c.errs[idx] != nil {
		return serverapi.SessionTranscriptPageResponse{}, c.errs[idx]
	}
	if idx < len(c.pages) {
		return c.pages[idx], nil
	}
	if len(c.pages) > 0 {
		return c.pages[len(c.pages)-1], nil
	}
	return serverapi.SessionTranscriptPageResponse{}, nil
}

func (c *flakySessionViewClient) GetRun(context.Context, serverapi.RunGetRequest) (serverapi.RunGetResponse, error) {
	return serverapi.RunGetResponse{}, nil
}

type runtimeClientFakeLLM struct {
	mu        sync.Mutex
	responses []llm.Response
}

func (f *runtimeClientFakeLLM) Generate(_ context.Context, req llm.Request) (llm.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.responses) == 0 {
		return llm.Response{}, nil
	}
	resp := f.responses[0]
	f.responses = f.responses[1:]
	return resp, nil
}

func (f *runtimeClientFakeLLM) ProviderCapabilities(context.Context) (llm.ProviderCapabilities, error) {
	return llm.ProviderCapabilities{
		ProviderID:                    "openai",
		SupportsResponsesAPI:          true,
		SupportsResponsesCompact:      true,
		SupportsReasoningEncrypted:    true,
		SupportsServerSideContextEdit: true,
		IsOpenAIFirstParty:            true,
	}, nil
}

type runtimeClientBlockingTool struct {
	started chan struct{}
	release chan struct{}
}

func (runtimeClientBlockingTool) Name() tools.ID { return tools.ToolShell }

func (t runtimeClientBlockingTool) Call(_ context.Context, c tools.Call) (tools.Result, error) {
	select {
	case <-t.started:
	default:
		close(t.started)
	}
	<-t.release
	out, _ := json.Marshal(map[string]any{"ok": true})
	return tools.Result{CallID: c.ID, Name: c.Name, Output: out}, nil
}

func TestRuntimeClientRefreshTranscriptRequestsOngoingTail(t *testing.T) {
	reads := &countingSessionViewClient{page: clientui.TranscriptPage{SessionID: "session-1"}}
	controls := sharedclient.NewLoopbackRuntimeControlClient(runtimecontrol.NewService(nil, nil))
	runtimeClient := newUIRuntimeClientWithReads("session-1", reads, controls)

	if _, err := runtimeClient.RefreshTranscript(); err != nil {
		t.Fatalf("refresh transcript: %v", err)
	}
	if reads.lastTranscriptReq.Window != clientui.TranscriptWindowOngoingTail {
		t.Fatalf("window = %q, want ongoing tail", reads.lastTranscriptReq.Window)
	}
}

func TestRuntimeClientLoadTranscriptPageDefaultsToOngoingTail(t *testing.T) {
	reads := &countingSessionViewClient{page: clientui.TranscriptPage{SessionID: "session-1"}}
	controls := sharedclient.NewLoopbackRuntimeControlClient(runtimecontrol.NewService(nil, nil))
	runtimeClient := newUIRuntimeClientWithReads("session-1", reads, controls)

	if _, err := runtimeClient.LoadTranscriptPage(clientui.TranscriptPageRequest{}); err != nil {
		t.Fatalf("load transcript page: %v", err)
	}
	if reads.lastTranscriptReq.Window != clientui.TranscriptWindowOngoingTail {
		t.Fatalf("window = %q, want ongoing tail", reads.lastTranscriptReq.Window)
	}
}

func TestRuntimeClientLoadTranscriptPageReusesFreshCachedPageForSameRequest(t *testing.T) {
	reads := &countingSessionViewClient{page: clientui.TranscriptPage{SessionID: "session-1", Offset: 300, TotalEntries: 500}}
	controls := sharedclient.NewLoopbackRuntimeControlClient(runtimecontrol.NewService(nil, nil))
	runtimeClient := newUIRuntimeClientWithReads("session-1", reads, controls)
	req := clientui.TranscriptPageRequest{Offset: 300, Limit: 200}

	if _, err := runtimeClient.LoadTranscriptPage(req); err != nil {
		t.Fatalf("first load transcript page: %v", err)
	}
	if _, err := runtimeClient.LoadTranscriptPage(req); err != nil {
		t.Fatalf("second load transcript page: %v", err)
	}
	if got := reads.count.Load(); got != 1 {
		t.Fatalf("session view call count = %d, want 1", got)
	}
}

func TestRuntimeClientLoadTranscriptPageCachesByRequestKey(t *testing.T) {
	reads := &countingSessionViewClient{page: clientui.TranscriptPage{SessionID: "session-1", TotalEntries: 500}}
	controls := sharedclient.NewLoopbackRuntimeControlClient(runtimecontrol.NewService(nil, nil))
	runtimeClient := newUIRuntimeClientWithReads("session-1", reads, controls)

	if _, err := runtimeClient.LoadTranscriptPage(clientui.TranscriptPageRequest{Offset: 300, Limit: 200}); err != nil {
		t.Fatalf("first load transcript page: %v", err)
	}
	if _, err := runtimeClient.LoadTranscriptPage(clientui.TranscriptPageRequest{Offset: 0, Limit: 250}); err != nil {
		t.Fatalf("second load transcript page: %v", err)
	}
	if got := reads.count.Load(); got != 2 {
		t.Fatalf("session view call count = %d, want 2", got)
	}
}

func TestRuntimeClientRefreshTranscriptBypassesFreshCachedPage(t *testing.T) {
	reads := &countingSessionViewClient{page: clientui.TranscriptPage{SessionID: "session-1"}}
	controls := sharedclient.NewLoopbackRuntimeControlClient(runtimecontrol.NewService(nil, nil))
	runtimeClient := newUIRuntimeClientWithReads("session-1", reads, controls)

	if _, err := runtimeClient.LoadTranscriptPage(clientui.TranscriptPageRequest{}); err != nil {
		t.Fatalf("load transcript page: %v", err)
	}
	if _, err := runtimeClient.RefreshTranscript(); err != nil {
		t.Fatalf("refresh transcript: %v", err)
	}
	if got := reads.count.Load(); got != 2 {
		t.Fatalf("session view call count = %d, want 2", got)
	}
}

func TestRuntimeClientLoadTranscriptPageDoesNotReplaceCachedTailTranscript(t *testing.T) {
	reads := &countingSessionViewClient{
		pageForRequest: func(req serverapi.SessionTranscriptPageRequest) clientui.TranscriptPage {
			if req.Window == clientui.TranscriptWindowOngoingTail {
				return clientui.TranscriptPage{
					SessionID:    "session-1",
					Offset:       0,
					TotalEntries: 500,
					Entries:      []clientui.ChatEntry{{Role: "assistant", Text: "tail"}},
				}
			}
			return clientui.TranscriptPage{
				SessionID:    "session-1",
				Offset:       req.Offset,
				TotalEntries: 500,
				Entries:      []clientui.ChatEntry{{Role: "assistant", Text: "paged"}},
			}
		},
	}
	controls := sharedclient.NewLoopbackRuntimeControlClient(runtimecontrol.NewService(nil, nil))
	runtimeClient := newUIRuntimeClientWithReads("session-1", reads, controls)

	if _, err := runtimeClient.RefreshTranscript(); err != nil {
		t.Fatalf("refresh transcript: %v", err)
	}
	if _, err := runtimeClient.LoadTranscriptPage(clientui.TranscriptPageRequest{Offset: 300, Limit: 100}); err != nil {
		t.Fatalf("load transcript page: %v", err)
	}
	page := runtimeClient.Transcript()
	if page.Offset != 0 {
		t.Fatalf("tail transcript offset = %d, want 0", page.Offset)
	}
	if len(page.Entries) != 1 || page.Entries[0].Text != "tail" {
		t.Fatalf("tail transcript entries = %+v", page.Entries)
	}
	if got := reads.count.Load(); got != 2 {
		t.Fatalf("session view call count = %d, want 2", got)
	}
}

func TestRuntimeClientLoadTranscriptPageEvictsLeastRecentlyUsedRequests(t *testing.T) {
	reads := &countingSessionViewClient{page: clientui.TranscriptPage{SessionID: "session-1", TotalEntries: 5000}}
	controls := sharedclient.NewLoopbackRuntimeControlClient(runtimecontrol.NewService(nil, nil))
	runtimeClient := newUIRuntimeClientWithReads("session-1", reads, controls)

	for i := 0; i <= uiRuntimeTranscriptPageCacheMaxEntries; i++ {
		if _, err := runtimeClient.LoadTranscriptPage(clientui.TranscriptPageRequest{Offset: i * 10, Limit: 10}); err != nil {
			t.Fatalf("load transcript page %d: %v", i, err)
		}
	}
	if _, err := runtimeClient.LoadTranscriptPage(clientui.TranscriptPageRequest{Offset: 0, Limit: 10}); err != nil {
		t.Fatalf("reload evicted transcript page: %v", err)
	}
	if got, want := reads.count.Load(), int32(uiRuntimeTranscriptPageCacheMaxEntries+2); got != want {
		t.Fatalf("session view call count = %d, want %d", got, want)
	}
}

func TestRuntimeClientLoadTranscriptPageRetainsCachedTailEntryUnderEvictionPressure(t *testing.T) {
	reads := &countingSessionViewClient{
		pageForRequest: func(req serverapi.SessionTranscriptPageRequest) clientui.TranscriptPage {
			if req.Window == clientui.TranscriptWindowOngoingTail {
				return clientui.TranscriptPage{
					SessionID:    "session-1",
					Offset:       490,
					TotalEntries: 500,
					Entries:      []clientui.ChatEntry{{Role: "assistant", Text: "tail"}},
				}
			}
			return clientui.TranscriptPage{
				SessionID:    "session-1",
				Offset:       req.Offset,
				TotalEntries: 500,
				Entries:      []clientui.ChatEntry{{Role: "assistant", Text: "paged"}},
			}
		},
	}
	controls := sharedclient.NewLoopbackRuntimeControlClient(runtimecontrol.NewService(nil, nil))
	runtimeClient := newUIRuntimeClientWithReads("session-1", reads, controls)

	if _, err := runtimeClient.RefreshTranscript(); err != nil {
		t.Fatalf("refresh transcript: %v", err)
	}
	for i := 0; i <= uiRuntimeTranscriptPageCacheMaxEntries; i++ {
		if _, err := runtimeClient.LoadTranscriptPage(clientui.TranscriptPageRequest{Offset: i * 10, Limit: 10}); err != nil {
			t.Fatalf("load transcript page %d: %v", i, err)
		}
	}

	concrete, ok := runtimeClient.(*sessionRuntimeClient)
	if !ok {
		t.Fatalf("runtime client type = %T, want *sessionRuntimeClient", runtimeClient)
	}
	tailKey := ongoingTailTranscriptCacheKey()
	concrete.mu.RLock()
	_, hasTailKey := concrete.transcriptPages[tailKey]
	cacheSize := len(concrete.transcriptPages)
	concrete.mu.RUnlock()
	if !hasTailKey {
		t.Fatal("expected ongoing-tail cache entry to survive eviction pressure")
	}
	if cacheSize > uiRuntimeTranscriptPageCacheMaxEntries {
		t.Fatalf("cache size = %d, want <= %d", cacheSize, uiRuntimeTranscriptPageCacheMaxEntries)
	}

	page := runtimeClient.Transcript()
	if page.Offset != 490 {
		t.Fatalf("tail transcript offset = %d, want 490", page.Offset)
	}
	if len(page.Entries) != 1 || page.Entries[0].Text != "tail" {
		t.Fatalf("tail transcript entries = %+v", page.Entries)
	}
	if got, want := reads.count.Load(), int32(uiRuntimeTranscriptPageCacheMaxEntries+2); got != want {
		t.Fatalf("session view call count = %d, want %d", got, want)
	}
}

func TestRuntimeClientFromEngineSeedsCachedTranscriptTail(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	if _, err := store.AppendEvent("step-1", "message", llm.Message{Role: llm.RoleUser, Content: "u1"}); err != nil {
		t.Fatalf("append user message: %v", err)
	}
	if _, err := store.AppendEvent("step-1", "message", llm.Message{Role: llm.RoleAssistant, Content: "a1", Phase: llm.MessagePhaseFinal}); err != nil {
		t.Fatalf("append assistant message: %v", err)
	}
	eng, err := runtime.New(store, &runtimeClientFakeLLM{}, tools.NewRegistry(), runtime.Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	runtimeClient := newUIRuntimeClientFromEngine(eng)
	page := runtimeClient.Transcript()

	if got, want := page.TotalEntries, 2; got != want {
		t.Fatalf("total entries = %d, want %d", got, want)
	}
	if got, want := page.Offset, 0; got != want {
		t.Fatalf("offset = %d, want %d", got, want)
	}
	if got, want := len(page.Entries), 2; got != want {
		t.Fatalf("entry count = %d, want %d", got, want)
	}
	if page.Entries[1].Text != "a1" {
		t.Fatalf("expected cached transcript tail entry, got %+v", page.Entries)
	}
}

func TestRuntimeClientMainViewIncludesActiveRunFromRealEngine(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	fakeLLM := &runtimeClientFakeLLM{responses: []llm.Response{
		{
			Assistant: llm.Message{Role: llm.RoleAssistant, Content: "working", Phase: llm.MessagePhaseCommentary},
			ToolCalls: []llm.ToolCall{{ID: "call_shell_1", Name: string(tools.ToolShell), Input: json.RawMessage(`{"command":"pwd"}`)}},
			Usage:     llm.Usage{WindowTokens: 200000},
		},
		{
			Assistant: llm.Message{Role: llm.RoleAssistant, Content: "done", Phase: llm.MessagePhaseFinal},
			Usage:     llm.Usage{WindowTokens: 200000},
		},
	}}
	eng, err := runtime.New(store, fakeLLM, tools.NewRegistry(runtimeClientBlockingTool{started: started, release: release}), runtime.Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	runtimeRegistry := registry.NewRuntimeRegistry()
	runtimeRegistry.Register(store.Meta().SessionID, eng)

	runtimeClient := newRuntimeClient(
		store.Meta().SessionID,
		sharedclient.NewLoopbackSessionViewClient(sessionview.NewService(nil, runtimeRegistry, nil)),
		sharedclient.NewLoopbackRuntimeControlClient(runtimecontrol.NewService(runtimeRegistry, runtimeRegistry)),
	)
	result := make(chan error, 1)
	go func() {
		_, submitErr := eng.SubmitUserMessage(context.Background(), "run tools")
		result <- submitErr
	}()

	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for active run")
	}

	view := runtimeClient.MainView()
	if view.Session.SessionID != store.Meta().SessionID {
		t.Fatalf("session id = %q, want %q", view.Session.SessionID, store.Meta().SessionID)
	}
	if view.ActiveRun == nil {
		t.Fatal("expected active run in main view")
	}
	if view.ActiveRun.RunID == "" || view.ActiveRun.StepID == "" {
		t.Fatalf("expected run identifiers, got %+v", view.ActiveRun)
	}
	if view.ActiveRun.SessionID != store.Meta().SessionID {
		t.Fatalf("run session id = %q, want %q", view.ActiveRun.SessionID, store.Meta().SessionID)
	}
	if view.ActiveRun.Status != "running" || view.ActiveRun.StartedAt.IsZero() || !view.ActiveRun.FinishedAt.IsZero() {
		t.Fatalf("unexpected active run payload: %+v", view.ActiveRun)
	}

	close(release)
	if err := <-result; err != nil {
		t.Fatalf("submit user message: %v", err)
	}
}

func TestRuntimeClientMainViewFallsBackToLocalRuntimeProjectionOnReadError(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	if err := store.SetParentSessionID("parent-123"); err != nil {
		t.Fatalf("set parent session id: %v", err)
	}
	eng, err := runtime.New(store, &runtimeClientFakeLLM{}, tools.NewRegistry(), runtime.Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	if err := eng.SetThinkingLevel("high"); err != nil {
		t.Fatalf("set thinking level: %v", err)
	}
	runtimeRegistry := registry.NewRuntimeRegistry()
	runtimeRegistry.Register(store.Meta().SessionID, eng)

	runtimeClient := newUIRuntimeClientWithReads(
		store.Meta().SessionID,
		sharedclient.NewLoopbackSessionViewClient(sessionview.NewService(nil, runtimeRegistry, nil)),
		sharedclient.NewLoopbackRuntimeControlClient(runtimecontrol.NewService(runtimeRegistry, runtimeRegistry)),
	)
	view := runtimeClient.MainView()
	if view.Session.SessionID != store.Meta().SessionID {
		t.Fatalf("session id = %q, want %q", view.Session.SessionID, store.Meta().SessionID)
	}
	if view.Status.ParentSessionID != "parent-123" {
		t.Fatalf("parent session id = %q, want parent-123", view.Status.ParentSessionID)
	}
	if view.Status.ThinkingLevel != "high" {
		t.Fatalf("thinking level = %q, want high", view.Status.ThinkingLevel)
	}
}

func TestRuntimeClientMainViewLeavesTranscriptHydrationToTranscriptEndpoint(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	if _, err := store.AppendEvent("step-1", "message", llm.Message{Role: llm.RoleAssistant, Content: "seeded from main view", Phase: llm.MessagePhaseFinal}); err != nil {
		t.Fatalf("append assistant message: %v", err)
	}
	eng, err := runtime.New(store, &runtimeClientFakeLLM{}, tools.NewRegistry(), runtime.Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	runtimeRegistry := registry.NewRuntimeRegistry()
	runtimeRegistry.Register(store.Meta().SessionID, eng)

	runtimeClient := newUIRuntimeClientWithReads(
		store.Meta().SessionID,
		sharedclient.NewLoopbackSessionViewClient(sessionview.NewService(nil, runtimeRegistry, nil)),
		sharedclient.NewLoopbackRuntimeControlClient(runtimecontrol.NewService(runtimeRegistry, runtimeRegistry)),
	)
	view := runtimeClient.MainView()
	if got := len(view.Session.Chat.Entries); got != 0 {
		t.Fatalf("main view chat entry count = %d, want 0", got)
	}
	if page := runtimeClient.Transcript(); len(page.Entries) != 0 {
		t.Fatalf("expected transcript() to return uncached page before async hydration, got %+v", page)
	}

	if _, err := runtimeClient.RefreshTranscript(); err != nil {
		t.Fatalf("refresh transcript: %v", err)
	}
	page := runtimeClient.Transcript()
	if got := len(page.Entries); got != 1 {
		t.Fatalf("transcript entry count = %d, want 1", got)
	}
	if got := page.Entries[0].Text; got != "seeded from main view" {
		t.Fatalf("transcript entry text = %q, want seeded from main view", got)
	}
}

func TestRuntimeClientWithoutClientsIsNil(t *testing.T) {
	if client := newUIRuntimeClientWithReads("session-1", nil, nil); client != nil {
		t.Fatalf("expected nil runtime client, got %#v", client)
	}
	if client := newRuntimeClient("session-1", nil, nil); client != nil {
		t.Fatalf("expected nil runtime client, got %#v", client)
	}
	_ = clientui.RuntimeMainView{}
}

func TestRuntimeClientMainViewCachesSuccessfulRead(t *testing.T) {
	reads := &countingSessionViewClient{view: clientui.RuntimeMainView{Session: clientui.RuntimeSessionView{SessionID: "session-1"}, Status: clientui.RuntimeStatus{ThinkingLevel: "high"}}}
	runtimeClient := newUIRuntimeClientWithReads(
		"session-1",
		reads,
		sharedclient.NewLoopbackRuntimeControlClient(runtimecontrol.NewService(registry.NewRuntimeRegistry(), nil)),
	)

	first := runtimeClient.MainView()
	second := runtimeClient.MainView()
	third := runtimeClient.MainView()
	if first.Status.ThinkingLevel != "high" || second.Status.ThinkingLevel != "high" || third.Status.ThinkingLevel != "high" {
		t.Fatalf("expected cached main view to preserve projected status, got %+v / %+v / %+v", first, second, third)
	}
	if got := reads.count.Load(); got != 1 {
		t.Fatalf("main view read count = %d, want 1", got)
	}
}

func TestRuntimeClientRefreshMainViewBypassesCache(t *testing.T) {
	reads := &countingSessionViewClient{view: clientui.RuntimeMainView{Session: clientui.RuntimeSessionView{SessionID: "session-1"}, Status: clientui.RuntimeStatus{ThinkingLevel: "high"}}}
	runtimeClient := newUIRuntimeClientWithReads(
		"session-1",
		reads,
		sharedclient.NewLoopbackRuntimeControlClient(runtimecontrol.NewService(registry.NewRuntimeRegistry(), nil)),
	)
	if _, err := runtimeClient.RefreshMainView(); err != nil {
		t.Fatalf("RefreshMainView: %v", err)
	}
	reads.view.Status.ThinkingLevel = "low"
	refreshed, err := runtimeClient.RefreshMainView()
	if err != nil {
		t.Fatalf("RefreshMainView second call: %v", err)
	}
	if refreshed.Status.ThinkingLevel != "low" {
		t.Fatalf("expected refreshed main view to bypass cache, got %+v", refreshed)
	}
	if got := reads.count.Load(); got != 2 {
		t.Fatalf("refresh main view read count = %d, want 2", got)
	}
}

func TestRuntimeClientMainViewSeedsTranscriptCacheBeforeTranscriptFetch(t *testing.T) {
	reads := &countingSessionViewClient{view: clientui.RuntimeMainView{Session: clientui.RuntimeSessionView{
		SessionID: "session-1",
		Transcript: clientui.TranscriptMetadata{
			Revision:            3,
			CommittedEntryCount: 1,
		},
		Chat: clientui.ChatSnapshot{
			Entries: []clientui.ChatEntry{{Role: "assistant", Text: "seed"}},
		},
	}}}
	runtimeClient := newUIRuntimeClientWithReads(
		"session-1",
		reads,
		sharedclient.NewLoopbackRuntimeControlClient(runtimecontrol.NewService(registry.NewRuntimeRegistry(), nil)),
	)

	view := runtimeClient.MainView()
	if view.Session.SessionID != "session-1" {
		t.Fatalf("session id = %q, want session-1", view.Session.SessionID)
	}
	page := runtimeClient.Transcript()
	if got := len(page.Entries); got != 1 {
		t.Fatalf("transcript entry count = %d, want 1", got)
	}
	if got := page.Entries[0].Text; got != "seed" {
		t.Fatalf("transcript entry text = %q, want seed", got)
	}
	if got := reads.count.Load(); got != 1 {
		t.Fatalf("session view call count = %d, want 1", got)
	}
}

func TestRuntimeClientMainViewBootstrapDoesNotSeedStreamingOngoingState(t *testing.T) {
	reads := &countingSessionViewClient{view: clientui.RuntimeMainView{Session: clientui.RuntimeSessionView{
		SessionID: "session-1",
		Transcript: clientui.TranscriptMetadata{
			Revision:            3,
			CommittedEntryCount: 1,
		},
		Chat: clientui.ChatSnapshot{
			Entries: []clientui.ChatEntry{{Role: "assistant", Text: "seed"}},
			Ongoing: "NO_OP",
		},
	}}}
	runtimeClient := newUIRuntimeClientWithReads(
		"session-1",
		reads,
		sharedclient.NewLoopbackRuntimeControlClient(runtimecontrol.NewService(registry.NewRuntimeRegistry(), nil)),
	)

	_ = runtimeClient.MainView()
	page := runtimeClient.Transcript()
	if got := page.Ongoing; got != "" {
		t.Fatalf("bootstrap ongoing text = %q, want empty", got)
	}
}

func TestRuntimeClientRefreshMainViewDoesNotDowngradeCachedTranscriptTail(t *testing.T) {
	reads := &countingSessionViewClient{
		view: clientui.RuntimeMainView{Session: clientui.RuntimeSessionView{
			SessionID: "session-1",
			Transcript: clientui.TranscriptMetadata{
				Revision:            3,
				CommittedEntryCount: 2,
			},
			Chat: clientui.ChatSnapshot{
				Entries: []clientui.ChatEntry{{Role: "assistant", Text: "seed"}},
			},
		}},
		page: clientui.TranscriptPage{
			SessionID:    "session-1",
			Revision:     3,
			TotalEntries: 2,
			Entries: []clientui.ChatEntry{
				{Role: "assistant", Text: "seed"},
				{Role: "reviewer_status", Text: "Supervisor ran and applied 2 suggestions."},
			},
		},
	}
	runtimeClient := newUIRuntimeClientWithReads(
		"session-1",
		reads,
		sharedclient.NewLoopbackRuntimeControlClient(runtimecontrol.NewService(registry.NewRuntimeRegistry(), nil)),
	)
	concrete, ok := runtimeClient.(*sessionRuntimeClient)
	if !ok {
		t.Fatalf("runtime client type = %T, want *sessionRuntimeClient", runtimeClient)
	}

	if _, err := runtimeClient.RefreshTranscript(); err != nil {
		t.Fatalf("RefreshTranscript: %v", err)
	}
	tailKey := ongoingTailTranscriptCacheKey()
	concrete.mu.RLock()
	seededTail, hasSeededTail := concrete.transcriptPages[tailKey]
	concrete.mu.RUnlock()
	if !hasSeededTail {
		t.Fatal("expected ongoing-tail cache entry after transcript refresh")
	}
	if _, err := runtimeClient.RefreshMainView(); err != nil {
		t.Fatalf("RefreshMainView: %v", err)
	}
	concrete.mu.RLock()
	refreshedTail, hasRefreshedTail := concrete.transcriptPages[tailKey]
	concrete.mu.RUnlock()
	if !hasRefreshedTail {
		t.Fatal("expected ongoing-tail cache entry retained after main-view refresh")
	}
	if len(refreshedTail.page.Entries) != len(seededTail.page.Entries) {
		t.Fatalf("cached ongoing-tail entry count = %d, want %d", len(refreshedTail.page.Entries), len(seededTail.page.Entries))
	}
	if refreshedTail.page.Entries[1].Role != seededTail.page.Entries[1].Role || refreshedTail.page.Entries[1].Text != seededTail.page.Entries[1].Text {
		t.Fatalf("cached ongoing-tail page downgraded after main-view refresh: before=%+v after=%+v", seededTail.page.Entries, refreshedTail.page.Entries)
	}

	page := runtimeClient.Transcript()
	if got := len(page.Entries); got != 2 {
		t.Fatalf("transcript entry count = %d, want 2", got)
	}
	if got := page.Entries[1].Role; got != "reviewer_status" {
		t.Fatalf("second transcript role = %q, want reviewer_status", got)
	}
	if got := page.Entries[1].Text; got != "Supervisor ran and applied 2 suggestions." {
		t.Fatalf("second transcript text = %q", got)
	}
	if got := reads.count.Load(); got != 2 {
		t.Fatalf("session view call count = %d, want 2", got)
	}
}

func TestRuntimeClientMainViewFailsFastWhenReadStalls(t *testing.T) {
	runtimeClient := newUIRuntimeClientWithReads(
		"session-1",
		blockingSessionViewClient{},
		sharedclient.NewLoopbackRuntimeControlClient(runtimecontrol.NewService(registry.NewRuntimeRegistry(), nil)),
	)
	start := time.Now()
	view := runtimeClient.MainView()
	elapsed := time.Since(start)
	if elapsed >= time.Second {
		t.Fatalf("expected stalled main-view read to fail fast, took %v", elapsed)
	}
	if view.Session.SessionID != "session-1" {
		t.Fatalf("expected fallback main view to preserve session id, got %+v", view)
	}
}

func TestRuntimeClientMainViewCachesFallbackAfterReadError(t *testing.T) {
	reads := &blockingCountingSessionViewClient{}
	runtimeClient := newUIRuntimeClientWithReads(
		"session-1",
		reads,
		sharedclient.NewLoopbackRuntimeControlClient(runtimecontrol.NewService(registry.NewRuntimeRegistry(), nil)),
	)

	first := runtimeClient.MainView()
	if first.Session.SessionID != "session-1" {
		t.Fatalf("fallback session id = %q, want session-1", first.Session.SessionID)
	}
	second := runtimeClient.MainView()
	if second.Session.SessionID != "session-1" {
		t.Fatalf("expected cached fallback session id preserved, got %+v", second)
	}
	if got := reads.count.Load(); got != 1 {
		t.Fatalf("main view read count after cached fallback = %d, want 1", got)
	}
}

func TestRuntimeClientRefreshTranscriptPagePreservesLastKnownPageOnReadError(t *testing.T) {
	reads := &countingSessionViewClient{}
	runtimeClient := newUIRuntimeClientWithReads(
		"session-1",
		reads,
		sharedclient.NewLoopbackRuntimeControlClient(runtimecontrol.NewService(registry.NewRuntimeRegistry(), nil)),
	)
	concrete, ok := runtimeClient.(*sessionRuntimeClient)
	if !ok {
		t.Fatalf("runtime client type = %T, want *sessionRuntimeClient", runtimeClient)
	}
	seedReq := clientui.TranscriptPageRequest{Page: 2, PageSize: 25}
	seedPage := clientui.TranscriptPage{
		SessionID:    "session-1",
		Revision:     7,
		Offset:       25,
		TotalEntries: 40,
		Entries:      []clientui.ChatEntry{{Role: "assistant", Text: "cached page"}},
	}
	concrete.storeTranscriptForRequest(seedReq, seedPage)

	var observedErr error
	concrete.SetConnectionStateObserver(func(err error) { observedErr = err })
	concrete.reads = &flakySessionViewClient{errs: []error{context.DeadlineExceeded}}

	page, err := concrete.refreshTranscriptPageSync(seedReq, time.Millisecond)
	if err != context.DeadlineExceeded {
		t.Fatalf("refresh transcript page error = %v, want %v", err, context.DeadlineExceeded)
	}
	if observedErr != context.DeadlineExceeded {
		t.Fatalf("observed connection state error = %v, want %v", observedErr, context.DeadlineExceeded)
	}
	if !reflect.DeepEqual(page, seedPage) {
		t.Fatalf("refresh transcript page fallback = %+v, want %+v", page, seedPage)
	}
}

func TestRuntimeClientAsyncMainViewRefreshNotifiesConnectionObserverOnRecovery(t *testing.T) {
	reads := &countingSessionViewClient{view: clientui.RuntimeMainView{Session: clientui.RuntimeSessionView{SessionID: "session-1"}}}
	controls := sharedclient.NewLoopbackRuntimeControlClient(runtimecontrol.NewService(registry.NewRuntimeRegistry(), nil))
	runtimeClient := newUIRuntimeClientWithReads("session-1", reads, controls).(*sessionRuntimeClient)
	runtimeClient.storeMainView(clientui.RuntimeMainView{Session: clientui.RuntimeSessionView{SessionID: "session-1"}})
	runtimeClient.mu.Lock()
	runtimeClient.lastMainViewAt = time.Now().Add(-2 * uiRuntimeMainViewRefreshInterval)
	runtimeClient.mu.Unlock()
	notified := make(chan error, 1)
	runtimeClient.SetConnectionStateObserver(func(err error) {
		notified <- err
	})

	_ = runtimeClient.MainView()

	select {
	case err := <-notified:
		if err != nil {
			t.Fatalf("expected recovery observer notification without error, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for async refresh observer notification")
	}
}

func TestRuntimeClientSetFastModeEnabledUpdatesCachedMainView(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	eng, err := runtime.New(store, &runtimeClientFakeLLM{}, tools.NewRegistry(), runtime.Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	runtimeRegistry := registry.NewRuntimeRegistry()
	runtimeRegistry.Register(store.Meta().SessionID, eng)
	runtimeClient := newRuntimeClient(
		store.Meta().SessionID,
		sharedclient.NewLoopbackSessionViewClient(sessionview.NewService(nil, runtimeRegistry, nil)),
		sharedclient.NewLoopbackRuntimeControlClient(runtimecontrol.NewService(runtimeRegistry, nil)),
	)
	if _, err := runtimeClient.SetFastModeEnabled(true); err != nil {
		t.Fatalf("SetFastModeEnabled: %v", err)
	}
	if !runtimeClient.MainView().Status.FastModeEnabled {
		t.Fatalf("expected cached main view to reflect fast-mode toggle")
	}
}

func TestRuntimeClientSetFastModeEnabledPreservesCachedMainViewOnError(t *testing.T) {
	reads := &countingSessionViewClient{view: clientui.RuntimeMainView{Session: clientui.RuntimeSessionView{SessionID: "session-1"}, Status: clientui.RuntimeStatus{FastModeEnabled: true}}}
	runtimeClient := newUIRuntimeClientWithReads("session-1", reads, sharedclient.NewLoopbackRuntimeControlClient(nil))
	if !runtimeClient.MainView().Status.FastModeEnabled {
		t.Fatal("expected initial cached main view")
	}
	if _, err := runtimeClient.SetFastModeEnabled(false); err == nil {
		t.Fatal("expected fast-mode toggle error")
	}
	if !runtimeClient.MainView().Status.FastModeEnabled {
		t.Fatal("expected failed fast-mode toggle to preserve cached main view")
	}
}

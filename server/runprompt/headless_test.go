package runprompt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"builder/server/auth"
	"builder/server/primaryrun"
	"builder/server/session"
	"builder/server/tools"
	"builder/shared/config"
	"builder/shared/serverapi"
)

type stubRunPromptService struct {
	mu       sync.Mutex
	calls    int
	run      func(context.Context, serverapi.RunPromptRequest, serverapi.RunPromptProgressSink) (serverapi.RunPromptResponse, error)
	callHook func()
}

func (s *stubRunPromptService) RunPrompt(ctx context.Context, req serverapi.RunPromptRequest, progress serverapi.RunPromptProgressSink) (serverapi.RunPromptResponse, error) {
	s.mu.Lock()
	s.calls++
	hook := s.callHook
	run := s.run
	s.mu.Unlock()
	if hook != nil {
		hook()
	}
	if run == nil {
		return serverapi.RunPromptResponse{}, nil
	}
	return run(ctx, req, progress)
}

func (s *stubRunPromptService) CallCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func resetRunPromptDedupeRegistry() {
	runPromptDedupeRegistry.mu.Lock()
	defer runPromptDedupeRegistry.mu.Unlock()
	runPromptDedupeRegistry.entries = map[string]*dedupeEntry{}
}

func runPromptDedupeEntryCount() int {
	runPromptDedupeRegistry.mu.Lock()
	defer runPromptDedupeRegistry.mu.Unlock()
	return len(runPromptDedupeRegistry.entries)
}

func TestDeduplicatingPromptServiceSharesInFlightAndCachedResult(t *testing.T) {
	resetRunPromptDedupeRegistry()
	t.Cleanup(resetRunPromptDedupeRegistry)

	release := make(chan struct{})
	started := make(chan struct{}, 1)
	inner := &stubRunPromptService{
		callHook: func() { started <- struct{}{} },
		run: func(_ context.Context, req serverapi.RunPromptRequest, _ serverapi.RunPromptProgressSink) (serverapi.RunPromptResponse, error) {
			<-release
			return serverapi.RunPromptResponse{SessionID: "session-1", Result: "echo:" + req.Prompt}, nil
		},
	}
	service := newDeduplicatingPromptService("scope-a", inner)
	req := serverapi.RunPromptRequest{ClientRequestID: "dup-1", SelectedSessionID: "session-1", Prompt: "hello"}

	type result struct {
		response serverapi.RunPromptResponse
		err      error
	}
	results := make(chan result, 2)
	go func() {
		response, err := service.RunPrompt(context.Background(), req, nil)
		results <- result{response: response, err: err}
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for first duplicate-suppressed run to start")
	}

	go func() {
		response, err := service.RunPrompt(context.Background(), req, nil)
		results <- result{response: response, err: err}
	}()

	close(release)
	first := <-results
	second := <-results
	if first.err != nil {
		t.Fatalf("first run error: %v", first.err)
	}
	if second.err != nil {
		t.Fatalf("second run error: %v", second.err)
	}
	if got := inner.CallCount(); got != 1 {
		t.Fatalf("inner call count = %d, want 1", got)
	}
	if first.response != second.response {
		t.Fatalf("duplicate responses differ: first=%+v second=%+v", first.response, second.response)
	}

	third, err := service.RunPrompt(context.Background(), req, nil)
	if err != nil {
		t.Fatalf("third cached run error: %v", err)
	}
	if got := inner.CallCount(); got != 1 {
		t.Fatalf("inner call count after cached run = %d, want 1", got)
	}
	if third != first.response {
		t.Fatalf("cached response = %+v, want %+v", third, first.response)
	}
}

func TestDeduplicatingPromptServiceRejectsPayloadMismatch(t *testing.T) {
	resetRunPromptDedupeRegistry()
	t.Cleanup(resetRunPromptDedupeRegistry)

	inner := &stubRunPromptService{
		run: func(_ context.Context, req serverapi.RunPromptRequest, _ serverapi.RunPromptProgressSink) (serverapi.RunPromptResponse, error) {
			return serverapi.RunPromptResponse{SessionID: req.SelectedSessionID, Result: req.Prompt}, nil
		},
	}
	service := newDeduplicatingPromptService("scope-b", inner)
	base := serverapi.RunPromptRequest{ClientRequestID: "dup-2", SelectedSessionID: "session-2", Prompt: "first"}

	if _, err := service.RunPrompt(context.Background(), base, nil); err != nil {
		t.Fatalf("first run error: %v", err)
	}

	_, err := service.RunPrompt(context.Background(), serverapi.RunPromptRequest{
		ClientRequestID:   base.ClientRequestID,
		SelectedSessionID: base.SelectedSessionID,
		Prompt:            "second",
	}, nil)
	if err == nil {
		t.Fatal("expected payload mismatch error")
	}
	if got := err.Error(); got != "client_request_id \"dup-2\" reused with different payload" {
		t.Fatalf("unexpected mismatch error: %v", err)
	}
	if got := inner.CallCount(); got != 1 {
		t.Fatalf("inner call count = %d, want 1", got)
	}
}

func TestDeduplicatingPromptServiceRejectsOverrideMismatch(t *testing.T) {
	resetRunPromptDedupeRegistry()
	t.Cleanup(resetRunPromptDedupeRegistry)

	inner := &stubRunPromptService{
		run: func(_ context.Context, req serverapi.RunPromptRequest, _ serverapi.RunPromptProgressSink) (serverapi.RunPromptResponse, error) {
			return serverapi.RunPromptResponse{SessionID: req.SelectedSessionID, Result: req.Prompt}, nil
		},
	}
	service := newDeduplicatingPromptService("scope-b-overrides", inner)
	base := serverapi.RunPromptRequest{
		ClientRequestID:   "dup-2-overrides",
		SelectedSessionID: "session-2",
		Prompt:            "first",
		Timeout:           5 * time.Second,
		Overrides: serverapi.RunPromptOverrides{
			Model:         "gpt-5.4",
			Tools:         "shell,patch",
			OpenAIBaseURL: "http://127.0.0.1:11434/v1",
		},
	}

	if _, err := service.RunPrompt(context.Background(), base, nil); err != nil {
		t.Fatalf("first run error: %v", err)
	}

	_, err := service.RunPrompt(context.Background(), serverapi.RunPromptRequest{
		ClientRequestID:   base.ClientRequestID,
		SelectedSessionID: base.SelectedSessionID,
		Prompt:            base.Prompt,
		Timeout:           6 * time.Second,
		Overrides: serverapi.RunPromptOverrides{
			Model:         "gpt-5.4-mini",
			Tools:         base.Overrides.Tools,
			OpenAIBaseURL: base.Overrides.OpenAIBaseURL,
		},
	}, nil)
	if err == nil {
		t.Fatal("expected payload mismatch error")
	}
	if got := err.Error(); got != "client_request_id \"dup-2-overrides\" reused with different payload" {
		t.Fatalf("unexpected mismatch error: %v", err)
	}
	if got := inner.CallCount(); got != 1 {
		t.Fatalf("inner call count = %d, want 1", got)
	}
}

func TestDeduplicatingPromptServiceDoesNotCacheCanceledErrors(t *testing.T) {
	resetRunPromptDedupeRegistry()
	t.Cleanup(resetRunPromptDedupeRegistry)

	inner := &stubRunPromptService{}
	inner.run = func(_ context.Context, req serverapi.RunPromptRequest, _ serverapi.RunPromptProgressSink) (serverapi.RunPromptResponse, error) {
		if inner.CallCount() == 1 {
			return serverapi.RunPromptResponse{}, context.Canceled
		}
		return serverapi.RunPromptResponse{SessionID: req.SelectedSessionID, Result: "ok"}, nil
	}
	service := newDeduplicatingPromptService("scope-c", inner)
	req := serverapi.RunPromptRequest{ClientRequestID: "dup-3", SelectedSessionID: "session-3", Prompt: "retry me"}

	_, err := service.RunPrompt(context.Background(), req, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("first run error = %v, want context canceled", err)
	}
	if got := inner.CallCount(); got != 1 {
		t.Fatalf("inner call count after canceled run = %d, want 1", got)
	}
	if got := runPromptDedupeEntryCount(); got != 0 {
		t.Fatalf("entry count after canceled run = %d, want 0", got)
	}

	response, err := service.RunPrompt(context.Background(), req, nil)
	if err != nil {
		t.Fatalf("retry run error: %v", err)
	}
	if got := inner.CallCount(); got != 2 {
		t.Fatalf("inner call count after retry = %d, want 2", got)
	}
	if response.Result != "ok" {
		t.Fatalf("retry response result = %q, want ok", response.Result)
	}
}

func TestDeduplicatingPromptServiceScopesClientRequestIDByWorkspace(t *testing.T) {
	resetRunPromptDedupeRegistry()
	t.Cleanup(resetRunPromptDedupeRegistry)

	serviceA := newDeduplicatingPromptService(runPromptDedupeScopeID(HeadlessBootstrap{
		Config:       config.App{PersistenceRoot: "/tmp/persistence", WorkspaceRoot: "/tmp/workspace-a"},
		ContainerDir: "/tmp/persistence/workspace-a",
	}), &stubRunPromptService{run: func(_ context.Context, _ serverapi.RunPromptRequest, _ serverapi.RunPromptProgressSink) (serverapi.RunPromptResponse, error) {
		return serverapi.RunPromptResponse{SessionID: "session-a", Result: "workspace-a"}, nil
	}})
	serviceB := newDeduplicatingPromptService(runPromptDedupeScopeID(HeadlessBootstrap{
		Config:       config.App{PersistenceRoot: "/tmp/persistence", WorkspaceRoot: "/tmp/workspace-b"},
		ContainerDir: "/tmp/persistence/workspace-b",
	}), &stubRunPromptService{run: func(_ context.Context, _ serverapi.RunPromptRequest, _ serverapi.RunPromptProgressSink) (serverapi.RunPromptResponse, error) {
		return serverapi.RunPromptResponse{SessionID: "session-b", Result: "workspace-b"}, nil
	}})

	req := serverapi.RunPromptRequest{ClientRequestID: "dup-same", Prompt: "hello"}
	responseA, err := serviceA.RunPrompt(context.Background(), req, nil)
	if err != nil {
		t.Fatalf("service A run error: %v", err)
	}
	responseB, err := serviceB.RunPrompt(context.Background(), req, nil)
	if err != nil {
		t.Fatalf("service B run error: %v", err)
	}
	if responseA.SessionID == responseB.SessionID {
		t.Fatalf("workspace-scoped dedupe collided: responseA=%+v responseB=%+v", responseA, responseB)
	}
	if responseA.Result != "workspace-a" {
		t.Fatalf("service A result = %q, want workspace-a", responseA.Result)
	}
	if responseB.Result != "workspace-b" {
		t.Fatalf("service B result = %q, want workspace-b", responseB.Result)
	}
}

func TestDeduplicatingPromptServiceEvictsExpiredCacheEntries(t *testing.T) {
	resetRunPromptDedupeRegistry()
	t.Cleanup(resetRunPromptDedupeRegistry)

	originalNow := runPromptDedupeNow
	now := time.Unix(1_700_000_000, 0)
	runPromptDedupeNow = func() time.Time { return now }
	t.Cleanup(func() { runPromptDedupeNow = originalNow })

	inner := &stubRunPromptService{}
	inner.run = func(_ context.Context, req serverapi.RunPromptRequest, _ serverapi.RunPromptProgressSink) (serverapi.RunPromptResponse, error) {
		return serverapi.RunPromptResponse{SessionID: req.SelectedSessionID, Result: fmt.Sprintf("call-%d", inner.CallCount())}, nil
	}
	service := newDeduplicatingPromptService("scope-ttl", inner)
	req := serverapi.RunPromptRequest{ClientRequestID: "dup-ttl", SelectedSessionID: "session-ttl", Prompt: "hello"}

	first, err := service.RunPrompt(context.Background(), req, nil)
	if err != nil {
		t.Fatalf("first run error: %v", err)
	}
	if got := runPromptDedupeEntryCount(); got != 1 {
		t.Fatalf("entry count after first run = %d, want 1", got)
	}

	now = now.Add(runPromptDedupeRetention + time.Second)
	second, err := service.RunPrompt(context.Background(), req, nil)
	if err != nil {
		t.Fatalf("second run error: %v", err)
	}
	if got := inner.CallCount(); got != 2 {
		t.Fatalf("inner call count after expired replay = %d, want 2", got)
	}
	if first.Result == second.Result {
		t.Fatalf("expired cache entry reused old response: first=%+v second=%+v", first, second)
	}
	if got := runPromptDedupeEntryCount(); got != 1 {
		t.Fatalf("entry count after eviction + rerun = %d, want 1", got)
	}
}

func TestGuardingPromptServiceRejectsConcurrentSelectedSessionRun(t *testing.T) {
	release := make(chan struct{})
	inner := &stubRunPromptService{run: func(_ context.Context, req serverapi.RunPromptRequest, _ serverapi.RunPromptProgressSink) (serverapi.RunPromptResponse, error) {
		<-release
		return serverapi.RunPromptResponse{SessionID: req.SelectedSessionID, Result: "ok"}, nil
	}}
	gate := newTestPrimaryRunGate()
	service := primaryrun.NewGuardingPromptService(gate, inner)

	firstDone := make(chan error, 1)
	go func() {
		_, err := service.RunPrompt(context.Background(), serverapi.RunPromptRequest{ClientRequestID: "req-1", SelectedSessionID: "session-1", Prompt: "hello"}, nil)
		firstDone <- err
	}()

	gate.waitForAcquire(t, 1)
	_, err := service.RunPrompt(context.Background(), serverapi.RunPromptRequest{ClientRequestID: "req-2", SelectedSessionID: "session-1", Prompt: "different"}, nil)
	if !errors.Is(err, primaryrun.ErrActivePrimaryRun) {
		t.Fatalf("second RunPrompt error = %v, want active primary run", err)
	}
	close(release)
	if err := <-firstDone; err != nil {
		t.Fatalf("first RunPrompt error: %v", err)
	}
}

func TestLoopbackRunPromptClientUsesSelectedSessionContinuationContext(t *testing.T) {
	resetRunPromptDedupeRegistry()
	t.Cleanup(resetRunPromptDedupeRegistry)

	root := t.TempDir()
	containerDir := filepath.Join(root, "sessions", "workspace-a")
	store, err := session.Create(containerDir, "workspace-a", "/tmp/workspace-a")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got == "" {
			t.Fatal("expected authorization header")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"total_tokens\":2},\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"phase\":\"final\",\"content\":[{\"type\":\"output_text\",\"text\":\"from persisted continuation\"}]}]}}\n\n")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
	}))
	defer server.Close()

	if err := store.SetContinuationContext(session.ContinuationContext{OpenAIBaseURL: server.URL}); err != nil {
		t.Fatalf("set continuation context: %v", err)
	}

	authManager := auth.NewManager(auth.NewMemoryStore(auth.State{
		Method: auth.Method{Type: auth.MethodAPIKey, APIKey: &auth.APIKeyMethod{Key: "test-key"}},
	}), nil, time.Now)

	client := NewLoopbackRunPromptClient(HeadlessBootstrap{
		Config: config.App{
			WorkspaceRoot:   "/tmp/workspace-a",
			PersistenceRoot: root,
			Settings: config.Settings{
				Model:         "gpt-5",
				OpenAIBaseURL: "http://wrong.invalid",
			},
		},
		ContainerDir: containerDir,
		AuthManager:  authManager,
	})

	response, err := client.RunPrompt(context.Background(), serverapi.RunPromptRequest{
		ClientRequestID:   "continuation-direct-1",
		SelectedSessionID: store.Meta().SessionID,
		Prompt:            "hello",
	}, nil)
	if err != nil {
		t.Fatalf("RunPrompt: %v", err)
	}
	if response.SessionID != store.Meta().SessionID {
		t.Fatalf("session id = %q, want %q", response.SessionID, store.Meta().SessionID)
	}
	if response.Result != "from persisted continuation" {
		t.Fatalf("result = %q, want from persisted continuation", response.Result)
	}
	if got := store.Meta().Continuation; got == nil || got.OpenAIBaseURL != server.URL {
		t.Fatalf("expected persisted continuation preserved, got %+v", got)
	}
}

func TestHeadlessRunPromptOverridesRespectLockedModelContract(t *testing.T) {
	resetRunPromptDedupeRegistry()
	t.Cleanup(resetRunPromptDedupeRegistry)

	root := t.TempDir()
	containerDir := filepath.Join(root, "sessions", "workspace-a")
	store, err := session.Create(containerDir, "workspace-a", "/tmp/workspace-a")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := store.MarkModelDispatchLocked(session.LockedContract{Model: "locked-model", EnabledTools: []string{string(tools.ToolShell)}}); err != nil {
		t.Fatalf("mark model dispatch locked: %v", err)
	}

	requestBodies := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request payload: %v", err)
		}
		requestBodies <- payload
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"total_tokens\":2},\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"phase\":\"final\",\"content\":[{\"type\":\"output_text\",\"text\":\"locked response\"}]}]}}\n\n")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
	}))
	defer server.Close()

	authManager := auth.NewManager(auth.NewMemoryStore(auth.State{
		Method: auth.Method{Type: auth.MethodAPIKey, APIKey: &auth.APIKeyMethod{Key: "test-key"}},
	}), nil, time.Now)

	client := NewLoopbackRunPromptClient(HeadlessBootstrap{
		Config: config.App{
			WorkspaceRoot:   "/tmp/workspace-a",
			PersistenceRoot: root,
			Settings: config.Settings{
				Model:         "base-model",
				OpenAIBaseURL: server.URL,
				EnabledTools:  map[tools.ID]bool{tools.ToolPatch: true},
			},
		},
		ContainerDir: containerDir,
		AuthManager:  authManager,
	})

	response, err := client.RunPrompt(context.Background(), serverapi.RunPromptRequest{
		ClientRequestID:   "locked-direct-1",
		SelectedSessionID: store.Meta().SessionID,
		Prompt:            "hello",
		Overrides: serverapi.RunPromptOverrides{
			Model: "override-model",
			Tools: "patch",
		},
	}, nil)
	if err != nil {
		t.Fatalf("RunPrompt: %v", err)
	}
	if response.Result != "locked response" {
		t.Fatalf("result = %q, want locked response", response.Result)
	}
	runLog, err := os.ReadFile(filepath.Join(store.Dir(), RunLogFileName))
	if err != nil {
		t.Fatalf("read run log: %v", err)
	}
	if !strings.Contains(string(runLog), "model=locked-model") {
		t.Fatalf("expected run log to preserve locked model, got %q", string(runLog))
	}
	if strings.Contains(string(runLog), "model=override-model") {
		t.Fatalf("did not expect run log to use override model, got %q", string(runLog))
	}
	select {
	case payload := <-requestBodies:
		toolsPayload, ok := payload["tools"].([]any)
		if !ok || len(toolsPayload) != 1 {
			t.Fatalf("expected one locked tool in request payload, got %#v", payload["tools"])
		}
		toolPayload, ok := toolsPayload[0].(map[string]any)
		if !ok {
			t.Fatalf("unexpected tool payload: %#v", toolsPayload[0])
		}
		if got := toolPayload["name"]; got != string(tools.ToolShell) {
			t.Fatalf("expected locked shell tool, got %#v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for provider request payload")
	}
}

type testPrimaryRunGate struct {
	mu           sync.Mutex
	active       map[string]bool
	acquireCount int
}

func newTestPrimaryRunGate() *testPrimaryRunGate {
	return &testPrimaryRunGate{active: map[string]bool{}}
}

func (g *testPrimaryRunGate) AcquirePrimaryRun(sessionID string) (primaryrun.Lease, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.acquireCount++
	if g.active[sessionID] {
		return nil, primaryrun.ErrActivePrimaryRun
	}
	g.active[sessionID] = true
	return primaryrun.LeaseFunc(func() {
		g.mu.Lock()
		delete(g.active, sessionID)
		g.mu.Unlock()
	}), nil
}

func (g *testPrimaryRunGate) waitForAcquire(t *testing.T, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		g.mu.Lock()
		got := g.acquireCount
		g.mu.Unlock()
		if got >= want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d primary run acquires", want)
}

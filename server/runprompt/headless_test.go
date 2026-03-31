package runprompt

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"builder/server/auth"
	"builder/server/session"
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

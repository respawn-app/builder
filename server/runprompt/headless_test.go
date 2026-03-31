package runprompt

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

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

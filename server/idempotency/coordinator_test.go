package idempotency

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"builder/server/metadata"
	"builder/server/primaryrun"
	"builder/shared/serverapi"
)

type stubStore struct {
	mu      sync.Mutex
	records map[string]metadata.MutationDedupRecord
	deletes int
}

func newStubStore() *stubStore {
	return &stubStore{records: map[string]metadata.MutationDedupRecord{}}
}

func (s *stubStore) GetMutationDedupRecord(_ context.Context, method string, resourceID string, clientRequestID string) (metadata.MutationDedupRecord, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.records[method+"|"+resourceID+"|"+clientRequestID]
	return record, ok, nil
}

func (s *stubStore) UpsertMutationDedupRecord(_ context.Context, record metadata.MutationDedupRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records[record.Method+"|"+record.ResourceID+"|"+record.ClientRequestID] = record
	return nil
}

func (s *stubStore) DeleteExpiredMutationDedupRecords(_ context.Context, expiresAt time.Time) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deletes++
	deleted := int64(0)
	for key, record := range s.records {
		if !record.ExpiresAt.After(expiresAt) {
			delete(s.records, key)
			deleted++
		}
	}
	return deleted, nil
}

func TestCoordinatorReplaysPersistedSuccessForSameRequest(t *testing.T) {
	store := newStubStore()
	coordinator := NewCoordinator(store, time.Minute)
	coordinator.now = func() time.Time { return time.Unix(100, 0).UTC() }
	fingerprint, err := FingerprintPayload(struct {
		Text string `json:"text"`
	}{Text: "hello"})
	if err != nil {
		t.Fatalf("FingerprintPayload: %v", err)
	}
	request := Request{Method: "runtime.submit_user_message", ResourceID: "session-1", ClientRequestID: "req-1", PayloadFingerprint: fingerprint}

	callCount := 0
	response, err := Execute(context.Background(), coordinator, request, JSONCodec[serverapi.RuntimeSubmitUserMessageResponse]{}, func(context.Context) (serverapi.RuntimeSubmitUserMessageResponse, error) {
		callCount++
		return serverapi.RuntimeSubmitUserMessageResponse{Message: "done"}, nil
	})
	if err != nil {
		t.Fatalf("Execute first: %v", err)
	}
	if response.Message != "done" {
		t.Fatalf("first response = %+v", response)
	}
	response, err = Execute(context.Background(), coordinator, request, JSONCodec[serverapi.RuntimeSubmitUserMessageResponse]{}, func(context.Context) (serverapi.RuntimeSubmitUserMessageResponse, error) {
		callCount++
		return serverapi.RuntimeSubmitUserMessageResponse{Message: "other"}, nil
	})
	if err != nil {
		t.Fatalf("Execute replay: %v", err)
	}
	if response.Message != "done" {
		t.Fatalf("replayed response = %+v", response)
	}
	if callCount != 1 {
		t.Fatalf("call count = %d, want 1", callCount)
	}
}

func TestCoordinatorRejectsPayloadMismatch(t *testing.T) {
	store := newStubStore()
	coordinator := NewCoordinator(store, time.Minute)
	coordinator.now = func() time.Time { return time.Unix(100, 0).UTC() }
	fingerprintA, err := FingerprintPayload(struct{ Text string }{Text: "a"})
	if err != nil {
		t.Fatalf("FingerprintPayload A: %v", err)
	}
	fingerprintB, err := FingerprintPayload(struct{ Text string }{Text: "b"})
	if err != nil {
		t.Fatalf("FingerprintPayload B: %v", err)
	}
	requestA := Request{Method: "runtime.queue_user_message", ResourceID: "session-1", ClientRequestID: "req-1", PayloadFingerprint: fingerprintA}
	requestB := Request{Method: "runtime.queue_user_message", ResourceID: "session-1", ClientRequestID: "req-1", PayloadFingerprint: fingerprintB}

	if _, err := Execute(context.Background(), coordinator, requestA, JSONCodec[struct{}]{}, func(context.Context) (struct{}, error) {
		return struct{}{}, nil
	}); err != nil {
		t.Fatalf("Execute first: %v", err)
	}
	if _, err := Execute(context.Background(), coordinator, requestB, JSONCodec[struct{}]{}, func(context.Context) (struct{}, error) {
		return struct{}{}, nil
	}); err == nil || err.Error() == "" {
		t.Fatal("expected payload mismatch error")
	}
}

func TestCoordinatorDoesNotCacheCanceledRequests(t *testing.T) {
	store := newStubStore()
	coordinator := NewCoordinator(store, time.Minute)
	coordinator.now = func() time.Time { return time.Unix(100, 0).UTC() }
	fingerprint, err := FingerprintPayload(struct{}{})
	if err != nil {
		t.Fatalf("FingerprintPayload: %v", err)
	}
	request := Request{Method: "runtime.interrupt", ResourceID: "session-1", ClientRequestID: "req-1", PayloadFingerprint: fingerprint}

	callCount := 0
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = Execute(ctx, coordinator, request, JSONCodec[struct{}]{}, func(context.Context) (struct{}, error) {
		callCount++
		return struct{}{}, context.Canceled
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Execute canceled error = %v, want context canceled", err)
	}
	_, err = Execute(context.Background(), coordinator, request, JSONCodec[struct{}]{}, func(context.Context) (struct{}, error) {
		callCount++
		return struct{}{}, nil
	})
	if err != nil {
		t.Fatalf("Execute retry: %v", err)
	}
	if callCount != 2 {
		t.Fatalf("call count = %d, want 2", callCount)
	}
}

func TestCoordinatorReplaysKnownErrorSentinel(t *testing.T) {
	store := newStubStore()
	coordinator := NewCoordinator(store, time.Minute)
	coordinator.now = func() time.Time { return time.Unix(100, 0).UTC() }
	fingerprint, err := FingerprintPayload(struct{}{})
	if err != nil {
		t.Fatalf("FingerprintPayload: %v", err)
	}
	request := Request{Method: "runtime.submit_user_message", ResourceID: "session-1", ClientRequestID: "req-1", PayloadFingerprint: fingerprint}

	callCount := 0
	_, err = Execute(context.Background(), coordinator, request, JSONCodec[serverapi.RuntimeSubmitUserMessageResponse]{}, func(context.Context) (serverapi.RuntimeSubmitUserMessageResponse, error) {
		callCount++
		return serverapi.RuntimeSubmitUserMessageResponse{}, primaryrun.ErrActivePrimaryRun
	})
	if !errors.Is(err, primaryrun.ErrActivePrimaryRun) {
		t.Fatalf("Execute first error = %v, want ErrActivePrimaryRun", err)
	}
	_, err = Execute(context.Background(), coordinator, request, JSONCodec[serverapi.RuntimeSubmitUserMessageResponse]{}, func(context.Context) (serverapi.RuntimeSubmitUserMessageResponse, error) {
		callCount++
		return serverapi.RuntimeSubmitUserMessageResponse{}, nil
	})
	if !errors.Is(err, primaryrun.ErrActivePrimaryRun) {
		t.Fatalf("Execute replay error = %v, want ErrActivePrimaryRun", err)
	}
	if callCount != 1 {
		t.Fatalf("call count = %d, want 1", callCount)
	}
}

func TestExecuteWithPolicyDoesNotCacheActivePrimaryRun(t *testing.T) {
	store := newStubStore()
	coordinator := NewCoordinator(store, time.Minute)
	coordinator.now = func() time.Time { return time.Unix(100, 0).UTC() }
	fingerprint, err := FingerprintPayload(struct{}{})
	if err != nil {
		t.Fatalf("FingerprintPayload: %v", err)
	}
	request := Request{Method: "run_prompt.run", ResourceID: "scope|session-1", ClientRequestID: "req-1", PayloadFingerprint: fingerprint}

	callCount := 0
	_, err = ExecuteWithPolicy(context.Background(), coordinator, request, JSONCodec[serverapi.RunPromptResponse]{}, CachePolicy{
		ShouldCacheError: func(err error) bool {
			return !errors.Is(err, primaryrun.ErrActivePrimaryRun)
		},
	}, func(context.Context) (serverapi.RunPromptResponse, error) {
		callCount++
		if callCount == 1 {
			return serverapi.RunPromptResponse{}, primaryrun.ErrActivePrimaryRun
		}
		return serverapi.RunPromptResponse{Result: "ok"}, nil
	})
	if !errors.Is(err, primaryrun.ErrActivePrimaryRun) {
		t.Fatalf("ExecuteWithPolicy first error = %v, want ErrActivePrimaryRun", err)
	}
	response, err := ExecuteWithPolicy(context.Background(), coordinator, request, JSONCodec[serverapi.RunPromptResponse]{}, CachePolicy{
		ShouldCacheError: func(err error) bool {
			return !errors.Is(err, primaryrun.ErrActivePrimaryRun)
		},
	}, func(context.Context) (serverapi.RunPromptResponse, error) {
		callCount++
		return serverapi.RunPromptResponse{Result: "ok"}, nil
	})
	if err != nil {
		t.Fatalf("ExecuteWithPolicy retry error = %v", err)
	}
	if response.Result != "ok" {
		t.Fatalf("retry response = %+v, want ok", response)
	}
	if callCount != 2 {
		t.Fatalf("call count = %d, want 2", callCount)
	}
}

func TestDecodeReplayableErrorPreservesServerapiSentinel(t *testing.T) {
	err := decodeReplayableError("prompt_not_found", "missing prompt")
	if !errors.Is(err, serverapi.ErrPromptNotFound) {
		t.Fatalf("decodeReplayableError = %v, want ErrPromptNotFound", err)
	}
}

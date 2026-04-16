package runtimecontrol

import (
	"context"
	"testing"

	"builder/server/idempotency"
	"builder/server/llm"
	"builder/server/runtime"
	"builder/server/session"
	"builder/server/tools"
	"builder/shared/serverapi"
)

type stubRuntimeResolver struct {
	engine *runtime.Engine
}

func (s stubRuntimeResolver) ResolveRuntime(context.Context, string) (*runtime.Engine, error) {
	return s.engine, nil
}

type stubRuntimeLeaseVerifier struct {
	calls int
	err   error
}

func (s *stubRuntimeLeaseVerifier) RequireControllerLease(context.Context, string, string) error {
	s.calls++
	return s.err
}

type runtimeControlFakeClient struct{}

func (runtimeControlFakeClient) Generate(context.Context, llm.Request) (llm.Response, error) {
	return llm.Response{}, nil
}

func TestServiceSetSessionNameReplaysAcrossLaterLeaseChange(t *testing.T) {
	store, err := session.Create(t.TempDir(), "workspace-x", "/tmp/workspace-x")
	if err != nil {
		t.Fatalf("create session store: %v", err)
	}
	if err := store.SetName("before"); err != nil {
		t.Fatalf("persist initial session name: %v", err)
	}
	engine, err := runtime.New(store, runtimeControlFakeClient{}, tools.NewRegistry(), runtime.Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("create runtime engine: %v", err)
	}
	verifier := &stubRuntimeLeaseVerifier{}
	service := NewService(stubRuntimeResolver{engine: engine}, nil).
		WithControllerLeaseVerifier(verifier).
		WithIdempotencyCoordinator(idempotency.NewCoordinator(nil, idempotency.DefaultRetention))
	first := serverapi.RuntimeSetSessionNameRequest{
		ClientRequestID:   "req-1",
		SessionID:         store.Meta().SessionID,
		ControllerLeaseID: "lease-1",
		Name:              "after",
	}
	second := first
	second.ControllerLeaseID = "lease-2"

	if err := service.SetSessionName(context.Background(), first); err != nil {
		t.Fatalf("SetSessionName first: %v", err)
	}
	verifier.err = serverapi.ErrInvalidControllerLease
	if err := service.SetSessionName(context.Background(), second); err != nil {
		t.Fatalf("SetSessionName replay: %v", err)
	}
	if verifier.calls != 1 {
		t.Fatalf("lease verifier call count = %d, want 1", verifier.calls)
	}
	if got := store.Meta().Name; got != "after" {
		t.Fatalf("session name = %q, want after", got)
	}
	if reopened, err := session.Open(store.Dir()); err != nil {
		t.Fatalf("reopen session store: %v", err)
	} else if got := reopened.Meta().Name; got != "after" {
		t.Fatalf("reopened session name = %q, want after", got)
	}
}

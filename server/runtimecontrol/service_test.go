package runtimecontrol

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"builder/server/llm"
	"builder/server/runtime"
	"builder/server/session"
	"builder/server/tools"
	"builder/shared/serverapi"
	"builder/shared/toolspec"
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

type runtimeControlFakeClient struct {
	mu        sync.Mutex
	responses []llm.Response
	calls     int
}

type fakeShellHandler struct{}

func (fakeShellHandler) Name() toolspec.ID { return toolspec.ToolShell }

func (fakeShellHandler) Call(context.Context, tools.Call) (tools.Result, error) {
	return tools.Result{Output: json.RawMessage(`{"output":"ok","exit_code":0,"truncated":false}`)}, nil
}

func (c *runtimeControlFakeClient) Generate(context.Context, llm.Request) (llm.Response, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	if len(c.responses) == 0 {
		return llm.Response{}, nil
	}
	resp := c.responses[0]
	c.responses = c.responses[1:]
	return resp, nil
}

func TestServiceSetSessionNameRequiresControllerLease(t *testing.T) {
	store, err := session.Create(t.TempDir(), "workspace-x", "/tmp/workspace-x")
	if err != nil {
		t.Fatalf("create session store: %v", err)
	}
	if err := store.SetName("before"); err != nil {
		t.Fatalf("persist initial session name: %v", err)
	}
	engine, err := runtime.New(store, &runtimeControlFakeClient{}, tools.NewRegistry(fakeShellHandler{}), runtime.Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("create runtime engine: %v", err)
	}
	verifier := &stubRuntimeLeaseVerifier{}
	service := NewService(stubRuntimeResolver{engine: engine}, nil).
		WithControllerLeaseVerifier(verifier)
	req := serverapi.RuntimeSetSessionNameRequest{
		ClientRequestID:   "req-1",
		SessionID:         store.Meta().SessionID,
		ControllerLeaseID: "lease-1",
		Name:              "after",
	}

	if err := service.SetSessionName(context.Background(), req); err != nil {
		t.Fatalf("SetSessionName first: %v", err)
	}
	verifier.err = serverapi.ErrInvalidControllerLease
	if err := service.SetSessionName(context.Background(), req); !errors.Is(err, serverapi.ErrInvalidControllerLease) {
		t.Fatalf("SetSessionName second = %v, want ErrInvalidControllerLease", err)
	}
	if verifier.calls != 2 {
		t.Fatalf("lease verifier call count = %d, want 2", verifier.calls)
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

func TestServiceSubmitUserMessageDedupesSuccessfulRetry(t *testing.T) {
	store, err := session.Create(t.TempDir(), "workspace-x", "/tmp/workspace-x")
	if err != nil {
		t.Fatalf("create session store: %v", err)
	}
	client := &runtimeControlFakeClient{responses: []llm.Response{{
		Assistant: llm.Message{Role: llm.RoleAssistant, Content: "done", Phase: llm.MessagePhaseFinal},
		Usage:     llm.Usage{WindowTokens: 200000},
	}}}
	engine, err := runtime.New(store, client, tools.NewRegistry(), runtime.Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("create runtime engine: %v", err)
	}
	service := NewService(stubRuntimeResolver{engine: engine}, nil)
	req := serverapi.RuntimeSubmitUserMessageRequest{
		ClientRequestID:   "req-1",
		SessionID:         store.Meta().SessionID,
		ControllerLeaseID: "lease-1",
		Text:              "hello",
	}

	first, err := service.SubmitUserMessage(context.Background(), req)
	if err != nil {
		t.Fatalf("SubmitUserMessage first: %v", err)
	}
	second, err := service.SubmitUserMessage(context.Background(), req)
	if err != nil {
		t.Fatalf("SubmitUserMessage retry: %v", err)
	}
	if first.Message != "done" || second.Message != "done" {
		t.Fatalf("responses = (%q, %q), want both done", first.Message, second.Message)
	}
	if client.calls != 1 {
		t.Fatalf("generate call count = %d, want 1", client.calls)
	}
}

func TestServiceSubmitUserMessageReplaysSuccessfulRetryAfterLeaseInvalidation(t *testing.T) {
	store, err := session.Create(t.TempDir(), "workspace-x", "/tmp/workspace-x")
	if err != nil {
		t.Fatalf("create session store: %v", err)
	}
	client := &runtimeControlFakeClient{responses: []llm.Response{{
		Assistant: llm.Message{Role: llm.RoleAssistant, Content: "done", Phase: llm.MessagePhaseFinal},
		Usage:     llm.Usage{WindowTokens: 200000},
	}}}
	engine, err := runtime.New(store, client, tools.NewRegistry(), runtime.Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("create runtime engine: %v", err)
	}
	verifier := &stubRuntimeLeaseVerifier{}
	service := NewService(stubRuntimeResolver{engine: engine}, nil).
		WithControllerLeaseVerifier(verifier)
	req := serverapi.RuntimeSubmitUserMessageRequest{
		ClientRequestID:   "req-1",
		SessionID:         store.Meta().SessionID,
		ControllerLeaseID: "lease-1",
		Text:              "hello",
	}

	first, err := service.SubmitUserMessage(context.Background(), req)
	if err != nil {
		t.Fatalf("SubmitUserMessage first: %v", err)
	}
	verifier.err = serverapi.ErrInvalidControllerLease
	second, err := service.SubmitUserMessage(context.Background(), req)
	if err != nil {
		t.Fatalf("SubmitUserMessage replay: %v", err)
	}
	if first.Message != "done" || second.Message != "done" {
		t.Fatalf("responses = (%q, %q), want both done", first.Message, second.Message)
	}
	if verifier.calls != 1 {
		t.Fatalf("lease verifier call count = %d, want 1", verifier.calls)
	}
	if client.calls != 1 {
		t.Fatalf("generate call count = %d, want 1", client.calls)
	}
}

func TestServiceSubmitUserMessageRejectsClientRequestIDPayloadMismatch(t *testing.T) {
	store, err := session.Create(t.TempDir(), "workspace-x", "/tmp/workspace-x")
	if err != nil {
		t.Fatalf("create session store: %v", err)
	}
	client := &runtimeControlFakeClient{responses: []llm.Response{{
		Assistant: llm.Message{Role: llm.RoleAssistant, Content: "done", Phase: llm.MessagePhaseFinal},
		Usage:     llm.Usage{WindowTokens: 200000},
	}}}
	engine, err := runtime.New(store, client, tools.NewRegistry(), runtime.Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("create runtime engine: %v", err)
	}
	service := NewService(stubRuntimeResolver{engine: engine}, nil)
	first := serverapi.RuntimeSubmitUserMessageRequest{
		ClientRequestID:   "req-1",
		SessionID:         store.Meta().SessionID,
		ControllerLeaseID: "lease-1",
		Text:              "hello",
	}
	if _, err := service.SubmitUserMessage(context.Background(), first); err != nil {
		t.Fatalf("SubmitUserMessage first: %v", err)
	}
	second := first
	second.Text = "different"
	if _, err := service.SubmitUserMessage(context.Background(), second); err == nil || err.Error() != "client_request_id \"req-1\" was reused with different parameters" {
		t.Fatalf("SubmitUserMessage mismatch error = %v, want request id payload mismatch", err)
	}
	if client.calls != 1 {
		t.Fatalf("generate call count = %d, want 1", client.calls)
	}
}

func TestServiceSubmitUserShellCommandDedupesSuccessfulRetry(t *testing.T) {
	store, err := session.Create(t.TempDir(), "workspace-x", "/tmp/workspace-x")
	if err != nil {
		t.Fatalf("create session store: %v", err)
	}
	engine, err := runtime.New(store, &runtimeControlFakeClient{}, tools.NewRegistry(fakeShellHandler{}), runtime.Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("create runtime engine: %v", err)
	}
	service := NewService(stubRuntimeResolver{engine: engine}, nil)
	req := serverapi.RuntimeSubmitUserShellCommandRequest{
		ClientRequestID:   "req-1",
		SessionID:         store.Meta().SessionID,
		ControllerLeaseID: "lease-1",
		Command:           "pwd",
	}

	if err := service.SubmitUserShellCommand(context.Background(), req); err != nil {
		t.Fatalf("SubmitUserShellCommand first: %v", err)
	}
	afterFirst := countDirectShellCommandMessages(t, store, "pwd")
	if afterFirst != 1 {
		t.Fatalf("direct shell message count after first call = %d, want 1", afterFirst)
	}
	if err := service.SubmitUserShellCommand(context.Background(), req); err != nil {
		t.Fatalf("SubmitUserShellCommand replay: %v", err)
	}
	afterReplay := countDirectShellCommandMessages(t, store, "pwd")
	if afterReplay != 1 {
		t.Fatalf("direct shell message count after replay = %d, want 1", afterReplay)
	}
}

func TestServiceSubmitUserShellCommandReplaysSuccessfulRetryAfterLeaseInvalidation(t *testing.T) {
	store, err := session.Create(t.TempDir(), "workspace-x", "/tmp/workspace-x")
	if err != nil {
		t.Fatalf("create session store: %v", err)
	}
	engine, err := runtime.New(store, &runtimeControlFakeClient{}, tools.NewRegistry(fakeShellHandler{}), runtime.Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("create runtime engine: %v", err)
	}
	verifier := &stubRuntimeLeaseVerifier{}
	service := NewService(stubRuntimeResolver{engine: engine}, nil).
		WithControllerLeaseVerifier(verifier)
	req := serverapi.RuntimeSubmitUserShellCommandRequest{
		ClientRequestID:   "req-1",
		SessionID:         store.Meta().SessionID,
		ControllerLeaseID: "lease-1",
		Command:           "pwd",
	}

	if err := service.SubmitUserShellCommand(context.Background(), req); err != nil {
		t.Fatalf("SubmitUserShellCommand first: %v", err)
	}
	verifier.err = serverapi.ErrInvalidControllerLease
	if err := service.SubmitUserShellCommand(context.Background(), req); err != nil {
		t.Fatalf("SubmitUserShellCommand replay: %v", err)
	}
	if verifier.calls != 1 {
		t.Fatalf("lease verifier call count = %d, want 1", verifier.calls)
	}
	if got := countDirectShellCommandMessages(t, store, "pwd"); got != 1 {
		t.Fatalf("direct shell message count = %d, want 1", got)
	}
}

func TestServiceSubmitUserShellCommandRejectsClientRequestIDPayloadMismatch(t *testing.T) {
	store, err := session.Create(t.TempDir(), "workspace-x", "/tmp/workspace-x")
	if err != nil {
		t.Fatalf("create session store: %v", err)
	}
	engine, err := runtime.New(store, &runtimeControlFakeClient{}, tools.NewRegistry(fakeShellHandler{}), runtime.Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("create runtime engine: %v", err)
	}
	service := NewService(stubRuntimeResolver{engine: engine}, nil)
	first := serverapi.RuntimeSubmitUserShellCommandRequest{
		ClientRequestID:   "req-1",
		SessionID:         store.Meta().SessionID,
		ControllerLeaseID: "lease-1",
		Command:           "pwd",
	}
	if err := service.SubmitUserShellCommand(context.Background(), first); err != nil {
		t.Fatalf("SubmitUserShellCommand first: %v", err)
	}
	second := first
	second.Command = "ls"
	if err := service.SubmitUserShellCommand(context.Background(), second); err == nil || err.Error() != "client_request_id \"req-1\" was reused with different parameters" {
		t.Fatalf("SubmitUserShellCommand mismatch error = %v, want request id payload mismatch", err)
	}
	if got := countDirectShellCommandMessages(t, store, "pwd"); got != 1 {
		t.Fatalf("direct shell message count = %d, want 1", got)
	}
}

func countDirectShellCommandMessages(t *testing.T, store *session.Store, command string) int {
	t.Helper()
	events, err := store.ReadEvents()
	if err != nil {
		t.Fatalf("ReadEvents: %v", err)
	}
	count := 0
	for _, evt := range events {
		if evt.Kind != "message" {
			continue
		}
		var msg llm.Message
		if err := json.Unmarshal(evt.Payload, &msg); err != nil {
			t.Fatalf("decode message event: %v", err)
		}
		if msg.Role == llm.RoleDeveloper && msg.Content == "User ran shell command directly:\n"+command {
			count++
		}
	}
	return count
}

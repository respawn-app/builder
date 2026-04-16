package sessionlifecycle

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"builder/server/auth"
	"builder/server/llm"
	"builder/server/session"
	"builder/shared/serverapi"
	"builder/shared/toolspec"
)

func testIntPtr(v int) *int { return &v }

const testControllerLeaseID = "lease-test-controller"

type stubSessionLifecycleLeaseVerifier struct {
	calls int
	err   error
}

func (s *stubSessionLifecycleLeaseVerifier) RequireControllerLease(context.Context, string, string) error {
	s.calls++
	return s.err
}

func createPersistedSession(t *testing.T) (string, string, *session.Store) {
	t.Helper()
	persistenceRoot := t.TempDir()
	containerDir := filepath.Join(persistenceRoot, "sessions", "workspace-x")
	store, err := session.Create(containerDir, "workspace-x", "/tmp/work")
	if err != nil {
		t.Fatalf("create session store: %v", err)
	}
	return persistenceRoot, containerDir, store
}

func TestServiceGetInitialInputPrefersStoredDraft(t *testing.T) {
	_, containerDir, store := createPersistedSession(t)
	if err := store.SetInputDraft("draft from store"); err != nil {
		t.Fatalf("set input draft: %v", err)
	}

	service := NewService(containerDir, nil, nil)
	resp, err := service.GetInitialInput(context.Background(), serverapi.SessionInitialInputRequest{
		SessionID:       store.Meta().SessionID,
		TransitionInput: "transition input",
	})
	if err != nil {
		t.Fatalf("GetInitialInput: %v", err)
	}
	if resp.Input != "draft from store" {
		t.Fatalf("input = %q, want %q", resp.Input, "draft from store")
	}
}

func TestServiceGetInitialInputAllowsEmptySessionID(t *testing.T) {
	service := NewService(t.TempDir(), nil, nil)
	resp, err := service.GetInitialInput(context.Background(), serverapi.SessionInitialInputRequest{
		TransitionInput: "transition input",
	})
	if err != nil {
		t.Fatalf("GetInitialInput: %v", err)
	}
	if resp.Input != "transition input" {
		t.Fatalf("input = %q, want %q", resp.Input, "transition input")
	}
}

func TestServiceGetInitialInputRejectsPathLikeSessionID(t *testing.T) {
	service := NewService(t.TempDir(), nil, nil)
	_, err := service.GetInitialInput(context.Background(), serverapi.SessionInitialInputRequest{
		SessionID: "../session-1",
	})
	if err == nil || !strings.Contains(err.Error(), "single session id") {
		t.Fatalf("expected path-like session id rejection, got %v", err)
	}
}

func TestServicePersistInputDraftWritesBySessionID(t *testing.T) {
	_, containerDir, store := createPersistedSession(t)
	if err := store.SetName("session name"); err != nil {
		t.Fatalf("set session name: %v", err)
	}

	service := NewService(containerDir, nil, nil)
	if _, err := service.PersistInputDraft(context.Background(), serverapi.SessionPersistInputDraftRequest{
		ClientRequestID:   "req-1",
		SessionID:         store.Meta().SessionID,
		ControllerLeaseID: testControllerLeaseID,
		Input:             "saved by service",
	}); err != nil {
		t.Fatalf("PersistInputDraft: %v", err)
	}

	reopened, err := session.Open(store.Dir())
	if err != nil {
		t.Fatalf("reopen session store: %v", err)
	}
	if reopened.Meta().InputDraft != "saved by service" {
		t.Fatalf("input draft = %q, want %q", reopened.Meta().InputDraft, "saved by service")
	}
}

func TestServicePersistInputDraftRequiresControllerLease(t *testing.T) {
	_, containerDir, store := createPersistedSession(t)
	if err := store.SetName("session name"); err != nil {
		t.Fatalf("set session name: %v", err)
	}
	verifier := &stubSessionLifecycleLeaseVerifier{}
	service := NewService(containerDir, nil, nil).
		WithControllerLeaseVerifier(verifier)
	req := serverapi.SessionPersistInputDraftRequest{
		ClientRequestID:   "req-1",
		SessionID:         store.Meta().SessionID,
		ControllerLeaseID: "lease-1",
		Input:             "saved by service",
	}

	if _, err := service.PersistInputDraft(context.Background(), req); err != nil {
		t.Fatalf("PersistInputDraft first: %v", err)
	}
	verifier.err = serverapi.ErrInvalidControllerLease
	if _, err := service.PersistInputDraft(context.Background(), req); err != serverapi.ErrInvalidControllerLease {
		t.Fatalf("PersistInputDraft second = %v, want ErrInvalidControllerLease", err)
	}
	if verifier.calls != 2 {
		t.Fatalf("lease verifier call count = %d, want 2", verifier.calls)
	}
	reopened, err := session.Open(store.Dir())
	if err != nil {
		t.Fatalf("reopen session store: %v", err)
	}
	if reopened.Meta().InputDraft != "saved by service" {
		t.Fatalf("input draft = %q, want %q", reopened.Meta().InputDraft, "saved by service")
	}
}

func TestServicePersistInputDraftRejectsPathLikeSessionID(t *testing.T) {
	service := NewService(t.TempDir(), nil, nil)
	_, err := service.PersistInputDraft(context.Background(), serverapi.SessionPersistInputDraftRequest{
		ClientRequestID:   "req-1",
		SessionID:         "sessions/workspace-x/session-1",
		ControllerLeaseID: testControllerLeaseID,
		Input:             "draft",
	})
	if err == nil || !strings.Contains(err.Error(), "single session id") {
		t.Fatalf("expected path-like session id rejection, got %v", err)
	}
}

func TestServiceResolveTransitionRejectsPathLikeSessionID(t *testing.T) {
	service := NewService(t.TempDir(), nil, nil)
	_, err := service.ResolveTransition(context.Background(), serverapi.SessionResolveTransitionRequest{
		ClientRequestID:   "req-1",
		SessionID:         "../session-1",
		ControllerLeaseID: testControllerLeaseID,
		Transition: serverapi.SessionTransition{
			Action: "continue",
		},
	})
	if err == nil || !strings.Contains(err.Error(), "single session id") {
		t.Fatalf("expected path-like session id rejection, got %v", err)
	}
}

func TestServiceResolveTransitionForkRollbackCreatesFork(t *testing.T) {
	root, containerDir, store := createPersistedSession(t)
	if _, err := store.AppendEvent("step-1", "message", llm.Message{Role: llm.RoleUser, Content: "u1"}); err != nil {
		t.Fatalf("append user message: %v", err)
	}
	if _, err := store.AppendEvent("step-1", "message", llm.Message{Role: llm.RoleAssistant, Content: "a1"}); err != nil {
		t.Fatalf("append assistant message: %v", err)
	}
	if _, err := store.AppendEvent("step-2", "message", llm.Message{Role: llm.RoleUser, Content: "u2"}); err != nil {
		t.Fatalf("append second user message: %v", err)
	}
	if _, err := store.AppendEvent("step-2", "message", llm.Message{Role: llm.RoleAssistant, Content: "a2"}); err != nil {
		t.Fatalf("append second assistant message: %v", err)
	}

	service := NewService(containerDir, nil, nil)
	resp, err := service.ResolveTransition(context.Background(), serverapi.SessionResolveTransitionRequest{
		ClientRequestID:   "req-1",
		SessionID:         store.Meta().SessionID,
		ControllerLeaseID: testControllerLeaseID,
		Transition: serverapi.SessionTransition{
			Action:               "fork_rollback",
			InitialPrompt:        "edited prompt",
			ForkUserMessageIndex: 2,
		},
	})
	if err != nil {
		t.Fatalf("ResolveTransition: %v", err)
	}
	if !resp.ShouldContinue {
		t.Fatal("expected lifecycle continuation")
	}
	if resp.NextSessionID == "" || resp.NextSessionID == store.Meta().SessionID {
		t.Fatalf("unexpected fork session id %q", resp.NextSessionID)
	}
	if resp.InitialPrompt != "edited prompt" {
		t.Fatalf("initial prompt = %q, want %q", resp.InitialPrompt, "edited prompt")
	}
	if _, err := session.OpenByID(root, resp.NextSessionID); err != nil {
		t.Fatalf("open forked session store: %v", err)
	}
}

func TestServiceResolveTransitionForkRollbackResolvesTranscriptEntryIndex(t *testing.T) {
	root, containerDir, store := createPersistedSession(t)
	if _, err := store.AppendEvent("step-1", "message", llm.Message{Role: llm.RoleUser, Content: "u1"}); err != nil {
		t.Fatalf("append user message: %v", err)
	}
	if _, err := store.AppendEvent("step-1", "message", llm.Message{Role: llm.RoleAssistant, Content: "a1"}); err != nil {
		t.Fatalf("append assistant message: %v", err)
	}
	if _, err := store.AppendEvent("step-2", "message", llm.Message{Role: llm.RoleUser, Content: "u2"}); err != nil {
		t.Fatalf("append second user message: %v", err)
	}
	if _, err := store.AppendEvent("step-2", "message", llm.Message{Role: llm.RoleAssistant, Content: "a2"}); err != nil {
		t.Fatalf("append second assistant message: %v", err)
	}

	service := NewService(containerDir, nil, nil)
	resp, err := service.ResolveTransition(context.Background(), serverapi.SessionResolveTransitionRequest{
		ClientRequestID:   "req-1",
		SessionID:         store.Meta().SessionID,
		ControllerLeaseID: testControllerLeaseID,
		Transition: serverapi.SessionTransition{
			Action:                   "fork_rollback",
			InitialPrompt:            "edited prompt",
			ForkTranscriptEntryIndex: testIntPtr(2),
		},
	})
	if err != nil {
		t.Fatalf("ResolveTransition: %v", err)
	}
	if _, err := session.OpenByID(root, resp.NextSessionID); err != nil {
		t.Fatalf("open forked session store: %v", err)
	}
	if resp.InitialPrompt != "edited prompt" {
		t.Fatalf("initial prompt = %q, want %q", resp.InitialPrompt, "edited prompt")
	}
}

func TestResolveForkUserMessageIndexFromTranscriptEntryStopsBeforeBrokenTail(t *testing.T) {
	_, _, store := createPersistedSession(t)
	if _, err := store.AppendEvent("step-1", "message", llm.Message{Role: llm.RoleUser, Content: "u1"}); err != nil {
		t.Fatalf("append user message: %v", err)
	}
	if _, err := store.AppendEvent("step-1", "message", llm.Message{Role: llm.RoleAssistant, Content: "a1"}); err != nil {
		t.Fatalf("append assistant message: %v", err)
	}
	if _, err := store.AppendEvent("step-2", "message", llm.Message{Role: llm.RoleUser, Content: "u2"}); err != nil {
		t.Fatalf("append second user message: %v", err)
	}
	eventsPath := filepath.Join(store.Dir(), "events.jsonl")
	fp, err := os.OpenFile(eventsPath, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open events file: %v", err)
	}
	defer fp.Close()
	if _, err := fp.WriteString("{broken json}\n"); err != nil {
		t.Fatalf("append malformed tail: %v", err)
	}

	got, err := resolveForkUserMessageIndexFromTranscriptEntry(context.Background(), store, 2)
	if err != nil {
		t.Fatalf("resolveForkUserMessageIndexFromTranscriptEntry: %v", err)
	}
	if got != 2 {
		t.Fatalf("user message index = %d, want 2", got)
	}
}

func TestServiceResolveTransitionForkRollbackRejectsNonUserTranscriptEntryIndex(t *testing.T) {
	_, containerDir, store := createPersistedSession(t)
	if _, err := store.AppendEvent("step-1", "message", llm.Message{Role: llm.RoleUser, Content: "u1"}); err != nil {
		t.Fatalf("append user message: %v", err)
	}
	if _, err := store.AppendEvent("step-1", "message", llm.Message{Role: llm.RoleAssistant, Content: "a1"}); err != nil {
		t.Fatalf("append assistant message: %v", err)
	}

	service := NewService(containerDir, nil, nil)
	_, err := service.ResolveTransition(context.Background(), serverapi.SessionResolveTransitionRequest{
		ClientRequestID:   "req-1",
		SessionID:         store.Meta().SessionID,
		ControllerLeaseID: testControllerLeaseID,
		Transition: serverapi.SessionTransition{
			Action:                   "fork_rollback",
			ForkTranscriptEntryIndex: testIntPtr(1),
		},
	})
	if err == nil || !strings.Contains(err.Error(), "is not a user message") {
		t.Fatalf("expected non-user transcript entry rejection, got %v", err)
	}
}

func TestResolveForkUserMessageIndexFromTranscriptEntryIgnoresToolCompletedOrdinalDrift(t *testing.T) {
	_, _, store := createPersistedSession(t)
	if _, err := store.AppendEvent("step-1", "message", llm.Message{Role: llm.RoleUser, Content: "u1"}); err != nil {
		t.Fatalf("append first user message: %v", err)
	}
	if _, err := store.AppendEvent("step-1", "message", llm.Message{
		Role: llm.RoleAssistant,
		ToolCalls: []llm.ToolCall{{
			ID:    "call-1",
			Name:  string(toolspec.ToolShell),
			Input: json.RawMessage(`{"command":"pwd"}`),
		}},
	}); err != nil {
		t.Fatalf("append assistant tool call: %v", err)
	}
	if _, err := store.AppendEvent("step-1", "tool_completed", map[string]any{
		"call_id":  "call-1",
		"name":     string(toolspec.ToolShell),
		"is_error": false,
		"output":   json.RawMessage(`{"output":"/tmp","exit_code":0,"truncated":false}`),
	}); err != nil {
		t.Fatalf("append tool_completed: %v", err)
	}
	if _, err := store.AppendEvent("step-1", "message", llm.Message{Role: llm.RoleTool, ToolCallID: "call-1", Name: string(toolspec.ToolShell)}); err != nil {
		t.Fatalf("append tool message: %v", err)
	}
	if _, err := store.AppendEvent("step-1", "message", llm.Message{Role: llm.RoleAssistant, Content: "done", Phase: llm.MessagePhaseFinal}); err != nil {
		t.Fatalf("append assistant final: %v", err)
	}
	if _, err := store.AppendEvent("step-2", "message", llm.Message{Role: llm.RoleUser, Content: "u2"}); err != nil {
		t.Fatalf("append second user message: %v", err)
	}

	got, err := resolveForkUserMessageIndexFromTranscriptEntry(context.Background(), store, 4)
	if err != nil {
		t.Fatalf("resolveForkUserMessageIndexFromTranscriptEntry: %v", err)
	}
	if got != 2 {
		t.Fatalf("user message index = %d, want 2", got)
	}
}

func TestServiceGetInitialInputRejectsSessionOutsideContainer(t *testing.T) {
	root := t.TempDir()
	containerA := filepath.Join(root, "sessions", "workspace-a")
	containerB := filepath.Join(root, "sessions", "workspace-b")
	if err := os.MkdirAll(containerA, 0o755); err != nil {
		t.Fatalf("mkdir container A: %v", err)
	}
	store, err := session.Create(containerB, "workspace-b", "/tmp/workspace-b")
	if err != nil {
		t.Fatalf("create foreign session store: %v", err)
	}
	if err := store.SetInputDraft("foreign draft"); err != nil {
		t.Fatalf("set foreign input draft: %v", err)
	}

	service := NewService(containerA, nil, nil)
	_, err = service.GetInitialInput(context.Background(), serverapi.SessionInitialInputRequest{SessionID: store.Meta().SessionID})
	if err == nil {
		t.Fatal("expected foreign session lookup rejection")
	}
	if !strings.Contains(err.Error(), "not found") && !strings.Contains(err.Error(), "outside workspace container") {
		t.Fatalf("expected scoped lookup rejection, got %v", err)
	}
}

func TestServicePersistInputDraftRejectsSessionOutsideContainer(t *testing.T) {
	root := t.TempDir()
	containerA := filepath.Join(root, "sessions", "workspace-a")
	containerB := filepath.Join(root, "sessions", "workspace-b")
	if err := os.MkdirAll(containerA, 0o755); err != nil {
		t.Fatalf("mkdir container A: %v", err)
	}
	store, err := session.Create(containerB, "workspace-b", "/tmp/workspace-b")
	if err != nil {
		t.Fatalf("create foreign session store: %v", err)
	}
	if err := store.SetName("foreign session"); err != nil {
		t.Fatalf("persist foreign session meta: %v", err)
	}

	service := NewService(containerA, nil, nil)
	_, err = service.PersistInputDraft(context.Background(), serverapi.SessionPersistInputDraftRequest{
		ClientRequestID:   "req-1",
		SessionID:         store.Meta().SessionID,
		ControllerLeaseID: testControllerLeaseID,
		Input:             "should fail",
	})
	if err == nil {
		t.Fatal("expected foreign session mutation rejection")
	}
	if !strings.Contains(err.Error(), "not found") && !strings.Contains(err.Error(), "outside workspace container") {
		t.Fatalf("expected scoped lookup rejection, got %v", err)
	}
}

func TestServiceResolveTransitionLogoutUsesSessionIDWithoutStoreLookup(t *testing.T) {
	mgr := auth.NewManager(auth.NewMemoryStore(auth.State{
		Scope: auth.ScopeGlobal,
		Method: auth.Method{
			Type:   auth.MethodAPIKey,
			APIKey: &auth.APIKeyMethod{Key: "sk-before"},
		},
	}), nil, time.Now)
	service := NewService(t.TempDir(), nil, mgr)

	resp, err := service.ResolveTransition(context.Background(), serverapi.SessionResolveTransitionRequest{
		ClientRequestID:   "req-1",
		SessionID:         "session-42",
		ControllerLeaseID: testControllerLeaseID,
		Transition: serverapi.SessionTransition{
			Action: "logout",
		},
	})
	if err != nil {
		t.Fatalf("ResolveTransition logout: %v", err)
	}
	if !resp.ShouldContinue || !resp.RequiresReauth {
		t.Fatalf("unexpected logout response: %+v", resp)
	}
	if resp.NextSessionID != "session-42" {
		t.Fatalf("next session id = %q, want %q", resp.NextSessionID, "session-42")
	}
	state, err := mgr.Load(context.Background())
	if err != nil {
		t.Fatalf("load auth state: %v", err)
	}
	if state.Method.Type != "" || state.Method.APIKey != nil {
		t.Fatalf("expected auth method to be cleared, got %+v", state.Method)
	}
}

func TestServiceResolveTransitionRequiresClientRequestID(t *testing.T) {
	service := NewService(t.TempDir(), nil, nil)
	_, err := service.ResolveTransition(context.Background(), serverapi.SessionResolveTransitionRequest{
		Transition: serverapi.SessionTransition{Action: "continue"},
	})
	if err == nil || err.Error() != "client_request_id is required" {
		t.Fatalf("expected missing client_request_id error, got %v", err)
	}
}

func TestServiceResolveTransitionRequiresControllerLease(t *testing.T) {
	mgr := auth.NewManager(auth.NewMemoryStore(auth.State{
		Scope: auth.ScopeGlobal,
		Method: auth.Method{
			Type:   auth.MethodAPIKey,
			APIKey: &auth.APIKeyMethod{Key: "sk-before"},
		},
	}), nil, time.Now)
	verifier := &stubSessionLifecycleLeaseVerifier{}
	service := NewService(t.TempDir(), nil, mgr).
		WithControllerLeaseVerifier(verifier)
	req := serverapi.SessionResolveTransitionRequest{
		ClientRequestID:   "dup-lease",
		SessionID:         "session-42",
		ControllerLeaseID: "lease-1",
		Transition:        serverapi.SessionTransition{Action: "logout"},
	}

	firstResp, err := service.ResolveTransition(context.Background(), req)
	if err != nil {
		t.Fatalf("ResolveTransition first: %v", err)
	}
	verifier.err = serverapi.ErrInvalidControllerLease
	secondResp, err := service.ResolveTransition(context.Background(), req)
	if err != serverapi.ErrInvalidControllerLease {
		t.Fatalf("ResolveTransition second = %v, want ErrInvalidControllerLease", err)
	}
	if verifier.calls != 2 {
		t.Fatalf("lease verifier call count = %d, want 2", verifier.calls)
	}
	if !firstResp.ShouldContinue || !firstResp.RequiresReauth {
		t.Fatalf("unexpected first logout response: %+v", firstResp)
	}
	if secondResp != (serverapi.SessionResolveTransitionResponse{}) {
		t.Fatalf("unexpected second response on lease failure: %+v", secondResp)
	}
}

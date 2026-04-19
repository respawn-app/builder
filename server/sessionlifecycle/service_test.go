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
	"builder/server/metadata"
	"builder/server/session"
	"builder/shared/config"
	"builder/shared/serverapi"
	"builder/shared/toolspec"
)

func testIntPtr(v int) *int { return &v }

const testControllerLeaseID = "lease-test-controller"

type stubSessionLifecycleLeaseVerifier struct {
	calls int
	err   error
}

type noopSessionLifecycleLeaseVerifier struct{}

func (s *stubSessionLifecycleLeaseVerifier) RequireControllerLease(context.Context, string, string) error {
	s.calls++
	return s.err
}

func (noopSessionLifecycleLeaseVerifier) RequireControllerLease(context.Context, string, string) error {
	return nil
}

func newTestSessionLifecycleService(containerDir string, authManager *auth.Manager) *Service {
	return NewService(containerDir, nil, authManager).WithControllerLeaseVerifier(noopSessionLifecycleLeaseVerifier{})
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

func createAuthoritativeSessionLifecycleSession(t *testing.T, workspaceRoot string) (config.App, *metadata.Store, metadata.Binding, *session.Store) {
	t.Helper()
	cfg := config.App{PersistenceRoot: t.TempDir(), WorkspaceRoot: workspaceRoot}
	store, err := metadata.Open(cfg.PersistenceRoot)
	if err != nil {
		t.Fatalf("metadata.Open: %v", err)
	}
	binding, err := store.RegisterWorkspaceBinding(context.Background(), cfg.WorkspaceRoot)
	if err != nil {
		t.Fatalf("RegisterWorkspaceBinding: %v", err)
	}
	sess, err := session.Create(
		config.ProjectSessionsRoot(cfg, binding.ProjectID),
		filepath.Base(cfg.WorkspaceRoot),
		cfg.WorkspaceRoot,
		store.AuthoritativeSessionStoreOptions()...,
	)
	if err != nil {
		_ = store.Close()
		t.Fatalf("session.Create: %v", err)
	}
	if err := sess.SetName("incident triage"); err != nil {
		_ = store.Close()
		t.Fatalf("SetName: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return cfg, store, binding, sess
}

func TestServiceGetInitialInputPrefersStoredDraft(t *testing.T) {
	_, containerDir, store := createPersistedSession(t)
	if err := store.SetInputDraft("draft from store"); err != nil {
		t.Fatalf("set input draft: %v", err)
	}

	service := newTestSessionLifecycleService(containerDir, nil)
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
	service := newTestSessionLifecycleService(t.TempDir(), nil)
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
	service := newTestSessionLifecycleService(t.TempDir(), nil)
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

	service := newTestSessionLifecycleService(containerDir, nil)
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

func TestServiceRetargetSessionWorkspaceUpdatesBindingAndSession(t *testing.T) {
	oldWorkspace := t.TempDir()
	newWorkspace := t.TempDir()
	cfg, metadataStore, binding, sess := createAuthoritativeSessionLifecycleSession(t, oldWorkspace)

	service := NewGlobalService(cfg.PersistenceRoot, nil, nil)
	resp, err := service.RetargetSessionWorkspace(context.Background(), serverapi.SessionRetargetWorkspaceRequest{
		ClientRequestID: "req-1",
		SessionID:       sess.Meta().SessionID,
		WorkspaceRoot:   newWorkspace,
	})
	if err != nil {
		t.Fatalf("RetargetSessionWorkspace: %v", err)
	}
	if resp.Binding.ProjectID != binding.ProjectID {
		t.Fatalf("binding project id = %q, want %q", resp.Binding.ProjectID, binding.ProjectID)
	}
	target, err := metadataStore.ResolveSessionExecutionTarget(context.Background(), sess.Meta().SessionID)
	if err != nil {
		t.Fatalf("ResolveSessionExecutionTarget: %v", err)
	}
	if target.WorkspaceRoot != resp.Binding.CanonicalRoot {
		t.Fatalf("target workspace root = %q, want %q", target.WorkspaceRoot, resp.Binding.CanonicalRoot)
	}
	reopened, err := session.OpenByID(cfg.PersistenceRoot, sess.Meta().SessionID, metadataStore.AuthoritativeSessionStoreOptions()...)
	if err != nil {
		t.Fatalf("OpenByID: %v", err)
	}
	if reopened.Meta().WorkspaceRoot != resp.Binding.CanonicalRoot {
		t.Fatalf("session workspace root = %q, want %q", reopened.Meta().WorkspaceRoot, resp.Binding.CanonicalRoot)
	}
}

func TestServiceRetargetSessionWorkspaceRequiresPersistenceRoot(t *testing.T) {
	service := NewService(t.TempDir(), nil, nil)
	_, err := service.RetargetSessionWorkspace(context.Background(), serverapi.SessionRetargetWorkspaceRequest{
		ClientRequestID: "req-1",
		SessionID:       "session-1",
		WorkspaceRoot:   t.TempDir(),
	})
	if err == nil || err.Error() != "persistence root is required" {
		t.Fatalf("RetargetSessionWorkspace error = %v, want persistence root is required", err)
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
	if _, err := service.PersistInputDraft(context.Background(), req); err != nil {
		t.Fatalf("PersistInputDraft replay: %v", err)
	}
	deniedReq := req
	deniedReq.ClientRequestID = "req-2"
	deniedReq.Input = "should not persist"
	if _, err := service.PersistInputDraft(context.Background(), deniedReq); err != serverapi.ErrInvalidControllerLease {
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

func TestServicePersistInputDraftRejectsClientRequestIDPayloadMismatch(t *testing.T) {
	_, containerDir, store := createPersistedSession(t)
	if err := store.SetName("session name"); err != nil {
		t.Fatalf("set session name: %v", err)
	}
	service := newTestSessionLifecycleService(containerDir, nil)
	first := serverapi.SessionPersistInputDraftRequest{
		ClientRequestID:   "req-1",
		SessionID:         store.Meta().SessionID,
		ControllerLeaseID: testControllerLeaseID,
		Input:             "saved by service",
	}

	if _, err := service.PersistInputDraft(context.Background(), first); err != nil {
		t.Fatalf("PersistInputDraft first: %v", err)
	}
	second := first
	second.Input = "different draft"
	if _, err := service.PersistInputDraft(context.Background(), second); err == nil || err.Error() != "client_request_id \"req-1\" was reused with different parameters" {
		t.Fatalf("PersistInputDraft mismatch error = %v, want request id payload mismatch", err)
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
	service := newTestSessionLifecycleService(t.TempDir(), nil)
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
	service := newTestSessionLifecycleService(t.TempDir(), nil)
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

func TestServicePersistInputDraftFailsClosedWithoutControllerVerifier(t *testing.T) {
	_, containerDir, store := createPersistedSession(t)
	service := NewService(containerDir, nil, nil)
	_, err := service.PersistInputDraft(context.Background(), serverapi.SessionPersistInputDraftRequest{
		ClientRequestID:   "req-1",
		SessionID:         store.Meta().SessionID,
		ControllerLeaseID: testControllerLeaseID,
		Input:             "draft",
	})
	if err != serverapi.ErrInvalidControllerLease {
		t.Fatalf("PersistInputDraft error = %v, want ErrInvalidControllerLease", err)
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

	service := newTestSessionLifecycleService(containerDir, nil)
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

	service := newTestSessionLifecycleService(containerDir, nil)
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

	service := newTestSessionLifecycleService(containerDir, nil)
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

	service := newTestSessionLifecycleService(containerA, nil)
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

	service := newTestSessionLifecycleService(containerA, nil)
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
	service := newTestSessionLifecycleService(t.TempDir(), mgr)

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
	service := newTestSessionLifecycleService(t.TempDir(), nil)
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
	if err != nil {
		t.Fatalf("ResolveTransition second replay: %v", err)
	}
	if verifier.calls != 1 {
		t.Fatalf("lease verifier call count = %d, want 1", verifier.calls)
	}
	if !firstResp.ShouldContinue || !firstResp.RequiresReauth {
		t.Fatalf("unexpected first logout response: %+v", firstResp)
	}
	if secondResp != firstResp {
		t.Fatalf("expected duplicate transition replay response %+v, got %+v", firstResp, secondResp)
	}

	newReq := req
	newReq.ClientRequestID = "dup-lease-2"
	if _, err := service.ResolveTransition(context.Background(), newReq); err != serverapi.ErrInvalidControllerLease {
		t.Fatalf("ResolveTransition third = %v, want ErrInvalidControllerLease", err)
	}
	if verifier.calls != 2 {
		t.Fatalf("lease verifier call count after new request = %d, want 2", verifier.calls)
	}
}

func TestServiceResolveTransitionReplaysSuccessfulRetryAfterLeaseRotation(t *testing.T) {
	mgr := auth.NewManager(auth.NewMemoryStore(auth.State{
		Scope: auth.ScopeGlobal,
		Method: auth.Method{
			Type:   auth.MethodAPIKey,
			APIKey: &auth.APIKeyMethod{Key: "sk-before"},
		},
	}), nil, time.Now)
	service := newTestSessionLifecycleService(t.TempDir(), mgr)
	first := serverapi.SessionResolveTransitionRequest{
		ClientRequestID:   "req-1",
		SessionID:         "session-42",
		ControllerLeaseID: "lease-1",
		Transition:        serverapi.SessionTransition{Action: "logout"},
	}

	firstResp, err := service.ResolveTransition(context.Background(), first)
	if err != nil {
		t.Fatalf("ResolveTransition first: %v", err)
	}
	second := first
	second.ControllerLeaseID = "lease-2"
	secondResp, err := service.ResolveTransition(context.Background(), second)
	if err != nil {
		t.Fatalf("ResolveTransition replay after lease rotation: %v", err)
	}
	if secondResp != firstResp {
		t.Fatalf("expected replay response %+v, got %+v", firstResp, secondResp)
	}
}

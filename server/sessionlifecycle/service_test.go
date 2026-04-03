package sessionlifecycle

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"builder/server/auth"
	"builder/server/llm"
	"builder/server/session"
	"builder/shared/serverapi"
)

func createPersistedSession(t *testing.T) (string, *session.Store) {
	t.Helper()
	persistenceRoot := t.TempDir()
	containerDir := filepath.Join(persistenceRoot, "sessions", "workspace-x")
	store, err := session.Create(containerDir, "workspace-x", "/tmp/work")
	if err != nil {
		t.Fatalf("create session store: %v", err)
	}
	return persistenceRoot, store
}

func TestServiceGetInitialInputPrefersStoredDraft(t *testing.T) {
	root, store := createPersistedSession(t)
	if err := store.SetInputDraft("draft from store"); err != nil {
		t.Fatalf("set input draft: %v", err)
	}

	service := NewService(root, nil, nil)
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
	root, store := createPersistedSession(t)
	if err := store.SetName("session name"); err != nil {
		t.Fatalf("set session name: %v", err)
	}

	service := NewService(root, nil, nil)
	if _, err := service.PersistInputDraft(context.Background(), serverapi.SessionPersistInputDraftRequest{
		SessionID: store.Meta().SessionID,
		Input:     "saved by service",
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

func TestServicePersistInputDraftRejectsPathLikeSessionID(t *testing.T) {
	service := NewService(t.TempDir(), nil, nil)
	_, err := service.PersistInputDraft(context.Background(), serverapi.SessionPersistInputDraftRequest{
		SessionID: "sessions/workspace-x/session-1",
		Input:     "draft",
	})
	if err == nil || !strings.Contains(err.Error(), "single session id") {
		t.Fatalf("expected path-like session id rejection, got %v", err)
	}
}

func TestServiceResolveTransitionRejectsPathLikeSessionID(t *testing.T) {
	service := NewService(t.TempDir(), nil, nil)
	_, err := service.ResolveTransition(context.Background(), serverapi.SessionResolveTransitionRequest{
		SessionID: "../session-1",
		Transition: serverapi.SessionTransition{
			Action: "continue",
		},
	})
	if err == nil || !strings.Contains(err.Error(), "single session id") {
		t.Fatalf("expected path-like session id rejection, got %v", err)
	}
}

func TestServiceResolveTransitionForkRollbackCreatesFork(t *testing.T) {
	root, store := createPersistedSession(t)
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

	service := NewService(root, nil, nil)
	resp, err := service.ResolveTransition(context.Background(), serverapi.SessionResolveTransitionRequest{
		SessionID: store.Meta().SessionID,
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
		SessionID: "session-42",
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

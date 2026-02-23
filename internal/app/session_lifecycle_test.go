package app

import (
	"builder/internal/llm"
	"builder/internal/session"
	"context"
	"testing"
)

func TestResolveSessionActionResumeReopensPicker(t *testing.T) {
	nextSessionID, initialPrompt, parentSessionID, forceNewSession, shouldContinue, err := resolveSessionAction(
		context.Background(),
		appBootstrap{},
		nil,
		UITransition{Action: UIActionResume},
	)
	if err != nil {
		t.Fatalf("resolve session action: %v", err)
	}
	if !shouldContinue {
		t.Fatal("expected lifecycle to continue for resume action")
	}
	if nextSessionID != "" {
		t.Fatalf("expected empty session id to force picker, got %q", nextSessionID)
	}
	if forceNewSession {
		t.Fatal("did not expect force-new for resume action")
	}
	if parentSessionID != "" {
		t.Fatalf("expected no parent session id on resume, got %q", parentSessionID)
	}
	if initialPrompt != "" {
		t.Fatalf("expected no initial prompt on resume, got %q", initialPrompt)
	}
}

func TestResolveSessionActionNewSessionUsesForceNewFlow(t *testing.T) {
	nextSessionID, initialPrompt, parentSessionID, forceNewSession, shouldContinue, err := resolveSessionAction(
		context.Background(),
		appBootstrap{},
		nil,
		UITransition{Action: UIActionNewSession, InitialPrompt: "hello", ParentSessionID: "parent-1"},
	)
	if err != nil {
		t.Fatalf("resolve session action: %v", err)
	}
	if !shouldContinue {
		t.Fatal("expected lifecycle to continue for new session action")
	}
	if !forceNewSession {
		t.Fatal("expected force-new session flow")
	}
	if nextSessionID != "" {
		t.Fatalf("expected empty session id for force-new flow, got %q", nextSessionID)
	}
	if parentSessionID != "parent-1" {
		t.Fatalf("expected parent session id passthrough, got %q", parentSessionID)
	}
	if initialPrompt != "hello" {
		t.Fatalf("expected initial prompt passthrough, got %q", initialPrompt)
	}
}

func TestResolveSessionActionForkRollbackTeleportsToForkWithPrompt(t *testing.T) {
	root := t.TempDir()
	store, err := session.Create(root, "workspace-x", "/tmp/work")
	if err != nil {
		t.Fatalf("create session store: %v", err)
	}
	if _, err := store.AppendEvent("s1", "message", llm.Message{Role: llm.RoleUser, Content: "u1"}); err != nil {
		t.Fatalf("append user message: %v", err)
	}
	if _, err := store.AppendEvent("s1", "message", llm.Message{Role: llm.RoleAssistant, Content: "a1"}); err != nil {
		t.Fatalf("append assistant message: %v", err)
	}

	nextSessionID, initialPrompt, parentSessionID, forceNewSession, shouldContinue, err := resolveSessionAction(
		context.Background(),
		appBootstrap{},
		store,
		UITransition{Action: UIActionForkRollback, InitialPrompt: "edited user message", ForkUserMessageIndex: 1},
	)
	if err != nil {
		t.Fatalf("resolve session action: %v", err)
	}
	if !shouldContinue {
		t.Fatal("expected lifecycle to continue for fork rollback action")
	}
	if forceNewSession {
		t.Fatal("did not expect force-new for fork rollback action")
	}
	if parentSessionID != "" {
		t.Fatalf("expected no deferred parent for pre-created fork session, got %q", parentSessionID)
	}
	if nextSessionID == "" {
		t.Fatal("expected target fork session id")
	}
	if nextSessionID == store.Meta().SessionID {
		t.Fatalf("expected fork session id to differ from parent, got %q", nextSessionID)
	}
	if initialPrompt != "edited user message" {
		t.Fatalf("expected initial prompt passthrough, got %q", initialPrompt)
	}
}

func TestResolveSessionActionOpenSessionUsesTargetID(t *testing.T) {
	nextSessionID, initialPrompt, parentSessionID, forceNewSession, shouldContinue, err := resolveSessionAction(
		context.Background(),
		appBootstrap{},
		nil,
		UITransition{Action: UIActionOpenSession, TargetSessionID: "session-42"},
	)
	if err != nil {
		t.Fatalf("resolve session action: %v", err)
	}
	if !shouldContinue {
		t.Fatal("expected lifecycle to continue for open session action")
	}
	if nextSessionID != "session-42" {
		t.Fatalf("expected target session id passthrough, got %q", nextSessionID)
	}
	if initialPrompt != "" {
		t.Fatalf("expected no initial prompt, got %q", initialPrompt)
	}
	if parentSessionID != "" {
		t.Fatalf("expected no parent session id, got %q", parentSessionID)
	}
	if forceNewSession {
		t.Fatal("did not expect force-new session")
	}
}

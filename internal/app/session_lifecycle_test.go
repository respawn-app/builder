package app

import (
	"context"
	"testing"
)

func TestResolveSessionActionResumeReopensPicker(t *testing.T) {
	nextSessionID, initialPrompt, forceNewSession, shouldContinue, err := resolveSessionAction(
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
	if initialPrompt != "" {
		t.Fatalf("expected no initial prompt on resume, got %q", initialPrompt)
	}
}

func TestResolveSessionActionNewSessionUsesForceNewFlow(t *testing.T) {
	nextSessionID, initialPrompt, forceNewSession, shouldContinue, err := resolveSessionAction(
		context.Background(),
		appBootstrap{},
		nil,
		UITransition{Action: UIActionNewSession, InitialPrompt: "hello"},
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
	if initialPrompt != "hello" {
		t.Fatalf("expected initial prompt passthrough, got %q", initialPrompt)
	}
}

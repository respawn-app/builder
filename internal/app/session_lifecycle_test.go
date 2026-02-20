package app

import (
	"context"
	"testing"
)

func TestResolveSessionActionResumeReopensPicker(t *testing.T) {
	nextSessionID, initialPrompt, shouldContinue, err := resolveSessionAction(
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
	if initialPrompt != "" {
		t.Fatalf("expected no initial prompt on resume, got %q", initialPrompt)
	}
}

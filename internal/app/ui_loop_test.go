package app

import (
	"testing"
)

func TestExtractUITransitionIncludesInitialPrompt(t *testing.T) {
	model := &uiModel{
		exitAction:               UIActionNewSession,
		nextSessionInitialPrompt: "review prompt payload",
	}
	transition := extractUITransition(model)
	if transition.Action != UIActionNewSession {
		t.Fatalf("expected action %q, got %q", UIActionNewSession, transition.Action)
	}
	if transition.InitialPrompt != "review prompt payload" {
		t.Fatalf("expected initial prompt payload, got %q", transition.InitialPrompt)
	}
}

package app

import (
	"testing"
)

func TestExtractUITransitionIncludesInitialPrompt(t *testing.T) {
	model := &uiModel{
		uiSessionTransitionFeatureState: uiSessionTransitionFeatureState{
			exitAction:               UIActionNewSession,
			nextSessionInitialPrompt: "review prompt payload",
			nextSessionInitialInput:  "draft payload",
		},
	}
	transition := extractUITransition(model)
	if transition.Action != UIActionNewSession {
		t.Fatalf("expected action %q, got %q", UIActionNewSession, transition.Action)
	}
	if transition.InitialPrompt != "review prompt payload" {
		t.Fatalf("expected initial prompt payload, got %q", transition.InitialPrompt)
	}
	if transition.InitialInput != "draft payload" {
		t.Fatalf("expected initial input payload, got %q", transition.InitialInput)
	}
}

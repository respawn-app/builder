package app

import (
	"testing"

	"builder/internal/config"
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

func TestMainUIProgramOptionsIncludeMouseCapture(t *testing.T) {
	options := mainUIProgramOptions(config.Settings{TUIAlternateScreen: config.TUIAlternateScreenAuto})
	if len(options) != 1 {
		t.Fatalf("expected exactly one main UI option with auto alt-screen (mouse capture), got %d", len(options))
	}
}

func TestMainUIProgramOptionsIncludeAltScreenAndMouseCapture(t *testing.T) {
	options := mainUIProgramOptions(config.Settings{TUIAlternateScreen: config.TUIAlternateScreenAlways})
	if len(options) != 2 {
		t.Fatalf("expected alt-screen and mouse capture options, got %d", len(options))
	}
}

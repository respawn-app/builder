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

func TestMainUIProgramOptionsAutoAltModeEnablesMouseCapture(t *testing.T) {
	options := mainUIProgramOptions(config.Settings{TUIAlternateScreen: config.TUIAlternateScreenAuto, TUIScrollMode: config.TUIScrollModeAlt})
	if len(options) != 1 {
		t.Fatalf("expected mouse option with auto alt-screen in alt mode, got %d", len(options))
	}
}

func TestMainUIProgramOptionsAlwaysAltModeUsesAltScreenAndMouse(t *testing.T) {
	options := mainUIProgramOptions(config.Settings{TUIAlternateScreen: config.TUIAlternateScreenAlways, TUIScrollMode: config.TUIScrollModeAlt})
	if len(options) != 2 {
		t.Fatalf("expected alt-screen and mouse options, got %d", len(options))
	}
}

func TestMainUIProgramOptionsNativeDisablesMouseCapture(t *testing.T) {
	options := mainUIProgramOptions(config.Settings{TUIAlternateScreen: config.TUIAlternateScreenAuto, TUIScrollMode: config.TUIScrollModeNative})
	if len(options) != 0 {
		t.Fatalf("expected no options in native mode with auto alt-screen, got %d", len(options))
	}
}

func TestMainUIProgramOptionsNativeDisablesAltScreenEvenWhenAlways(t *testing.T) {
	options := mainUIProgramOptions(config.Settings{TUIAlternateScreen: config.TUIAlternateScreenAlways, TUIScrollMode: config.TUIScrollModeNative})
	if len(options) != 0 {
		t.Fatalf("expected no options in native mode with always alt-screen, got %d", len(options))
	}
}

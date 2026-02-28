package app

import (
	"testing"

	"builder/internal/config"
	"builder/internal/runtime"
	"builder/internal/tui"
)

func TestAltScreenPolicyHelpers(t *testing.T) {
	if !shouldStartMainUIInAltScreen(config.TUIAlternateScreenAlways) {
		t.Fatal("expected always policy to start main UI in alt-screen")
	}
	if shouldStartMainUIInAltScreen(config.TUIAlternateScreenAuto) {
		t.Fatal("expected auto policy to start main UI in normal screen")
	}
	if shouldUseDetailAltScreen(config.TUIAlternateScreenAuto) {
		t.Fatal("expected auto policy to keep detail in normal screen")
	}
	if shouldUseDetailAltScreen(config.TUIAlternateScreenNever) {
		t.Fatal("expected never policy to keep detail in normal screen")
	}
	if !shouldUseSessionPickerAltScreen(config.TUIAlternateScreenAuto) {
		t.Fatal("expected auto policy to use picker alt-screen")
	}
	if shouldUseSessionPickerAltScreen(config.TUIAlternateScreenNever) {
		t.Fatal("expected never policy to disable picker alt-screen")
	}
}

func TestToggleTranscriptModeAutoDoesNotToggleAltScreen(t *testing.T) {
	m := NewUIModel(
		nil,
		make(chan runtime.Event),
		make(chan askEvent),
		WithUIAlternateScreenPolicy(config.TUIAlternateScreenAuto),
	).(*uiModel)

	if m.view.Mode() != tui.ModeOngoing {
		t.Fatalf("mode=%q want ongoing", m.view.Mode())
	}
	if m.altScreenActive {
		t.Fatal("expected initial alt-screen inactive")
	}

	cmd := m.toggleTranscriptMode()
	if cmd == nil {
		t.Fatal("expected clear-screen command when toggling into detail")
	}
	if m.view.Mode() != tui.ModeDetail {
		t.Fatalf("mode=%q want detail", m.view.Mode())
	}
	if m.altScreenActive {
		t.Fatal("expected alt-screen unchanged when entering detail")
	}

	cmd = m.toggleTranscriptMode()
	if cmd == nil {
		t.Fatal("expected clear-screen command when toggling out of detail")
	}
	if m.view.Mode() != tui.ModeOngoing {
		t.Fatalf("mode=%q want ongoing", m.view.Mode())
	}
	if m.altScreenActive {
		t.Fatal("expected alt-screen unchanged after leaving detail")
	}
}

func TestToggleTranscriptModeNeverDoesNotEnterAltScreen(t *testing.T) {
	m := NewUIModel(
		nil,
		make(chan runtime.Event),
		make(chan askEvent),
		WithUIAlternateScreenPolicy(config.TUIAlternateScreenNever),
	).(*uiModel)

	cmd := m.toggleTranscriptMode()
	if cmd == nil {
		t.Fatal("expected clear-screen command for detail mode")
	}
	if m.view.Mode() != tui.ModeDetail {
		t.Fatalf("mode=%q want detail", m.view.Mode())
	}
	if m.altScreenActive {
		t.Fatal("expected alt-screen inactive in detail mode")
	}
}

func TestToggleTranscriptModeAlwaysKeepsAltScreenWithoutDetailTransition(t *testing.T) {
	m := NewUIModel(
		nil,
		make(chan runtime.Event),
		make(chan askEvent),
		WithUIAlternateScreenPolicy(config.TUIAlternateScreenAlways),
	).(*uiModel)
	if !m.altScreenActive {
		t.Fatal("expected alt-screen active at startup for always policy")
	}

	cmd := m.toggleTranscriptMode()
	if cmd == nil {
		t.Fatal("expected clear-screen command when entering detail")
	}
	if m.view.Mode() != tui.ModeDetail {
		t.Fatalf("mode=%q want detail", m.view.Mode())
	}
	if !m.altScreenActive {
		t.Fatal("expected alt-screen to stay active")
	}

	cmd = m.toggleTranscriptMode()
	if cmd == nil {
		t.Fatal("expected clear-screen command when leaving detail")
	}
	if m.view.Mode() != tui.ModeOngoing {
		t.Fatalf("mode=%q want ongoing", m.view.Mode())
	}
	if !m.altScreenActive {
		t.Fatal("expected alt-screen to remain active for always policy")
	}
}

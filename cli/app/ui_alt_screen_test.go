package app

import (
	"testing"

	"builder/cli/tui"
	"builder/server/runtime"
	"builder/shared/config"
)

func TestAltScreenPolicyHelpers(t *testing.T) {
	if !shouldStartMainUIInAltScreen(config.TUIAlternateScreenAlways) {
		t.Fatal("expected always policy to start main UI in alt-screen")
	}
	if shouldStartMainUIInAltScreen(config.TUIAlternateScreenAuto) {
		t.Fatal("expected auto policy to start main UI in normal screen")
	}
	if !shouldUseDetailAltScreen(config.TUIAlternateScreenAuto) {
		t.Fatal("expected auto policy to use detail alt-screen")
	}
	if shouldUseDetailAltScreen(config.TUIAlternateScreenNever) {
		t.Fatal("expected never policy to keep detail in normal screen")
	}
	if !shouldUseStartupPickerAltScreen(config.TUIAlternateScreenAuto) {
		t.Fatal("expected auto policy to use picker alt-screen")
	}
	if shouldUseStartupPickerAltScreen(config.TUIAlternateScreenNever) {
		t.Fatal("expected never policy to disable picker alt-screen")
	}
}

func TestToggleTranscriptModeAutoUsesDetailAltScreen(t *testing.T) {
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
		t.Fatal("expected alt-screen command when toggling into detail")
	}
	if m.view.Mode() != tui.ModeDetail {
		t.Fatalf("mode=%q want detail", m.view.Mode())
	}
	if !m.altScreenActive {
		t.Fatal("expected alt-screen active when entering detail")
	}

	cmd = m.toggleTranscriptMode()
	if cmd == nil {
		t.Fatal("expected alt-screen command when toggling out of detail")
	}
	if m.view.Mode() != tui.ModeOngoing {
		t.Fatalf("mode=%q want ongoing", m.view.Mode())
	}
	if m.altScreenActive {
		t.Fatal("expected alt-screen inactive after leaving detail in auto policy")
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
		t.Fatal("expected command for detail mode")
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
		t.Fatal("expected command when entering detail")
	}
	if m.view.Mode() != tui.ModeDetail {
		t.Fatalf("mode=%q want detail", m.view.Mode())
	}
	if !m.altScreenActive {
		t.Fatal("expected alt-screen to stay active")
	}

	cmd = m.toggleTranscriptMode()
	if cmd == nil {
		t.Fatal("expected command when leaving detail")
	}
	if m.view.Mode() != tui.ModeOngoing {
		t.Fatalf("mode=%q want ongoing", m.view.Mode())
	}
	if !m.altScreenActive {
		t.Fatal("expected alt-screen to remain active for always policy")
	}
}

func TestNativeReplayCmdForModeTransitionPreservesAppendOnlyWhenScreenNotReplaced(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.windowSizeKnown = true
	m.termWidth = 80
	initial := tui.TranscriptProjection{Blocks: []tui.TranscriptProjectionBlock{{Role: "assistant", Lines: []string{"before"}}}}
	updated := tui.TranscriptProjection{Blocks: []tui.TranscriptProjectionBlock{{Role: "assistant", Lines: []string{"before"}}, {Role: "assistant", Lines: []string{"after"}}}}
	m.nativeProjection = updated
	m.nativeRenderedProjection = initial
	m.nativeRenderedSnapshot = initial.Render(tui.TranscriptDivider)

	cmd := m.nativeReplayCmdForModeTransition(tui.ModeDetail, tui.ModeOngoing, true)
	if cmd == nil {
		t.Fatal("expected append-only replay command")
	}
	msgs := collectCmdMessages(t, cmd)
	if len(msgs) != 1 {
		t.Fatalf("expected append-only replay without clear-screen, got %d message(s)", len(msgs))
	}
	flush, ok := msgs[0].(nativeHistoryFlushMsg)
	if !ok {
		t.Fatalf("expected nativeHistoryFlushMsg, got %T", msgs[0])
	}
	if got := stripANSIText(flush.Text); got != "after" {
		t.Fatalf("expected append-only replay of deferred delta, got %q", got)
	}
}

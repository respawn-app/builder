package app

import (
	"testing"

	"builder/cli/tui"
)

func TestToggleTranscriptModeUsesFixedDetailAltScreen(t *testing.T) {
	m := newProjectedStaticUIModel()

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
		t.Fatal("expected alt-screen inactive after leaving detail")
	}
}

func TestNativeReplayCmdForModeTransitionPreservesAppendOnlyWhenScreenNotReplaced(t *testing.T) {
	m := newProjectedStaticUIModel()
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

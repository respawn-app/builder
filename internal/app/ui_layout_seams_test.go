package app

import (
	"strings"
	"testing"

	"builder/internal/runtime"
	"builder/internal/tui"
)

func TestUIRenderFrameRenderRespectsPaddingPolicy(t *testing.T) {
	frame := uiRenderFrame{
		width:      6,
		height:     4,
		chatPanel:  []string{"chat"},
		statusLine: "status",
	}

	withoutPadding := strings.Split(strings.TrimSuffix(frame.render(), ansiHideCursor), "\n")
	if len(withoutPadding) != 2 {
		t.Fatalf("expected compact frame without padding, got %d lines", len(withoutPadding))
	}

	frame.padToHeight = true
	withPadding := strings.Split(strings.TrimSuffix(frame.render(), ansiHideCursor), "\n")
	if len(withPadding) != 4 {
		t.Fatalf("expected padded frame to fill height, got %d lines", len(withPadding))
	}
}

func TestComputeNativeLiveRegionStateTracksStreamingBoundary(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)
	m.termWidth = 40
	m.termHeight = 10
	m.windowSizeKnown = true
	m.transcriptEntries = []tui.TranscriptEntry{{Role: "assistant", Text: "seed"}}
	m.forwardToView(tui.SetConversationMsg{Entries: m.transcriptEntries})

	idle := m.layout().computeNativeLiveRegionState()
	if idle.streamingActive {
		t.Fatal("did not expect idle native live region to be streaming")
	}
	if idle.lines <= 0 {
		t.Fatalf("expected idle native live region line count, got %d", idle.lines)
	}

	m.forwardToView(tui.SetConversationMsg{Entries: m.transcriptEntries, Ongoing: "streaming"})
	streaming := m.layout().computeNativeLiveRegionState()
	if !streaming.streamingActive {
		t.Fatal("expected native live region to report active streaming")
	}
	if streaming.lines <= 0 {
		t.Fatalf("expected streaming live region line count, got %d", streaming.lines)
	}
	if streaming.pad != 0 {
		t.Fatalf("expected computed live region pad to stay explicit, got %d", streaming.pad)
	}

	next, _ := m.view.Update(tui.ToggleModeMsg{})
	m.view = next.(tui.Model)
	detail := m.layout().computeNativeLiveRegionState()
	if detail != (nativeLiveRegionState{}) {
		t.Fatalf("expected detail mode to disable native live region state, got %+v", detail)
	}
}

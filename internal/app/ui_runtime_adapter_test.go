package app

import (
	"testing"

	"builder/internal/runtime"
	"builder/internal/tui"
)

func TestApplyChatSnapshotMarksPendingDeltaDedup(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)

	_ = m.runtimeAdapter().applyChatSnapshot(runtime.ChatSnapshot{Ongoing: "hello"})

	if !m.pendingSnapshotDeltaDedup {
		t.Fatal("expected pending snapshot delta dedupe marker")
	}
	if m.pendingSnapshotOngoingLen != len("hello") {
		t.Fatalf("expected pending ongoing length %d, got %d", len("hello"), m.pendingSnapshotOngoingLen)
	}
	if m.pendingSnapshotPreviousOngoingLen != 0 {
		t.Fatalf("expected previous ongoing length 0, got %d", m.pendingSnapshotPreviousOngoingLen)
	}
}

func TestAssistantDeltaSkippedWhenSnapshotAlreadyApplied(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)

	m.forwardToView(tui.SetConversationMsg{Ongoing: "hel"})
	_ = m.runtimeAdapter().applyChatSnapshot(runtime.ChatSnapshot{Ongoing: "hello"})
	_ = m.runtimeAdapter().handleRuntimeEvent(runtime.Event{Kind: runtime.EventAssistantDelta, AssistantDelta: "lo"})

	if got := m.view.OngoingStreamingText(); got != "hello" {
		t.Fatalf("expected no duplicate delta append, got %q", got)
	}
	if m.pendingSnapshotDeltaDedup {
		t.Fatal("expected pending dedupe marker cleared after assistant delta")
	}
}

func TestAssistantDeltaAppendsWhenSnapshotMarkerCannotMatch(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)

	_ = m.runtimeAdapter().applyChatSnapshot(runtime.ChatSnapshot{Ongoing: "hello"})
	m.pendingSnapshotOngoingLen = 4
	_ = m.runtimeAdapter().handleRuntimeEvent(runtime.Event{Kind: runtime.EventAssistantDelta, AssistantDelta: "!"})

	if got := m.view.OngoingStreamingText(); got != "hello!" {
		t.Fatalf("expected delta append when marker does not match, got %q", got)
	}
}

func TestAssistantDeltaAppendsWhenSnapshotWasNotDeltaProgression(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)

	m.forwardToView(tui.SetConversationMsg{Ongoing: "hello"})
	_ = m.runtimeAdapter().applyChatSnapshot(runtime.ChatSnapshot{Ongoing: "hello"})
	_ = m.runtimeAdapter().handleRuntimeEvent(runtime.Event{Kind: runtime.EventAssistantDelta, AssistantDelta: "lo"})

	if got := m.view.OngoingStreamingText(); got != "hellolo" {
		t.Fatalf("expected legitimate delta append after non-progression snapshot, got %q", got)
	}
}

package app

import (
	"testing"

	"builder/internal/runtime"
	"builder/internal/tui"
)

func TestApplyChatSnapshotMarksPendingDeltaDedup(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)

	_ = m.runtimeAdapter().applyChatSnapshot("", runtime.ChatSnapshot{Ongoing: "hello"})

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
	_ = m.runtimeAdapter().applyChatSnapshot("", runtime.ChatSnapshot{Ongoing: "hello"})
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

	_ = m.runtimeAdapter().applyChatSnapshot("", runtime.ChatSnapshot{Ongoing: "hello"})
	m.pendingSnapshotOngoingLen = 4
	_ = m.runtimeAdapter().handleRuntimeEvent(runtime.Event{Kind: runtime.EventAssistantDelta, AssistantDelta: "!"})

	if got := m.view.OngoingStreamingText(); got != "hello!" {
		t.Fatalf("expected delta append when marker does not match, got %q", got)
	}
}

func TestAssistantDeltaAppendsWhenSnapshotWasNotDeltaProgression(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)

	m.forwardToView(tui.SetConversationMsg{Ongoing: "hello"})
	_ = m.runtimeAdapter().applyChatSnapshot("", runtime.ChatSnapshot{Ongoing: "hello"})
	_ = m.runtimeAdapter().handleRuntimeEvent(runtime.Event{Kind: runtime.EventAssistantDelta, AssistantDelta: "lo"})

	if got := m.view.OngoingStreamingText(); got != "hellolo" {
		t.Fatalf("expected legitimate delta append after non-progression snapshot, got %q", got)
	}
}

func TestAssistantDeltaSuppressedAfterCommittedSnapshotForSameStep(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)

	m.forwardToView(tui.SetConversationMsg{Ongoing: "fin"})
	m.transcriptEntries = []tui.TranscriptEntry{{Role: "user", Text: "u"}}
	_ = m.runtimeAdapter().applyChatSnapshot("step-1", runtime.ChatSnapshot{
		Entries: []runtime.ChatEntry{{Role: "user", Text: "u"}, {Role: "assistant", Text: "final"}},
		Ongoing: "",
	})
	_ = m.runtimeAdapter().handleRuntimeEvent(runtime.Event{Kind: runtime.EventAssistantDelta, StepID: "step-1", AssistantDelta: "late"})

	if got := m.view.OngoingStreamingText(); got != "" {
		t.Fatalf("expected late delta for committed step to be suppressed, got %q", got)
	}
}

func TestConversationUpdatedWithEmptyStepIDDoesNotClearLateDeltaSuppression(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)

	m.suppressLateDeltaStepID = "step-1"
	_ = m.runtimeAdapter().applyChatSnapshot("", runtime.ChatSnapshot{Ongoing: ""})
	_ = m.runtimeAdapter().handleRuntimeEvent(runtime.Event{Kind: runtime.EventAssistantDelta, StepID: "step-1", AssistantDelta: "late"})

	if got := m.view.OngoingStreamingText(); got != "" {
		t.Fatalf("expected suppression to persist across empty-step snapshot, got %q", got)
	}
}

func TestToolCallConversationUpdateDoesNotSuppressLaterSameStepDelta(t *testing.T) {
	m := NewUIModel(nil, make(chan runtime.Event), make(chan askEvent)).(*uiModel)

	m.transcriptEntries = []tui.TranscriptEntry{{Role: "user", Text: "u"}}
	_ = m.runtimeAdapter().applyChatSnapshot("step-1", runtime.ChatSnapshot{
		Entries: []runtime.ChatEntry{
			{Role: "user", Text: "u"},
			{Role: "tool_call", Text: "shell"},
		},
		Ongoing: "",
	})
	_ = m.runtimeAdapter().handleRuntimeEvent(runtime.Event{Kind: runtime.EventAssistantDelta, StepID: "step-1", AssistantDelta: "hello"})

	if got := m.view.OngoingStreamingText(); got != "hello" {
		t.Fatalf("expected later same-step assistant delta after tool update, got %q", got)
	}
}

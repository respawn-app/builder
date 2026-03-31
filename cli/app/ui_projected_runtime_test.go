package app

import (
	"testing"

	"builder/shared/clientui"
)

func TestWaitRuntimeEventReturnsProjectedMessage(t *testing.T) {
	ch := make(chan clientui.Event, 1)
	ch <- clientui.Event{Kind: clientui.EventAssistantDelta, AssistantDelta: "hello"}
	cmd := waitRuntimeEvent(ch)
	msg, ok := cmd().(runtimeEventMsg)
	if !ok {
		t.Fatalf("expected runtimeEventMsg, got %T", cmd())
	}
	if msg.event.Kind != clientui.EventAssistantDelta || msg.event.AssistantDelta != "hello" {
		t.Fatalf("unexpected projected event: %+v", msg.event)
	}
}

func TestProjectedRuntimeEventUpdateStreamsAssistantDelta(t *testing.T) {
	m := NewProjectedUIModel(nil, make(chan clientui.Event), make(chan askEvent)).(*uiModel)
	next, _ := m.Update(runtimeEventMsg{event: clientui.Event{Kind: clientui.EventAssistantDelta, AssistantDelta: "hello"}})
	updated := next.(*uiModel)
	if got := updated.view.OngoingStreamingText(); got != "hello" {
		t.Fatalf("expected projected delta to reach view, got %q", got)
	}
}

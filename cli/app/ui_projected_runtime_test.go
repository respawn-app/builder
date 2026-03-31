package app

import (
	"testing"
	"time"

	"builder/server/runtime"
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

func TestProjectRuntimeEventChannelStopsWhenRequested(t *testing.T) {
	src := make(chan runtime.Event, 1)
	stop := make(chan struct{})
	out := projectRuntimeEventChannel(src, stop)

	src <- runtime.Event{Kind: runtime.EventAssistantDelta, AssistantDelta: "first"}

	deadline := time.After(2 * time.Second)
	for len(out) == 0 {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for first projected event")
		default:
			time.Sleep(time.Millisecond)
		}
	}

	first, ok := <-out
	if !ok {
		t.Fatal("projected runtime channel closed before first event")
	}
	if first.AssistantDelta != "first" {
		t.Fatalf("first projected delta = %q, want first", first.AssistantDelta)
	}

	sentSecond := make(chan struct{})
	go func() {
		src <- runtime.Event{Kind: runtime.EventAssistantDelta, AssistantDelta: "second"}
		close(sentSecond)
	}()
	select {
	case <-sentSecond:
	case <-deadline:
		t.Fatal("timed out queueing second projected event")
	}

	close(stop)

	for {
		select {
		case _, ok := <-out:
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("timed out waiting for projected runtime channel to stop")
		}
	}
}

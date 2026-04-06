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
	msg, ok := cmd().(runtimeEventBatchMsg)
	if !ok {
		t.Fatalf("expected runtimeEventBatchMsg, got %T", cmd())
	}
	if len(msg.events) != 1 {
		t.Fatalf("events len = %d, want 1", len(msg.events))
	}
	if msg.events[0].Kind != clientui.EventAssistantDelta || msg.events[0].AssistantDelta != "hello" {
		t.Fatalf("unexpected projected event: %+v", msg.events[0])
	}
}

func TestWaitRuntimeEventDrainsQueuedBatch(t *testing.T) {
	ch := make(chan clientui.Event, 3)
	ch <- clientui.Event{Kind: clientui.EventAssistantDelta, AssistantDelta: "hello"}
	ch <- clientui.Event{Kind: clientui.EventAssistantDelta, AssistantDelta: " world"}
	ch <- clientui.Event{Kind: clientui.EventRunStateChanged, RunState: &clientui.RunState{Busy: true}}
	msg, ok := waitRuntimeEvent(ch)().(runtimeEventBatchMsg)
	if !ok {
		t.Fatalf("expected runtimeEventBatchMsg, got %T", waitRuntimeEvent(ch)())
	}
	if len(msg.events) != 3 {
		t.Fatalf("events len = %d, want 3", len(msg.events))
	}
}

func TestWaitRuntimeEventFencesTranscriptEventIntoCarry(t *testing.T) {
	ch := make(chan clientui.Event, 3)
	ch <- clientui.Event{Kind: clientui.EventAssistantDelta, AssistantDelta: "hello"}
	ch <- clientui.Event{
		Kind:        clientui.EventUserMessageFlushed,
		UserMessage: "steer now",
		TranscriptEntries: []clientui.ChatEntry{{
			Role: "user",
			Text: "steer now",
		}},
	}
	ch <- clientui.Event{Kind: clientui.EventAssistantDelta, AssistantDelta: "after"}

	msg, ok := waitRuntimeEvent(ch)().(runtimeEventBatchMsg)
	if !ok {
		t.Fatalf("expected runtimeEventBatchMsg, got %T", waitRuntimeEvent(ch)())
	}
	if len(msg.events) != 1 {
		t.Fatalf("events len = %d, want 1", len(msg.events))
	}
	if msg.events[0].Kind != clientui.EventAssistantDelta {
		t.Fatalf("first event kind = %q, want assistant_delta", msg.events[0].Kind)
	}
	if msg.carry == nil {
		t.Fatal("expected fenced transcript event carried into next batch")
	}
	if msg.carry.Kind != clientui.EventUserMessageFlushed {
		t.Fatalf("carry kind = %q, want user_message_flushed", msg.carry.Kind)
	}
	if remaining := (<-ch); remaining.Kind != clientui.EventAssistantDelta || remaining.AssistantDelta != "after" {
		t.Fatalf("expected unfenced later delta to remain unread, got %+v", remaining)
	}
}

func TestWaitRuntimeEventReturnsFencedFirstEventWithoutCoalescing(t *testing.T) {
	ch := make(chan clientui.Event, 2)
	ch <- clientui.Event{
		Kind:        clientui.EventUserMessageFlushed,
		UserMessage: "steer now",
		TranscriptEntries: []clientui.ChatEntry{{
			Role: "user",
			Text: "steer now",
		}},
	}
	ch <- clientui.Event{Kind: clientui.EventAssistantDelta, AssistantDelta: "after"}

	msg, ok := waitRuntimeEvent(ch)().(runtimeEventBatchMsg)
	if !ok {
		t.Fatalf("expected runtimeEventBatchMsg, got %T", waitRuntimeEvent(ch)())
	}
	if len(msg.events) != 1 {
		t.Fatalf("events len = %d, want 1", len(msg.events))
	}
	if msg.events[0].Kind != clientui.EventUserMessageFlushed {
		t.Fatalf("first event kind = %q, want user_message_flushed", msg.events[0].Kind)
	}
	if msg.carry != nil {
		t.Fatalf("did not expect carry when first event is already fenced, got %+v", *msg.carry)
	}
	if remaining := (<-ch); remaining.Kind != clientui.EventAssistantDelta || remaining.AssistantDelta != "after" {
		t.Fatalf("expected later delta to remain unread, got %+v", remaining)
	}
}

func TestWaitRuntimeEventCmdPrefersPendingCarryBeforeRuntimeChannel(t *testing.T) {
	runtimeEvents := make(chan clientui.Event, 1)
	runtimeEvents <- clientui.Event{Kind: clientui.EventAssistantDelta, AssistantDelta: "later"}
	m := newProjectedTestUIModel(nil, runtimeEvents, nil)
	m.pendingRuntimeEvents = []clientui.Event{{Kind: clientui.EventUserMessageFlushed, UserMessage: "steer now"}}

	first := m.waitRuntimeEventCmd()
	if first == nil {
		t.Fatal("expected wait command for pending carry event")
	}
	firstMsg, ok := first().(runtimeEventBatchMsg)
	if !ok {
		t.Fatalf("expected runtimeEventBatchMsg for pending carry, got %T", first())
	}
	if len(firstMsg.events) != 1 || firstMsg.events[0].Kind != clientui.EventUserMessageFlushed {
		t.Fatalf("unexpected pending carry batch: %+v", firstMsg.events)
	}
	if len(m.pendingRuntimeEvents) != 0 {
		t.Fatalf("expected pending carry queue drained, got %+v", m.pendingRuntimeEvents)
	}

	second := m.waitRuntimeEventCmd()
	if second == nil {
		t.Fatal("expected wait command for runtime channel after carry drain")
	}
	secondMsg, ok := second().(runtimeEventBatchMsg)
	if !ok {
		t.Fatalf("expected runtimeEventBatchMsg from runtime channel, got %T", second())
	}
	if len(secondMsg.events) != 1 || secondMsg.events[0].AssistantDelta != "later" {
		t.Fatalf("unexpected runtime channel batch after carry drain: %+v", secondMsg.events)
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

package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"builder/server/runtime"
	"builder/shared/clientui"
	"builder/shared/serverapi"
)

type sessionActivityTestSubscription struct {
	events chan clientui.Event
}

func (s *sessionActivityTestSubscription) Next(ctx context.Context) (clientui.Event, error) {
	select {
	case <-ctx.Done():
		return clientui.Event{}, ctx.Err()
	case evt := <-s.events:
		return evt, nil
	}
}

func (s *sessionActivityTestSubscription) Close() error { return nil }

var _ serverapi.SessionActivitySubscription = (*sessionActivityTestSubscription)(nil)

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

func TestWaitRuntimeEventCmdStaysPausedWhileHydrationFenceIsArmed(t *testing.T) {
	runtimeEvents := make(chan clientui.Event, 1)
	runtimeEvents <- clientui.Event{Kind: clientui.EventAssistantDelta, AssistantDelta: "later"}
	m := newProjectedTestUIModel(nil, runtimeEvents, nil)
	m.waitRuntimeEventAfterHydration = true

	if cmd := m.waitRuntimeEventCmd(); cmd != nil {
		t.Fatalf("expected runtime wait to remain paused while hydration fence is armed, got %T", cmd())
	}
	if len(runtimeEvents) != 1 {
		t.Fatalf("expected runtime event to remain unread while hydration fence is armed, remaining=%d", len(runtimeEvents))
	}

	m.waitRuntimeEventAfterHydration = false
	cmd := m.waitRuntimeEventCmd()
	if cmd == nil {
		t.Fatal("expected runtime wait after hydration fence clears")
	}
	msg, ok := cmd().(runtimeEventBatchMsg)
	if !ok {
		t.Fatalf("expected runtimeEventBatchMsg after hydration fence clears, got %T", cmd())
	}
	if len(msg.events) != 1 || msg.events[0].AssistantDelta != "later" {
		t.Fatalf("unexpected runtime event after hydration fence clears: %+v", msg.events)
	}
}

func TestConversationUpdateHydrationFencesLaterRuntimeEvents(t *testing.T) {
	client := &refreshingRuntimeClient{
		transcripts: []clientui.TranscriptPage{{
			SessionID:    "session-1",
			TotalEntries: 1,
			Entries:      []clientui.ChatEntry{{Role: "assistant", Text: "hydrated"}},
		}},
	}
	runtimeEvents := make(chan clientui.Event, 1)
	runtimeEvents <- clientui.Event{Kind: clientui.EventAssistantDelta, AssistantDelta: "later"}
	m := newProjectedTestUIModel(client, runtimeEvents, nil)
	m.startupCmds = nil

	next, cmd := m.Update(runtimeEventMsg{event: clientui.Event{Kind: clientui.EventConversationUpdated}})
	updated := next.(*uiModel)
	if !updated.waitRuntimeEventAfterHydration {
		t.Fatal("expected conversation update to arm hydration fence")
	}
	if len(runtimeEvents) != 1 {
		t.Fatalf("expected later runtime event to remain unread until hydration completes, remaining=%d", len(runtimeEvents))
	}
	msgs := collectCmdMessages(t, cmd)
	var refresh runtimeTranscriptRefreshedMsg
	refreshFound := false
	for _, msg := range msgs {
		switch typed := msg.(type) {
		case runtimeTranscriptRefreshedMsg:
			refresh = typed
			refreshFound = true
		case runtimeEventBatchMsg:
			t.Fatalf("did not expect runtime stream to resume before hydration completes, got %+v", typed)
		}
	}
	if !refreshFound {
		t.Fatalf("expected authoritative transcript refresh for conversation update, got %+v", msgs)
	}

	next, followCmd := updated.Update(refresh)
	updated = next.(*uiModel)
	if updated.waitRuntimeEventAfterHydration {
		t.Fatal("expected hydration fence cleared after transcript refresh applies")
	}
	followMsgs := collectCmdMessages(t, followCmd)
	resumed := false
	for _, msg := range followMsgs {
		batch, ok := msg.(runtimeEventBatchMsg)
		if !ok {
			continue
		}
		if len(batch.events) == 1 && batch.events[0].AssistantDelta == "later" {
			resumed = true
		}
	}
	if !resumed {
		t.Fatalf("expected runtime stream to resume after hydration, got %+v", followMsgs)
	}
}

func TestHydrationRetryErrorReleasesRuntimeEventFenceWhileRetryIsScheduled(t *testing.T) {
	client := &refreshingRuntimeClient{}
	runtimeEvents := make(chan clientui.Event, 1)
	runtimeEvents <- clientui.Event{Kind: clientui.EventAssistantDelta, AssistantDelta: "later"}
	m := newProjectedTestUIModel(client, runtimeEvents, nil)
	m.startupCmds = nil
	m.runtimeTranscriptBusy = true
	m.runtimeTranscriptToken = 7
	m.waitRuntimeEventAfterHydration = true

	next, cmd := m.Update(runtimeTranscriptRefreshedMsg{token: 7, err: errors.New("temporary refresh failure")})
	updated := next.(*uiModel)
	if updated.waitRuntimeEventAfterHydration {
		t.Fatal("expected hydration retry path to release runtime event fence after failure")
	}
	if updated.runtimeTranscriptBusy {
		t.Fatal("expected hydration retry path to clear in-flight busy flag after failure")
	}
	msgs := collectCmdMessages(t, cmd)
	retryFound := false
	resumed := false
	for _, msg := range msgs {
		switch typed := msg.(type) {
		case runtimeTranscriptRetryMsg:
			retryFound = true
		case runtimeEventBatchMsg:
			if len(typed.events) == 1 && typed.events[0].AssistantDelta == "later" {
				resumed = true
			}
		}
	}
	if !retryFound {
		t.Fatalf("expected hydration retry to remain scheduled after failure, got %+v", msgs)
	}
	if !resumed {
		t.Fatalf("expected runtime event consumption to resume while retry is pending, got %+v", msgs)
	}
	if len(runtimeEvents) != 0 {
		t.Fatalf("expected resumed runtime wait to consume pending event, remaining=%d", len(runtimeEvents))
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
	out := projectRuntimeEventChannel(src, nil, stop)

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

func TestProjectRuntimeEventChannelPublishesSyntheticConversationUpdateAfterBridgeGap(t *testing.T) {
	bridge := newRuntimeEventBridge(1, nil)
	bridge.Publish(runtime.Event{Kind: runtime.EventAssistantDelta, AssistantDelta: "first"})
	bridge.Publish(runtime.Event{Kind: runtime.EventToolCallStarted, StepID: "step-1"})

	stop := make(chan struct{})
	out := projectRuntimeEventChannel(bridge.Channel(), bridge.GapChannel(), stop)
	t.Cleanup(func() { close(stop) })

	deadline := time.After(2 * time.Second)
	events := make([]clientui.Event, 0, 2)
	for len(events) < 2 {
		select {
		case evt, ok := <-out:
			if !ok {
				t.Fatalf("projected runtime channel closed early after %d events", len(events))
			}
			events = append(events, evt)
		case <-deadline:
			t.Fatalf("timed out waiting for projected runtime events, got %+v", events)
		}
	}

	sawAssistantDelta := false
	sawRecovery := false
	for _, evt := range events {
		if evt.AssistantDelta == "first" {
			sawAssistantDelta = true
		}
		if evt.Kind == clientui.EventConversationUpdated {
			sawRecovery = true
		}
	}
	if !sawAssistantDelta || !sawRecovery {
		t.Fatalf("expected projected runtime channel to emit surviving event and recovery signal, got %+v", events)
	}
}

func TestSessionActivityEventsDoNotLogDiagnosticsWhenDisabled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sub := &sessionActivityTestSubscription{events: make(chan clientui.Event, 1)}
	lines := make([]string, 0, 1)
	out, stop := startSessionActivityEvents(ctx, sub, func(context.Context) (serverapi.SessionActivitySubscription, error) {
		return sub, nil
	}, false, func(line string) {
		lines = append(lines, line)
	})
	defer stop()

	sub.events <- clientui.Event{Kind: clientui.EventAssistantDelta, AssistantDelta: "hello"}
	select {
	case evt := <-out:
		if evt.AssistantDelta != "hello" {
			t.Fatalf("assistant delta = %q, want hello", evt.AssistantDelta)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for session activity event")
	}
	stop()
	if len(lines) != 0 {
		t.Fatalf("expected no diagnostics when disabled, got %q", lines)
	}
}

func TestBridgeGapHydratesTranscriptStateInProjectedUI(t *testing.T) {
	client := &refreshingRuntimeClient{
		transcripts: []clientui.TranscriptPage{{
			SessionID:    "session-1",
			Revision:     7,
			TotalEntries: 2,
			Entries: []clientui.ChatEntry{
				{Role: "tool_call", Text: "pwd", ToolCallID: "call-1", ToolCall: &clientui.ToolCallMeta{ToolName: "shell", IsShell: true, Command: "pwd"}},
				{Role: "tool_result_ok", Text: "/tmp", ToolCallID: "call-1"},
			},
		}},
	}
	bridge := newRuntimeEventBridge(1, nil)
	// Overflow the bridge before starting the projector so recovery is deterministic.
	bridge.Publish(runtime.Event{Kind: runtime.EventAssistantDelta, AssistantDelta: "partial"})
	bridge.Publish(runtime.Event{Kind: runtime.EventToolCallStarted, StepID: "step-1"})

	stop := make(chan struct{})
	runtimeEvents := projectRuntimeEventChannel(bridge.Channel(), bridge.GapChannel(), stop)
	t.Cleanup(func() { close(stop) })

	events := make([]clientui.Event, 0, 2)
	deadline := time.After(2 * time.Second)
	for len(events) < 2 {
		select {
		case evt, ok := <-runtimeEvents:
			if !ok {
				t.Fatalf("projected runtime channel closed early after %d events", len(events))
			}
			events = append(events, evt)
		case <-deadline:
			t.Fatalf("timed out waiting for projected runtime events, got %+v", events)
		}
	}

	m := newProjectedTestUIModel(client, closedProjectedRuntimeEvents(), nil)
	m.startupCmds = nil
	for _, evt := range events {
		next, cmd := m.Update(runtimeEventMsg{event: evt})
		m = next.(*uiModel)
		msgs := collectCmdMessages(t, cmd)
		for _, msg := range msgs {
			refresh, ok := msg.(runtimeTranscriptRefreshedMsg)
			if !ok {
				continue
			}
			next, follow := m.Update(refresh)
			m = next.(*uiModel)
			_ = collectCmdMessages(t, follow)
		}
	}

	if got := client.calls; got != 1 {
		t.Fatalf("transcript refresh calls = %d, want 1", got)
	}
	if got := len(m.transcriptEntries); got != 2 {
		t.Fatalf("transcript entry count after recovery hydrate = %d, want 2", got)
	}
	if got := m.transcriptEntries[0].Role; got != "tool_call" {
		t.Fatalf("first transcript role after recovery hydrate = %q, want tool_call", got)
	}
	if got := m.transcriptEntries[0].ToolCallID; got != "call-1" {
		t.Fatalf("first transcript tool call id after recovery hydrate = %q, want call-1", got)
	}
	if got := m.transcriptEntries[1].Role; got != "tool_result_ok" {
		t.Fatalf("second transcript role after recovery hydrate = %q, want tool_result_ok", got)
	}
	loaded := m.view.LoadedTranscriptEntries()
	if len(loaded) != 2 || loaded[0].Role != "tool_call" || loaded[1].Role != "tool_result_ok" {
		t.Fatalf("expected hydrated tool transcript visible in view, got %+v", loaded)
	}
}

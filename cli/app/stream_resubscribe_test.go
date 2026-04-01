package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"builder/shared/clientui"
	"builder/shared/serverapi"
)

func TestStartSessionActivityEventsResubscribesAfterStreamGap(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	initial := &stubSessionActivitySubscription{steps: []stubSessionActivityStep{{evt: clientui.Event{Kind: clientui.EventAssistantDelta, AssistantDelta: "first"}}, {err: serverapi.ErrStreamGap}}}
	resubscribed := &stubSessionActivitySubscription{steps: []stubSessionActivityStep{{evt: clientui.Event{Kind: clientui.EventRunStateChanged, RunState: &clientui.RunState{Busy: true}}}}}
	remaining := []serverapi.SessionActivitySubscription{resubscribed}

	events, stop := startSessionActivityEvents(ctx, initial, func(context.Context) (serverapi.SessionActivitySubscription, error) {
		if len(remaining) == 0 {
			return nil, context.Canceled
		}
		next := remaining[0]
		remaining = remaining[1:]
		return next, nil
	})
	defer stop()

	first := waitSessionActivityEvent(t, events)
	if first.Kind != clientui.EventAssistantDelta || first.AssistantDelta != "first" {
		t.Fatalf("unexpected initial event: %+v", first)
	}

	rehydrate := waitSessionActivityEvent(t, events)
	if rehydrate.Kind != clientui.EventConversationUpdated {
		t.Fatalf("expected synthetic conversation update after resubscribe, got %+v", rehydrate)
	}

	second := waitSessionActivityEvent(t, events)
	if second.Kind != clientui.EventRunStateChanged || second.RunState == nil || !second.RunState.Busy {
		t.Fatalf("unexpected resubscribed event: %+v", second)
	}
}

func TestStartPendingPromptEventsResubscribesWithoutDuplicatingPendingPrompt(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	initial := &stubPromptActivitySubscription{steps: []stubPromptActivityStep{{evt: clientui.PendingPromptEvent{Type: clientui.PendingPromptEventPending, PromptID: "ask-1", SessionID: "session-1", Question: "First?"}}, {err: serverapi.ErrStreamGap}}}
	resubscribed := &stubPromptActivitySubscription{steps: []stubPromptActivityStep{{evt: clientui.PendingPromptEvent{Type: clientui.PendingPromptEventPending, PromptID: "ask-1", SessionID: "session-1", Question: "First?"}}, {evt: clientui.PendingPromptEvent{Type: clientui.PendingPromptEventPending, PromptID: "ask-2", SessionID: "session-1", Question: "Second?"}}}}
	remaining := []serverapi.PromptActivitySubscription{resubscribed}

	events, stop := startPendingPromptEvents(ctx, initial, func(context.Context) (serverapi.PromptActivitySubscription, error) {
		if len(remaining) == 0 {
			return nil, context.Canceled
		}
		next := remaining[0]
		remaining = remaining[1:]
		return next, nil
	}, stubPromptControlClient{})
	defer stop()

	first := waitPromptEvent(t, events)
	if first.req.ID != "ask-1" || first.req.Question != "First?" {
		t.Fatalf("unexpected first prompt event: %+v", first.req)
	}

	second := waitPromptEvent(t, events)
	if second.req.ID != "ask-2" || second.req.Question != "Second?" {
		t.Fatalf("unexpected second prompt event: %+v", second.req)
	}

	select {
	case duplicate := <-events:
		t.Fatalf("unexpected duplicate pending prompt after resubscribe: %+v", duplicate.req)
	case <-time.After(150 * time.Millisecond):
	}
}

type stubSessionActivityStep struct {
	evt clientui.Event
	err error
}

type stubSessionActivitySubscription struct {
	steps  []stubSessionActivityStep
	closed bool
}

func (s *stubSessionActivitySubscription) Next(ctx context.Context) (clientui.Event, error) {
	if len(s.steps) == 0 {
		<-ctx.Done()
		return clientui.Event{}, ctx.Err()
	}
	step := s.steps[0]
	s.steps = s.steps[1:]
	if step.err != nil {
		return clientui.Event{}, step.err
	}
	return step.evt, nil
}

func (s *stubSessionActivitySubscription) Close() error {
	s.closed = true
	return nil
}

type stubPromptActivityStep struct {
	evt clientui.PendingPromptEvent
	err error
}

type stubPromptActivitySubscription struct {
	steps  []stubPromptActivityStep
	closed bool
}

func (s *stubPromptActivitySubscription) Next(ctx context.Context) (clientui.PendingPromptEvent, error) {
	if len(s.steps) == 0 {
		<-ctx.Done()
		return clientui.PendingPromptEvent{}, ctx.Err()
	}
	step := s.steps[0]
	s.steps = s.steps[1:]
	if step.err != nil {
		return clientui.PendingPromptEvent{}, step.err
	}
	return step.evt, nil
}

func (s *stubPromptActivitySubscription) Close() error {
	s.closed = true
	return nil
}

type stubPromptControlClient struct{}

func (stubPromptControlClient) AnswerAsk(context.Context, serverapi.AskAnswerRequest) error {
	return nil
}

func (stubPromptControlClient) AnswerApproval(context.Context, serverapi.ApprovalAnswerRequest) error {
	return nil
}

func waitSessionActivityEvent(t *testing.T, events <-chan clientui.Event) clientui.Event {
	t.Helper()
	select {
	case evt := <-events:
		return evt
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for session activity event")
		return clientui.Event{}
	}
}

func waitPromptEvent(t *testing.T, events <-chan askEvent) askEvent {
	t.Helper()
	select {
	case evt := <-events:
		return evt
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for prompt event")
		return askEvent{}
	}
}

var _ serverapi.SessionActivitySubscription = (*stubSessionActivitySubscription)(nil)
var _ serverapi.PromptActivitySubscription = (*stubPromptActivitySubscription)(nil)

func TestStubSubscriptionsSatisfyInterfaces(t *testing.T) {
	if errors.Is(nil, context.Canceled) {
		t.Fatal("unreachable")
	}
}

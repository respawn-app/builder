package app

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"builder/server/tools/askquestion"
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
	}, nil, stubPromptControlClient{})
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

func TestStartPendingPromptEventsResubscribeEmitsResolutionForPromptMissingFromSnapshot(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	initial := &stubPromptActivitySubscription{steps: []stubPromptActivityStep{{evt: clientui.PendingPromptEvent{Type: clientui.PendingPromptEventPending, PromptID: "ask-1", SessionID: "session-1", Question: "First?"}}, {err: serverapi.ErrStreamGap}}}
	resubscribed := &stubPromptActivitySubscription{steps: []stubPromptActivityStep{{evt: clientui.PendingPromptEvent{Type: clientui.PendingPromptEventPending, PromptID: "ask-2", SessionID: "session-1", Question: "Second?"}}}}
	remaining := []serverapi.PromptActivitySubscription{resubscribed}

	events, stop := startPendingPromptEvents(ctx, initial, func(context.Context) (serverapi.PromptActivitySubscription, error) {
		if len(remaining) == 0 {
			return nil, context.Canceled
		}
		next := remaining[0]
		remaining = remaining[1:]
		return next, nil
	}, func(context.Context) (map[string]struct{}, error) {
		return map[string]struct{}{"ask-2": {}}, nil
	}, stubPromptControlClient{})
	defer stop()

	first := waitPromptEvent(t, events)
	if first.req.ID != "ask-1" {
		t.Fatalf("unexpected first prompt event: %+v", first.req)
	}
	resolved := waitPromptEvent(t, events)
	if !resolved.isResolution() || resolved.promptID() != "ask-1" {
		t.Fatalf("expected resolution event for ask-1 after resubscribe, got %+v", resolved)
	}
	second := waitPromptEvent(t, events)
	if second.req.ID != "ask-2" || second.req.Question != "Second?" {
		t.Fatalf("unexpected second prompt event: %+v", second.req)
	}
}

func TestStartPendingPromptEventsRetriesResubscribeWhenSnapshotReadFails(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	initial := &stubPromptActivitySubscription{steps: []stubPromptActivityStep{{evt: clientui.PendingPromptEvent{Type: clientui.PendingPromptEventPending, PromptID: "ask-1", SessionID: "session-1", Question: "First?"}}, {err: serverapi.ErrStreamGap}}}
	firstResubscribe := &stubPromptActivitySubscription{}
	secondResubscribe := &stubPromptActivitySubscription{steps: []stubPromptActivityStep{{evt: clientui.PendingPromptEvent{Type: clientui.PendingPromptEventPending, PromptID: "ask-2", SessionID: "session-1", Question: "Second?"}}}}
	remaining := []serverapi.PromptActivitySubscription{firstResubscribe, secondResubscribe}
	snapshotCalls := 0

	events, stop := startPendingPromptEvents(ctx, initial, func(context.Context) (serverapi.PromptActivitySubscription, error) {
		if len(remaining) == 0 {
			return nil, context.Canceled
		}
		next := remaining[0]
		remaining = remaining[1:]
		return next, nil
	}, func(context.Context) (map[string]struct{}, error) {
		snapshotCalls++
		if snapshotCalls == 1 {
			return nil, errors.New("snapshot unavailable")
		}
		return map[string]struct{}{"ask-2": {}}, nil
	}, stubPromptControlClient{})
	defer stop()

	first := waitPromptEvent(t, events)
	if first.req.ID != "ask-1" {
		t.Fatalf("unexpected first prompt event: %+v", first.req)
	}
	resolved := waitPromptEventWithin(t, events, 2*time.Second)
	if !resolved.isResolution() || resolved.promptID() != "ask-1" {
		t.Fatalf("expected resolution event for ask-1 after successful retry, got %+v", resolved)
	}
	second := waitPromptEventWithin(t, events, 2*time.Second)
	if second.req.ID != "ask-2" || second.req.Question != "Second?" {
		t.Fatalf("unexpected second prompt event: %+v", second.req)
	}
	if snapshotCalls != 2 {
		t.Fatalf("snapshot calls = %d, want 2", snapshotCalls)
	}
	if !firstResubscribe.closed {
		t.Fatal("expected failed resubscribe stream to be closed")
	}
}

func TestPendingPromptEventRequeuesWhenAnswerRPCFails(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	initial := &stubPromptActivitySubscription{steps: []stubPromptActivityStep{{evt: clientui.PendingPromptEvent{Type: clientui.PendingPromptEventPending, PromptID: "ask-1", SessionID: "session-1", Question: "First?"}}}}
	control := &retryingPromptControlClient{askErr: errors.New("transport down")}

	events, stop := startPendingPromptEvents(ctx, initial, func(context.Context) (serverapi.PromptActivitySubscription, error) {
		return nil, context.Canceled
	}, nil, control)
	defer stop()

	first := waitPromptEvent(t, events)
	if first.req.ID != "ask-1" {
		t.Fatalf("unexpected first prompt id: %q", first.req.ID)
	}
	first.reply <- askReply{response: askquestion.Response{RequestID: first.req.ID, Answer: "handled"}}

	retried := waitPromptEvent(t, events)
	if retried.req.ID != "ask-1" || retried.req.Question != "First?" {
		t.Fatalf("unexpected retried prompt event: %+v", retried.req)
	}
	if got := control.askCallCount(); got != 1 {
		t.Fatalf("AnswerAsk call count = %d, want 1", got)
	}
	if retried.reply == nil {
		t.Fatal("retried prompt reply channel is nil")
	}
	if retried.reply == first.reply {
		t.Fatal("retried prompt should use a fresh reply channel")
	}
	close(retried.reply)
	stop()
	select {
	case _, ok := <-events:
		if ok {
			t.Fatal("expected prompt channel to close after stop")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for prompt channel to close")
	}
}

func TestPendingPromptEventRetryAfterStopDoesNotPanic(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	initial := &stubPromptActivitySubscription{steps: []stubPromptActivityStep{{evt: clientui.PendingPromptEvent{Type: clientui.PendingPromptEventPending, PromptID: "ask-1", SessionID: "session-1", Question: "First?"}}}}
	control := &retryingPromptControlClient{askErr: errors.New("transport down")}

	events, stop := startPendingPromptEvents(ctx, initial, func(context.Context) (serverapi.PromptActivitySubscription, error) {
		return nil, context.Canceled
	}, nil, control)

	first := waitPromptEvent(t, events)
	stop()
	first.reply <- askReply{response: askquestion.Response{RequestID: first.req.ID, Answer: "handled"}}
	select {
	case _, ok := <-events:
		if ok {
			t.Fatal("expected prompt channel to close after stop")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for prompt channel to close")
	}
	time.Sleep(150 * time.Millisecond)
}

func TestStartPendingPromptEventsEmitsResolutionEvent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	initial := &stubPromptActivitySubscription{steps: []stubPromptActivityStep{
		{evt: clientui.PendingPromptEvent{Type: clientui.PendingPromptEventPending, PromptID: "ask-1", SessionID: "session-1", Question: "First?"}},
		{evt: clientui.PendingPromptEvent{Type: clientui.PendingPromptEventResolved, PromptID: "ask-1", SessionID: "session-1"}},
	}}

	events, stop := startPendingPromptEvents(ctx, initial, func(context.Context) (serverapi.PromptActivitySubscription, error) {
		return nil, context.Canceled
	}, nil, stubPromptControlClient{})
	defer stop()

	first := waitPromptEvent(t, events)
	if first.req.ID != "ask-1" {
		t.Fatalf("unexpected first prompt event: %+v", first.req)
	}
	resolved := waitPromptEvent(t, events)
	if !resolved.isResolution() || resolved.promptID() != "ask-1" {
		t.Fatalf("expected resolution event for ask-1, got %+v", resolved)
	}
}

func TestPendingPromptEventDoesNotRequeueOnTerminalAnswerError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	initial := &stubPromptActivitySubscription{steps: []stubPromptActivityStep{{evt: clientui.PendingPromptEvent{Type: clientui.PendingPromptEventPending, PromptID: "ask-1", SessionID: "session-1", Question: "First?"}}}}
	control := &retryingPromptControlClient{askErr: serverapi.ErrPromptAlreadyResolved}

	events, stop := startPendingPromptEvents(ctx, initial, func(context.Context) (serverapi.PromptActivitySubscription, error) {
		return nil, context.Canceled
	}, nil, control)
	defer stop()

	first := waitPromptEvent(t, events)
	first.reply <- askReply{response: askquestion.Response{RequestID: first.req.ID, Answer: "handled"}}
	select {
	case retried := <-events:
		t.Fatalf("did not expect retry after terminal prompt error: %+v", retried.req)
	case <-time.After(150 * time.Millisecond):
	}
	if got := control.askCallCount(); got != 1 {
		t.Fatalf("AnswerAsk call count = %d, want 1", got)
	}
}

func TestPendingPromptEventDoesNotRequeueAfterPromptAlreadyResolvedLocally(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	initial := &stubPromptActivitySubscription{steps: []stubPromptActivityStep{
		{evt: clientui.PendingPromptEvent{Type: clientui.PendingPromptEventPending, PromptID: "ask-1", SessionID: "session-1", Question: "First?"}},
		{evt: clientui.PendingPromptEvent{Type: clientui.PendingPromptEventResolved, PromptID: "ask-1", SessionID: "session-1"}},
	}}
	control := &retryingPromptControlClient{askErr: errors.New("transport down")}

	events, stop := startPendingPromptEvents(ctx, initial, func(context.Context) (serverapi.PromptActivitySubscription, error) {
		return nil, context.Canceled
	}, nil, control)
	defer stop()

	first := waitPromptEvent(t, events)
	if first.req.ID != "ask-1" {
		t.Fatalf("unexpected first prompt event: %+v", first.req)
	}
	first.reply <- askReply{response: askquestion.Response{RequestID: first.req.ID, Answer: "handled"}}

	resolved := waitPromptEvent(t, events)
	if !resolved.isResolution() || resolved.promptID() != "ask-1" {
		t.Fatalf("expected prompt resolution event, got %+v", resolved)
	}
	select {
	case retried := <-events:
		t.Fatalf("did not expect stale retry after local resolution: %+v", retried.req)
	case <-time.After(150 * time.Millisecond):
	}
	if got := control.askCallCount(); got != 1 {
		t.Fatalf("AnswerAsk call count = %d, want 1", got)
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

type retryingPromptControlClient struct {
	mu                 sync.Mutex
	askErr             error
	approvalErr        error
	askCalls           int
	approvalCallCountV int
}

func (c *retryingPromptControlClient) askCallCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.askCalls
}

func (c *retryingPromptControlClient) AnswerAsk(context.Context, serverapi.AskAnswerRequest) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.askCalls++
	return c.askErr
}

func (c *retryingPromptControlClient) AnswerApproval(context.Context, serverapi.ApprovalAnswerRequest) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.approvalCallCountV++
	return c.approvalErr
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
	return waitPromptEventWithin(t, events, time.Second)
}

func waitPromptEventWithin(t *testing.T, events <-chan askEvent, timeout time.Duration) askEvent {
	t.Helper()
	select {
	case evt := <-events:
		return evt
	case <-time.After(timeout):
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

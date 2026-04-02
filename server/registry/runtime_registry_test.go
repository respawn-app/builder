package registry

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"builder/server/primaryrun"
	"builder/server/runtime"
	askquestion "builder/server/tools/askquestion"
	"builder/shared/clientui"
	"builder/shared/serverapi"
)

func TestRuntimeRegistryBroadcastsSessionActivityToMultipleSubscribers(t *testing.T) {
	registry := NewRuntimeRegistry()
	engine := &runtime.Engine{}
	registry.Register("session-1", engine)
	t.Cleanup(func() { registry.Unregister("session-1", engine) })

	first, err := registry.SubscribeSessionActivity(context.Background(), "session-1")
	if err != nil {
		t.Fatalf("SubscribeSessionActivity first: %v", err)
	}
	defer func() { _ = first.Close() }()
	second, err := registry.SubscribeSessionActivity(context.Background(), "session-1")
	if err != nil {
		t.Fatalf("SubscribeSessionActivity second: %v", err)
	}
	defer func() { _ = second.Close() }()

	registry.PublishRuntimeEvent("session-1", runtime.Event{Kind: runtime.EventConversationUpdated, StepID: "step-1"})

	ctx := context.Background()
	firstEvt, err := first.Next(ctx)
	if err != nil {
		t.Fatalf("first.Next: %v", err)
	}
	secondEvt, err := second.Next(ctx)
	if err != nil {
		t.Fatalf("second.Next: %v", err)
	}
	if firstEvt.Kind != clientui.EventConversationUpdated || secondEvt.Kind != clientui.EventConversationUpdated {
		t.Fatalf("unexpected events: first=%+v second=%+v", firstEvt, secondEvt)
	}
	if firstEvt.StepID != "step-1" || secondEvt.StepID != "step-1" {
		t.Fatalf("unexpected step ids: first=%+v second=%+v", firstEvt, secondEvt)
	}
}

func TestRuntimeRegistryClosesLaggedSubscriberWithGapError(t *testing.T) {
	registry := NewRuntimeRegistry()
	engine := &runtime.Engine{}
	registry.Register("session-1", engine)
	t.Cleanup(func() { registry.Unregister("session-1", engine) })

	sub, err := registry.SubscribeSessionActivity(context.Background(), "session-1")
	if err != nil {
		t.Fatalf("SubscribeSessionActivity: %v", err)
	}
	defer func() { _ = sub.Close() }()

	for i := 0; i <= sessionActivityBufferSize; i++ {
		registry.PublishRuntimeEvent("session-1", runtime.Event{Kind: runtime.EventConversationUpdated})
	}

	for i := 0; i < sessionActivityBufferSize; i++ {
		evt, err := sub.Next(context.Background())
		if err != nil {
			t.Fatalf("unexpected early stream error after %d events: %v", i, err)
		}
		if evt.Kind != clientui.EventConversationUpdated {
			t.Fatalf("unexpected event at %d: %+v", i, evt)
		}
	}
	if _, err := sub.Next(context.Background()); !errors.Is(err, serverapi.ErrStreamGap) {
		t.Fatalf("expected gap error, got %v", err)
	}
}

func TestRuntimeRegistryRejectsInactiveSessionActivityStreamWithUnavailableError(t *testing.T) {
	registry := NewRuntimeRegistry()
	if _, err := registry.SubscribeSessionActivity(context.Background(), "missing-session"); !errors.Is(err, serverapi.ErrStreamUnavailable) {
		t.Fatalf("expected unavailable error, got %v", err)
	}
}

func TestRuntimeRegistryNormalizesSessionActivitySubscriptionFailures(t *testing.T) {
	sub := newSessionActivityHub().subscribe()
	sub.closeWithError(errors.New("writer failed"))
	if _, err := sub.Next(context.Background()); !errors.Is(err, serverapi.ErrStreamFailed) {
		t.Fatalf("expected stream failed error, got %v", err)
	}
}

func TestRuntimeRegistryPassesThroughSessionActivityEOF(t *testing.T) {
	sub := newSessionActivityHub().subscribe()
	sub.closeWithError(io.EOF)
	if _, err := sub.Next(context.Background()); !errors.Is(err, io.EOF) {
		t.Fatalf("expected EOF, got %v", err)
	}
}

func TestRuntimeRegistryPassesThroughSessionActivityContextCanceled(t *testing.T) {
	sub := newSessionActivityHub().subscribe()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := sub.Next(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestRuntimeRegistryTracksPendingPromptsPerSession(t *testing.T) {
	registry := NewRuntimeRegistry()
	engine := &runtime.Engine{}
	registry.Register("session-1", engine)
	t.Cleanup(func() { registry.Unregister("session-1", engine) })

	registry.BeginPendingPrompt("session-1", askquestion.Request{ID: "ask-1", Question: "one?"})
	registry.BeginPendingPrompt("session-1", askquestion.Request{ID: "approval-1", Question: "allow?", Approval: true})

	items := registry.ListPendingPrompts("session-1")
	if len(items) != 2 {
		t.Fatalf("expected two pending prompts, got %+v", items)
	}
	if items[0].Request.ID != "ask-1" || items[1].Request.ID != "approval-1" {
		t.Fatalf("unexpected pending prompts ordering: %+v", items)
	}

	registry.CompletePendingPrompt("session-1", "ask-1")
	items = registry.ListPendingPrompts("session-1")
	if len(items) != 1 || items[0].Request.ID != "approval-1" {
		t.Fatalf("unexpected pending prompts after completion: %+v", items)
	}

	registry.Unregister("session-1", engine)
	if items := registry.ListPendingPrompts("session-1"); len(items) != 0 {
		t.Fatalf("expected no pending prompts after unregister, got %+v", items)
	}
}

func TestRuntimeRegistrySubmitPromptResponseRemovesPendingPromptBeforeWaiterReturns(t *testing.T) {
	registry := NewRuntimeRegistry()
	engine := &runtime.Engine{}
	registry.Register("session-1", engine)
	t.Cleanup(func() { registry.Unregister("session-1", engine) })

	responseDone := make(chan error, 1)
	go func() {
		_, err := registry.AwaitPromptResponse(context.Background(), "session-1", askquestion.Request{ID: "ask-1", Question: "Proceed?"})
		responseDone <- err
	}()

	deadline := time.Now().Add(time.Second)
	for {
		items := registry.ListPendingPrompts("session-1")
		if len(items) == 1 && items[0].Request.ID == "ask-1" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("pending prompt was not registered: %+v", items)
		}
		time.Sleep(10 * time.Millisecond)
	}

	if err := registry.SubmitPromptResponse("session-1", askquestion.Response{RequestID: "ask-1", Answer: "yes"}, nil); err != nil {
		t.Fatalf("SubmitPromptResponse: %v", err)
	}
	if items := registry.ListPendingPrompts("session-1"); len(items) != 0 {
		t.Fatalf("expected pending prompt removed immediately, got %+v", items)
	}
	select {
	case err := <-responseDone:
		if err != nil {
			t.Fatalf("AwaitPromptResponse error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for prompt response")
	}
}

func TestRuntimeRegistryDoesNotUnregisterNewerRuntimeForSameSession(t *testing.T) {
	registry := NewRuntimeRegistry()
	older := &runtime.Engine{}
	newer := &runtime.Engine{}
	registry.Register("session-1", older)
	registry.Register("session-1", newer)
	t.Cleanup(func() { registry.Unregister("session-1", newer) })

	registry.Unregister("session-1", older)

	resolved, err := registry.ResolveRuntime(context.Background(), "session-1")
	if err != nil {
		t.Fatalf("ResolveRuntime: %v", err)
	}
	if resolved != newer {
		t.Fatalf("expected newer runtime to remain registered, got %p want %p", resolved, newer)
	}

	if _, err := registry.SubscribeSessionActivity(context.Background(), "session-1"); err != nil {
		t.Fatalf("SubscribeSessionActivity after stale unregister: %v", err)
	}
}

func TestRuntimeRegistryAcquirePrimaryRunEnforcesSingleLeasePerSession(t *testing.T) {
	registry := NewRuntimeRegistry()
	lease, err := registry.AcquirePrimaryRun("session-1")
	if err != nil {
		t.Fatalf("AcquirePrimaryRun first: %v", err)
	}
	if _, err := registry.AcquirePrimaryRun("session-1"); !errors.Is(err, primaryrun.ErrActivePrimaryRun) {
		t.Fatalf("AcquirePrimaryRun second error = %v, want active primary run", err)
	}
	lease.Release()
	lease.Release()
	if _, err := registry.AcquirePrimaryRun("session-1"); err != nil {
		t.Fatalf("AcquirePrimaryRun after release: %v", err)
	}
	if _, err := registry.AcquirePrimaryRun("session-2"); err != nil {
		t.Fatalf("AcquirePrimaryRun other session: %v", err)
	}
}

func TestRuntimeEntryClosePendingPromptsDoesNotBlockWhenResponseAlreadyBuffered(t *testing.T) {
	entry := &runtimeEntry{pendingPrompt: map[string]*pendingPromptEntry{}}
	pending := &pendingPromptEntry{
		PendingPromptSnapshot: PendingPromptSnapshot{Request: askquestion.Request{ID: "ask-1"}},
		response:              make(chan promptResponseResult, 1),
	}
	pending.response <- promptResponseResult{response: askquestion.Response{RequestID: "ask-1"}}
	entry.pendingPrompt["ask-1"] = pending

	done := make(chan struct{})
	go func() {
		entry.closePendingPrompts(io.EOF)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("closePendingPrompts blocked with buffered response")
	}
}

func TestRuntimeRegistryClearsPrimaryRunLeaseWhenRuntimeIsReplacedOrRemoved(t *testing.T) {
	registry := NewRuntimeRegistry()
	older := &runtime.Engine{}
	newer := &runtime.Engine{}

	registry.Register("session-1", older)
	lease, err := registry.AcquirePrimaryRun("session-1")
	if err != nil {
		t.Fatalf("AcquirePrimaryRun first: %v", err)
	}
	registry.Register("session-1", newer)
	lease.Release()

	secondLease, err := registry.AcquirePrimaryRun("session-1")
	if err != nil {
		t.Fatalf("AcquirePrimaryRun after replace: %v", err)
	}
	secondLease.Release()

	registry.Unregister("session-1", newer)
	thirdLease, err := registry.AcquirePrimaryRun("session-1")
	if err != nil {
		t.Fatalf("AcquirePrimaryRun after unregister: %v", err)
	}
	thirdLease.Release()
}

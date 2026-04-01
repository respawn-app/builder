package registry

import (
	"context"
	"errors"
	"testing"

	"builder/server/primaryrun"
	"builder/server/runtime"
	askquestion "builder/server/tools/askquestion"
	"builder/shared/clientui"
	"builder/shared/serverapi"
)

func TestRuntimeRegistryBroadcastsSessionActivityToMultipleSubscribers(t *testing.T) {
	registry := NewRuntimeRegistry()
	registry.Register("session-1", &runtime.Engine{})
	t.Cleanup(func() { registry.Unregister("session-1") })

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
	registry.Register("session-1", &runtime.Engine{})
	t.Cleanup(func() { registry.Unregister("session-1") })

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
	if _, err := sub.Next(context.Background()); !errors.Is(err, serverapi.ErrSessionActivityGap) {
		t.Fatalf("expected gap error, got %v", err)
	}
}

func TestRuntimeRegistryTracksPendingPromptsPerSession(t *testing.T) {
	registry := NewRuntimeRegistry()
	registry.Register("session-1", &runtime.Engine{})
	t.Cleanup(func() { registry.Unregister("session-1") })

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

	registry.Unregister("session-1")
	if items := registry.ListPendingPrompts("session-1"); len(items) != 0 {
		t.Fatalf("expected no pending prompts after unregister, got %+v", items)
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

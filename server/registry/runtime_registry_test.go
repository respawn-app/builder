package registry

import (
	"context"
	"errors"
	"testing"

	"builder/server/runtime"
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

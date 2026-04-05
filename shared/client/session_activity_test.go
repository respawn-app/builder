package client

import (
	"context"
	"io"
	"testing"

	"builder/shared/clientui"
	"builder/shared/serverapi"
)

type stubSessionActivityService struct {
	req serverapi.SessionActivitySubscribeRequest
	sub serverapi.SessionActivitySubscription
	err error
}

type stubSessionActivitySubscription struct {
	event  clientui.Event
	err    error
	closed bool
}

func (s *stubSessionActivityService) SubscribeSessionActivity(_ context.Context, req serverapi.SessionActivitySubscribeRequest) (serverapi.SessionActivitySubscription, error) {
	s.req = req
	if s.err != nil {
		return nil, s.err
	}
	return s.sub, nil
}

func (s *stubSessionActivitySubscription) Next(context.Context) (clientui.Event, error) {
	if s.err != nil {
		return clientui.Event{}, s.err
	}
	err := io.EOF
	s.err = err
	return s.event, nil
}

func (s *stubSessionActivitySubscription) Close() error {
	s.closed = true
	return nil
}

func TestLoopbackSessionActivityClientDelegatesToService(t *testing.T) {
	sub := &stubSessionActivitySubscription{event: clientui.Event{Kind: clientui.EventConversationUpdated, StepID: "step-1"}}
	svc := &stubSessionActivityService{sub: sub}
	client := NewLoopbackSessionActivityClient(svc)

	stream, err := client.SubscribeSessionActivity(context.Background(), serverapi.SessionActivitySubscribeRequest{SessionID: "session-1"})
	if err != nil {
		t.Fatalf("SubscribeSessionActivity: %v", err)
	}
	if svc.req.SessionID != "session-1" {
		t.Fatalf("unexpected request: %+v", svc.req)
	}
	evt, err := stream.Next(context.Background())
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if evt.Kind != clientui.EventConversationUpdated || evt.StepID != "step-1" {
		t.Fatalf("unexpected event: %+v", evt)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !sub.closed {
		t.Fatal("expected subscription to close")
	}
}

func TestLoopbackSessionActivityClientRequiresService(t *testing.T) {
	client := NewLoopbackSessionActivityClient(nil)
	if _, err := client.SubscribeSessionActivity(context.Background(), serverapi.SessionActivitySubscribeRequest{SessionID: "session-1"}); err == nil {
		t.Fatal("expected SubscribeSessionActivity to fail without service")
	}
}

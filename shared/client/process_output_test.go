package client

import (
	"context"
	"io"
	"testing"

	"builder/shared/clientui"
	"builder/shared/serverapi"
)

type stubProcessOutputService struct {
	req serverapi.ProcessOutputSubscribeRequest
	sub serverapi.ProcessOutputSubscription
	err error
}

func (s *stubProcessOutputService) SubscribeProcessOutput(_ context.Context, req serverapi.ProcessOutputSubscribeRequest) (serverapi.ProcessOutputSubscription, error) {
	s.req = req
	return s.sub, s.err
}

type stubProcessOutputSubscription struct {
	next clientui.ProcessOutputChunk
	err  error
}

func (s *stubProcessOutputSubscription) Next(context.Context) (clientui.ProcessOutputChunk, error) {
	if s.err != nil {
		return clientui.ProcessOutputChunk{}, s.err
	}
	chunk := s.next
	s.err = io.EOF
	return chunk, nil
}

func (s *stubProcessOutputSubscription) Close() error { return nil }

func TestLoopbackProcessOutputClientDelegatesToService(t *testing.T) {
	svc := &stubProcessOutputService{sub: &stubProcessOutputSubscription{next: clientui.ProcessOutputChunk{ProcessID: "proc-1", OffsetBytes: 5, NextOffsetBytes: 10, Text: "hello"}}}
	client := NewLoopbackProcessOutputClient(svc)
	sub, err := client.SubscribeProcessOutput(context.Background(), serverapi.ProcessOutputSubscribeRequest{ProcessID: "proc-1", OffsetBytes: 5})
	if err != nil {
		t.Fatalf("SubscribeProcessOutput: %v", err)
	}
	if svc.req.ProcessID != "proc-1" || svc.req.OffsetBytes != 5 {
		t.Fatalf("unexpected subscribe request: %+v", svc.req)
	}
	chunk, err := sub.Next(context.Background())
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if chunk.ProcessID != "proc-1" || chunk.OffsetBytes != 5 || chunk.NextOffsetBytes != 10 || chunk.Text != "hello" {
		t.Fatalf("unexpected chunk: %+v", chunk)
	}
}

func TestLoopbackProcessOutputClientRequiresService(t *testing.T) {
	client := NewLoopbackProcessOutputClient(nil)
	if _, err := client.SubscribeProcessOutput(context.Background(), serverapi.ProcessOutputSubscribeRequest{ProcessID: "proc-1"}); err == nil {
		t.Fatal("expected SubscribeProcessOutput to fail without service")
	}
}

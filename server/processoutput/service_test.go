package processoutput

import (
	"context"
	"io"
	"testing"

	shelltool "builder/server/tools/shell"
	"builder/shared/serverapi"
)

type stubSubscriber struct {
	sub shelltool.OutputSubscription
	err error
}

func (s *stubSubscriber) SubscribeOutput(context.Context, string, int64) (shelltool.OutputSubscription, error) {
	return s.sub, s.err
}

type stubShellOutputSubscription struct {
	chunk shelltool.OutputChunk
	err   error
}

func (s *stubShellOutputSubscription) Next(context.Context) (shelltool.OutputChunk, error) {
	if s.err != nil {
		return shelltool.OutputChunk{}, s.err
	}
	chunk := s.chunk
	s.err = io.EOF
	return chunk, nil
}

func (s *stubShellOutputSubscription) Close() error { return nil }

func TestServiceSubscribesAndProjectsChunks(t *testing.T) {
	svc := NewService(&stubSubscriber{sub: &stubShellOutputSubscription{chunk: shelltool.OutputChunk{ProcessID: "proc-1", OffsetBytes: 10, Text: "hello"}}})
	sub, err := svc.SubscribeProcessOutput(context.Background(), serverapi.ProcessOutputSubscribeRequest{ProcessID: "proc-1", OffsetBytes: 10})
	if err != nil {
		t.Fatalf("SubscribeProcessOutput: %v", err)
	}
	chunk, err := sub.Next(context.Background())
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if chunk.ProcessID != "proc-1" || chunk.OffsetBytes != 10 || chunk.Text != "hello" {
		t.Fatalf("unexpected chunk: %+v", chunk)
	}
}

func TestServiceValidatesRequest(t *testing.T) {
	if _, err := NewService(&stubSubscriber{}).SubscribeProcessOutput(context.Background(), serverapi.ProcessOutputSubscribeRequest{}); err == nil {
		t.Fatal("expected validation error")
	}
}

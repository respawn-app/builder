package processoutput

import (
	"context"
	"errors"
	"io"
	"testing"

	shelltool "builder/server/tools/shell"
	"builder/shared/serverapi"
)

type stubSubscriber struct {
	sub shelltool.OutputSubscription
	err error
}

type stubProcessSource struct {
	snapshot shelltool.Snapshot
	err      error
}

func (s *stubSubscriber) SubscribeOutput(context.Context, string, int64) (shelltool.OutputSubscription, error) {
	return s.sub, s.err
}

func (s *stubProcessSource) Snapshot(string) (shelltool.Snapshot, error) {
	if s.err != nil {
		return shelltool.Snapshot{}, s.err
	}
	return s.snapshot, nil
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
	svc := NewService(
		&stubSubscriber{sub: &stubShellOutputSubscription{chunk: shelltool.OutputChunk{ProcessID: "proc-1", OffsetBytes: 10, Text: "hello"}}},
		&stubProcessSource{snapshot: shelltool.Snapshot{ID: "proc-1", LogPath: "/tmp/proc-1.log", OutputAvailable: true, OutputRetainedToBytes: 10}},
	)
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
	if _, err := NewService(&stubSubscriber{}, &stubProcessSource{}).SubscribeProcessOutput(context.Background(), serverapi.ProcessOutputSubscribeRequest{}); err == nil {
		t.Fatal("expected validation error")
	}
}

func TestServiceRejectsUnavailableStream(t *testing.T) {
	svc := NewService(&stubSubscriber{}, &stubProcessSource{err: errors.New("missing")})
	if _, err := svc.SubscribeProcessOutput(context.Background(), serverapi.ProcessOutputSubscribeRequest{ProcessID: "proc-1"}); !errors.Is(err, serverapi.ErrStreamUnavailable) {
		t.Fatalf("expected unavailable error, got %v", err)
	}
}

func TestServiceRejectsOffsetOutsideRetainedRange(t *testing.T) {
	svc := NewService(
		&stubSubscriber{},
		&stubProcessSource{snapshot: shelltool.Snapshot{ID: "proc-1", LogPath: "/tmp/proc-1.log", OutputAvailable: true, OutputRetainedFromBytes: 0, OutputRetainedToBytes: 5}},
	)
	if _, err := svc.SubscribeProcessOutput(context.Background(), serverapi.ProcessOutputSubscribeRequest{ProcessID: "proc-1", OffsetBytes: 6}); !errors.Is(err, serverapi.ErrStreamGap) {
		t.Fatalf("expected gap error, got %v", err)
	}
}

func TestServiceNormalizesSubscriptionNextFailures(t *testing.T) {
	svc := NewService(
		&stubSubscriber{sub: &stubShellOutputSubscription{err: errors.New("disk read failed")}},
		&stubProcessSource{snapshot: shelltool.Snapshot{ID: "proc-1", LogPath: "/tmp/proc-1.log", OutputAvailable: true, OutputRetainedToBytes: 1}},
	)
	sub, err := svc.SubscribeProcessOutput(context.Background(), serverapi.ProcessOutputSubscribeRequest{ProcessID: "proc-1"})
	if err != nil {
		t.Fatalf("SubscribeProcessOutput: %v", err)
	}
	if _, err := sub.Next(context.Background()); !errors.Is(err, serverapi.ErrStreamFailed) {
		t.Fatalf("expected stream failed error, got %v", err)
	}
}

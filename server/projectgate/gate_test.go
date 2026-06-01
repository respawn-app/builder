package projectgate

import (
	"context"
	"testing"
	"time"
)

func TestGateSerializesSameProject(t *testing.T) {
	gate := New()
	entered := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)

	go func() {
		done <- gate.WithProject(context.Background(), "project-1", func(context.Context) error {
			close(entered)
			<-release
			return nil
		})
	}()
	<-entered

	blocked := make(chan struct{})
	secondDone := make(chan error, 1)
	go func() {
		secondDone <- gate.WithProject(context.Background(), "project-1", func(context.Context) error {
			close(blocked)
			return nil
		})
	}()

	select {
	case <-blocked:
		t.Fatal("second critical section entered before first released")
	case <-time.After(20 * time.Millisecond):
	}

	close(release)
	if err := <-done; err != nil {
		t.Fatalf("first critical section: %v", err)
	}
	select {
	case <-blocked:
	case <-time.After(time.Second):
		t.Fatal("second critical section did not enter after first released")
	}
	if err := <-secondDone; err != nil {
		t.Fatalf("second critical section: %v", err)
	}
}

func TestGateHonorsContextWhileWaiting(t *testing.T) {
	gate := New()
	entered := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- gate.WithProject(context.Background(), "project-1", func(context.Context) error {
			close(entered)
			<-release
			return nil
		})
	}()
	<-entered

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := gate.WithProject(ctx, "project-1", func(context.Context) error {
		t.Fatal("canceled waiter entered critical section")
		return nil
	})
	if err == nil {
		t.Fatal("expected canceled context error")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("first critical section: %v", err)
	}
}

func TestGateAllowsNestedSameProjectCriticalSection(t *testing.T) {
	gate := New()
	err := gate.WithProject(context.Background(), "project-1", func(ctx context.Context) error {
		return gate.WithProject(ctx, "project-1", func(context.Context) error {
			return nil
		})
	})
	if err != nil {
		t.Fatalf("nested critical section: %v", err)
	}
}

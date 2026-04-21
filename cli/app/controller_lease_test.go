package app

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestControllerLeaseManagerSharesInFlightRecovery(t *testing.T) {
	manager := newControllerLeaseManager("lease-old")
	started := make(chan struct{})
	release := make(chan struct{})
	var closeStarted sync.Once
	var calls atomic.Int32
	manager.SetRecoverFunc(func(context.Context) (string, error) {
		calls.Add(1)
		closeStarted.Do(func() { close(started) })
		<-release
		return "lease-new", nil
	})

	type result struct {
		leaseID string
		err     error
	}
	results := make(chan result, 2)
	go func() {
		leaseID, err := manager.Recover(context.Background())
		results <- result{leaseID: leaseID, err: err}
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for shared recovery to start")
	}
	go func() {
		leaseID, err := manager.Recover(context.Background())
		results <- result{leaseID: leaseID, err: err}
	}()
	time.Sleep(20 * time.Millisecond)
	if got := calls.Load(); got != 1 {
		t.Fatalf("recovery call count during in-flight wait = %d, want 1", got)
	}
	close(release)

	for i := 0; i < 2; i++ {
		res := <-results
		if res.err != nil {
			t.Fatalf("Recover result error = %v", res.err)
		}
		if res.leaseID != "lease-new" {
			t.Fatalf("Recover lease id = %q, want lease-new", res.leaseID)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("recovery call count = %d, want 1", got)
	}
	if got := manager.Value(); got != "lease-new" {
		t.Fatalf("manager lease id = %q, want lease-new", got)
	}
}

func TestControllerLeaseManagerKeepsSharedRecoveryAliveAfterInitiatorCancel(t *testing.T) {
	manager := newControllerLeaseManager("lease-old")
	started := make(chan struct{})
	release := make(chan struct{})
	var closeStarted sync.Once
	var calls atomic.Int32
	manager.SetRecoverFunc(func(ctx context.Context) (string, error) {
		calls.Add(1)
		closeStarted.Do(func() { close(started) })
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-release:
			return "lease-new", nil
		}
	})

	type result struct {
		leaseID string
		err     error
	}
	results := make(chan result, 2)
	firstCtx, firstCancel := context.WithCancel(context.Background())
	defer firstCancel()
	go func() {
		leaseID, err := manager.Recover(firstCtx)
		results <- result{leaseID: leaseID, err: err}
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for recovery to start")
	}
	go func() {
		leaseID, err := manager.Recover(context.Background())
		results <- result{leaseID: leaseID, err: err}
	}()

	firstCancel()
	first := <-results
	if first.err != context.Canceled {
		t.Fatalf("first Recover error = %v, want %v", first.err, context.Canceled)
	}
	select {
	case second := <-results:
		t.Fatalf("second Recover completed before shared recovery release: %+v", second)
	case <-time.After(50 * time.Millisecond):
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("recovery call count after initiator cancel = %d, want 1", got)
	}

	close(release)
	second := <-results
	if second.err != nil {
		t.Fatalf("second Recover error = %v", second.err)
	}
	if second.leaseID != "lease-new" {
		t.Fatalf("second Recover lease id = %q, want lease-new", second.leaseID)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("recovery call count after shared recovery release = %d, want 1", got)
	}
	if got := manager.Value(); got != "lease-new" {
		t.Fatalf("manager lease id = %q, want lease-new", got)
	}
}

func TestControllerLeaseManagerRejectsEmptyRecoveredLeaseID(t *testing.T) {
	manager := newControllerLeaseManager("lease-old")
	manager.SetRecoverFunc(func(context.Context) (string, error) {
		return "   ", nil
	})

	leaseID, err := manager.Recover(context.Background())
	if !errors.Is(err, errControllerLeaseRecoveryEmptyLeaseID) {
		t.Fatalf("Recover error = %v, want errControllerLeaseRecoveryEmptyLeaseID", err)
	}
	if leaseID != "" {
		t.Fatalf("Recover lease id = %q, want empty", leaseID)
	}
	if got := manager.Value(); got != "lease-old" {
		t.Fatalf("manager lease id = %q, want lease-old", got)
	}
}

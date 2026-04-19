package rpcwire

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestNormalizeHandshakeContextErrorReturnsDeadlineExceededForTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	time.Sleep(10 * time.Millisecond)
	err := normalizeHandshakeContextError(ctx, timeoutErr{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("normalizeHandshakeContextError error = %v, want context deadline exceeded", err)
	}
}

func TestNormalizeHandshakeContextErrorPreservesNonTimeoutError(t *testing.T) {
	ctx := context.Background()
	errExpected := errors.New("boom")
	err := normalizeHandshakeContextError(ctx, errExpected)
	if !errors.Is(err, errExpected) {
		t.Fatalf("normalizeHandshakeContextError error = %v, want %v", err, errExpected)
	}
}

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return false }

//go:build unix

package rpcwire

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestUnixDialHonorsContextDuringWebSocketHandshake(t *testing.T) {
	socketPath := filepath.Join(os.TempDir(), fmt.Sprintf("rpcwire-%d.sock", time.Now().UnixNano()))
	_ = os.Remove(socketPath)
	defer func() { _ = os.Remove(socketPath) }()
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("Listen unix: %v", err)
	}
	defer func() { _ = listener.Close() }()
	accepted := make(chan struct{}, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		accepted <- struct{}{}
		time.Sleep(5 * time.Second)
	}()
	endpoint, err := NewUnixEndpoint(socketPath, "/rpc")
	if err != nil {
		t.Fatalf("NewUnixEndpoint: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = NewWebSocketTransport().Dial(ctx, endpoint)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Dial error = %v, want context deadline exceeded", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("Dial elapsed = %v, want <= 1s", elapsed)
	}
	select {
	case <-accepted:
	case <-time.After(time.Second):
		t.Fatal("expected unix listener accept")
	}
	if err := os.Remove(socketPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Remove socket path: %v", err)
	}
}

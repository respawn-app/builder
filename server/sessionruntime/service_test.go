package sessionruntime

import (
	"context"
	"errors"
	"strings"
	"testing"

	"builder/server/registry"
	"builder/server/session"
	"builder/shared/serverapi"
)

func TestInstallHandleDoesNotDoubleCountDuplicateActivationRequest(t *testing.T) {
	svc := &Service{handles: map[string]*runtimeHandle{
		"session-1": {
			refs:               1,
			activationRequests: map[string]struct{}{"req-1": {}},
			releaseRequests:    make(map[string]struct{}),
			ready:              make(chan struct{}),
		},
	}}
	close(svc.handles["session-1"].ready)

	handle, installed := svc.installHandle("session-1", "req-1", &runtimeHandle{})
	if installed {
		t.Fatal("expected duplicate activation to reuse existing handle")
	}
	if handle != svc.handles["session-1"] {
		t.Fatal("expected duplicate activation to return existing handle")
	}
	if got := svc.handles["session-1"].refs; got != 1 {
		t.Fatalf("refs = %d, want 1", got)
	}
}

func TestInstallHandleCountsDistinctActivationRequestOnExistingHandle(t *testing.T) {
	svc := &Service{handles: map[string]*runtimeHandle{
		"session-1": {
			refs:               1,
			activationRequests: map[string]struct{}{"req-1": {}},
			releaseRequests:    make(map[string]struct{}),
			ready:              make(chan struct{}),
		},
	}}
	close(svc.handles["session-1"].ready)

	handle, installed := svc.installHandle("session-1", "req-2", &runtimeHandle{})
	if installed {
		t.Fatal("expected existing handle to remain authoritative")
	}
	if handle != svc.handles["session-1"] {
		t.Fatal("expected distinct activation to return existing handle")
	}
	if got := svc.handles["session-1"].refs; got != 2 {
		t.Fatalf("refs = %d, want 2", got)
	}
}

func TestActivateSessionRuntimeWaitsForExistingHandleReady(t *testing.T) {
	handle := &runtimeHandle{
		refs:               1,
		activationRequests: map[string]struct{}{"req-1": {}},
		releaseRequests:    make(map[string]struct{}),
		ready:              make(chan struct{}),
	}
	svc := &Service{handles: map[string]*runtimeHandle{"session-1": handle}}
	done := make(chan error, 1)
	go func() {
		done <- svc.ActivateSessionRuntime(context.Background(), serverapi.SessionRuntimeActivateRequest{
			ClientRequestID: "req-2",
			SessionID:       "session-1",
			WorkspaceRoot:   "/tmp/workspace-a",
		})
	}()
	select {
	case err := <-done:
		t.Fatalf("expected activation to wait for ready handle, got %v", err)
	default:
	}
	close(handle.ready)
	if err := <-done; err != nil {
		t.Fatalf("ActivateSessionRuntime: %v", err)
	}
	if got := svc.handles["session-1"].refs; got != 2 {
		t.Fatalf("refs = %d, want 2", got)
	}
}

func TestReleaseSessionRuntimeWaitsForHandleReadyBeforeClose(t *testing.T) {
	closed := make(chan struct{}, 1)
	handle := &runtimeHandle{
		refs:               1,
		activationRequests: map[string]struct{}{"req-1": {}},
		releaseRequests:    make(map[string]struct{}),
		ready:              make(chan struct{}),
		close: func() {
			closed <- struct{}{}
		},
	}
	svc := &Service{handles: map[string]*runtimeHandle{"session-1": handle}}
	done := make(chan error, 1)
	go func() {
		done <- svc.ReleaseSessionRuntime(context.Background(), serverapi.SessionRuntimeReleaseRequest{
			ClientRequestID: "rel-1",
			SessionID:       "session-1",
		})
	}()
	select {
	case <-closed:
		t.Fatal("expected release to wait for ready handle before close")
	default:
	}
	close(handle.ready)
	if err := <-done; err != nil {
		t.Fatalf("ReleaseSessionRuntime: %v", err)
	}
	select {
	case <-closed:
	default:
		t.Fatal("expected close after ready handle release")
	}
}

func TestActivateSessionRuntimeHonorsCanceledContextBeforeInstallingHandle(t *testing.T) {
	root := t.TempDir()
	containerDir := t.TempDir()
	store, err := session.Create(containerDir, "workspace-a", "/tmp/workspace-a")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	svc := &Service{
		persistenceRoot: root,
		handles:         make(map[string]*runtimeHandle),
		sessionStores:   registry.NewSessionStoreRegistry(),
	}
	svc.sessionStores.RegisterStore(store)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = svc.ActivateSessionRuntime(ctx, serverapi.SessionRuntimeActivateRequest{ClientRequestID: "req-1", SessionID: store.Meta().SessionID, WorkspaceRoot: "/tmp/workspace-a"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ActivateSessionRuntime error = %v, want context canceled", err)
	}
	if len(svc.handles) != 0 {
		t.Fatalf("expected no installed handles after canceled activation, got %+v", svc.handles)
	}
}

func TestActivateSessionRuntimeRejectsPathLikeSessionID(t *testing.T) {
	svc := &Service{handles: make(map[string]*runtimeHandle)}
	err := svc.ActivateSessionRuntime(context.Background(), serverapi.SessionRuntimeActivateRequest{
		ClientRequestID: "req-1",
		SessionID:       "../session-1",
		WorkspaceRoot:   "/tmp/workspace-a",
	})
	if err == nil || !strings.Contains(err.Error(), "single session id") {
		t.Fatalf("expected path-like session id rejection, got %v", err)
	}
}

func TestReleaseSessionRuntimeRejectsPathLikeSessionID(t *testing.T) {
	svc := &Service{handles: make(map[string]*runtimeHandle)}
	err := svc.ReleaseSessionRuntime(context.Background(), serverapi.SessionRuntimeReleaseRequest{
		ClientRequestID: "req-1",
		SessionID:       "sessions/workspace-a/session-1",
	})
	if err == nil || !strings.Contains(err.Error(), "single session id") {
		t.Fatalf("expected path-like session id rejection, got %v", err)
	}
}

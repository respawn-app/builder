package sessionruntime

import (
	"context"
	"errors"
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
		},
	}}

	installed := svc.installHandle("session-1", "req-1", &runtimeHandle{})
	if installed {
		t.Fatal("expected duplicate activation to reuse existing handle")
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
		},
	}}

	installed := svc.installHandle("session-1", "req-2", &runtimeHandle{})
	if installed {
		t.Fatal("expected existing handle to remain authoritative")
	}
	if got := svc.handles["session-1"].refs; got != 2 {
		t.Fatalf("refs = %d, want 2", got)
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

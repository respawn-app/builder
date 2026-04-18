package sessionruntime

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"builder/server/metadata"
	"builder/server/registry"
	"builder/server/session"
	"builder/shared/config"
	"builder/shared/serverapi"
)

func TestClaimActivationReusesDuplicateRequest(t *testing.T) {
	svc := &Service{handles: map[string]*runtimeHandle{
		"session-1": {
			controllerRequestID: "req-1",
			controllerLeaseID:   "lease-1",
			ready:               make(chan struct{}),
		},
	}}
	close(svc.handles["session-1"].ready)

	handle, owner, err := svc.claimActivation("session-1", "req-1")
	if err != nil {
		t.Fatalf("claimActivation: %v", err)
	}
	if owner {
		t.Fatal("expected duplicate activation to reuse existing controller")
	}
	if handle != svc.handles["session-1"] {
		t.Fatal("expected duplicate activation to return existing handle")
	}
}

func TestClaimActivationRejectsDistinctController(t *testing.T) {
	svc := &Service{handles: map[string]*runtimeHandle{
		"session-1": {
			controllerRequestID: "req-1",
			controllerLeaseID:   "lease-1",
			ready:               make(chan struct{}),
		},
	}}
	close(svc.handles["session-1"].ready)

	_, _, err := svc.claimActivation("session-1", "req-2")
	if !errors.Is(err, serverapi.ErrSessionAlreadyControlled) {
		t.Fatalf("claimActivation error = %v, want session already controlled", err)
	}
}

func TestActivateSessionRuntimeReplaysDuplicateRequestAfterReady(t *testing.T) {
	fixture := newSessionRuntimeFixture(t)
	handle := &runtimeHandle{
		controllerRequestID: "req-1",
		controllerLeaseID:   "lease-1",
		ready:               make(chan struct{}),
	}
	fixture.service.handles = map[string]*runtimeHandle{fixture.store.Meta().SessionID: handle}
	done := make(chan serverapi.SessionRuntimeActivateResponse, 1)
	errCh := make(chan error, 1)
	go func() {
		resp, err := fixture.service.ActivateSessionRuntime(context.Background(), serverapi.SessionRuntimeActivateRequest{
			ClientRequestID: "req-1",
			SessionID:       fixture.store.Meta().SessionID,
		})
		done <- resp
		errCh <- err
	}()
	select {
	case <-done:
		t.Fatal("expected duplicate activation to wait for ready handle")
	default:
	}
	close(handle.ready)
	if err := <-errCh; err != nil {
		t.Fatalf("ActivateSessionRuntime: %v", err)
	}
	if got := (<-done).LeaseID; got != "lease-1" {
		t.Fatalf("lease id = %q, want lease-1", got)
	}
}

func TestActivateSessionRuntimeRejectsSecondControllerWhileActive(t *testing.T) {
	fixture := newSessionRuntimeFixture(t)
	handle := &runtimeHandle{
		controllerRequestID: "req-1",
		controllerLeaseID:   "lease-1",
		ready:               make(chan struct{}),
	}
	close(handle.ready)
	fixture.service.handles = map[string]*runtimeHandle{fixture.store.Meta().SessionID: handle}

	_, err := fixture.service.ActivateSessionRuntime(context.Background(), serverapi.SessionRuntimeActivateRequest{
		ClientRequestID: "req-2",
		SessionID:       fixture.store.Meta().SessionID,
	})
	if !errors.Is(err, serverapi.ErrSessionAlreadyControlled) {
		t.Fatalf("ActivateSessionRuntime error = %v, want session already controlled", err)
	}
}

func TestActivateSessionRuntimeHonorsCanceledContextBeforeInstallingHandle(t *testing.T) {
	fixture := newSessionRuntimeFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := fixture.service.ActivateSessionRuntime(ctx, serverapi.SessionRuntimeActivateRequest{ClientRequestID: "req-1", SessionID: fixture.store.Meta().SessionID})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ActivateSessionRuntime error = %v, want context canceled", err)
	}
	if len(fixture.service.handles) != 0 {
		t.Fatalf("expected no installed handles after canceled activation, got %+v", fixture.service.handles)
	}
}

func TestReleaseSessionRuntimeWaitsForHandleReadyBeforeClose(t *testing.T) {
	fixture := newSessionRuntimeFixture(t)
	lease, err := fixture.metadata.CreateRuntimeLease(context.Background(), fixture.store.Meta().SessionID, "req-1")
	if err != nil {
		t.Fatalf("CreateRuntimeLease: %v", err)
	}
	closed := make(chan struct{}, 1)
	handle := &runtimeHandle{
		controllerRequestID: "req-1",
		controllerLeaseID:   lease.LeaseID,
		ready:               make(chan struct{}),
		close: func() {
			closed <- struct{}{}
		},
	}
	fixture.service.handles = map[string]*runtimeHandle{fixture.store.Meta().SessionID: handle}
	done := make(chan error, 1)
	go func() {
		_, err := fixture.service.ReleaseSessionRuntime(context.Background(), serverapi.SessionRuntimeReleaseRequest{
			ClientRequestID: "rel-1",
			SessionID:       fixture.store.Meta().SessionID,
			LeaseID:         lease.LeaseID,
		})
		done <- err
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

func TestReleaseSessionRuntimeStillClosesHandleWhenLeaseReleaseFails(t *testing.T) {
	fixture := newSessionRuntimeFixture(t)
	closed := atomic.Int32{}
	handle := &runtimeHandle{
		controllerRequestID: "req-1",
		controllerLeaseID:   "lease-missing",
		ready:               make(chan struct{}),
		close: func() {
			closed.Add(1)
		},
	}
	close(handle.ready)
	fixture.service.handles[fixture.store.Meta().SessionID] = handle
	_, err := fixture.service.ReleaseSessionRuntime(context.Background(), serverapi.SessionRuntimeReleaseRequest{
		ClientRequestID: "rel-1",
		SessionID:       fixture.store.Meta().SessionID,
		LeaseID:         "lease-missing",
	})
	if err == nil {
		t.Fatal("expected releaseRuntimeLease error for missing lease record")
	}
	if closed.Load() != 1 {
		t.Fatalf("expected closeFn to run exactly once, got %d", closed.Load())
	}
	if _, ok := fixture.service.handles[fixture.store.Meta().SessionID]; ok {
		t.Fatal("expected runtime handle to be removed even when lease release fails")
	}
}

func TestReleaseSessionRuntimeRejectsMismatchedControllerLeaseWithoutClosingHandle(t *testing.T) {
	fixture := newSessionRuntimeFixture(t)
	lease, err := fixture.metadata.CreateRuntimeLease(context.Background(), fixture.store.Meta().SessionID, "req-1")
	if err != nil {
		t.Fatalf("CreateRuntimeLease: %v", err)
	}
	closed := atomic.Int32{}
	handle := &runtimeHandle{
		controllerRequestID: "req-1",
		controllerLeaseID:   lease.LeaseID,
		ready:               make(chan struct{}),
		close: func() {
			closed.Add(1)
		},
	}
	close(handle.ready)
	fixture.service.handles[fixture.store.Meta().SessionID] = handle

	_, err = fixture.service.ReleaseSessionRuntime(context.Background(), serverapi.SessionRuntimeReleaseRequest{
		ClientRequestID: "rel-1",
		SessionID:       fixture.store.Meta().SessionID,
		LeaseID:         "lease-other",
	})
	if !errors.Is(err, serverapi.ErrInvalidControllerLease) {
		t.Fatalf("ReleaseSessionRuntime error = %v, want invalid controller lease", err)
	}
	if closed.Load() != 0 {
		t.Fatalf("expected closeFn not to run for mismatched lease, got %d", closed.Load())
	}
	if got := fixture.service.handles[fixture.store.Meta().SessionID]; got != handle {
		t.Fatalf("expected runtime handle preserved for mismatched lease, got %+v", got)
	}
	if _, err := fixture.metadata.ReleaseRuntimeLease(context.Background(), fixture.store.Meta().SessionID, lease.LeaseID); err != nil {
		t.Fatalf("expected original runtime lease to remain releasable after mismatched release, got %v", err)
	}
}

func TestRequireControllerLeaseAcceptsActiveController(t *testing.T) {
	svc := &Service{handles: map[string]*runtimeHandle{
		"session-1": {
			controllerRequestID: "req-1",
			controllerLeaseID:   "lease-1",
			ready:               make(chan struct{}),
		},
	}}
	close(svc.handles["session-1"].ready)
	if err := svc.RequireControllerLease(context.Background(), "session-1", "lease-1"); err != nil {
		t.Fatalf("RequireControllerLease: %v", err)
	}
}

func TestRequireControllerLeaseRejectsUnknownLease(t *testing.T) {
	svc := &Service{handles: map[string]*runtimeHandle{
		"session-1": {
			controllerRequestID: "req-1",
			controllerLeaseID:   "lease-1",
			ready:               make(chan struct{}),
		},
	}}
	close(svc.handles["session-1"].ready)
	err := svc.RequireControllerLease(context.Background(), "session-1", "lease-2")
	if !errors.Is(err, serverapi.ErrInvalidControllerLease) {
		t.Fatalf("RequireControllerLease error = %v, want invalid controller lease", err)
	}
}

func TestResolveStoreFallsBackThroughMetadataAuthority(t *testing.T) {
	fixture := newSessionRuntimeFixture(t)
	resolved, err := fixture.service.resolveStore(context.Background(), fixture.store.Meta().SessionID)
	if err != nil {
		t.Fatalf("resolveStore: %v", err)
	}
	if resolved.Meta().SessionID != fixture.store.Meta().SessionID {
		t.Fatalf("resolved session id = %q, want %q", resolved.Meta().SessionID, fixture.store.Meta().SessionID)
	}
}

func TestResolveStoreRejectsUnknownSession(t *testing.T) {
	fixture := newSessionRuntimeFixture(t)
	_, err := fixture.service.resolveStore(context.Background(), "session-missing")
	if err == nil {
		t.Fatal("expected resolveStore to reject unknown session")
	}
}

func TestActivateSessionRuntimeRejectsPathLikeSessionID(t *testing.T) {
	svc := &Service{handles: make(map[string]*runtimeHandle)}
	_, err := svc.ActivateSessionRuntime(context.Background(), serverapi.SessionRuntimeActivateRequest{
		ClientRequestID: "req-1",
		SessionID:       "../session-1",
	})
	if err == nil || !strings.Contains(err.Error(), "single session id") {
		t.Fatalf("expected path-like session id rejection, got %v", err)
	}
}

func TestReleaseSessionRuntimeRejectsPathLikeSessionID(t *testing.T) {
	svc := &Service{handles: make(map[string]*runtimeHandle)}
	_, err := svc.ReleaseSessionRuntime(context.Background(), serverapi.SessionRuntimeReleaseRequest{
		ClientRequestID: "req-1",
		SessionID:       "sessions/workspace-a/session-1",
		LeaseID:         "lease-1",
	})
	if err == nil || !strings.Contains(err.Error(), "single session id") {
		t.Fatalf("expected path-like session id rejection, got %v", err)
	}
}

type sessionRuntimeFixture struct {
	config   config.App
	metadata *metadata.Store
	store    *session.Store
	service  *Service
}

func newSessionRuntimeFixture(t *testing.T) sessionRuntimeFixture {
	t.Helper()
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	appCfg, err := config.Load(workspace, config.LoadOptions{})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	metadataStore, err := metadata.Open(appCfg.PersistenceRoot)
	if err != nil {
		t.Fatalf("metadata.Open: %v", err)
	}
	t.Cleanup(func() { _ = metadataStore.Close() })
	binding, err := metadataStore.RegisterWorkspaceBinding(context.Background(), appCfg.WorkspaceRoot)
	if err != nil {
		t.Fatalf("RegisterWorkspaceBinding: %v", err)
	}
	projectSessionsDir := config.ProjectSessionsRoot(appCfg, binding.ProjectID)
	store, err := session.Create(projectSessionsDir, filepath.Base(projectSessionsDir), appCfg.WorkspaceRoot, metadataStore.AuthoritativeSessionStoreOptions()...)
	if err != nil {
		t.Fatalf("session.Create: %v", err)
	}
	if err := store.SetName("session-a"); err != nil {
		t.Fatalf("SetName: %v", err)
	}
	service := NewService(appCfg.PersistenceRoot, metadataStore, nil, nil, nil, nil, nil, registry.NewSessionStoreRegistry(), metadataStore.AuthoritativeSessionStoreOptions()...)
	return sessionRuntimeFixture{config: appCfg, metadata: metadataStore, store: store, service: service}
}

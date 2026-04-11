package sessionruntime

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"builder/server/metadata"
	"builder/server/registry"
	"builder/server/session"
	"builder/shared/config"
	"builder/shared/serverapi"
)

func TestInstallHandleDoesNotDoubleCountDuplicateActivationRequest(t *testing.T) {
	svc := &Service{handles: map[string]*runtimeHandle{
		"session-1": {
			refs:               1,
			activationRequests: map[string]string{"req-1": "lease-1"},
			activeLeases:       map[string]struct{}{"lease-1": {}},
			ready:              make(chan struct{}),
		},
	}}
	close(svc.handles["session-1"].ready)

	handle, installed, leaseID, ownsClaim := svc.installHandle("session-1", "req-1", "lease-2", &runtimeHandle{})
	if installed {
		t.Fatal("expected duplicate activation to reuse existing handle")
	}
	if ownsClaim {
		t.Fatal("expected duplicate activation request to keep existing claim ownership")
	}
	if handle != svc.handles["session-1"] {
		t.Fatal("expected duplicate activation to return existing handle")
	}
	if leaseID != "lease-1" {
		t.Fatalf("lease id = %q, want lease-1", leaseID)
	}
	if got := svc.handles["session-1"].refs; got != 1 {
		t.Fatalf("refs = %d, want 1", got)
	}
	if got := len(svc.handles["session-1"].activeLeases); got != 1 {
		t.Fatalf("active leases = %d, want 1", got)
	}
}

func TestInstallHandleCountsDistinctActivationRequestOnExistingHandle(t *testing.T) {
	svc := &Service{handles: map[string]*runtimeHandle{
		"session-1": {
			refs:               1,
			activationRequests: map[string]string{"req-1": "lease-1"},
			activeLeases:       map[string]struct{}{"lease-1": {}},
			ready:              make(chan struct{}),
		},
	}}
	close(svc.handles["session-1"].ready)

	handle, installed, leaseID, ownsClaim := svc.installHandle("session-1", "req-2", "lease-2", &runtimeHandle{})
	if installed {
		t.Fatal("expected existing handle to remain authoritative")
	}
	if !ownsClaim {
		t.Fatal("expected distinct activation request to own the newly added claim")
	}
	if handle != svc.handles["session-1"] {
		t.Fatal("expected distinct activation to return existing handle")
	}
	if leaseID != "lease-2" {
		t.Fatalf("lease id = %q, want lease-2", leaseID)
	}
	if got := svc.handles["session-1"].refs; got != 2 {
		t.Fatalf("refs = %d, want 2", got)
	}
	if _, ok := svc.handles["session-1"].activeLeases["lease-2"]; !ok {
		t.Fatal("expected distinct lease to be tracked")
	}
}

func TestClaimExistingHandleLeaseKeepsExistingClaimOwnershipForDuplicateRequest(t *testing.T) {
	svc := &Service{handles: map[string]*runtimeHandle{
		"session-1": {
			refs:               1,
			activationRequests: map[string]string{"req-1": "lease-1"},
			activeLeases:       map[string]struct{}{"lease-1": {}},
			ready:              make(chan struct{}),
		},
	}}

	handle, leaseID, ok, ownsClaim := svc.claimExistingHandleLease("session-1", "req-1", "lease-2")
	if !ok {
		t.Fatal("expected existing handle lease claim to succeed")
	}
	if ownsClaim {
		t.Fatal("expected duplicate activation request to keep existing claim ownership")
	}
	if handle != svc.handles["session-1"] {
		t.Fatal("expected duplicate request to return existing handle")
	}
	if leaseID != "lease-1" {
		t.Fatalf("lease id = %q, want lease-1", leaseID)
	}
}

func TestClaimExistingHandleLeaseOwnsDistinctRequest(t *testing.T) {
	svc := &Service{handles: map[string]*runtimeHandle{
		"session-1": {
			refs:               1,
			activationRequests: map[string]string{"req-1": "lease-1"},
			activeLeases:       map[string]struct{}{"lease-1": {}},
			ready:              make(chan struct{}),
		},
	}}

	handle, leaseID, ok, ownsClaim := svc.claimExistingHandleLease("session-1", "req-2", "lease-2")
	if !ok {
		t.Fatal("expected existing handle lease claim to succeed")
	}
	if !ownsClaim {
		t.Fatal("expected distinct activation request to own the newly added claim")
	}
	if handle != svc.handles["session-1"] {
		t.Fatal("expected distinct request to return existing handle")
	}
	if leaseID != "lease-2" {
		t.Fatalf("lease id = %q, want lease-2", leaseID)
	}
	if got := handle.refs; got != 2 {
		t.Fatalf("refs = %d, want 2", got)
	}
}

func TestActivateSessionRuntimeWaitsForExistingHandleReady(t *testing.T) {
	fixture := newSessionRuntimeFixture(t)
	handle := &runtimeHandle{
		refs:               1,
		activationRequests: map[string]string{"req-1": "lease-1"},
		activeLeases:       map[string]struct{}{"lease-1": {}},
		ready:              make(chan struct{}),
	}
	fixture.service.handles = map[string]*runtimeHandle{fixture.store.Meta().SessionID: handle}
	done := make(chan error, 1)
	go func() {
		_, err := fixture.service.ActivateSessionRuntime(context.Background(), serverapi.SessionRuntimeActivateRequest{
			ClientRequestID: "req-2",
			SessionID:       fixture.store.Meta().SessionID,
		})
		done <- err
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
	if got := fixture.service.handles[fixture.store.Meta().SessionID].refs; got != 2 {
		t.Fatalf("refs = %d, want 2", got)
	}
}

func TestActivateSessionRuntimeRollsBackClaimWhenWaitIsCanceled(t *testing.T) {
	fixture := newSessionRuntimeFixture(t)
	handle := &runtimeHandle{
		refs:               1,
		activationRequests: map[string]string{"req-1": "lease-1"},
		activeLeases:       map[string]struct{}{"lease-1": {}},
		ready:              make(chan struct{}),
	}
	fixture.service.handles = map[string]*runtimeHandle{fixture.store.Meta().SessionID: handle}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := fixture.service.ActivateSessionRuntime(ctx, serverapi.SessionRuntimeActivateRequest{
			ClientRequestID: "req-2",
			SessionID:       fixture.store.Meta().SessionID,
		})
		done <- err
	}()
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("ActivateSessionRuntime error = %v, want context canceled", err)
	}
	if got := fixture.service.handles[fixture.store.Meta().SessionID].refs; got != 1 {
		t.Fatalf("refs = %d, want 1", got)
	}
	if _, ok := fixture.service.handles[fixture.store.Meta().SessionID].activationRequests["req-2"]; ok {
		t.Fatal("expected canceled activation request to be rolled back")
	}
}

func TestRollbackActivationClaimPreservesExistingClaimWhenNotOwned(t *testing.T) {
	svc := &Service{handles: map[string]*runtimeHandle{
		"session-1": {
			refs:               1,
			activationRequests: map[string]string{"req-1": "lease-1"},
			activeLeases:       map[string]struct{}{"lease-1": {}},
			ready:              make(chan struct{}),
		},
	}}
	handle := svc.handles["session-1"]

	svc.rollbackActivationClaim("session-1", "req-1", "lease-1", handle, false)

	if got := handle.refs; got != 1 {
		t.Fatalf("refs = %d, want 1", got)
	}
	if got := handle.activationRequests["req-1"]; got != "lease-1" {
		t.Fatalf("activation request lease = %q, want lease-1", got)
	}
	if _, ok := handle.activeLeases["lease-1"]; !ok {
		t.Fatal("expected original lease to remain active")
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
		refs:               1,
		activationRequests: map[string]string{"req-1": lease.LeaseID},
		activeLeases:       map[string]struct{}{lease.LeaseID: {}},
		ready:              make(chan struct{}),
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

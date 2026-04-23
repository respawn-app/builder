package sessionruntime

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"builder/server/metadata"
	"builder/server/registry"
	"builder/server/session"
	"builder/shared/clientui"
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

	handle, takeover, claim, err := svc.claimActivation("session-1", "req-1")
	if err != nil {
		t.Fatalf("claimActivation: %v", err)
	}
	if takeover != nil {
		t.Fatalf("claimActivation takeover = %+v, want nil", takeover)
	}
	if claim != activationClaimReuse {
		t.Fatal("expected duplicate activation to reuse existing controller")
	}
	if handle != svc.handles["session-1"] {
		t.Fatal("expected duplicate activation to return existing handle")
	}
}

func TestClaimActivationAllowsTakeoverAfterReady(t *testing.T) {
	svc := &Service{handles: map[string]*runtimeHandle{
		"session-1": {
			controllerRequestID: "req-1",
			controllerLeaseID:   "lease-1",
			ready:               make(chan struct{}),
		},
	}}
	close(svc.handles["session-1"].ready)

	handle, takeover, claim, err := svc.claimActivation("session-1", "req-2")
	if err != nil {
		t.Fatalf("claimActivation: %v", err)
	}
	if takeover == nil {
		t.Fatal("expected takeover activation to allocate pending takeover state")
	}
	if claim != activationClaimTakeover {
		t.Fatalf("claimActivation claim = %v, want takeover", claim)
	}
	if handle != svc.handles["session-1"] {
		t.Fatal("expected takeover activation to return existing handle")
	}
}

func TestClaimActivationReusesPendingTakeoverRequest(t *testing.T) {
	svc := &Service{handles: map[string]*runtimeHandle{
		"session-1": {
			controllerRequestID: "req-1",
			controllerLeaseID:   "lease-1",
			ready:               make(chan struct{}),
		},
	}}
	close(svc.handles["session-1"].ready)

	handle, takeover, claim, err := svc.claimActivation("session-1", "req-2")
	if err != nil {
		t.Fatalf("claimActivation first takeover: %v", err)
	}
	if claim != activationClaimTakeover {
		t.Fatalf("first claimActivation claim = %v, want takeover", claim)
	}
	reusedHandle, reusedTakeover, reusedClaim, err := svc.claimActivation("session-1", "req-2")
	if err != nil {
		t.Fatalf("claimActivation pending retry: %v", err)
	}
	if reusedClaim != activationClaimTakeoverReuse {
		t.Fatalf("pending retry claim = %v, want takeover reuse", reusedClaim)
	}
	if reusedHandle != handle {
		t.Fatal("expected pending retry to return same handle")
	}
	if reusedTakeover != takeover {
		t.Fatal("expected pending retry to return same takeover state")
	}
	if !svc.completeTakeover("session-1", handle, takeover, "req-2", "lease-2") {
		t.Fatal("expected completeTakeover to succeed")
	}
	resp, err := activationResponseForTakeover(reusedTakeover)
	if err != nil {
		t.Fatalf("activationResponseForTakeover: %v", err)
	}
	if resp.LeaseID != "lease-2" {
		t.Fatalf("takeover lease id = %q, want lease-2", resp.LeaseID)
	}
}

func TestPendingTakeoverRetryUnblocksWhenTakeoverFails(t *testing.T) {
	svc := &Service{handles: map[string]*runtimeHandle{
		"session-1": {
			controllerRequestID: "req-1",
			controllerLeaseID:   "lease-1",
			ready:               make(chan struct{}),
		},
	}}
	close(svc.handles["session-1"].ready)

	handle, takeover, claim, err := svc.claimActivation("session-1", "req-2")
	if err != nil {
		t.Fatalf("claimActivation first takeover: %v", err)
	}
	if claim != activationClaimTakeover {
		t.Fatalf("first claimActivation claim = %v, want takeover", claim)
	}
	_, reusedTakeover, reusedClaim, err := svc.claimActivation("session-1", "req-2")
	if err != nil {
		t.Fatalf("claimActivation pending retry: %v", err)
	}
	if reusedClaim != activationClaimTakeoverReuse {
		t.Fatalf("pending retry claim = %v, want takeover reuse", reusedClaim)
	}
	errCh := make(chan error, 1)
	go func() {
		if err := waitForRuntimeTakeoverReady(context.Background(), reusedTakeover); err != nil {
			errCh <- err
			return
		}
		_, err := activationResponseForTakeover(reusedTakeover)
		errCh <- err
	}()

	expectedErr := errors.Join(serverapi.ErrSessionAlreadyControlled, errors.New("takeover lost"))
	svc.failTakeover("session-1", handle, takeover, expectedErr)

	err = <-errCh
	if !errors.Is(err, serverapi.ErrSessionAlreadyControlled) {
		t.Fatalf("takeover waiter error = %v, want session already controlled", err)
	}
}

func TestCloseReleasedRuntimeHandleSignalsPendingTakeoverWaiters(t *testing.T) {
	svc := &Service{handles: map[string]*runtimeHandle{
		"session-1": {
			controllerRequestID: "req-1",
			controllerLeaseID:   "lease-1",
			ready:               make(chan struct{}),
		},
	}}
	close(svc.handles["session-1"].ready)

	handle, takeover, claim, err := svc.claimActivation("session-1", "req-2")
	if err != nil {
		t.Fatalf("claimActivation first takeover: %v", err)
	}
	if claim != activationClaimTakeover {
		t.Fatalf("first claimActivation claim = %v, want takeover", claim)
	}
	_, reusedTakeover, reusedClaim, err := svc.claimActivation("session-1", "req-2")
	if err != nil {
		t.Fatalf("claimActivation pending retry: %v", err)
	}
	if reusedClaim != activationClaimTakeoverReuse {
		t.Fatalf("pending retry claim = %v, want takeover reuse", reusedClaim)
	}
	errCh := make(chan error, 1)
	go func() {
		if err := waitForRuntimeTakeoverReady(context.Background(), reusedTakeover); err != nil {
			errCh <- err
			return
		}
		_, err := activationResponseForTakeover(reusedTakeover)
		errCh <- err
	}()

	svc.closeReleasedRuntimeHandle("session-1", handle)

	err = <-errCh
	if !errors.Is(err, serverapi.ErrInvalidControllerLease) {
		t.Fatalf("takeover waiter error = %v, want invalid controller lease", err)
	}
	if _, ok := svc.handles["session-1"]; ok {
		t.Fatal("expected runtime handle removed after closeReleasedRuntimeHandle")
	}
	if takeover.err == nil {
		t.Fatal("expected takeover terminal error to be recorded")
	}
}

func TestClaimActivationRejectsConcurrentDifferentTakeoverRequest(t *testing.T) {
	svc := &Service{handles: map[string]*runtimeHandle{
		"session-1": {
			controllerRequestID: "req-1",
			controllerLeaseID:   "lease-1",
			ready:               make(chan struct{}),
		},
	}}
	close(svc.handles["session-1"].ready)

	_, _, claim, err := svc.claimActivation("session-1", "req-2")
	if err != nil {
		t.Fatalf("claimActivation first takeover: %v", err)
	}
	if claim != activationClaimTakeover {
		t.Fatalf("first claimActivation claim = %v, want takeover", claim)
	}
	_, _, _, err = svc.claimActivation("session-1", "req-3")
	if !errors.Is(err, serverapi.ErrSessionAlreadyControlled) {
		t.Fatalf("claimActivation competing takeover error = %v, want session already controlled", err)
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

func TestActivateSessionRuntimeReissuesControllerLeaseForTakeover(t *testing.T) {
	fixture := newSessionRuntimeFixture(t)
	lease, err := fixture.metadata.CreateRuntimeLease(context.Background(), fixture.store.Meta().SessionID, "req-1")
	if err != nil {
		t.Fatalf("CreateRuntimeLease: %v", err)
	}
	handle := &runtimeHandle{
		controllerRequestID: "req-1",
		controllerLeaseID:   lease.LeaseID,
		ready:               make(chan struct{}),
	}
	close(handle.ready)
	fixture.service.handles = map[string]*runtimeHandle{fixture.store.Meta().SessionID: handle}

	resp, err := fixture.service.ActivateSessionRuntime(context.Background(), serverapi.SessionRuntimeActivateRequest{
		ClientRequestID: "req-2",
		SessionID:       fixture.store.Meta().SessionID,
	})
	if err != nil {
		t.Fatalf("ActivateSessionRuntime takeover: %v", err)
	}
	if strings.TrimSpace(resp.LeaseID) == "" || resp.LeaseID == lease.LeaseID {
		t.Fatalf("takeover lease id = %q, want non-empty replacement for %q", resp.LeaseID, lease.LeaseID)
	}
	if handle.controllerRequestID != "req-2" {
		t.Fatalf("controller request id = %q, want req-2", handle.controllerRequestID)
	}
	if handle.controllerLeaseID != resp.LeaseID {
		t.Fatalf("controller lease id = %q, want %q", handle.controllerLeaseID, resp.LeaseID)
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

func TestReleaseSessionRuntimeClosesHandleWhenLeaseAlreadyReleasedAndWaitCanceled(t *testing.T) {
	fixture := newSessionRuntimeFixture(t)
	lease, err := fixture.metadata.CreateRuntimeLease(context.Background(), fixture.store.Meta().SessionID, "req-1")
	if err != nil {
		t.Fatalf("CreateRuntimeLease: %v", err)
	}
	if _, err := fixture.metadata.ReleaseRuntimeLease(context.Background(), fixture.store.Meta().SessionID, lease.LeaseID); err != nil {
		t.Fatalf("ReleaseRuntimeLease setup: %v", err)
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
	fixture.service.handles[fixture.store.Meta().SessionID] = handle
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, err = fixture.service.ReleaseSessionRuntime(ctx, serverapi.SessionRuntimeReleaseRequest{
		ClientRequestID: "rel-1",
		SessionID:       fixture.store.Meta().SessionID,
		LeaseID:         lease.LeaseID,
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ReleaseSessionRuntime error = %v, want context deadline exceeded", err)
	}
	if closed.Load() != 1 {
		t.Fatalf("expected closeFn to run exactly once, got %d", closed.Load())
	}
	if _, ok := fixture.service.handles[fixture.store.Meta().SessionID]; ok {
		t.Fatal("expected runtime handle removed after canceled wait with released lease")
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

func TestReleaseSessionRuntimeSucceedsWhenHandleAlreadyMissingAfterLeaseReleased(t *testing.T) {
	fixture := newSessionRuntimeFixture(t)
	lease, err := fixture.metadata.CreateRuntimeLease(context.Background(), fixture.store.Meta().SessionID, "req-1")
	if err != nil {
		t.Fatalf("CreateRuntimeLease: %v", err)
	}
	if _, err := fixture.metadata.ReleaseRuntimeLease(context.Background(), fixture.store.Meta().SessionID, lease.LeaseID); err != nil {
		t.Fatalf("ReleaseRuntimeLease: %v", err)
	}

	if _, err := fixture.service.ReleaseSessionRuntime(context.Background(), serverapi.SessionRuntimeReleaseRequest{
		ClientRequestID: "rel-1",
		SessionID:       fixture.store.Meta().SessionID,
		LeaseID:         lease.LeaseID,
	}); err != nil {
		t.Fatalf("ReleaseSessionRuntime retry: %v", err)
	}
}

func TestReleaseSessionRuntimeReleasesPersistedLeaseWhenHandleAlreadyMissing(t *testing.T) {
	fixture := newSessionRuntimeFixture(t)
	lease, err := fixture.metadata.CreateRuntimeLease(context.Background(), fixture.store.Meta().SessionID, "req-1")
	if err != nil {
		t.Fatalf("CreateRuntimeLease: %v", err)
	}

	if _, err := fixture.service.ReleaseSessionRuntime(context.Background(), serverapi.SessionRuntimeReleaseRequest{
		ClientRequestID: "rel-1",
		SessionID:       fixture.store.Meta().SessionID,
		LeaseID:         lease.LeaseID,
	}); err != nil {
		t.Fatalf("ReleaseSessionRuntime: %v", err)
	}

	released, err := fixture.metadata.ReleaseRuntimeLease(context.Background(), fixture.store.Meta().SessionID, lease.LeaseID)
	if err != nil {
		t.Fatalf("ReleaseRuntimeLease verification: %v", err)
	}
	if released.Active() {
		t.Fatalf("expected lease to stay released after missing-handle cleanup, got %+v", released)
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

func TestRequireControllerLeaseRejectsReplacedHandleAfterReadyWait(t *testing.T) {
	svc := &Service{handles: map[string]*runtimeHandle{
		"session-1": {
			controllerRequestID: "req-1",
			controllerLeaseID:   "lease-1",
			ready:               make(chan struct{}),
		},
	}}
	original := svc.handles["session-1"]
	replacement := &runtimeHandle{
		controllerRequestID: "req-2",
		controllerLeaseID:   "lease-2",
		ready:               make(chan struct{}),
	}
	close(replacement.ready)
	errCh := make(chan error, 1)
	go func() {
		errCh <- svc.RequireControllerLease(context.Background(), "session-1", "lease-1")
	}()
	svc.mu.Lock()
	svc.handles["session-1"] = replacement
	svc.mu.Unlock()
	close(original.ready)
	err := <-errCh
	if !errors.Is(err, serverapi.ErrInvalidControllerLease) {
		t.Fatalf("RequireControllerLease error = %v, want invalid controller lease", err)
	}
}

func TestRecordWorktreeTransitionPersistsPendingReminderState(t *testing.T) {
	fixture := newSessionRuntimeFixture(t)
	lease, err := fixture.metadata.CreateRuntimeLease(context.Background(), fixture.store.Meta().SessionID, "req-1")
	if err != nil {
		t.Fatalf("CreateRuntimeLease: %v", err)
	}
	handle := &runtimeHandle{
		controllerRequestID: "req-1",
		controllerLeaseID:   lease.LeaseID,
		ready:               make(chan struct{}),
	}
	close(handle.ready)
	fixture.service.handles = map[string]*runtimeHandle{fixture.store.Meta().SessionID: handle}

	err = fixture.service.RecordWorktreeTransition(context.Background(), fixture.store.Meta().SessionID, lease.LeaseID, session.WorktreeReminderState{
		Mode:                  session.WorktreeReminderModeEnter,
		Branch:                " feature/worktree ",
		WorktreePath:          " /tmp/worktree-a ",
		WorkspaceRoot:         " /tmp/workspace ",
		EffectiveCwd:          " /tmp/worktree-a/pkg ",
		HasIssuedInGeneration: true,
		IssuedCompactionCount: 9,
	})
	if err != nil {
		t.Fatalf("RecordWorktreeTransition: %v", err)
	}

	resolved, err := fixture.service.resolveStore(context.Background(), fixture.store.Meta().SessionID)
	if err != nil {
		t.Fatalf("resolveStore: %v", err)
	}
	state := resolved.Meta().WorktreeReminder
	if state == nil {
		t.Fatal("expected persisted worktree reminder state")
	}
	if state.Mode != session.WorktreeReminderModeEnter {
		t.Fatalf("mode = %q, want enter", state.Mode)
	}
	if state.Branch != "feature/worktree" {
		t.Fatalf("branch = %q, want feature/worktree", state.Branch)
	}
	if state.WorktreePath != "/tmp/worktree-a" {
		t.Fatalf("worktree path = %q, want /tmp/worktree-a", state.WorktreePath)
	}
	if state.WorkspaceRoot != "/tmp/workspace" {
		t.Fatalf("workspace root = %q, want /tmp/workspace", state.WorkspaceRoot)
	}
	if state.EffectiveCwd != "/tmp/worktree-a/pkg" {
		t.Fatalf("effective cwd = %q, want /tmp/worktree-a/pkg", state.EffectiveCwd)
	}
	if state.HasIssuedInGeneration {
		t.Fatal("expected reminder issuance reset for new transition")
	}
	if state.IssuedCompactionCount != 0 {
		t.Fatalf("issued compaction count = %d, want 0", state.IssuedCompactionCount)
	}
}

func TestEnsureCurrentControllerLeaseLockedRejectsChangedLease(t *testing.T) {
	handle := &runtimeHandle{controllerRequestID: "req-1", controllerLeaseID: "lease-1", ready: make(chan struct{})}
	svc := &Service{handles: map[string]*runtimeHandle{"session-1": handle}}

	err := svc.ensureCurrentControllerLeaseLocked("session-1", "lease-2", handle)
	if !errors.Is(err, serverapi.ErrInvalidControllerLease) {
		t.Fatalf("ensureCurrentControllerLeaseLocked error = %v, want invalid controller lease", err)
	}
}

func TestEnsureCurrentControllerLeaseLockedRejectsReplacedHandle(t *testing.T) {
	original := &runtimeHandle{controllerRequestID: "req-1", controllerLeaseID: "lease-1", ready: make(chan struct{})}
	replacement := &runtimeHandle{controllerRequestID: "req-2", controllerLeaseID: "lease-1", ready: make(chan struct{})}
	svc := &Service{handles: map[string]*runtimeHandle{"session-1": replacement}}

	err := svc.ensureCurrentControllerLeaseLocked("session-1", "lease-1", original)
	if !errors.Is(err, serverapi.ErrInvalidControllerLease) {
		t.Fatalf("ensureCurrentControllerLeaseLocked error = %v, want invalid controller lease", err)
	}
}

func TestActiveRuntimeHandleReturnsActivationError(t *testing.T) {
	activationErr := errors.New("activation failed")
	handle := &runtimeHandle{controllerRequestID: "req-1", controllerLeaseID: "lease-1", activationErr: activationErr, ready: make(chan struct{})}
	close(handle.ready)
	svc := &Service{handles: map[string]*runtimeHandle{"session-1": handle}}

	resolved, err := svc.activeRuntimeHandle(context.Background(), "session-1")
	if !errors.Is(err, activationErr) {
		t.Fatalf("activeRuntimeHandle error = %v, want %v", err, activationErr)
	}
	if resolved != nil {
		t.Fatalf("activeRuntimeHandle returned handle %+v, want nil", resolved)
	}
}

func TestSyncExecutionTargetPersistsReminderWithoutActiveRuntime(t *testing.T) {
	fixture := newSessionRuntimeFixture(t)

	err := fixture.service.SyncExecutionTarget(context.Background(), fixture.store.Meta().SessionID, clientui.SessionExecutionTarget{
		WorkspaceRoot:    " /tmp/workspace ",
		EffectiveWorkdir: " /tmp/workspace ",
	}, &session.WorktreeReminderState{
		Mode:          session.WorktreeReminderModeExit,
		Branch:        " feature/worktree ",
		WorktreePath:  " /tmp/worktree-a ",
		WorkspaceRoot: " /tmp/workspace ",
		EffectiveCwd:  " /tmp/workspace ",
	})
	if err != nil {
		t.Fatalf("SyncExecutionTarget: %v", err)
	}

	resolved, err := fixture.service.resolveStore(context.Background(), fixture.store.Meta().SessionID)
	if err != nil {
		t.Fatalf("resolveStore: %v", err)
	}
	state := resolved.Meta().WorktreeReminder
	if state == nil {
		t.Fatal("expected persisted worktree reminder state")
	}
	if state.Mode != session.WorktreeReminderModeExit {
		t.Fatalf("mode = %q, want exit", state.Mode)
	}
	if state.Branch != "feature/worktree" {
		t.Fatalf("branch = %q, want feature/worktree", state.Branch)
	}
	if state.WorktreePath != "/tmp/worktree-a" {
		t.Fatalf("worktree path = %q, want /tmp/worktree-a", state.WorktreePath)
	}
	if state.EffectiveCwd != "/tmp/workspace" {
		t.Fatalf("effective cwd = %q, want /tmp/workspace", state.EffectiveCwd)
	}
}

func TestSyncExecutionTargetRebindsActiveRuntime(t *testing.T) {
	fixture := newSessionRuntimeFixture(t)
	reboundRoot := ""
	handle := &runtimeHandle{
		controllerRequestID: "req-1",
		controllerLeaseID:   "lease-1",
		ready:               make(chan struct{}),
		rebind: func(root string) error {
			reboundRoot = root
			return nil
		},
	}
	close(handle.ready)
	fixture.service.handles = map[string]*runtimeHandle{fixture.store.Meta().SessionID: handle}

	err := fixture.service.SyncExecutionTarget(context.Background(), fixture.store.Meta().SessionID, clientui.SessionExecutionTarget{
		EffectiveWorkdir: " /tmp/workspace/pkg ",
	}, nil)
	if err != nil {
		t.Fatalf("SyncExecutionTarget: %v", err)
	}
	if reboundRoot != "/tmp/workspace/pkg" {
		t.Fatalf("rebound root = %q, want /tmp/workspace/pkg", reboundRoot)
	}
}

func TestSyncExecutionTargetDoesNotPersistReminderWhenActiveRuntimeRebindFails(t *testing.T) {
	fixture := newSessionRuntimeFixture(t)
	handle := &runtimeHandle{
		controllerRequestID: "req-1",
		controllerLeaseID:   "lease-1",
		ready:               make(chan struct{}),
		rebind: func(string) error {
			return errors.New("rebind failed")
		},
	}
	close(handle.ready)
	fixture.service.handles = map[string]*runtimeHandle{fixture.store.Meta().SessionID: handle}

	err := fixture.service.SyncExecutionTarget(context.Background(), fixture.store.Meta().SessionID, clientui.SessionExecutionTarget{
		EffectiveWorkdir: "/tmp/workspace/pkg",
	}, &session.WorktreeReminderState{
		Mode:          session.WorktreeReminderModeExit,
		Branch:        "feature/worktree",
		WorktreePath:  "/tmp/worktree-a",
		WorkspaceRoot: "/tmp/workspace",
		EffectiveCwd:  "/tmp/workspace",
	})
	if err == nil || !strings.Contains(err.Error(), "rebind failed") {
		t.Fatalf("SyncExecutionTarget error = %v, want rebind failure", err)
	}

	resolved, err := fixture.service.resolveStore(context.Background(), fixture.store.Meta().SessionID)
	if err != nil {
		t.Fatalf("resolveStore: %v", err)
	}
	if state := resolved.Meta().WorktreeReminder; state != nil {
		t.Fatalf("expected reminder state not persisted after failed rebind, got %+v", state)
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

package worktree

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"builder/server/metadata"
	"builder/server/primaryrun"
	"builder/server/session"
	shelltool "builder/server/tools/shell"
	"builder/shared/clientui"
	"builder/shared/config"
	"builder/shared/serverapi"
)

type serviceTestRuntime struct {
	mu             sync.Mutex
	requireCalls   []serviceRuntimeCall
	rebindCalls    []serviceRuntimeCall
	reminderCalls  []session.WorktreeReminderState
	activeSessions map[string]bool
	rebindErr      error
	rebindErrRoot  string
	requireErr     error
	controllerSeen bool
}

type serviceRuntimeCall struct {
	sessionID string
	leaseID   string
	root      string
}

func (r *serviceTestRuntime) RequireControllerLease(_ context.Context, sessionID string, leaseID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.controllerSeen = true
	r.requireCalls = append(r.requireCalls, serviceRuntimeCall{sessionID: sessionID, leaseID: leaseID})
	return r.requireErr
}

func (r *serviceTestRuntime) RebindLocalTools(_ context.Context, sessionID string, leaseID string, workspaceRoot string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rebindCalls = append(r.rebindCalls, serviceRuntimeCall{sessionID: sessionID, leaseID: leaseID, root: workspaceRoot})
	if r.rebindErr != nil && (strings.TrimSpace(r.rebindErrRoot) == "" || strings.TrimSpace(r.rebindErrRoot) == strings.TrimSpace(workspaceRoot)) {
		return r.rebindErr
	}
	return nil
}

func (r *serviceTestRuntime) RecordWorktreeTransition(_ context.Context, _ string, _ string, state session.WorktreeReminderState) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reminderCalls = append(r.reminderCalls, state)
	return nil
}

func (r *serviceTestRuntime) SyncExecutionTarget(_ context.Context, sessionID string, target clientui.SessionExecutionTarget, reminder *session.WorktreeReminderState) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if reminder != nil {
		r.reminderCalls = append(r.reminderCalls, *reminder)
	}
	if !r.activeSessions[strings.TrimSpace(sessionID)] {
		return nil
	}
	r.rebindCalls = append(r.rebindCalls, serviceRuntimeCall{sessionID: sessionID, root: strings.TrimSpace(target.EffectiveWorkdir)})
	if r.rebindErr != nil && (strings.TrimSpace(r.rebindErrRoot) == "" || strings.TrimSpace(r.rebindErrRoot) == strings.TrimSpace(target.EffectiveWorkdir)) {
		return r.rebindErr
	}
	return nil
}

type serviceTestGate struct{}

func (serviceTestGate) AcquirePrimaryRun(string) (primaryrun.Lease, error) {
	return primaryrun.LeaseFunc(func() {}), nil
}

type serviceTestProcessSource struct {
	snapshots []shelltool.Snapshot
}

func (s *serviceTestProcessSource) List() []shelltool.Snapshot {
	return append([]shelltool.Snapshot(nil), s.snapshots...)
}

type serviceTestLocalNotes struct {
	mu             sync.Mutex
	texts          []string
	sessionTexts   []string
	appendLocalErr error
}

func (n *serviceTestLocalNotes) AppendLocalEntry(_ context.Context, req serverapi.RuntimeAppendLocalEntryRequest) error {
	if n.appendLocalErr != nil {
		return n.appendLocalErr
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	n.texts = append(n.texts, req.Text)
	return nil
}

func (n *serviceTestLocalNotes) AppendSessionEntry(_ context.Context, _ string, _ string, text string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.sessionTexts = append(n.sessionTexts, text)
	return nil
}

func (n *serviceTestLocalNotes) snapshot() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	combined := append([]string(nil), n.texts...)
	combined = append(combined, n.sessionTexts...)
	return combined
}

type serviceTestEnv struct {
	t             *testing.T
	ctx           context.Context
	store         *metadata.Store
	cfg           config.App
	binding       metadata.Binding
	session       *session.Store
	runtime       *serviceTestRuntime
	processes     *serviceTestProcessSource
	localNotes    *serviceTestLocalNotes
	service       *Service
	leaseID       string
	workspaceRoot string
	baseDir       string
}

func TestCreateWorktreeMarksProvenanceAndRunsSetupScriptWithProjectID(t *testing.T) {
	env := newServiceTestEnv(t)
	payloadPath := filepath.Join(t.TempDir(), "worktree-payload.json")
	stdinPath := filepath.Join(t.TempDir(), "worktree-stdin.json")
	argsPath := filepath.Join(t.TempDir(), "worktree-args.txt")
	cwdPath := filepath.Join(t.TempDir(), "worktree-cwd.txt")
	scriptRelpath := filepath.Join("scripts", "setup-worktree.sh")
	writeExecutableFile(t, filepath.Join(env.workspaceRoot, scriptRelpath), fmt.Sprintf("#!/bin/sh\npwd > %q\nprintf '%%s\n%%s\n%%s\n' \"$1\" \"$2\" \"$3\" > %q\ncat > %q\nprintf '%%s' \"$BUILDER_WORKTREE_PAYLOAD_JSON\" > %q\n", cwdPath, argsPath, stdinPath, payloadPath))
	env.service.setupScript = scriptRelpath

	resp, err := env.service.CreateWorktree(env.ctx, serverapi.WorktreeCreateRequest{
		ClientRequestID:   "req-create",
		SessionID:         env.session.Meta().SessionID,
		ControllerLeaseID: env.leaseID,
		CreateBranch:      true,
		BranchName:        "feature/create-provenance",
	})
	if err != nil {
		t.Fatalf("CreateWorktree: %v", err)
	}
	if !resp.CreatedBranch {
		t.Fatal("expected create to report created branch")
	}
	if !resp.SetupScheduled {
		t.Fatal("expected setup script to be scheduled")
	}
	if !resp.Worktree.BuilderManaged {
		t.Fatal("expected worktree builder_managed=true")
	}
	if resp.Target.WorktreeID != resp.Worktree.WorktreeID {
		t.Fatalf("create target worktree id = %q, want %q", resp.Target.WorktreeID, resp.Worktree.WorktreeID)
	}
	if resp.Target.EffectiveWorkdir != resp.Worktree.CanonicalRoot {
		t.Fatalf("create effective workdir = %q, want %q", resp.Target.EffectiveWorkdir, resp.Worktree.CanonicalRoot)
	}
	if !resp.Worktree.CreatedBranch {
		t.Fatal("expected worktree created_branch=true")
	}
	if resp.Worktree.OriginSessionID != env.session.Meta().SessionID {
		t.Fatalf("origin session id = %q, want %q", resp.Worktree.OriginSessionID, env.session.Meta().SessionID)
	}
	record, err := env.store.GetWorktreeRecordByID(env.ctx, resp.Worktree.WorktreeID)
	if err != nil {
		t.Fatalf("GetWorktreeRecordByID: %v", err)
	}
	if !record.BuilderManaged || !record.CreatedBranch || record.OriginSessionID != env.session.Meta().SessionID {
		t.Fatalf("unexpected worktree record: %+v", record)
	}
	payload := waitForSetupPayload(t, payloadPath)
	if payload.ProjectID != env.binding.ProjectID {
		t.Fatalf("setup payload project_id = %q, want %q", payload.ProjectID, env.binding.ProjectID)
	}
	if payload.WorkspaceID != env.binding.WorkspaceID {
		t.Fatalf("setup payload workspace_id = %q, want %q", payload.WorkspaceID, env.binding.WorkspaceID)
	}
	if payload.SessionID != env.session.Meta().SessionID {
		t.Fatalf("setup payload session_id = %q, want %q", payload.SessionID, env.session.Meta().SessionID)
	}
	if payload.WorktreeID != resp.Worktree.WorktreeID {
		t.Fatalf("setup payload worktree_id = %q, want %q", payload.WorktreeID, resp.Worktree.WorktreeID)
	}
	if !payload.CreatedBranch {
		t.Fatal("expected setup payload created_branch=true")
	}
	if got := waitForFileText(t, cwdPath); got != resp.Worktree.CanonicalRoot {
		t.Fatalf("setup cwd = %q, want %q", got, resp.Worktree.CanonicalRoot)
	}
	if got := waitForFileLines(t, argsPath); len(got) != 3 || got[0] != env.workspaceRoot || got[1] != "feature/create-provenance" || got[2] != resp.Worktree.CanonicalRoot {
		t.Fatalf("setup args = %+v, want [%q %q %q]", got, env.workspaceRoot, "feature/create-provenance", resp.Worktree.CanonicalRoot)
	}
	if stdinPayload := waitForSetupPayload(t, stdinPath); stdinPayload != payload {
		t.Fatalf("stdin payload = %+v, want %+v", stdinPayload, payload)
	}
	if len(env.runtime.rebindCalls) != 1 || env.runtime.rebindCalls[0].root != resp.Worktree.CanonicalRoot {
		t.Fatalf("expected create-time rebind to created worktree, got %+v", env.runtime.rebindCalls)
	}
	if notes := env.localNotes.snapshot(); len(notes) == 0 || !strings.Contains(notes[0], "Switched worktree to") {
		t.Fatalf("expected create-time switch note, got %+v", notes)
	}
	worktrees := mustListWorktrees(t, env)
	created := findWorktreeByID(t, worktrees.Worktrees, resp.Worktree.WorktreeID)
	if !created.BuilderManaged || !created.CreatedBranch || created.OriginSessionID != env.session.Meta().SessionID {
		t.Fatalf("sync lost worktree provenance: %+v", created)
	}
}

func TestRunSetupScriptDoesNotAppendSuccessNote(t *testing.T) {
	notes := &serviceTestLocalNotes{}
	service := &Service{localNotes: notes}
	scriptPath := filepath.Join(t.TempDir(), "setup.sh")
	writeExecutableFile(t, scriptPath, "#!/bin/sh\nexit 0\n")

	service.runSetupScript(scriptPath, "session-1", setupScriptPayload{WorktreeRoot: t.TempDir()})

	if got := notes.snapshot(); len(got) != 0 {
		t.Fatalf("expected no setup success note, got %+v", got)
	}
}

func TestCreateWorktreeAllowsExistingRefWithoutCreatingBranch(t *testing.T) {
	env := newServiceTestEnv(t)
	runGit(t, env.workspaceRoot, "branch", "feature/existing-ref")

	resp, err := env.service.CreateWorktree(env.ctx, serverapi.WorktreeCreateRequest{
		ClientRequestID:   "req-create-existing-ref",
		SessionID:         env.session.Meta().SessionID,
		ControllerLeaseID: env.leaseID,
		BaseRef:           "feature/existing-ref",
		CreateBranch:      false,
	})
	if err != nil {
		t.Fatalf("CreateWorktree existing ref: %v", err)
	}
	if resp.CreatedBranch {
		t.Fatal("expected created_branch=false for existing ref")
	}
	if resp.Worktree.BranchName != "feature/existing-ref" {
		t.Fatalf("branch name = %q, want feature/existing-ref", resp.Worktree.BranchName)
	}
	if !resp.Worktree.BuilderManaged {
		t.Fatal("expected builder-managed worktree for existing ref")
	}
	record, err := env.store.GetWorktreeRecordByID(env.ctx, resp.Worktree.WorktreeID)
	if err != nil {
		t.Fatalf("GetWorktreeRecordByID: %v", err)
	}
	if record.CreatedBranch {
		t.Fatalf("expected created_branch=false in metadata, got %+v", record)
	}
}

func TestResolveWorktreeCreateTargetClassifiesBranchDetachedRefAndNewBranch(t *testing.T) {
	env := newServiceTestEnv(t)
	runGit(t, env.workspaceRoot, "branch", "feature/existing-ref")

	existing, err := env.service.ResolveWorktreeCreateTarget(env.ctx, serverapi.WorktreeCreateTargetResolveRequest{SessionID: env.session.Meta().SessionID, Target: "feature/existing-ref"})
	if err != nil {
		t.Fatalf("ResolveWorktreeCreateTarget existing: %v", err)
	}
	if existing.Resolution.Kind != serverapi.WorktreeCreateTargetResolutionKindExistingBranch {
		t.Fatalf("existing kind = %q, want existing_branch", existing.Resolution.Kind)
	}

	detached, err := env.service.ResolveWorktreeCreateTarget(env.ctx, serverapi.WorktreeCreateTargetResolveRequest{SessionID: env.session.Meta().SessionID, Target: "HEAD"})
	if err != nil {
		t.Fatalf("ResolveWorktreeCreateTarget detached: %v", err)
	}
	if detached.Resolution.Kind != serverapi.WorktreeCreateTargetResolutionKindDetachedRef {
		t.Fatalf("detached kind = %q, want detached_ref", detached.Resolution.Kind)
	}

	newBranch, err := env.service.ResolveWorktreeCreateTarget(env.ctx, serverapi.WorktreeCreateTargetResolveRequest{SessionID: env.session.Meta().SessionID, Target: "feature/new-branch"})
	if err != nil {
		t.Fatalf("ResolveWorktreeCreateTarget new branch: %v", err)
	}
	if newBranch.Resolution.Kind != serverapi.WorktreeCreateTargetResolutionKindNewBranch {
		t.Fatalf("new branch kind = %q, want new_branch", newBranch.Resolution.Kind)
	}
}

func TestDeleteWorktreeKeepsExistingBranchUnlessExplicitlyRequested(t *testing.T) {
	env := newServiceTestEnv(t)
	runGit(t, env.workspaceRoot, "branch", "feature/shared-branch")
	resp, err := env.service.CreateWorktree(env.ctx, serverapi.WorktreeCreateRequest{
		ClientRequestID:   "req-create-shared-branch",
		SessionID:         env.session.Meta().SessionID,
		ControllerLeaseID: env.leaseID,
		BaseRef:           "feature/shared-branch",
		CreateBranch:      false,
	})
	if err != nil {
		t.Fatalf("CreateWorktree existing branch: %v", err)
	}

	deleteResp, err := env.service.DeleteWorktree(env.ctx, serverapi.WorktreeDeleteRequest{
		ClientRequestID:   "req-delete-shared-branch",
		SessionID:         env.session.Meta().SessionID,
		ControllerLeaseID: env.leaseID,
		WorktreeID:        resp.Worktree.WorktreeID,
	})
	if err != nil {
		t.Fatalf("DeleteWorktree: %v", err)
	}
	if deleteResp.BranchDeleted {
		t.Fatal("did not expect branch deletion without explicit confirmation")
	}
	if !strings.Contains(deleteResp.BranchCleanupMessage, "Kept branch feature/shared-branch") {
		t.Fatalf("unexpected branch cleanup message: %q", deleteResp.BranchCleanupMessage)
	}
	if got := runGit(t, env.workspaceRoot, "branch", "--list", "feature/shared-branch"); !strings.Contains(got, "feature/shared-branch") {
		t.Fatalf("expected shared branch to remain, got %q", got)
	}
}

func TestDeleteWorktreeDeletesExistingBranchWhenExplicitlyRequested(t *testing.T) {
	env := newServiceTestEnv(t)
	runGit(t, env.workspaceRoot, "branch", "feature/shared-branch")
	resp, err := env.service.CreateWorktree(env.ctx, serverapi.WorktreeCreateRequest{
		ClientRequestID:   "req-create-shared-branch-explicit",
		SessionID:         env.session.Meta().SessionID,
		ControllerLeaseID: env.leaseID,
		BaseRef:           "feature/shared-branch",
		CreateBranch:      false,
	})
	if err != nil {
		t.Fatalf("CreateWorktree existing branch: %v", err)
	}

	deleteResp, err := env.service.DeleteWorktree(env.ctx, serverapi.WorktreeDeleteRequest{
		ClientRequestID:   "req-delete-shared-branch-explicit",
		SessionID:         env.session.Meta().SessionID,
		ControllerLeaseID: env.leaseID,
		WorktreeID:        resp.Worktree.WorktreeID,
		DeleteBranch:      true,
	})
	if err != nil {
		t.Fatalf("DeleteWorktree explicit branch delete: %v", err)
	}
	if !deleteResp.BranchDeleted {
		t.Fatalf("expected branch deletion, got %+v", deleteResp)
	}
	if !strings.Contains(deleteResp.BranchCleanupMessage, "Deleted branch feature/shared-branch") {
		t.Fatalf("unexpected branch cleanup message: %q", deleteResp.BranchCleanupMessage)
	}
	if got := runGit(t, env.workspaceRoot, "branch", "--list", "feature/shared-branch"); strings.Contains(got, "feature/shared-branch") {
		t.Fatalf("expected shared branch removed, got %q", got)
	}
}

func TestResolveRequestedWorktreeRootCreatesBaseDirAndAutoSuffixesCollisions(t *testing.T) {
	baseDir := filepath.Join(t.TempDir(), "missing-base")
	service := &Service{baseDir: baseDir}
	firstRoot, err := defaultWorktreeRoot(baseDir, "workspace-1", "feature/collision")
	if err != nil {
		t.Fatalf("defaultWorktreeRoot: %v", err)
	}
	if err := os.MkdirAll(firstRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll collision root: %v", err)
	}
	firstRoot, err = config.CanonicalWorkspaceRoot(firstRoot)
	if err != nil {
		t.Fatalf("CanonicalWorkspaceRoot collision root: %v", err)
	}

	resolvedRoot, err := service.resolveRequestedWorktreeRoot("", "workspace-1", CreateSpec{CreateBranch: true, BranchName: "feature/collision"})
	if err != nil {
		t.Fatalf("resolveRequestedWorktreeRoot: %v", err)
	}
	if resolvedRoot == firstRoot {
		t.Fatalf("expected suffixed root after collision, got %q", resolvedRoot)
	}
	if !strings.HasPrefix(resolvedRoot, firstRoot+"-") {
		t.Fatalf("expected suffixed collision root, got %q (base %q)", resolvedRoot, firstRoot)
	}
	if _, err := os.Stat(filepath.Join(baseDir, "workspace-1")); err != nil {
		t.Fatalf("expected workspace base dir created, stat err=%v", err)
	}
}

func TestSwitchWorktreeClampsCwdAndAppendsLocalNote(t *testing.T) {
	env := newServiceTestEnv(t)
	created := mustCreateWorktree(t, env, "feature/switch-clamp")
	if err := os.MkdirAll(filepath.Join(created.CanonicalRoot, "pkg"), 0o755); err != nil {
		t.Fatalf("MkdirAll pkg: %v", err)
	}
	if err := env.store.UpdateSessionExecutionTargetByID(env.ctx, env.session.Meta().SessionID, env.binding.WorkspaceID, created.WorktreeID, "pkg"); err != nil {
		t.Fatalf("UpdateSessionExecutionTargetByID: %v", err)
	}
	main := findMainWorktreeView(t, mustListWorktrees(t, env).Worktrees)

	resp, err := env.service.SwitchWorktree(env.ctx, serverapi.WorktreeSwitchRequest{
		ClientRequestID:   "req-switch-main",
		SessionID:         env.session.Meta().SessionID,
		ControllerLeaseID: env.leaseID,
		WorktreeID:        main.WorktreeID,
	})
	if err != nil {
		t.Fatalf("SwitchWorktree: %v", err)
	}
	if resp.Target.WorktreeID != "" {
		t.Fatalf("target worktree id = %q, want main workspace", resp.Target.WorktreeID)
	}
	if resp.Target.CwdRelpath != "." {
		t.Fatalf("target cwd_relpath = %q, want .", resp.Target.CwdRelpath)
	}
	if resp.Target.EffectiveWorkdir != env.workspaceRoot {
		t.Fatalf("effective workdir = %q, want %q", resp.Target.EffectiveWorkdir, env.workspaceRoot)
	}
	if len(env.runtime.rebindCalls) == 0 || env.runtime.rebindCalls[len(env.runtime.rebindCalls)-1].root != env.workspaceRoot {
		t.Fatalf("expected rebind to main workspace, got %+v", env.runtime.rebindCalls)
	}
	notes := env.localNotes.snapshot()
	if len(notes) == 0 || !strings.Contains(notes[len(notes)-1], "Switched worktree to main workspace") {
		t.Fatalf("expected switch local note, got %+v", notes)
	}
	finalTarget, err := env.store.ResolveSessionExecutionTarget(env.ctx, env.session.Meta().SessionID)
	if err != nil {
		t.Fatalf("ResolveSessionExecutionTarget: %v", err)
	}
	if finalTarget.WorktreeID != "" || finalTarget.CwdRelpath != "." {
		t.Fatalf("unexpected final target after switch: %+v", finalTarget)
	}
}

func TestListWorktreesRetargetsMissingCurrentWorktreeBeforePruning(t *testing.T) {
	env := newServiceTestEnv(t)
	created := mustCreateWorktree(t, env, "feature/missing-current")
	otherSession := createServiceTestSession(t, env.store, env.cfg, env.binding)
	if err := os.MkdirAll(filepath.Join(created.CanonicalRoot, "pkg"), 0o755); err != nil {
		t.Fatalf("MkdirAll pkg: %v", err)
	}
	if err := env.store.UpdateSessionExecutionTargetByID(env.ctx, env.session.Meta().SessionID, env.binding.WorkspaceID, created.WorktreeID, "pkg"); err != nil {
		t.Fatalf("UpdateSessionExecutionTargetByID: %v", err)
	}
	if err := env.store.UpdateSessionExecutionTargetByID(env.ctx, otherSession.Meta().SessionID, env.binding.WorkspaceID, created.WorktreeID, "pkg"); err != nil {
		t.Fatalf("UpdateSessionExecutionTargetByID other session: %v", err)
	}
	env.runtime.rebindCalls = nil
	env.runtime.reminderCalls = nil
	runGit(t, env.workspaceRoot, "worktree", "remove", "--force", created.CanonicalRoot)

	resp, err := env.service.ListWorktrees(env.ctx, serverapi.WorktreeListRequest{SessionID: env.session.Meta().SessionID})
	if err != nil {
		t.Fatalf("ListWorktrees: %v", err)
	}
	if resp.Target.WorktreeID != "" {
		t.Fatalf("response target worktree id = %q, want main workspace", resp.Target.WorktreeID)
	}
	if resp.Target.CwdRelpath != "." {
		t.Fatalf("response target cwd_relpath = %q, want .", resp.Target.CwdRelpath)
	}
	if resp.Target.EffectiveWorkdir != env.workspaceRoot {
		t.Fatalf("response effective workdir = %q, want %q", resp.Target.EffectiveWorkdir, env.workspaceRoot)
	}
	for _, worktree := range resp.Worktrees {
		if worktree.WorktreeID == created.WorktreeID {
			t.Fatalf("expected missing worktree pruned from list, got %+v", worktree)
		}
	}
	resolved, err := env.store.ResolveSessionExecutionTarget(env.ctx, env.session.Meta().SessionID)
	if err != nil {
		t.Fatalf("ResolveSessionExecutionTarget: %v", err)
	}
	if resolved.WorktreeID != "" {
		t.Fatalf("stored target worktree id = %q, want main workspace", resolved.WorktreeID)
	}
	if resolved.WorktreeRoot != "" {
		t.Fatalf("stored target worktree root = %q, want empty", resolved.WorktreeRoot)
	}
	if resolved.CwdRelpath != "." {
		t.Fatalf("stored target cwd_relpath = %q, want .", resolved.CwdRelpath)
	}
	if resolved.EffectiveWorkdir != env.workspaceRoot {
		t.Fatalf("stored effective workdir = %q, want %q", resolved.EffectiveWorkdir, env.workspaceRoot)
	}
	otherTarget, err := env.store.ResolveSessionExecutionTarget(env.ctx, otherSession.Meta().SessionID)
	if err != nil {
		t.Fatalf("ResolveSessionExecutionTarget other session: %v", err)
	}
	if otherTarget.WorktreeID != "" || otherTarget.EffectiveWorkdir != env.workspaceRoot {
		t.Fatalf("expected other session retargeted to main workspace, got %+v", otherTarget)
	}
	if len(env.runtime.rebindCalls) != 1 {
		t.Fatalf("expected exactly one active-runtime rebind, got %+v", env.runtime.rebindCalls)
	}
	if got := env.runtime.rebindCalls[0]; got.sessionID != env.session.Meta().SessionID || got.root != env.workspaceRoot {
		t.Fatalf("unexpected active-runtime rebind call: %+v", got)
	}
	if len(env.runtime.reminderCalls) != 2 {
		t.Fatalf("expected reminder for each retargeted session, got %+v", env.runtime.reminderCalls)
	}
	for _, reminder := range env.runtime.reminderCalls {
		if reminder.Mode != session.WorktreeReminderModeExit {
			t.Fatalf("reminder mode = %q, want exit", reminder.Mode)
		}
		if reminder.WorktreePath != created.CanonicalRoot {
			t.Fatalf("reminder worktree path = %q, want %q", reminder.WorktreePath, created.CanonicalRoot)
		}
		if reminder.EffectiveCwd != env.workspaceRoot {
			t.Fatalf("reminder effective cwd = %q, want %q", reminder.EffectiveCwd, env.workspaceRoot)
		}
	}
}

func TestSwitchWorktreeRollsBackExecutionTargetWhenRebindFails(t *testing.T) {
	env := newServiceTestEnv(t)
	created := mustCreateWorktree(t, env, "feature/rebind-fail")
	main := findMainWorktreeView(t, mustListWorktrees(t, env).Worktrees)
	if _, err := env.service.SwitchWorktree(env.ctx, serverapi.WorktreeSwitchRequest{
		ClientRequestID:   "req-switch-reset-main",
		SessionID:         env.session.Meta().SessionID,
		ControllerLeaseID: env.leaseID,
		WorktreeID:        main.WorktreeID,
	}); err != nil {
		t.Fatalf("SwitchWorktree main reset: %v", err)
	}
	env.localNotes = &serviceTestLocalNotes{}
	env.service.localNotes = env.localNotes
	env.runtime.rebindErrRoot = created.CanonicalRoot
	env.runtime.rebindErr = errors.New("boom")

	_, err := env.service.SwitchWorktree(env.ctx, serverapi.WorktreeSwitchRequest{
		ClientRequestID:   "req-switch-fail",
		SessionID:         env.session.Meta().SessionID,
		ControllerLeaseID: env.leaseID,
		WorktreeID:        created.WorktreeID,
	})
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("SwitchWorktree error = %v, want rebind failure", err)
	}
	finalTarget, err := env.store.ResolveSessionExecutionTarget(env.ctx, env.session.Meta().SessionID)
	if err != nil {
		t.Fatalf("ResolveSessionExecutionTarget: %v", err)
	}
	if finalTarget.WorktreeID != "" || finalTarget.EffectiveWorkdir != env.workspaceRoot {
		t.Fatalf("expected execution target rollback to main workspace, got %+v", finalTarget)
	}
	if notes := env.localNotes.snapshot(); len(notes) != 0 {
		t.Fatalf("expected no local notes on failed switch, got %+v", notes)
	}
}

func TestDeleteWorktreeBlocksWhenAnotherSessionTargetsIt(t *testing.T) {
	env := newServiceTestEnv(t)
	created := mustCreateWorktree(t, env, "feature/delete-blocked-session")
	otherSession := createServiceTestSession(t, env.store, env.cfg, env.binding)
	if err := env.store.UpdateSessionExecutionTargetByID(env.ctx, otherSession.Meta().SessionID, env.binding.WorkspaceID, created.WorktreeID, "."); err != nil {
		t.Fatalf("UpdateSessionExecutionTargetByID other session: %v", err)
	}

	_, err := env.service.DeleteWorktree(env.ctx, serverapi.WorktreeDeleteRequest{
		ClientRequestID:   "req-delete-blocked-session",
		SessionID:         env.session.Meta().SessionID,
		ControllerLeaseID: env.leaseID,
		WorktreeID:        created.WorktreeID,
	})
	if !errors.Is(err, serverapi.ErrWorktreeBlocked) {
		t.Fatalf("DeleteWorktree error = %v, want ErrWorktreeBlocked", err)
	}
}

func TestDeleteWorktreeBlocksWhenBackgroundProcessUsesDescendantPath(t *testing.T) {
	env := newServiceTestEnv(t)
	created := mustCreateWorktree(t, env, "feature/delete-blocked-process")
	env.processes.snapshots = []shelltool.Snapshot{{ID: "proc-1", Command: "sleep 30", Workdir: filepath.Join(created.CanonicalRoot, "tmp"), Running: true}}

	_, err := env.service.DeleteWorktree(env.ctx, serverapi.WorktreeDeleteRequest{
		ClientRequestID:   "req-delete-blocked-process",
		SessionID:         env.session.Meta().SessionID,
		ControllerLeaseID: env.leaseID,
		WorktreeID:        created.WorktreeID,
	})
	if !errors.Is(err, serverapi.ErrWorktreeBlocked) {
		t.Fatalf("DeleteWorktree error = %v, want ErrWorktreeBlocked", err)
	}
}

func TestDeleteWorktreeRebindsCurrentSessionToMainBeforeRemoval(t *testing.T) {
	env := newServiceTestEnv(t)
	created := mustCreateWorktree(t, env, "feature/delete-current")
	if _, err := env.service.SwitchWorktree(env.ctx, serverapi.WorktreeSwitchRequest{
		ClientRequestID:   "req-switch-delete-target",
		SessionID:         env.session.Meta().SessionID,
		ControllerLeaseID: env.leaseID,
		WorktreeID:        created.WorktreeID,
	}); err != nil {
		t.Fatalf("SwitchWorktree: %v", err)
	}
	env.localNotes = &serviceTestLocalNotes{}
	env.service.localNotes = env.localNotes

	resp, err := env.service.DeleteWorktree(env.ctx, serverapi.WorktreeDeleteRequest{
		ClientRequestID:   "req-delete-current",
		SessionID:         env.session.Meta().SessionID,
		ControllerLeaseID: env.leaseID,
		WorktreeID:        created.WorktreeID,
	})
	if err != nil {
		t.Fatalf("DeleteWorktree: %v", err)
	}
	if resp.Target.WorktreeID != "" || resp.Target.EffectiveWorkdir != env.workspaceRoot {
		t.Fatalf("unexpected final delete target: %+v", resp.Target)
	}
	if len(env.runtime.rebindCalls) < 2 {
		t.Fatalf("expected switch to worktree and delete-time rebind back to main, got %+v", env.runtime.rebindCalls)
	}
	if got := env.runtime.rebindCalls[len(env.runtime.rebindCalls)-1].root; got != env.workspaceRoot {
		t.Fatalf("final rebind root = %q, want %q", got, env.workspaceRoot)
	}
	if _, err := os.Stat(created.CanonicalRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected worktree root removed, stat err=%v", err)
	}
	worktrees := mustListWorktrees(t, env).Worktrees
	for _, worktree := range worktrees {
		if worktree.WorktreeID == created.WorktreeID {
			t.Fatalf("expected deleted worktree to disappear from list, got %+v", worktree)
		}
	}
	notes := env.localNotes.snapshot()
	if len(notes) == 0 || !strings.Contains(notes[0], "Switched worktree to main workspace") {
		t.Fatalf("expected delete path to append switch note, got %+v", notes)
	}
}

func TestBeginMutationSerializesMutationsByWorkspace(t *testing.T) {
	env := newServiceTestEnv(t)
	otherSession := createServiceTestSession(t, env.store, env.cfg, env.binding)

	firstRelease, _, err := env.service.beginMutation(env.ctx, env.session.Meta().SessionID, env.leaseID)
	if err != nil {
		t.Fatalf("beginMutation first: %v", err)
	}
	firstReleased := false
	t.Cleanup(func() {
		if !firstReleased {
			firstRelease.Release()
		}
	})

	type mutationResult struct {
		release primaryrun.Lease
		err     error
	}
	resultCh := make(chan mutationResult, 1)
	go func() {
		release, _, err := env.service.beginMutation(env.ctx, otherSession.Meta().SessionID, "lease-2")
		resultCh <- mutationResult{release: release, err: err}
	}()

	select {
	case result := <-resultCh:
		if result.release != nil {
			result.release.Release()
		}
		t.Fatalf("expected second mutation to wait for workspace lock, got err=%v", result.err)
	case <-time.After(100 * time.Millisecond):
	}

	firstRelease.Release()
	firstReleased = true
	var result mutationResult
	select {
	case result = <-resultCh:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for second mutation")
	}
	if result.err != nil {
		t.Fatalf("beginMutation second: %v", result.err)
	}
	if result.release == nil {
		t.Fatal("expected second mutation lease")
	}
	result.release.Release()
}

func TestRetargetSessionsFromMissingWorktreeContinuesAfterRuntimeError(t *testing.T) {
	env := newServiceTestEnv(t)
	created := mustCreateWorktree(t, env, "feature/missing-runtime-error")
	otherSession := createServiceTestSession(t, env.store, env.cfg, env.binding)
	if err := env.store.UpdateSessionExecutionTargetByID(env.ctx, otherSession.Meta().SessionID, env.binding.WorkspaceID, created.WorktreeID, "."); err != nil {
		t.Fatalf("UpdateSessionExecutionTargetByID other session: %v", err)
	}
	if err := env.store.UpdateSessionExecutionTargetByID(env.ctx, env.session.Meta().SessionID, env.binding.WorkspaceID, created.WorktreeID, "."); err != nil {
		t.Fatalf("UpdateSessionExecutionTargetByID active session: %v", err)
	}
	record, err := env.store.GetWorktreeRecordByID(env.ctx, created.WorktreeID)
	if err != nil {
		t.Fatalf("GetWorktreeRecordByID: %v", err)
	}
	env.runtime.rebindErrRoot = env.workspaceRoot
	env.runtime.rebindErr = errors.New("runtime rebind failed")
	env.runtime.activeSessions = map[string]bool{env.session.Meta().SessionID: true}
	env.runtime.rebindCalls = nil
	env.runtime.reminderCalls = nil

	err = env.service.retargetSessionsFromMissingWorktree(env.ctx, env.binding.WorkspaceID, env.workspaceRoot, record)
	if err == nil || !strings.Contains(err.Error(), "runtime rebind failed") {
		t.Fatalf("retargetSessionsFromMissingWorktree error = %v, want runtime rebind failed", err)
	}
	for _, sessionID := range []string{env.session.Meta().SessionID, otherSession.Meta().SessionID} {
		target, resolveErr := env.store.ResolveSessionExecutionTarget(env.ctx, sessionID)
		if resolveErr != nil {
			t.Fatalf("ResolveSessionExecutionTarget %s: %v", sessionID, resolveErr)
		}
		if target.WorktreeID != "" || target.EffectiveWorkdir != env.workspaceRoot {
			t.Fatalf("expected session %s retargeted to main workspace, got %+v", sessionID, target)
		}
	}
	if len(env.runtime.rebindCalls) != 1 {
		t.Fatalf("expected one active runtime rebind attempt, got %+v", env.runtime.rebindCalls)
	}
	if len(env.runtime.reminderCalls) != 2 {
		t.Fatalf("expected reminder for both sessions, got %+v", env.runtime.reminderCalls)
	}
}

func TestNextAvailableWorktreeRootFailsAfterCollisionCap(t *testing.T) {
	baseRoot := filepath.Join(t.TempDir(), "collision")
	for idx := 0; idx < 1024; idx++ {
		candidate := baseRoot
		if idx > 0 {
			candidate = baseRoot + "-" + strconv.Itoa(idx+1)
		}
		if err := os.MkdirAll(candidate, 0o755); err != nil {
			t.Fatalf("MkdirAll %s: %v", candidate, err)
		}
	}

	_, err := nextAvailableWorktreeRoot(baseRoot)
	if err == nil || !strings.Contains(err.Error(), "after 1024 attempts") {
		t.Fatalf("nextAvailableWorktreeRoot error = %v, want capped collision error", err)
	}
}

func newServiceTestEnv(t *testing.T) *serviceTestEnv {
	t.Helper()
	ctx := context.Background()
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	initGitRepo(t, workspace)
	cfg, err := config.Load(workspace, config.LoadOptions{})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	store, err := metadata.Open(cfg.PersistenceRoot)
	if err != nil {
		t.Fatalf("metadata.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	binding, err := store.RegisterWorkspaceBinding(ctx, cfg.WorkspaceRoot)
	if err != nil {
		t.Fatalf("RegisterWorkspaceBinding: %v", err)
	}
	canonicalWorkspaceRoot, err := config.CanonicalWorkspaceRoot(cfg.WorkspaceRoot)
	if err != nil {
		t.Fatalf("CanonicalWorkspaceRoot: %v", err)
	}
	sess := createServiceTestSession(t, store, cfg, binding)
	runtime := &serviceTestRuntime{}
	runtime.activeSessions = map[string]bool{sess.Meta().SessionID: true}
	processes := &serviceTestProcessSource{}
	localNotes := &serviceTestLocalNotes{}
	service := NewService(store, nil, serviceTestGate{}, runtime, processes, localNotes, ServiceOptions{BaseDir: cfg.Settings.Worktrees.BaseDir})
	return &serviceTestEnv{
		t:             t,
		ctx:           ctx,
		store:         store,
		cfg:           cfg,
		binding:       binding,
		session:       sess,
		runtime:       runtime,
		processes:     processes,
		localNotes:    localNotes,
		service:       service,
		leaseID:       "lease-1",
		workspaceRoot: canonicalWorkspaceRoot,
		baseDir:       cfg.Settings.Worktrees.BaseDir,
	}
}

func createServiceTestSession(t *testing.T, store *metadata.Store, cfg config.App, binding metadata.Binding) *session.Store {
	t.Helper()
	projectSessionsDir := config.ProjectSessionsRoot(cfg, binding.ProjectID)
	sess, err := session.Create(projectSessionsDir, filepath.Base(projectSessionsDir), cfg.WorkspaceRoot, store.AuthoritativeSessionStoreOptions()...)
	if err != nil {
		t.Fatalf("session.Create: %v", err)
	}
	if err := sess.EnsureDurable(); err != nil {
		t.Fatalf("EnsureDurable: %v", err)
	}
	return sess
}

func initGitRepo(t *testing.T, root string) {
	t.Helper()
	runGit(t, root, "init", "-q")
	runGit(t, root, "config", "user.email", "builder@test.invalid")
	runGit(t, root, "config", "user.name", "Builder Test")
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("root\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	runGit(t, root, "add", "README.md")
	runGit(t, root, "commit", "-q", "-m", "init")
	canonicalRoot, err := config.CanonicalWorkspaceRoot(root)
	if err != nil {
		t.Fatalf("CanonicalWorkspaceRoot: %v", err)
	}
	if got, want := currentGitTopLevel(t, root), canonicalRoot; got != want {
		t.Fatalf("git top-level = %q, want %q", got, want)
	}
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = sanitizedGitTestEnv(os.Environ())
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return strings.TrimSpace(string(output))
}

func sanitizedGitTestEnv(base []string) []string {
	filtered := make([]string, 0, len(base))
	for _, entry := range base {
		key := entry
		if idx := strings.IndexByte(entry, '='); idx >= 0 {
			key = entry[:idx]
		}
		switch key {
		case "GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_COMMON_DIR", "GIT_CONFIG", "GIT_CONFIG_COUNT", "GIT_CONFIG_PARAMETERS", "GIT_DIR", "GIT_GLOB_PATHSPECS", "GIT_GRAFT_FILE", "GIT_ICASE_PATHSPECS", "GIT_IMPLICIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_INTERNAL_SUPER_PREFIX", "GIT_LITERAL_PATHSPECS", "GIT_NAMESPACE", "GIT_NOGLOB_PATHSPECS", "GIT_NO_REPLACE_OBJECTS", "GIT_OBJECT_DIRECTORY", "GIT_PREFIX", "GIT_REPLACE_REF_BASE", "GIT_SHALLOW_FILE", "GIT_WORK_TREE":
			continue
		}
		if strings.HasPrefix(key, "GIT_CONFIG_KEY_") || strings.HasPrefix(key, "GIT_CONFIG_VALUE_") {
			continue
		}
		filtered = append(filtered, entry)
	}
	return filtered
}

func currentGitTopLevel(t *testing.T, dir string) string {
	t.Helper()
	return runGit(t, dir, "rev-parse", "--show-toplevel")
}

func writeExecutableFile(t *testing.T, path string, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("WriteFile %s: %v", path, err)
	}
}

func waitForSetupPayload(t *testing.T, path string) setupScriptPayload {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		body, err := os.ReadFile(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				time.Sleep(20 * time.Millisecond)
				continue
			}
			t.Fatalf("ReadFile %s: %v", path, err)
		}
		var payload setupScriptPayload
		if err := json.Unmarshal(body, &payload); err != nil {
			time.Sleep(20 * time.Millisecond)
			continue
		}
		return payload
	}
	t.Fatalf("timed out waiting for setup payload at %s", path)
	return setupScriptPayload{}
}

func waitForFileText(t *testing.T, path string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		body, err := os.ReadFile(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				time.Sleep(20 * time.Millisecond)
				continue
			}
			t.Fatalf("ReadFile %s: %v", path, err)
		}
		return strings.TrimSpace(string(body))
	}
	t.Fatalf("timed out waiting for text file at %s", path)
	return ""
}

func waitForFileLines(t *testing.T, path string) []string {
	t.Helper()
	text := waitForFileText(t, path)
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

func mustCreateWorktree(t *testing.T, env *serviceTestEnv, branchName string) serverapi.WorktreeView {
	t.Helper()
	resp, err := env.service.CreateWorktree(env.ctx, serverapi.WorktreeCreateRequest{
		ClientRequestID:   "req-create-" + strings.ReplaceAll(branchName, "/", "-"),
		SessionID:         env.session.Meta().SessionID,
		ControllerLeaseID: env.leaseID,
		CreateBranch:      true,
		BranchName:        branchName,
	})
	if err != nil {
		t.Fatalf("CreateWorktree(%s): %v", branchName, err)
	}
	return resp.Worktree
}

func mustListWorktrees(t *testing.T, env *serviceTestEnv) serverapi.WorktreeListResponse {
	t.Helper()
	resp, err := env.service.ListWorktrees(env.ctx, serverapi.WorktreeListRequest{SessionID: env.session.Meta().SessionID})
	if err != nil {
		t.Fatalf("ListWorktrees: %v", err)
	}
	return resp
}

func findWorktreeByID(t *testing.T, worktrees []serverapi.WorktreeView, worktreeID string) serverapi.WorktreeView {
	t.Helper()
	for _, worktree := range worktrees {
		if worktree.WorktreeID == worktreeID {
			return worktree
		}
	}
	t.Fatalf("worktree %q not found in %+v", worktreeID, worktrees)
	return serverapi.WorktreeView{}
}

func findMainWorktreeView(t *testing.T, worktrees []serverapi.WorktreeView) serverapi.WorktreeView {
	t.Helper()
	for _, worktree := range worktrees {
		if worktree.IsMain {
			return worktree
		}
	}
	t.Fatalf("main worktree not found in %+v", worktrees)
	return serverapi.WorktreeView{}
}

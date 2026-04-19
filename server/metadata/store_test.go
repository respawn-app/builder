package metadata

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"builder/server/metadata/sqlitegen"
	"builder/server/session"
	"builder/shared/clientui"
	"builder/shared/config"
	"builder/shared/serverapi"
)

func TestEnsureWorkspaceBindingDoesNotRegisterUnknownWorkspace(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)

	cfg, err := config.Load(workspace, config.LoadOptions{})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	store, err := Open(cfg.PersistenceRoot)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = store.Close() }()

	if _, err := store.EnsureWorkspaceBinding(context.Background(), cfg.WorkspaceRoot); !errors.Is(err, serverapi.ErrWorkspaceNotRegistered) {
		t.Fatalf("EnsureWorkspaceBinding error = %v, want ErrWorkspaceNotRegistered", err)
	}
	projects, err := store.ListProjects(context.Background())
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(projects) != 0 {
		t.Fatalf("expected no registered projects, got %+v", projects)
	}

	binding, err := store.RegisterWorkspaceBinding(context.Background(), cfg.WorkspaceRoot)
	if err != nil {
		t.Fatalf("RegisterWorkspaceBinding: %v", err)
	}
	if binding.ProjectID == "" || binding.WorkspaceID == "" {
		t.Fatalf("expected registered binding ids, got %+v", binding)
	}

	resolved, err := store.EnsureWorkspaceBinding(context.Background(), cfg.WorkspaceRoot)
	if err != nil {
		t.Fatalf("EnsureWorkspaceBinding after registration: %v", err)
	}
	if resolved.ProjectID != binding.ProjectID || resolved.WorkspaceID != binding.WorkspaceID {
		t.Fatalf("resolved binding mismatch: got %+v want %+v", resolved, binding)
	}
}

func TestResolveWorkspacePathLeavesNestedDirectoryUnbound(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	nested := filepath.Join(workspace, "subdir", "deeper")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("MkdirAll nested: %v", err)
	}
	t.Setenv("HOME", home)

	cfg, err := config.Load(workspace, config.LoadOptions{})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	store, err := Open(cfg.PersistenceRoot)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = store.Close() }()

	binding, err := store.RegisterWorkspaceBinding(context.Background(), cfg.WorkspaceRoot)
	if err != nil {
		t.Fatalf("RegisterWorkspaceBinding: %v", err)
	}

	canonicalNested, resolved, err := store.ResolveWorkspacePath(context.Background(), nested)
	if err != nil {
		t.Fatalf("ResolveWorkspacePath nested: %v", err)
	}
	if canonicalNested == binding.CanonicalRoot {
		t.Fatalf("expected resolved canonical path for nested directory, got workspace root %q", canonicalNested)
	}
	if resolved != nil {
		t.Fatalf("expected nested directory to remain unbound, got %+v", *resolved)
	}

	if _, err := store.EnsureWorkspaceBinding(context.Background(), nested); !errors.Is(err, serverapi.ErrWorkspaceNotRegistered) {
		t.Fatalf("EnsureWorkspaceBinding nested error = %v, want ErrWorkspaceNotRegistered", err)
	}

	registered, err := store.RegisterWorkspaceBinding(context.Background(), nested)
	if err != nil {
		t.Fatalf("RegisterWorkspaceBinding nested: %v", err)
	}
	if registered.CanonicalRoot == binding.CanonicalRoot {
		t.Fatalf("expected nested registration to create its own workspace, got %+v", registered)
	}
	if registered.CanonicalRoot != canonicalNested {
		t.Fatalf("registered nested root = %q, want %q", registered.CanonicalRoot, canonicalNested)
	}

	projects, err := store.ListProjects(context.Background())
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(projects) != 2 {
		t.Fatalf("project count = %d, want 2", len(projects))
	}
}

func TestLookupWorkspaceBindingByIDReturnsWorkspaceNotRegisteredForUnknownID(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)

	cfg, err := config.Load(workspace, config.LoadOptions{})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	store, err := Open(cfg.PersistenceRoot)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = store.Close() }()

	if _, err := store.LookupWorkspaceBindingByID(context.Background(), "workspace-missing"); !errors.Is(err, serverapi.ErrWorkspaceNotRegistered) {
		t.Fatalf("LookupWorkspaceBindingByID error = %v, want ErrWorkspaceNotRegistered", err)
	}
}

func TestAttachWorkspaceToProjectAllowsNestedPathAsSeparateWorkspace(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	nested := filepath.Join(workspace, "nested")
	other := t.TempDir()
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("MkdirAll nested: %v", err)
	}
	t.Setenv("HOME", home)

	cfg, err := config.Load(workspace, config.LoadOptions{})
	if err != nil {
		t.Fatalf("config.Load workspace: %v", err)
	}
	otherCfg, err := config.Load(other, config.LoadOptions{})
	if err != nil {
		t.Fatalf("config.Load other: %v", err)
	}
	store, err := Open(cfg.PersistenceRoot)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = store.Close() }()

	binding, err := store.RegisterWorkspaceBinding(context.Background(), cfg.WorkspaceRoot)
	if err != nil {
		t.Fatalf("RegisterWorkspaceBinding workspace: %v", err)
	}
	otherBinding, err := store.RegisterWorkspaceBinding(context.Background(), otherCfg.WorkspaceRoot)
	if err != nil {
		t.Fatalf("RegisterWorkspaceBinding other: %v", err)
	}

	resolved, err := store.AttachWorkspaceToProject(context.Background(), binding.ProjectID, nested)
	if err != nil {
		t.Fatalf("AttachWorkspaceToProject nested: %v", err)
	}
	if resolved.ProjectID != binding.ProjectID {
		t.Fatalf("nested attach project id = %q, want %q", resolved.ProjectID, binding.ProjectID)
	}
	if resolved.CanonicalRoot == binding.CanonicalRoot {
		t.Fatalf("expected nested attach to create separate workspace, got %+v", resolved)
	}
	canonicalNested, err := config.CanonicalWorkspaceRoot(nested)
	if err != nil {
		t.Fatalf("CanonicalWorkspaceRoot nested: %v", err)
	}
	if resolved.CanonicalRoot != canonicalNested {
		t.Fatalf("nested attach root = %q, want %q", resolved.CanonicalRoot, canonicalNested)
	}

	_, err = store.AttachWorkspaceToProject(context.Background(), otherBinding.ProjectID, nested)
	if err == nil || !strings.Contains(err.Error(), "already bound") {
		t.Fatalf("expected exact-root conflict on second nested attach, got %v", err)
	}

	projects, err := store.ListProjects(context.Background())
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(projects) != 2 {
		t.Fatalf("project count = %d, want 2", len(projects))
	}
}

func TestRebindWorkspacePreservesWorkspaceIdentity(t *testing.T) {
	home := t.TempDir()
	oldWorkspace := t.TempDir()
	newParent := t.TempDir()
	newWorkspace := filepath.Join(newParent, "renamed-workspace")
	t.Setenv("HOME", home)

	cfg, err := config.Load(oldWorkspace, config.LoadOptions{})
	if err != nil {
		t.Fatalf("config.Load oldWorkspace: %v", err)
	}
	store, err := Open(cfg.PersistenceRoot)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = store.Close() }()

	binding, err := store.RegisterWorkspaceBinding(context.Background(), cfg.WorkspaceRoot)
	if err != nil {
		t.Fatalf("RegisterWorkspaceBinding: %v", err)
	}
	sessionID := "session-rebind"
	sessionDir := config.ProjectSessionDir(cfg, binding.ProjectID, sessionID)
	if err := store.ImportSessionSnapshot(context.Background(), session.PersistedStoreSnapshot{
		SessionDir: sessionDir,
		Meta: session.Meta{
			SessionID:          sessionID,
			WorkspaceRoot:      binding.CanonicalRoot,
			WorkspaceContainer: filepath.Base(sessionDir),
			FirstPromptPreview: "hello",
			CreatedAt:          time.Now().UTC(),
			UpdatedAt:          time.Now().UTC(),
		},
	}); err != nil {
		t.Fatalf("ImportSessionSnapshot: %v", err)
	}
	if err := os.Rename(oldWorkspace, newWorkspace); err != nil {
		t.Fatalf("Rename workspace: %v", err)
	}

	rebound, err := store.RebindWorkspace(context.Background(), oldWorkspace, newWorkspace)
	if err != nil {
		t.Fatalf("RebindWorkspace: %v", err)
	}
	canonicalNewWorkspace, err := config.CanonicalWorkspaceRoot(newWorkspace)
	if err != nil {
		t.Fatalf("CanonicalWorkspaceRoot newWorkspace: %v", err)
	}
	if rebound.WorkspaceID != binding.WorkspaceID {
		t.Fatalf("rebound workspace id = %q, want %q", rebound.WorkspaceID, binding.WorkspaceID)
	}
	if rebound.ProjectID != binding.ProjectID {
		t.Fatalf("rebound project id = %q, want %q", rebound.ProjectID, binding.ProjectID)
	}
	if rebound.CanonicalRoot != canonicalNewWorkspace {
		t.Fatalf("rebound canonical root = %q, want %q", rebound.CanonicalRoot, canonicalNewWorkspace)
	}
	if _, err := store.EnsureWorkspaceBinding(context.Background(), oldWorkspace); !errors.Is(err, serverapi.ErrWorkspaceNotRegistered) {
		t.Fatalf("EnsureWorkspaceBinding old workspace error = %v, want ErrWorkspaceNotRegistered", err)
	}
	resolved, err := store.EnsureWorkspaceBinding(context.Background(), newWorkspace)
	if err != nil {
		t.Fatalf("EnsureWorkspaceBinding new workspace: %v", err)
	}
	if resolved.WorkspaceID != binding.WorkspaceID {
		t.Fatalf("resolved rebound workspace id = %q, want %q", resolved.WorkspaceID, binding.WorkspaceID)
	}
	var sessionWorkspaceID string
	if err := store.db.QueryRowContext(context.Background(), "SELECT workspace_id FROM sessions WHERE id = ?", sessionID).Scan(&sessionWorkspaceID); err != nil {
		t.Fatalf("scan rebound session workspace id: %v", err)
	}
	if sessionWorkspaceID != binding.WorkspaceID {
		t.Fatalf("session workspace id = %q, want %q", sessionWorkspaceID, binding.WorkspaceID)
	}
	var workspaceCount int
	if err := store.db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM workspaces WHERE project_id = ?", binding.ProjectID).Scan(&workspaceCount); err != nil {
		t.Fatalf("count project workspaces: %v", err)
	}
	if workspaceCount != 1 {
		t.Fatalf("workspace count after rebind = %d, want 1", workspaceCount)
	}
}

func TestRebindWorkspaceRejectsInvalidTargets(t *testing.T) {
	home := t.TempDir()
	oldWorkspace := t.TempDir()
	otherWorkspace := t.TempDir()
	missingWorkspace := filepath.Join(t.TempDir(), "missing")
	t.Setenv("HOME", home)

	cfg, err := config.Load(oldWorkspace, config.LoadOptions{})
	if err != nil {
		t.Fatalf("config.Load oldWorkspace: %v", err)
	}
	otherCfg, err := config.Load(otherWorkspace, config.LoadOptions{})
	if err != nil {
		t.Fatalf("config.Load otherWorkspace: %v", err)
	}
	store, err := Open(cfg.PersistenceRoot)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = store.Close() }()

	oldBinding, err := store.RegisterWorkspaceBinding(context.Background(), cfg.WorkspaceRoot)
	if err != nil {
		t.Fatalf("RegisterWorkspaceBinding oldWorkspace: %v", err)
	}
	_, err = store.RegisterWorkspaceBinding(context.Background(), otherCfg.WorkspaceRoot)
	if err != nil {
		t.Fatalf("RegisterWorkspaceBinding otherWorkspace: %v", err)
	}

	if _, err := store.RebindWorkspace(context.Background(), filepath.Join(t.TempDir(), "unknown-old"), otherWorkspace); !errors.Is(err, serverapi.ErrWorkspaceNotRegistered) {
		t.Fatalf("RebindWorkspace unknown old error = %v, want ErrWorkspaceNotRegistered", err)
	}
	if _, err := store.RebindWorkspace(context.Background(), oldWorkspace, missingWorkspace); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("RebindWorkspace missing new error = %v, want does not exist", err)
	}
	if _, err := store.RebindWorkspace(context.Background(), oldWorkspace, otherWorkspace); err == nil || !strings.Contains(err.Error(), "already bound") {
		t.Fatalf("RebindWorkspace bound new error = %v, want already bound", err)
	}
	resolved, err := store.EnsureWorkspaceBinding(context.Background(), oldWorkspace)
	if err != nil {
		t.Fatalf("EnsureWorkspaceBinding old workspace after failed rebinds: %v", err)
	}
	if resolved.WorkspaceID != oldBinding.WorkspaceID {
		t.Fatalf("resolved workspace id after failed rebinds = %q, want %q", resolved.WorkspaceID, oldBinding.WorkspaceID)
	}
}

func TestRetargetSessionWorkspaceAttachesTargetAndUpdatesSession(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	workspaceA := t.TempDir()
	workspaceB := t.TempDir()
	t.Setenv("HOME", home)

	cfg, err := config.Load(workspaceA, config.LoadOptions{})
	if err != nil {
		t.Fatalf("config.Load workspaceA: %v", err)
	}
	store, err := Open(cfg.PersistenceRoot)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = store.Close() }()

	bindingA, err := store.RegisterWorkspaceBinding(ctx, cfg.WorkspaceRoot)
	if err != nil {
		t.Fatalf("RegisterWorkspaceBinding workspaceA: %v", err)
	}
	sess, err := session.Create(
		config.ProjectSessionsRoot(cfg, bindingA.ProjectID),
		filepath.Base(cfg.WorkspaceRoot),
		cfg.WorkspaceRoot,
		store.AuthoritativeSessionStoreOptions()...,
	)
	if err != nil {
		t.Fatalf("session.Create: %v", err)
	}
	if err := sess.SetName("incident triage"); err != nil {
		t.Fatalf("SetName: %v", err)
	}

	retargeted, err := store.RetargetSessionWorkspace(ctx, sess.Meta().SessionID, workspaceB)
	if err != nil {
		t.Fatalf("RetargetSessionWorkspace: %v", err)
	}
	canonicalWorkspaceB, err := config.CanonicalWorkspaceRoot(workspaceB)
	if err != nil {
		t.Fatalf("CanonicalWorkspaceRoot workspaceB: %v", err)
	}
	if retargeted.ProjectID != bindingA.ProjectID {
		t.Fatalf("retargeted project id = %q, want %q", retargeted.ProjectID, bindingA.ProjectID)
	}
	if retargeted.CanonicalRoot != canonicalWorkspaceB {
		t.Fatalf("retargeted canonical root = %q, want %q", retargeted.CanonicalRoot, canonicalWorkspaceB)
	}

	resolvedBinding, err := store.EnsureWorkspaceBinding(ctx, workspaceB)
	if err != nil {
		t.Fatalf("EnsureWorkspaceBinding workspaceB: %v", err)
	}
	if resolvedBinding.ProjectID != bindingA.ProjectID {
		t.Fatalf("workspaceB project id = %q, want %q", resolvedBinding.ProjectID, bindingA.ProjectID)
	}

	target, err := store.ResolveSessionExecutionTarget(ctx, sess.Meta().SessionID)
	if err != nil {
		t.Fatalf("ResolveSessionExecutionTarget: %v", err)
	}
	if target.WorkspaceID != retargeted.WorkspaceID {
		t.Fatalf("target workspace id = %q, want %q", target.WorkspaceID, retargeted.WorkspaceID)
	}
	if target.WorkspaceRoot != canonicalWorkspaceB {
		t.Fatalf("target workspace root = %q, want %q", target.WorkspaceRoot, canonicalWorkspaceB)
	}

	reopened, err := session.OpenByID(cfg.PersistenceRoot, sess.Meta().SessionID, store.AuthoritativeSessionStoreOptions()...)
	if err != nil {
		t.Fatalf("session.OpenByID: %v", err)
	}
	if reopened.Meta().WorkspaceRoot != canonicalWorkspaceB {
		t.Fatalf("reopened workspace root = %q, want %q", reopened.Meta().WorkspaceRoot, canonicalWorkspaceB)
	}
}

func TestRebindWorkspaceRetargetsDescendantWorktrees(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	oldWorkspace := t.TempDir()
	oldWorktree := filepath.Join(oldWorkspace, "wt-a")
	newParent := t.TempDir()
	newWorkspace := filepath.Join(newParent, "workspace-moved")
	newWorktree := filepath.Join(newWorkspace, "wt-a")
	if err := os.MkdirAll(oldWorktree, 0o755); err != nil {
		t.Fatalf("MkdirAll oldWorktree: %v", err)
	}
	t.Setenv("HOME", home)

	cfg, err := config.Load(oldWorkspace, config.LoadOptions{})
	if err != nil {
		t.Fatalf("config.Load oldWorkspace: %v", err)
	}
	store, err := Open(cfg.PersistenceRoot)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = store.Close() }()

	binding, err := store.RegisterWorkspaceBinding(ctx, cfg.WorkspaceRoot)
	if err != nil {
		t.Fatalf("RegisterWorkspaceBinding: %v", err)
	}
	worktreeID := "worktree-rebind"
	canonicalOldWorktree, err := config.CanonicalWorkspaceRoot(oldWorktree)
	if err != nil {
		t.Fatalf("CanonicalWorkspaceRoot oldWorktree: %v", err)
	}
	now := time.Now().UTC().UnixMilli()
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO worktrees (
			id,
			workspace_id,
			canonical_root_path,
			display_name,
			availability,
			is_main,
			git_metadata_json,
			created_at_unix_ms,
			updated_at_unix_ms
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, worktreeID, binding.WorkspaceID, canonicalOldWorktree, filepath.Base(canonicalOldWorktree), "available", 1, "{}", now, now); err != nil {
		t.Fatalf("insert worktree: %v", err)
	}
	projectSessionsDir := config.ProjectSessionsRoot(cfg, binding.ProjectID)
	sess, err := session.Create(projectSessionsDir, filepath.Base(projectSessionsDir), cfg.WorkspaceRoot, store.AuthoritativeSessionStoreOptions()...)
	if err != nil {
		t.Fatalf("session.Create: %v", err)
	}
	if err := sess.SetName("hello"); err != nil {
		t.Fatalf("SetName: %v", err)
	}
	if err := sess.EnsureDurable(); err != nil {
		t.Fatalf("EnsureDurable: %v", err)
	}
	sessionID := sess.Meta().SessionID
	if _, err := store.db.ExecContext(ctx, "UPDATE sessions SET worktree_id = ? WHERE id = ?", worktreeID, sessionID); err != nil {
		t.Fatalf("attach worktree to session: %v", err)
	}
	if err := os.Rename(oldWorkspace, newWorkspace); err != nil {
		t.Fatalf("Rename workspace: %v", err)
	}

	rebound, err := store.RebindWorkspace(ctx, oldWorkspace, newWorkspace)
	if err != nil {
		t.Fatalf("RebindWorkspace: %v", err)
	}
	canonicalNewWorktree, err := config.CanonicalWorkspaceRoot(newWorktree)
	if err != nil {
		t.Fatalf("CanonicalWorkspaceRoot newWorktree: %v", err)
	}
	var storedWorktreeRoot string
	if err := store.db.QueryRowContext(ctx, "SELECT canonical_root_path FROM worktrees WHERE id = ?", worktreeID).Scan(&storedWorktreeRoot); err != nil {
		t.Fatalf("scan rebound worktree root: %v", err)
	}
	if storedWorktreeRoot != canonicalNewWorktree {
		t.Fatalf("stored worktree root = %q, want %q", storedWorktreeRoot, canonicalNewWorktree)
	}
	target, err := store.ResolveSessionExecutionTarget(ctx, sessionID)
	if err != nil {
		t.Fatalf("ResolveSessionExecutionTarget: %v", err)
	}
	if target.WorktreeID != worktreeID {
		t.Fatalf("target worktree id = %q, want %q", target.WorktreeID, worktreeID)
	}
	if target.WorktreeRoot != canonicalNewWorktree {
		t.Fatalf("target worktree root = %q, want %q", target.WorktreeRoot, canonicalNewWorktree)
	}
	if target.EffectiveWorkdir != canonicalNewWorktree {
		t.Fatalf("effective workdir = %q, want %q", target.EffectiveWorkdir, canonicalNewWorktree)
	}
	reopened, err := session.OpenByID(cfg.PersistenceRoot, sessionID, store.AuthoritativeSessionStoreOptions()...)
	if err != nil {
		t.Fatalf("session.OpenByID: %v", err)
	}
	if got := reopened.Meta().WorkspaceRoot; got != rebound.CanonicalRoot {
		t.Fatalf("reopened workspace root = %q, want %q", got, rebound.CanonicalRoot)
	}
}

func TestRebindWorkspaceNormalizesUniqueConflictRace(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	oldWorkspace := t.TempDir()
	otherWorkspace := t.TempDir()
	newWorkspace := filepath.Join(t.TempDir(), "workspace-target")
	if err := os.MkdirAll(newWorkspace, 0o755); err != nil {
		t.Fatalf("MkdirAll newWorkspace: %v", err)
	}
	t.Setenv("HOME", home)

	cfg, err := config.Load(oldWorkspace, config.LoadOptions{})
	if err != nil {
		t.Fatalf("config.Load oldWorkspace: %v", err)
	}
	otherCfg, err := config.Load(otherWorkspace, config.LoadOptions{})
	if err != nil {
		t.Fatalf("config.Load otherWorkspace: %v", err)
	}
	storeA, err := Open(cfg.PersistenceRoot)
	if err != nil {
		t.Fatalf("Open storeA: %v", err)
	}
	defer func() { _ = storeA.Close() }()
	storeB, err := Open(cfg.PersistenceRoot)
	if err != nil {
		t.Fatalf("Open storeB: %v", err)
	}
	defer func() { _ = storeB.Close() }()

	oldBinding, err := storeA.RegisterWorkspaceBinding(ctx, cfg.WorkspaceRoot)
	if err != nil {
		t.Fatalf("RegisterWorkspaceBinding oldWorkspace: %v", err)
	}
	otherBinding, err := storeA.RegisterWorkspaceBinding(ctx, otherCfg.WorkspaceRoot)
	if err != nil {
		t.Fatalf("RegisterWorkspaceBinding otherWorkspace: %v", err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	rebindWorkspaceBeforeUpdateHook = func() {
		close(started)
		<-release
	}
	t.Cleanup(func() { rebindWorkspaceBeforeUpdateHook = nil })

	errCh := make(chan error, 1)
	go func() {
		_, err := storeA.RebindWorkspace(ctx, oldWorkspace, newWorkspace)
		errCh <- err
	}()
	<-started
	if _, err := storeB.AttachWorkspaceToProject(ctx, otherBinding.ProjectID, newWorkspace); err != nil {
		close(release)
		t.Fatalf("AttachWorkspaceToProject competing bind: %v", err)
	}
	close(release)
	err = <-errCh
	if err == nil || !strings.Contains(err.Error(), "already bound") {
		t.Fatalf("RebindWorkspace race error = %v, want already bound", err)
	}
	resolved, err := storeA.EnsureWorkspaceBinding(ctx, oldWorkspace)
	if err != nil {
		t.Fatalf("EnsureWorkspaceBinding oldWorkspace after race: %v", err)
	}
	if resolved.WorkspaceID != oldBinding.WorkspaceID {
		t.Fatalf("resolved old workspace id after race = %q, want %q", resolved.WorkspaceID, oldBinding.WorkspaceID)
	}
	newResolved, err := storeA.EnsureWorkspaceBinding(ctx, newWorkspace)
	if err != nil {
		t.Fatalf("EnsureWorkspaceBinding newWorkspace after race: %v", err)
	}
	if newResolved.ProjectID != otherBinding.ProjectID {
		t.Fatalf("new workspace project id after race = %q, want %q", newResolved.ProjectID, otherBinding.ProjectID)
	}
}

func TestInsertWorkspaceBindingRecoversFromCanonicalRootConflict(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)

	cfg, err := config.Load(workspace, config.LoadOptions{})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	store, err := Open(cfg.PersistenceRoot)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	canonicalRoot, err := config.CanonicalWorkspaceRoot(cfg.WorkspaceRoot)
	if err != nil {
		t.Fatalf("CanonicalWorkspaceRoot: %v", err)
	}
	now := time.Now().UTC()
	winner, err := store.insertWorkspaceBinding(ctx, canonicalRoot, filepath.Base(canonicalRoot), filepath.Base(canonicalRoot), "project-winner", "workspace-winner", now, true)
	if err != nil {
		t.Fatalf("insertWorkspaceBinding winner: %v", err)
	}
	loser, err := store.insertWorkspaceBinding(ctx, canonicalRoot, filepath.Base(canonicalRoot), filepath.Base(canonicalRoot), "project-loser", "workspace-loser", now, true)
	if err != nil {
		t.Fatalf("insertWorkspaceBinding loser: %v", err)
	}
	if loser.ProjectID != winner.ProjectID || loser.WorkspaceID != winner.WorkspaceID {
		t.Fatalf("conflict recovery mismatch: got %+v want %+v", loser, winner)
	}
	var projectCount int
	if err := store.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM projects").Scan(&projectCount); err != nil {
		t.Fatalf("count projects: %v", err)
	}
	if projectCount != 1 {
		t.Fatalf("project count = %d, want 1", projectCount)
	}
	var workspaceCount int
	if err := store.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM workspaces").Scan(&workspaceCount); err != nil {
		t.Fatalf("count workspaces: %v", err)
	}
	if workspaceCount != 1 {
		t.Fatalf("workspace count = %d, want 1", workspaceCount)
	}
	if _, err := store.EnsureWorkspaceBinding(ctx, cfg.WorkspaceRoot); err != nil {
		t.Fatalf("EnsureWorkspaceBinding after conflict recovery: %v", err)
	}
	if err := store.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM projects WHERE id = ?", winner.ProjectID).Scan(&projectCount); err != nil {
		t.Fatalf("count winner project: %v", err)
	}
	if projectCount != 1 {
		t.Fatalf("winner project unexpectedly deleted")
	}
	if _, err := store.lookupWorkspaceBinding(ctx, canonicalRoot); err != nil {
		t.Fatalf("lookupWorkspaceBinding: %v", err)
	}
}

func TestRegisterWorkspaceBindingConvergesUnderConcurrentFirstRegistration(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)

	cfg, err := config.Load(workspace, config.LoadOptions{})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	storeA, err := Open(cfg.PersistenceRoot)
	if err != nil {
		t.Fatalf("Open storeA: %v", err)
	}
	t.Cleanup(func() { _ = storeA.Close() })
	storeB, err := Open(cfg.PersistenceRoot)
	if err != nil {
		t.Fatalf("Open storeB: %v", err)
	}
	t.Cleanup(func() { _ = storeB.Close() })

	barrier := make(chan struct{})
	var once sync.Once
	var reached atomic.Int32
	registerWorkspaceBindingAfterLookupMissHook = func() {
		if reached.Add(1) == 2 {
			once.Do(func() { close(barrier) })
		}
		<-barrier
	}
	t.Cleanup(func() {
		registerWorkspaceBindingAfterLookupMissHook = nil
		once.Do(func() { close(barrier) })
	})

	results := make(chan Binding, 2)
	errs := make(chan error, 2)
	run := func(store *Store) {
		binding, err := store.RegisterWorkspaceBinding(ctx, cfg.WorkspaceRoot)
		if err != nil {
			errs <- err
			return
		}
		results <- binding
	}
	go run(storeA)
	go run(storeB)

	bindings := make([]Binding, 0, 2)
	for len(bindings) < 2 {
		select {
		case err := <-errs:
			t.Fatalf("RegisterWorkspaceBinding concurrent call: %v", err)
		case binding := <-results:
			bindings = append(bindings, binding)
		}
	}
	if bindings[0].ProjectID != bindings[1].ProjectID || bindings[0].WorkspaceID != bindings[1].WorkspaceID {
		t.Fatalf("concurrent bindings diverged: %+v vs %+v", bindings[0], bindings[1])
	}
	resolved, err := storeA.EnsureWorkspaceBinding(ctx, cfg.WorkspaceRoot)
	if err != nil {
		t.Fatalf("EnsureWorkspaceBinding after concurrent registration: %v", err)
	}
	if resolved.ProjectID != bindings[0].ProjectID || resolved.WorkspaceID != bindings[0].WorkspaceID {
		t.Fatalf("resolved binding mismatch: got %+v want %+v", resolved, bindings[0])
	}
	var projectCount int
	if err := storeA.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM projects").Scan(&projectCount); err != nil {
		t.Fatalf("count projects: %v", err)
	}
	if projectCount != 1 {
		t.Fatalf("project count = %d, want 1", projectCount)
	}
	var workspaceCount int
	if err := storeA.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM workspaces").Scan(&workspaceCount); err != nil {
		t.Fatalf("count workspaces: %v", err)
	}
	if workspaceCount != 1 {
		t.Fatalf("workspace count = %d, want 1", workspaceCount)
	}
}

func TestInsertWorkspaceBindingRollsBackProjectOnWorkspaceFailure(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)

	cfg, err := config.Load(workspace, config.LoadOptions{})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	store, err := Open(cfg.PersistenceRoot)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	canonicalRoot, err := config.CanonicalWorkspaceRoot(cfg.WorkspaceRoot)
	if err != nil {
		t.Fatalf("CanonicalWorkspaceRoot: %v", err)
	}
	ctx, cancel := context.WithCancel(ctx)
	insertWorkspaceBindingAfterProjectUpsertHook = cancel
	t.Cleanup(func() { insertWorkspaceBindingAfterProjectUpsertHook = nil })
	_, err = store.insertWorkspaceBinding(ctx, canonicalRoot, filepath.Base(canonicalRoot), filepath.Base(canonicalRoot), "project-cancelled", "workspace-cancelled", time.Now().UTC(), true)
	if err == nil {
		t.Fatal("expected insertWorkspaceBinding to fail after context cancellation")
	}
	var projectCount int
	if err := store.db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM projects WHERE id = ?", "project-cancelled").Scan(&projectCount); err != nil {
		t.Fatalf("count cancelled project: %v", err)
	}
	if projectCount != 0 {
		t.Fatalf("expected cancelled project insert to roll back, got %d rows", projectCount)
	}
}

func TestImportSessionSnapshotRejectsSessionDirOutsidePersistenceRoot(t *testing.T) {
	ctx := context.Background()
	store, cfg, _ := newMetadataTestStore(t)
	outsideDir := t.TempDir()
	err := store.ImportSessionSnapshot(ctx, session.PersistedStoreSnapshot{
		SessionDir: outsideDir,
		Meta: session.Meta{
			SessionID:          "session-outside",
			WorkspaceRoot:      cfg.WorkspaceRoot,
			WorkspaceContainer: filepath.Base(cfg.WorkspaceRoot),
			CreatedAt:          time.Now().UTC(),
			UpdatedAt:          time.Now().UTC(),
		},
	})
	if err == nil || !strings.Contains(err.Error(), "outside persistence root") {
		t.Fatalf("expected outside-persistence-root error, got %v", err)
	}
}

func TestResolvePersistedSessionRejectsEscapingArtifactRelpath(t *testing.T) {
	ctx := context.Background()
	store, _, binding := newMetadataTestStore(t)
	now := time.Now().UTC().UnixMilli()
	if err := store.queries.UpsertSession(ctx, sqlitegen.UpsertSessionParams{
		ID:                 "session-escape",
		ProjectID:          binding.ProjectID,
		WorkspaceID:        binding.WorkspaceID,
		WorktreeID:         sql.NullString{},
		ArtifactRelpath:    "../escape",
		Name:               "",
		FirstPromptPreview: "",
		InputDraft:         "",
		ParentSessionID:    "",
		CreatedAtUnixMs:    now,
		UpdatedAtUnixMs:    now,
		LastSequence:       0,
		ModelRequestCount:  0,
		InFlightStep:       0,
		AgentsInjected:     0,
		LaunchVisible:      0,
		CwdRelpath:         ".",
		ContinuationJson:   "{}",
		LockedJson:         "{}",
		UsageStateJson:     "{}",
		MetadataJson:       "{}",
	}); err != nil {
		t.Fatalf("UpsertSession: %v", err)
	}
	_, err := store.ResolvePersistedSession(ctx, "session-escape")
	if err == nil || !strings.Contains(err.Error(), "escapes persistence root") {
		t.Fatalf("expected escaping artifact relpath error, got %v", err)
	}
}

func TestSessionExecutionTargetClampsEscapingCwdRelpath(t *testing.T) {
	target := sessionExecutionTargetFromRow(sqlitegen.GetSessionExecutionTargetByIDRow{
		WorkspaceID:           "workspace-1",
		WorkspaceName:         "workspace",
		WorkspaceRoot:         "/tmp/workspace",
		WorkspaceAvailability: "available",
		WorktreeRoot:          "",
		CwdRelpath:            "../../other-project",
	})
	if target.CwdRelpath != "." {
		t.Fatalf("cwd relpath = %q, want .", target.CwdRelpath)
	}
	if target.EffectiveWorkdir != "/tmp/workspace" {
		t.Fatalf("effective workdir = %q, want /tmp/workspace", target.EffectiveWorkdir)
	}

	target = sessionExecutionTargetFromRow(sqlitegen.GetSessionExecutionTargetByIDRow{
		WorkspaceID:           "workspace-1",
		WorkspaceName:         "workspace",
		WorkspaceRoot:         "/tmp/workspace",
		WorkspaceAvailability: "available",
		WorktreeRoot:          "/tmp/workspace/worktree-a",
		CwdRelpath:            "/tmp/absolute",
	})
	if target.CwdRelpath != "." {
		t.Fatalf("absolute cwd relpath = %q, want .", target.CwdRelpath)
	}
	if target.EffectiveWorkdir != "/tmp/workspace/worktree-a" {
		t.Fatalf("absolute effective workdir = %q, want /tmp/workspace/worktree-a", target.EffectiveWorkdir)
	}
}

func TestResolveSessionExecutionTargetUsesMetadataAuthority(t *testing.T) {
	ctx := context.Background()
	store, cfg, binding := newMetadataTestStore(t)
	projectSessionsDir := config.ProjectSessionsRoot(cfg, binding.ProjectID)
	sess, err := session.Create(projectSessionsDir, filepath.Base(projectSessionsDir), cfg.WorkspaceRoot, store.AuthoritativeSessionStoreOptions()...)
	if err != nil {
		t.Fatalf("session.Create: %v", err)
	}
	if err := sess.EnsureDurable(); err != nil {
		t.Fatalf("EnsureDurable: %v", err)
	}

	target, err := store.ResolveSessionExecutionTarget(ctx, sess.Meta().SessionID)
	if err != nil {
		t.Fatalf("ResolveSessionExecutionTarget: %v", err)
	}
	canonicalRoot, err := config.CanonicalWorkspaceRoot(cfg.WorkspaceRoot)
	if err != nil {
		t.Fatalf("CanonicalWorkspaceRoot: %v", err)
	}
	if target.WorkspaceID != binding.WorkspaceID {
		t.Fatalf("workspace id = %q, want %q", target.WorkspaceID, binding.WorkspaceID)
	}
	if target.WorkspaceRoot != canonicalRoot {
		t.Fatalf("workspace root = %q, want %q", target.WorkspaceRoot, canonicalRoot)
	}
	if target.CwdRelpath != "." {
		t.Fatalf("cwd relpath = %q, want .", target.CwdRelpath)
	}
	if target.EffectiveWorkdir != canonicalRoot {
		t.Fatalf("effective workdir = %q, want %q", target.EffectiveWorkdir, canonicalRoot)
	}
}

func TestResolvePersistedSessionUsesReboundWorkspaceRoot(t *testing.T) {
	ctx := context.Background()
	store, cfg, binding := newMetadataTestStore(t)
	projectSessionsDir := config.ProjectSessionsRoot(cfg, binding.ProjectID)
	sess, err := session.Create(projectSessionsDir, filepath.Base(projectSessionsDir), cfg.WorkspaceRoot, store.AuthoritativeSessionStoreOptions()...)
	if err != nil {
		t.Fatalf("session.Create: %v", err)
	}
	if err := sess.SetName("hello"); err != nil {
		t.Fatalf("SetName: %v", err)
	}
	if err := sess.EnsureDurable(); err != nil {
		t.Fatalf("EnsureDurable: %v", err)
	}
	oldWorkspace := cfg.WorkspaceRoot
	newWorkspace := filepath.Join(t.TempDir(), "workspace-moved")
	if err := os.Rename(oldWorkspace, newWorkspace); err != nil {
		t.Fatalf("Rename workspace: %v", err)
	}
	if _, err := store.RebindWorkspace(ctx, oldWorkspace, newWorkspace); err != nil {
		t.Fatalf("RebindWorkspace: %v", err)
	}
	record, err := store.ResolvePersistedSession(ctx, sess.Meta().SessionID)
	if err != nil {
		t.Fatalf("ResolvePersistedSession: %v", err)
	}
	canonicalNewWorkspace, err := config.CanonicalWorkspaceRoot(newWorkspace)
	if err != nil {
		t.Fatalf("CanonicalWorkspaceRoot newWorkspace: %v", err)
	}
	if record.Meta == nil {
		t.Fatal("expected resolved metadata")
	}
	if record.Meta.WorkspaceRoot != canonicalNewWorkspace {
		t.Fatalf("resolved workspace root = %q, want %q", record.Meta.WorkspaceRoot, canonicalNewWorkspace)
	}
}

func TestRuntimeLeaseLifecycleIsIdempotent(t *testing.T) {
	ctx := context.Background()
	store, cfg, binding := newMetadataTestStore(t)
	projectSessionsDir := config.ProjectSessionsRoot(cfg, binding.ProjectID)
	sess, err := session.Create(projectSessionsDir, filepath.Base(projectSessionsDir), cfg.WorkspaceRoot, store.AuthoritativeSessionStoreOptions()...)
	if err != nil {
		t.Fatalf("session.Create: %v", err)
	}
	if err := sess.EnsureDurable(); err != nil {
		t.Fatalf("EnsureDurable: %v", err)
	}

	lease, err := store.CreateRuntimeLease(ctx, sess.Meta().SessionID, "req-1")
	if err != nil {
		t.Fatalf("CreateRuntimeLease: %v", err)
	}
	if !lease.Active() {
		t.Fatalf("expected active lease, got %+v", lease)
	}

	released, err := store.ReleaseRuntimeLease(ctx, sess.Meta().SessionID, lease.LeaseID)
	if err != nil {
		t.Fatalf("ReleaseRuntimeLease: %v", err)
	}
	if released.Active() {
		t.Fatalf("expected released lease, got %+v", released)
	}
	if released.ReleasedAt.IsZero() {
		t.Fatal("expected released_at to be populated")
	}

	again, err := store.ReleaseRuntimeLease(ctx, sess.Meta().SessionID, lease.LeaseID)
	if err != nil {
		t.Fatalf("ReleaseRuntimeLease second call: %v", err)
	}
	if again.Active() {
		t.Fatalf("expected second release to remain released, got %+v", again)
	}
}

func TestHiddenDurableSessionStaysOutOfProjectListingsUntilVisible(t *testing.T) {
	ctx := context.Background()
	store, cfg, binding := newMetadataTestStore(t)
	projectSessionsDir := config.ProjectSessionsRoot(cfg, binding.ProjectID)
	sess, err := session.Create(projectSessionsDir, filepath.Base(projectSessionsDir), cfg.WorkspaceRoot, store.AuthoritativeSessionStoreOptions()...)
	if err != nil {
		t.Fatalf("session.Create: %v", err)
	}
	if err := sess.EnsureDurable(); err != nil {
		t.Fatalf("EnsureDurable: %v", err)
	}

	projects, err := store.ListProjects(ctx)
	if err != nil {
		t.Fatalf("ListProjects before visibility: %v", err)
	}
	if len(projects) != 1 {
		t.Fatalf("expected one project, got %+v", projects)
	}
	if projects[0].SessionCount != 0 {
		t.Fatalf("hidden durable session must not affect project session count, got %+v", projects[0])
	}

	sessions, err := store.ListSessionsByProject(ctx, binding.ProjectID)
	if err != nil {
		t.Fatalf("ListSessionsByProject before visibility: %v", err)
	}
	if len(sessions) != 0 {
		t.Fatalf("expected hidden durable session to stay out of listings, got %+v", sessions)
	}

	if err := sess.SetName("incident triage"); err != nil {
		t.Fatalf("SetName: %v", err)
	}

	projects, err = store.ListProjects(ctx)
	if err != nil {
		t.Fatalf("ListProjects after visibility: %v", err)
	}
	if projects[0].SessionCount != 1 {
		t.Fatalf("visible session must affect project session count, got %+v", projects[0])
	}

	sessions, err = store.ListSessionsByProject(ctx, binding.ProjectID)
	if err != nil {
		t.Fatalf("ListSessionsByProject after visibility: %v", err)
	}
	if len(sessions) != 1 || sessions[0].SessionID != sess.Meta().SessionID {
		t.Fatalf("expected newly visible session in listings, got %+v", sessions)
	}
	if sessions[0].Name != "incident triage" {
		t.Fatalf("session name = %q, want incident triage", sessions[0].Name)
	}
}

func TestSessionLaunchVisibilityTransitions(t *testing.T) {
	tests := []struct {
		name        string
		mutate      func(*testing.T, *session.Store)
		wantVisible bool
	}{
		{
			name:        "input draft makes session launch-visible",
			wantVisible: true,
			mutate: func(t *testing.T, sess *session.Store) {
				t.Helper()
				if err := sess.SetInputDraft("draft prompt"); err != nil {
					t.Fatalf("SetInputDraft: %v", err)
				}
			},
		},
		{
			name:        "parent linkage makes session launch-visible",
			wantVisible: true,
			mutate: func(t *testing.T, sess *session.Store) {
				t.Helper()
				if err := sess.SetParentSessionID("session-parent"); err != nil {
					t.Fatalf("SetParentSessionID: %v", err)
				}
			},
		},
		{
			name:        "first user prompt makes session launch-visible",
			wantVisible: true,
			mutate: func(t *testing.T, sess *session.Store) {
				t.Helper()
				if _, err := sess.AppendEvent("step-1", "message", map[string]any{"role": "user", "content": "Investigate broken startup flow\nmore detail"}); err != nil {
					t.Fatalf("AppendEvent: %v", err)
				}
			},
		},
		{
			name:        "non-user events keep prepared session hidden",
			wantVisible: false,
			mutate: func(t *testing.T, sess *session.Store) {
				t.Helper()
				if _, err := sess.AppendEvent("step-1", "message", map[string]any{"role": "assistant", "content": "warming up"}); err != nil {
					t.Fatalf("AppendEvent: %v", err)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			store, cfg, binding := newMetadataTestStore(t)
			projectSessionsDir := config.ProjectSessionsRoot(cfg, binding.ProjectID)
			sess, err := session.Create(projectSessionsDir, filepath.Base(projectSessionsDir), cfg.WorkspaceRoot, store.AuthoritativeSessionStoreOptions()...)
			if err != nil {
				t.Fatalf("session.Create: %v", err)
			}
			if err := sess.EnsureDurable(); err != nil {
				t.Fatalf("EnsureDurable: %v", err)
			}

			assertProjectSessionListingCount(t, ctx, store, binding.ProjectID, 0)

			tc.mutate(t, sess)

			wantCount := 0
			if tc.wantVisible {
				wantCount = 1
			}
			listed := assertProjectSessionListingCount(t, ctx, store, binding.ProjectID, wantCount)
			if !tc.wantVisible {
				return
			}
			if listed[0].SessionID != sess.Meta().SessionID {
				t.Fatalf("listed session id = %q, want %q", listed[0].SessionID, sess.Meta().SessionID)
			}
		})
	}
}

func assertProjectSessionListingCount(t *testing.T, ctx context.Context, store *Store, projectID string, want int) []clientui.SessionSummary {
	t.Helper()
	projects, err := store.ListProjects(ctx)
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(projects) != 1 {
		t.Fatalf("expected one project, got %+v", projects)
	}
	if projects[0].SessionCount != want {
		t.Fatalf("project session count = %d, want %d", projects[0].SessionCount, want)
	}
	sessions, err := store.ListSessionsByProject(ctx, projectID)
	if err != nil {
		t.Fatalf("ListSessionsByProject: %v", err)
	}
	if len(sessions) != want {
		t.Fatalf("listed session count = %d, want %d, sessions=%+v", len(sessions), want, sessions)
	}
	return sessions
}

func newMetadataTestStore(t *testing.T) (*Store, config.App, Binding) {
	t.Helper()
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)

	cfg, err := config.Load(workspace, config.LoadOptions{})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	store, err := Open(cfg.PersistenceRoot)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	binding, err := store.RegisterWorkspaceBinding(context.Background(), cfg.WorkspaceRoot)
	if err != nil {
		t.Fatalf("RegisterWorkspaceBinding: %v", err)
	}
	return store, cfg, binding
}

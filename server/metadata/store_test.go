package metadata

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"builder/server/session"
	"builder/shared/config"
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

	if _, err := store.EnsureWorkspaceBinding(context.Background(), cfg.WorkspaceRoot); !errors.Is(err, ErrWorkspaceNotRegistered) {
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

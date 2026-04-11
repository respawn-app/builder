package metadata

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"builder/server/session"
	"builder/shared/clientui"
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
	winner, err := store.insertWorkspaceBinding(ctx, canonicalRoot, filepath.Base(canonicalRoot), "project-winner", "workspace-winner", now)
	if err != nil {
		t.Fatalf("insertWorkspaceBinding winner: %v", err)
	}
	loser, err := store.insertWorkspaceBinding(ctx, canonicalRoot, filepath.Base(canonicalRoot), "project-loser", "workspace-loser", now)
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
	if _, err := store.queries.DeleteProjectIfOrphaned(ctx, winner.ProjectID); err != nil {
		t.Fatalf("DeleteProjectIfOrphaned winner project: %v", err)
	}
	if err := store.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM projects WHERE id = ?", winner.ProjectID).Scan(&projectCount); err != nil {
		t.Fatalf("count winner project: %v", err)
	}
	if projectCount != 1 {
		t.Fatalf("winner project unexpectedly deleted")
	}
	if rows, err := store.queries.DeleteProjectIfOrphaned(ctx, "project-missing"); err != nil {
		t.Fatalf("DeleteProjectIfOrphaned missing project: %v", err)
	} else if rows != 0 {
		t.Fatalf("DeleteProjectIfOrphaned missing project rows = %d, want 0", rows)
	}
	if _, err := store.lookupWorkspaceBinding(ctx, canonicalRoot); err != nil {
		t.Fatalf("lookupWorkspaceBinding: %v", err)
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

func TestSessionLaunchVisibilityTransitions(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, *session.Store)
	}{
		{
			name: "input draft makes session launch-visible",
			mutate: func(t *testing.T, sess *session.Store) {
				t.Helper()
				if err := sess.SetInputDraft("draft prompt"); err != nil {
					t.Fatalf("SetInputDraft: %v", err)
				}
			},
		},
		{
			name: "parent linkage makes session launch-visible",
			mutate: func(t *testing.T, sess *session.Store) {
				t.Helper()
				if err := sess.SetParentSessionID("session-parent"); err != nil {
					t.Fatalf("SetParentSessionID: %v", err)
				}
			},
		},
		{
			name: "first user prompt makes session launch-visible",
			mutate: func(t *testing.T, sess *session.Store) {
				t.Helper()
				if _, err := sess.AppendEvent("step-1", "message", map[string]any{"role": "user", "content": "Investigate broken startup flow\nmore detail"}); err != nil {
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

			listed := assertProjectSessionListingCount(t, ctx, store, binding.ProjectID, 1)
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

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

func TestResolveWorkspacePathResolvesNestedDirectoryToAncestorBinding(t *testing.T) {
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
	if resolved == nil {
		t.Fatal("expected nested directory to resolve to ancestor binding")
	}
	if resolved.ProjectID != binding.ProjectID || resolved.CanonicalRoot != binding.CanonicalRoot {
		t.Fatalf("resolved nested binding = %+v, want %+v", *resolved, binding)
	}

	ensured, err := store.EnsureWorkspaceBinding(context.Background(), nested)
	if err != nil {
		t.Fatalf("EnsureWorkspaceBinding nested: %v", err)
	}
	if ensured.ProjectID != binding.ProjectID || ensured.CanonicalRoot != binding.CanonicalRoot {
		t.Fatalf("ensured nested binding = %+v, want %+v", ensured, binding)
	}

	registered, err := store.RegisterWorkspaceBinding(context.Background(), nested)
	if err != nil {
		t.Fatalf("RegisterWorkspaceBinding nested: %v", err)
	}
	if registered.ProjectID != binding.ProjectID || registered.CanonicalRoot != binding.CanonicalRoot {
		t.Fatalf("registered nested binding = %+v, want %+v", registered, binding)
	}

	projects, err := store.ListProjects(context.Background())
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(projects) != 1 {
		t.Fatalf("project count = %d, want 1", len(projects))
	}
}

func TestAttachWorkspaceToProjectRejectsNestedPathInsideExistingWorkspace(t *testing.T) {
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
		t.Fatalf("AttachWorkspaceToProject same project nested: %v", err)
	}
	if resolved.ProjectID != binding.ProjectID || resolved.CanonicalRoot != binding.CanonicalRoot {
		t.Fatalf("nested attach result = %+v, want %+v", resolved, binding)
	}

	_, err = store.AttachWorkspaceToProject(context.Background(), otherBinding.ProjectID, nested)
	if err == nil || !strings.Contains(err.Error(), "inside attached workspace") {
		t.Fatalf("expected nested attach rejection, got %v", err)
	}
	projects, err := store.ListProjects(context.Background())
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(projects) != 2 {
		t.Fatalf("project count = %d, want 2", len(projects))
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

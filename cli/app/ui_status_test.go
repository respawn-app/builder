package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"builder/cli/tui"
	"builder/server/auth"
	"builder/server/runtime"
	"builder/server/session"
	"builder/server/tools"
	"builder/shared/config"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type stubStatusCollector struct {
	snapshot uiStatusSnapshot
	err      error
}

func (s *stubStatusCollector) Collect(_ context.Context, _ uiStatusRequest) (uiStatusSnapshot, error) {
	return s.snapshot, s.err
}

type stubProgressiveStatusCollector struct {
	base       uiStatusSnapshot
	authResult uiStatusAuthStageResult
	gitResult  uiStatusGitStageResult
	envResult  uiStatusEnvironmentStageResult
}

func (s *stubProgressiveStatusCollector) Collect(_ context.Context, _ uiStatusRequest) (uiStatusSnapshot, error) {
	snapshot := s.base
	snapshot.Auth = s.authResult.Auth
	snapshot.Subscription = s.authResult.Subscription
	snapshot.Git = s.gitResult.Git
	snapshot.Skills = s.envResult.Skills
	snapshot.SkillTokenCounts = s.envResult.SkillTokenCounts
	snapshot.AgentsPaths = s.envResult.AgentsPaths
	snapshot.AgentTokenCounts = s.envResult.AgentTokenCounts
	snapshot.CollectorWarning = s.envResult.CollectorWarning
	return snapshot, nil
}

func (s *stubProgressiveStatusCollector) CollectBase(_ uiStatusRequest) uiStatusSnapshot {
	return s.base
}

func (s *stubProgressiveStatusCollector) CollectAuth(_ context.Context, _ uiStatusRequest, _ uiStatusSnapshot) uiStatusAuthStageResult {
	return s.authResult
}

func (s *stubProgressiveStatusCollector) CollectGit(_ context.Context, _ uiStatusRequest, _ uiStatusSnapshot) uiStatusGitStageResult {
	return s.gitResult
}

func (s *stubProgressiveStatusCollector) CollectEnvironment(_ context.Context, _ uiStatusRequest, _ uiStatusSnapshot) uiStatusEnvironmentStageResult {
	return s.envResult
}

func TestStatusCommandOpensDetailOverlayInNativeMode(t *testing.T) {
	collector := &stubStatusCollector{snapshot: uiStatusSnapshot{
		CollectedAt:       time.Date(2026, time.March, 24, 21, 15, 0, 0, time.UTC),
		Workdir:           "/tmp/workdir",
		SessionName:       "incident",
		SessionID:         "session-123",
		ParentSessionID:   "parent-456",
		ParentSessionName: "incident-root",
		Git:               uiStatusGitInfo{Visible: true, Branch: "master", Dirty: true, Ahead: 2, Behind: 1},
		Auth: uiStatusAuthInfo{
			Summary: "user@example.com",
		},
		Context: uiStatusContextInfo{UsedTokens: 120000, AvailableTokens: 280000, WindowTokens: 400000, ThresholdTokens: 300000},
		Model: uiStatusModelInfo{
			Summary: "gpt-5 high fast",
		},
		Config: uiStatusConfigInfo{
			SettingsPath:    "/Users/test/.builder/config.toml",
			OverrideSources: []string{"ENV", "CLI ARGS"},
			Supervisor:      "edits",
			AutoCompaction:  true,
		},
		Subscription: uiStatusSubscriptionInfo{
			Applicable: true,
			Summary:    "Pro subscription",
			Windows: []uiStatusSubscriptionWindow{
				{Label: "5h", UsedPercent: 12.5, ResetAt: time.Date(2026, time.March, 25, 2, 0, 0, 0, time.UTC)},
				{Label: "weekly", UsedPercent: 40.0, ResetAt: time.Date(2026, time.March, 31, 2, 0, 0, 0, time.UTC)},
			},
		},
		Skills: []runtime.SkillInspection{
			{Name: "apiresult", Path: "/Users/test/.builder/skills/apiresult/SKILL.md", Loaded: true},
			{Name: "local helper", Path: "/Users/test/.builder/skills/local-helper/SKILL.md", Loaded: true, Disabled: true},
			{Name: "broken", Path: "/Users/test/.builder/skills/broken/SKILL.md", Loaded: false, Reason: "missing SKILL.md"},
		},
		AgentsPaths:     []string{"/Users/test/.builder/AGENTS.md", "/tmp/workdir/AGENTS.md"},
		CompactionCount: 3,
	}}

	m := newProjectedStaticUIModel(
		WithUIStatusConfig(uiStatusConfig{
			WorkspaceRoot: "/tmp/workdir",
			Settings: config.Settings{
				ContextCompactionThresholdTokens: 300000,
			},
			Source: config.SourceReport{SettingsPath: "/Users/test/.builder/config.toml"},
		}),
		WithUIStatusCollector(collector),
	)
	m.termWidth = 100
	m.termHeight = 40
	m.windowSizeKnown = true
	m.input = "/status"

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := next.(*uiModel)
	if !updated.status.isOpen() {
		t.Fatal("expected /status to open the status overlay")
	}
	if !updated.status.ownsTranscriptMode {
		t.Fatal("expected /status to push a dedicated overlay")
	}
	if updated.view.Mode() != tui.ModeDetail {
		t.Fatalf("expected /status to switch into detail mode, got %q", updated.view.Mode())
	}
	if cmd == nil {
		t.Fatal("expected /status open to emit a screen transition command")
	}

	next, _ = updated.Update(statusRefreshDoneMsg{token: updated.status.refreshToken, snapshot: collector.snapshot})
	updated = next.(*uiModel)
	plain := stripANSIAndTrimRight(updated.View())
	for _, want := range []string{"Pro subscription", "CWD: /tmp/workdir", "Model: gpt-5 high fast", "incident", "Parent session: incident-root <parent-456>", "session-123", "master", "dirty | ahead 2 | behind 1"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("expected status overlay to contain %q, got %q", want, plain)
		}
	}
	for _, want := range []string{"3 skills", "/Users/test/.builder/skills", "├─ apiresult (0k)", "├─ local helper disabled", "└─ ! broken (missing SKILL.md)"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("expected grouped skill rendering to contain %q, got %q", want, plain)
		}
	}

	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyEnd})
	updated = next.(*uiModel)
	plain = stripANSIAndTrimRight(updated.View())
	for _, want := range []string{"weekly", "60% left", "auto-compaction on", "3 compactions", "2 agents files", "/Users/test/.builder/AGENTS.md", "supervisor edits"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("expected scrolled status overlay to contain %q, got %q", want, plain)
		}
	}

	next, cmd = updated.Update(tea.KeyMsg{Type: tea.KeyEsc})
	updated = next.(*uiModel)
	if updated.status.isOpen() {
		t.Fatal("expected esc to close the status overlay")
	}
	if updated.status.ownsTranscriptMode {
		t.Fatal("expected status overlay state cleared after close")
	}
	if updated.view.Mode() != tui.ModeOngoing {
		t.Fatalf("expected status overlay close to restore ongoing mode, got %q", updated.view.Mode())
	}
	if cmd == nil {
		t.Fatal("expected /status close to emit a screen transition command")
	}
}

func TestStatusCommandProgressivelyLoadsSections(t *testing.T) {
	collector := &stubProgressiveStatusCollector{
		base: uiStatusSnapshot{
			CollectedAt: time.Date(2026, time.March, 24, 21, 15, 0, 0, time.UTC),
			Workdir:     "/tmp/workdir",
			SessionName: "incident",
			SessionID:   "session-123",
			Model:       uiStatusModelInfo{Summary: "gpt-5 high fast"},
			Config:      uiStatusConfigInfo{Supervisor: "edits", AutoCompaction: true},
		},
		gitResult: uiStatusGitStageResult{Git: uiStatusGitInfo{Visible: true, Branch: "master", Dirty: true, Ahead: 1}},
	}

	m := newProjectedStaticUIModel(
		WithUIStatusConfig(uiStatusConfig{WorkspaceRoot: "/tmp/workdir"}),
		WithUIStatusCollector(collector),
	)
	m.termWidth = 100
	m.termHeight = 40
	m.windowSizeKnown = true
	m.input = "/status"

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := next.(*uiModel)
	plain := stripANSIAndTrimRight(updated.View())
	for _, want := range []string{"Loading account...", "CWD: /tmp/workdir", "Model: gpt-5 high fast", "Loading git..."} {
		if !strings.Contains(plain, want) {
			t.Fatalf("expected progressive base render to contain %q, got %q", want, plain)
		}
	}

	next, _ = updated.Update(statusGitRefreshDoneMsg{token: updated.status.refreshToken, result: collector.gitResult})
	updated = next.(*uiModel)
	plain = stripANSIAndTrimRight(updated.View())
	if !strings.Contains(plain, "master") || !strings.Contains(plain, "dirty | ahead 1 | behind 0") {
		t.Fatalf("expected parallel git render before base snapshot, got %q", plain)
	}

}

func TestStatusCommandPersistsPromptHistoryWithoutBlockingOpen(t *testing.T) {
	dir := t.TempDir()
	store, err := session.Create(dir, "ws", dir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	eng, err := runtime.New(store, &runtimeAdapterFakeClient{}, tools.NewRegistry(), runtime.Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	m := newProjectedEngineUIModel(
		eng,
		WithUIStatusConfig(uiStatusConfig{WorkspaceRoot: dir}),
		WithUIStatusCollector(&stubProgressiveStatusCollector{}),
	)
	m.termWidth = 100
	m.termHeight = 40
	m.windowSizeKnown = true
	m.input = "/status"

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := next.(*uiModel)
	if !updated.status.isOpen() {
		t.Fatal("expected /status to open immediately before prompt-history persistence completes")
	}
	if got := updated.promptHistory[len(updated.promptHistory)-1]; got != "/status" {
		t.Fatalf("expected in-memory prompt history updated immediately, got %+v", updated.promptHistory)
	}
	for _, msg := range collectCmdMessages(t, cmd) {
		if msg == nil {
			continue
		}
		next, _ = updated.Update(msg)
		updated = next.(*uiModel)
	}
	history, err := store.ReadPromptHistory()
	if err != nil {
		t.Fatalf("read prompt history: %v", err)
	}
	if len(history) == 0 || history[len(history)-1] != "/status" {
		t.Fatalf("expected persisted /status prompt history entry, got %+v", history)
	}
}

func TestStatusGroupSkillsByDirectoryKeepsBrokenSkillUnderSkillsRoot(t *testing.T) {
	groups := statusGroupSkillsByDirectory([]runtime.SkillInspection{
		{Name: "apiresult", Path: "/Users/test/.builder/skills/apiresult/SKILL.md", Loaded: true},
		{Name: "broken", Path: "/Users/test/.builder/skills/broken/SKILL.md", Loaded: false, Reason: "symlink target does not exist"},
	})

	if len(groups) != 1 {
		t.Fatalf("expected one skills directory group, got %+v", groups)
	}
	if groups[0].Directory != "/Users/test/.builder/skills" {
		t.Fatalf("expected skills root grouping, got %+v", groups)
	}
	if len(groups[0].Skills) != 2 {
		t.Fatalf("expected both skills in the same group, got %+v", groups)
	}
	if groups[0].Skills[1].Path != "/Users/test/.builder/skills/broken/SKILL.md" {
		t.Fatalf("expected broken skill path to remain in SKILL.md form, got %+v", groups[0].Skills[1])
	}
}

func TestStatusRepositorySeparatesAuthCacheByOAuthIdentity(t *testing.T) {
	repo := newMemoryUIStatusRepository()
	managerA := auth.NewManager(auth.NewMemoryStore(auth.State{
		Method: auth.Method{Type: auth.MethodOAuth, OAuth: &auth.OAuthMethod{AccessToken: "token-a", AccountID: "acct-a", Email: "a@example.com"}},
	}), nil, time.Now)
	managerB := auth.NewManager(auth.NewMemoryStore(auth.State{
		Method: auth.Method{Type: auth.MethodOAuth, OAuth: &auth.OAuthMethod{AccessToken: "token-b", AccountID: "acct-b", Email: "b@example.com"}},
	}), nil, time.Now)
	reqA := uiStatusRequest{WorkspaceRoot: "/tmp/workdir", AuthManager: managerA}
	reqB := uiStatusRequest{WorkspaceRoot: "/tmp/workdir", AuthManager: managerB}
	base := uiStatusSnapshot{Workdir: "/tmp/workdir"}

	repo.StoreAuth(statusAuthCacheKey(reqA), uiStatusAuthStageResult{
		Auth:         uiStatusAuthInfo{Summary: "a@example.com"},
		Subscription: uiStatusSubscriptionInfo{Applicable: true, Summary: "Pro subscription"},
	}, time.Now())

	seedA := repo.SeedSnapshot(reqA, base, time.Now())
	if got := seedA.Snapshot.Auth.Summary; got != "a@example.com" {
		t.Fatalf("expected cached auth summary for account A, got %q", got)
	}
	seedB := repo.SeedSnapshot(reqB, base, time.Now())
	if got := seedB.Snapshot.Auth.Summary; got != "" {
		t.Fatalf("expected no cached auth summary for account B, got %q", got)
	}
	if len(seedB.PendingSections) == 0 || seedB.PendingSections[0] != uiStatusSectionAuth {
		t.Fatalf("expected account B to require auth refresh, got %+v", seedB.PendingSections)
	}
}

func TestStatusRepositorySeparatesOpaqueOAuthCacheByTokenFingerprint(t *testing.T) {
	repo := newMemoryUIStatusRepository()
	managerA := auth.NewManager(auth.NewMemoryStore(auth.State{
		Method: auth.Method{Type: auth.MethodOAuth, OAuth: &auth.OAuthMethod{AccessToken: "token-a"}},
	}), nil, time.Now)
	managerB := auth.NewManager(auth.NewMemoryStore(auth.State{
		Method: auth.Method{Type: auth.MethodOAuth, OAuth: &auth.OAuthMethod{AccessToken: "token-b"}},
	}), nil, time.Now)
	reqA := uiStatusRequest{WorkspaceRoot: "/tmp/workdir", AuthManager: managerA}
	reqB := uiStatusRequest{WorkspaceRoot: "/tmp/workdir", AuthManager: managerB}
	base := uiStatusSnapshot{Workdir: "/tmp/workdir"}

	repo.StoreAuth(statusAuthCacheKey(reqA), uiStatusAuthStageResult{
		Auth:         uiStatusAuthInfo{Summary: "opaque-a"},
		Subscription: uiStatusSubscriptionInfo{Applicable: true, Summary: "Pro subscription"},
	}, time.Now())

	seedA := repo.SeedSnapshot(reqA, base, time.Now())
	if got := seedA.Snapshot.Auth.Summary; got != "opaque-a" {
		t.Fatalf("expected cached auth summary for opaque token A, got %q", got)
	}
	seedB := repo.SeedSnapshot(reqB, base, time.Now())
	if got := seedB.Snapshot.Auth.Summary; got != "" {
		t.Fatalf("expected no cached auth summary for opaque token B, got %q", got)
	}
	if len(seedB.PendingSections) == 0 || seedB.PendingSections[0] != uiStatusSectionAuth {
		t.Fatalf("expected opaque token B to require auth refresh, got %+v", seedB.PendingSections)
	}
}

func TestStatusRepositoryStoresAuthUnderCapturedIdentityKey(t *testing.T) {
	store := auth.NewMemoryStore(auth.State{
		Method: auth.Method{Type: auth.MethodOAuth, OAuth: &auth.OAuthMethod{AccessToken: "token-a", AccountID: "acct-a", Email: "a@example.com"}},
	})
	manager := auth.NewManager(store, nil, time.Now)
	req := uiStatusRequest{WorkspaceRoot: "/tmp/workdir", AuthManager: manager}
	base := uiStatusSnapshot{Workdir: "/tmp/workdir"}
	cacheKey := statusAuthCacheKey(req)

	if err := store.Save(context.Background(), auth.State{
		Method: auth.Method{Type: auth.MethodOAuth, OAuth: &auth.OAuthMethod{AccessToken: "token-b", AccountID: "acct-b", Email: "b@example.com"}},
	}); err != nil {
		t.Fatalf("switch auth identity: %v", err)
	}

	repo := newMemoryUIStatusRepository()
	repo.StoreAuth(cacheKey, uiStatusAuthStageResult{
		Auth:         uiStatusAuthInfo{Summary: "a@example.com"},
		Subscription: uiStatusSubscriptionInfo{Applicable: true, Summary: "Pro subscription"},
	}, time.Now())

	seedB := repo.SeedSnapshot(req, base, time.Now())
	if got := seedB.Snapshot.Auth.Summary; got != "" {
		t.Fatalf("expected no auth cached under switched identity, got %q", got)
	}

	if err := store.Save(context.Background(), auth.State{
		Method: auth.Method{Type: auth.MethodOAuth, OAuth: &auth.OAuthMethod{AccessToken: "token-a", AccountID: "acct-a", Email: "a@example.com"}},
	}); err != nil {
		t.Fatalf("restore auth identity: %v", err)
	}
	seedA := repo.SeedSnapshot(req, base, time.Now())
	if got := seedA.Snapshot.Auth.Summary; got != "a@example.com" {
		t.Fatalf("expected cached auth under original captured identity, got %q", got)
	}
}

func TestStatusCommandRefreshesGitWhenCachedResultIsInvisible(t *testing.T) {
	repo := newMemoryUIStatusRepository()
	repo.StoreGit(
		statusGitCacheKey("/tmp/workdir"),
		uiStatusGitStageResult{Git: uiStatusGitInfo{}},
		time.Now(),
	)
	collector := &stubProgressiveStatusCollector{
		base: uiStatusSnapshot{
			CollectedAt: time.Date(2026, time.March, 24, 21, 15, 0, 0, time.UTC),
			Workdir:     "/tmp/workdir",
			SessionName: "incident",
			SessionID:   "session-123",
			Model:       uiStatusModelInfo{Summary: "gpt-5 high fast"},
		},
		gitResult: uiStatusGitStageResult{Git: uiStatusGitInfo{Visible: true, Branch: "master", Dirty: true, Ahead: 2, Behind: 1}},
	}

	m := newProjectedStaticUIModel(
		WithUIStatusConfig(uiStatusConfig{WorkspaceRoot: "/tmp/workdir"}),
		WithUIStatusCollector(collector),
		WithUIStatusRepository(repo),
	)
	m.termWidth = 100
	m.termHeight = 40
	m.windowSizeKnown = true
	m.input = "/status"

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := next.(*uiModel)
	if updated.status.pendingSections == nil || !updated.status.pendingSections[uiStatusSectionGit] {
		t.Fatalf("expected git section to refresh when cached git result is invisible, got %+v", updated.status.pendingSections)
	}
	plain := stripANSIAndTrimRight(updated.View())
	if !strings.Contains(plain, "Loading git...") {
		t.Fatalf("expected git section placeholder before refreshed result, got %q", plain)
	}

	next, _ = updated.Update(statusGitRefreshDoneMsg{token: updated.status.refreshToken, result: collector.gitResult})
	updated = next.(*uiModel)
	plain = stripANSIAndTrimRight(updated.View())
	if !strings.Contains(plain, "master") || !strings.Contains(plain, "dirty | ahead 2 | behind 1") {
		t.Fatalf("expected refreshed git summary after invisible cached result, got %q", plain)
	}
}

func TestStatusRepositoryNormalizesGitCacheKeysAcrossSlashStyles(t *testing.T) {
	repo := newMemoryUIStatusRepository()
	now := time.Now()
	repo.StoreGit(
		statusGitCacheKey(`C:\repo`),
		uiStatusGitStageResult{Git: uiStatusGitInfo{Visible: true, Branch: "main", Ahead: 1}},
		now,
	)

	seed := repo.SeedSnapshot(
		uiStatusRequest{WorkspaceRoot: `C:\repo`},
		uiStatusSnapshot{Workdir: "C:/repo"},
		now,
	)
	if !seed.Snapshot.Git.Visible || seed.Snapshot.Git.Branch != "main" {
		t.Fatalf("expected cached git snapshot reused across slash styles, got %+v", seed.Snapshot.Git)
	}
	for _, section := range seed.PendingSections {
		if section == uiStatusSectionGit {
			t.Fatalf("did not expect git refresh when normalized cache key matches, got %+v", seed.PendingSections)
		}
	}
}

func TestCollectGitStatusSurfacesUnexpectedErrors(t *testing.T) {
	workdir := t.TempDir()
	cmd := exec.Command("git", "-C", workdir, "init")
	cmd.Env = sanitizedGitEnv(os.Environ())
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v (%s)", err, out)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	git := collectGitStatus(ctx, workdir)
	if !git.Visible {
		t.Fatalf("expected git section to remain visible on unexpected errors, got %+v", git)
	}
	if !strings.Contains(git.Error, "git status failed") {
		t.Fatalf("expected git error surfaced, got %+v", git)
	}
	if !strings.Contains(git.Error, context.Canceled.Error()) {
		t.Fatalf("expected git error to include underlying failure, got %+v", git)
	}
}

func TestCollectGitStatusHidesOutsideRepository(t *testing.T) {
	git := collectGitStatus(context.Background(), t.TempDir())
	if git.Visible {
		t.Fatalf("expected git section hidden outside repositories, got %+v", git)
	}
	if git.Error != "" {
		t.Fatalf("expected no git error outside repositories, got %+v", git)
	}
}

func TestCollectGitStatusDetectsNestedRepositorySubdirectory(t *testing.T) {
	repoRoot := t.TempDir()
	cmd := exec.Command("git", "-C", repoRoot, "init")
	cmd.Env = sanitizedGitEnv(os.Environ())
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v (%s)", err, out)
	}
	nestedDir := filepath.Join(repoRoot, "a", "b", "c")
	if err := os.MkdirAll(nestedDir, 0o755); err != nil {
		t.Fatalf("mkdir nested dir: %v", err)
	}

	git := collectGitStatus(context.Background(), nestedDir)
	if !git.Visible {
		t.Fatalf("expected git section visible for nested repository dir, got %+v", git)
	}
	if git.Error != "" {
		t.Fatalf("expected no git error for nested repository dir, got %+v", git)
	}
	if strings.TrimSpace(git.Branch) == "" {
		t.Fatalf("expected git branch detected for nested repository dir, got %+v", git)
	}
}

func TestCollectGitStatusDetectsSymlinkedRepositorySubdirectory(t *testing.T) {
	repoRoot := t.TempDir()
	cmd := exec.Command("git", "-C", repoRoot, "init")
	cmd.Env = sanitizedGitEnv(os.Environ())
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v (%s)", err, out)
	}
	realDir := filepath.Join(repoRoot, "real", "nested")
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		t.Fatalf("mkdir real dir: %v", err)
	}
	linkPath := filepath.Join(t.TempDir(), "workspace-link")
	if err := os.Symlink(realDir, linkPath); err != nil {
		t.Fatalf("symlink workdir: %v", err)
	}

	git := collectGitStatus(context.Background(), linkPath)
	if !git.Visible {
		t.Fatalf("expected git section visible for symlinked repository dir, got %+v", git)
	}
	if git.Error != "" {
		t.Fatalf("expected no git error for symlinked repository dir, got %+v", git)
	}
	if strings.TrimSpace(git.Branch) == "" {
		t.Fatalf("expected branch detected for symlinked repository dir, got %+v", git)
	}
}

func TestCollectGitStatusIgnoresInheritedGitRepositoryEnv(t *testing.T) {
	repoRoot := t.TempDir()
	cmd := exec.Command("git", "-C", repoRoot, "init")
	cmd.Env = sanitizedGitEnv(os.Environ())
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v (%s)", err, out)
	}
	nestedDir := filepath.Join(repoRoot, "nested")
	if err := os.MkdirAll(nestedDir, 0o755); err != nil {
		t.Fatalf("mkdir nested dir: %v", err)
	}
	t.Setenv("GIT_DIR", filepath.Join(t.TempDir(), ".git"))
	t.Setenv("GIT_WORK_TREE", t.TempDir())
	t.Setenv("GIT_COMMON_DIR", t.TempDir())

	git := collectGitStatus(context.Background(), nestedDir)
	if !git.Visible {
		t.Fatalf("expected git section visible when inherited git env points elsewhere, got %+v", git)
	}
	if git.Error != "" {
		t.Fatalf("expected no git error when inherited git env points elsewhere, got %+v", git)
	}
}

func TestStatusOverlayRendersGitErrorState(t *testing.T) {
	collector := &stubStatusCollector{snapshot: uiStatusSnapshot{
		CollectedAt: time.Date(2026, time.March, 24, 21, 15, 0, 0, time.UTC),
		Workdir:     "/tmp/workdir",
		Git:         uiStatusGitInfo{Visible: true, Error: "git status failed: context canceled"},
	}}

	m := newProjectedStaticUIModel(
		WithUIStatusConfig(uiStatusConfig{WorkspaceRoot: "/tmp/workdir"}),
		WithUIStatusCollector(collector),
	)
	m.termWidth = 100
	m.termHeight = 20
	m.windowSizeKnown = true
	m.input = "/status"

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := next.(*uiModel)
	next, _ = updated.Update(statusRefreshDoneMsg{token: testStatusRefreshToken(updated), snapshot: collector.snapshot})
	updated = next.(*uiModel)
	plain := stripANSIAndTrimRight(updated.View())
	if !strings.Contains(plain, "Git") || !strings.Contains(plain, "git status failed: context canceled") {
		t.Fatalf("expected git error section rendered, got %q", plain)
	}
	if strings.Contains(plain, "Loading git...") {
		t.Fatalf("did not expect loading placeholder when git error exists, got %q", plain)
	}
}

func TestStatusCommandProgressiveAuthWarningIsRendered(t *testing.T) {
	collector := &stubProgressiveStatusCollector{
		base: uiStatusSnapshot{
			CollectedAt: time.Date(2026, time.March, 24, 21, 15, 0, 0, time.UTC),
			Workdir:     "/tmp/workdir",
			SessionName: "incident",
			SessionID:   "session-123",
		},
		authResult: uiStatusAuthStageResult{
			Auth:         uiStatusAuthInfo{Summary: "Subscription | user@example.com"},
			Subscription: uiStatusSubscriptionInfo{Applicable: true, Summary: "Subscription unavailable: oauth refresh failed"},
			Warning:      "auth: oauth refresh failed",
		},
	}

	m := newProjectedStaticUIModel(
		WithUIStatusConfig(uiStatusConfig{WorkspaceRoot: "/tmp/workdir"}),
		WithUIStatusCollector(collector),
	)
	m.termWidth = 100
	m.termHeight = 24
	m.windowSizeKnown = true
	m.input = "/status"

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := next.(*uiModel)
	next, _ = updated.Update(statusBaseRefreshDoneMsg{token: testStatusRefreshToken(updated), snapshot: collector.base})
	updated = next.(*uiModel)
	next, _ = updated.Update(statusAuthRefreshDoneMsg{token: testStatusRefreshToken(updated), result: collector.authResult})
	updated = next.(*uiModel)
	if updated.status.snapshot.CollectorWarning != "auth: oauth refresh failed" {
		t.Fatalf("collector warning = %q", updated.status.snapshot.CollectorWarning)
	}
	if updated.status.snapshot.Subscription.Summary != "Subscription unavailable: oauth refresh failed" {
		t.Fatalf("subscription summary = %q", updated.status.snapshot.Subscription.Summary)
	}
	next, _ = updated.Update(tea.KeyMsg{Type: tea.KeyEnd})
	updated = next.(*uiModel)
	plain := stripANSIAndTrimRight(updated.View())
	for _, want := range []string{"Warnings", "auth: oauth refresh failed"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("expected progressive auth warning to contain %q, got %q", want, plain)
		}
	}
}

func TestStatusOverlaySubscriptionBarDoesNotLeakANSIFragments(t *testing.T) {
	collector := &stubStatusCollector{snapshot: uiStatusSnapshot{
		CollectedAt: time.Date(2026, time.March, 24, 21, 15, 0, 0, time.UTC),
		Auth:        uiStatusAuthInfo{Summary: "Subscription"},
		Subscription: uiStatusSubscriptionInfo{
			Applicable: true,
			Summary:    "Pro subscription",
			Windows: []uiStatusSubscriptionWindow{
				{Label: "5h", UsedPercent: 12.5, ResetAt: time.Date(2026, time.March, 24, 23, 15, 0, 0, time.UTC)},
			},
		},
	}}

	m := newProjectedStaticUIModel(
		WithUIStatusConfig(uiStatusConfig{WorkspaceRoot: "/tmp/workdir"}),
		WithUIStatusCollector(collector),
	)
	m.termWidth = 60
	m.termHeight = 20
	m.windowSizeKnown = true
	m.input = "/status"

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := next.(*uiModel)
	next, _ = updated.Update(statusRefreshDoneMsg{token: testStatusRefreshToken(updated), snapshot: collector.snapshot})
	updated = next.(*uiModel)
	raw := updated.View()
	if strings.Contains(raw, "\n38;2;") || strings.Contains(raw, "\n2;") {
		t.Fatalf("expected intact ANSI sequences without leaked fragments, got %q", raw)
	}
}

func TestStatusOverlaySubscriptionLineShowsRelativeResetTime(t *testing.T) {
	previousLocal := time.Local
	time.Local = time.FixedZone("TEST", 0)
	defer func() { time.Local = previousLocal }()

	collector := &stubStatusCollector{snapshot: uiStatusSnapshot{
		CollectedAt: time.Date(2026, time.March, 24, 21, 15, 0, 0, time.UTC),
		Auth:        uiStatusAuthInfo{Summary: "Subscription"},
		Subscription: uiStatusSubscriptionInfo{
			Applicable: true,
			Summary:    "Pro subscription",
			Windows: []uiStatusSubscriptionWindow{
				{Label: "5h", UsedPercent: 12.5, ResetAt: time.Now().Add(49 * time.Hour)},
			},
		},
	}}

	m := newProjectedStaticUIModel(
		WithUIStatusConfig(uiStatusConfig{WorkspaceRoot: "/tmp/workdir"}),
		WithUIStatusCollector(collector),
	)
	m.termWidth = 80
	m.termHeight = 20
	m.windowSizeKnown = true
	m.input = "/status"

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := next.(*uiModel)
	next, _ = updated.Update(statusRefreshDoneMsg{token: testStatusRefreshToken(updated), snapshot: collector.snapshot})
	updated = next.(*uiModel)
	plain := stripANSIAndTrimRight(updated.View())
	if !strings.Contains(plain, "resets in 2d1h") {
		t.Fatalf("expected rendered subscription line to include relative reset time, got %q", plain)
	}
	if !strings.Contains(plain, "at ") {
		t.Fatalf("expected rendered subscription line to include 'at' before local time, got %q", plain)
	}
	if !strings.Contains(plain, "TEST") {
		t.Fatalf("expected rendered subscription line to include local timestamp, got %q", plain)
	}
}

func TestStatusOverlaySubscriptionBarFitsNarrowWidth(t *testing.T) {
	collector := &stubStatusCollector{snapshot: uiStatusSnapshot{
		CollectedAt: time.Date(2026, time.March, 24, 21, 15, 0, 0, time.UTC),
		Auth:        uiStatusAuthInfo{Summary: "Subscription"},
		Subscription: uiStatusSubscriptionInfo{
			Applicable: true,
			Summary:    "Pro subscription",
			Windows: []uiStatusSubscriptionWindow{
				{Label: "5h", UsedPercent: 12.5, ResetAt: time.Date(2026, time.March, 24, 23, 15, 0, 0, time.UTC)},
			},
		},
	}}

	m := newProjectedStaticUIModel(
		WithUIStatusConfig(uiStatusConfig{WorkspaceRoot: "/tmp/workdir"}),
		WithUIStatusCollector(collector),
	)
	m.termWidth = 18
	m.termHeight = 20
	m.windowSizeKnown = true
	m.input = "/status"

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := next.(*uiModel)
	next, _ = updated.Update(statusRefreshDoneMsg{token: testStatusRefreshToken(updated), snapshot: collector.snapshot})
	updated = next.(*uiModel)
	for _, line := range strings.Split(strings.TrimSuffix(updated.View(), ansiHideCursor), "\n") {
		if lipgloss.Width(line) > m.termWidth {
			t.Fatalf("expected status line to fit width %d, got %d in %q", m.termWidth, lipgloss.Width(line), line)
		}
	}
}

func TestStatusLimitDurationMatchesCodexBuckets(t *testing.T) {
	if got := statusLimitDuration(300); got != "5h" {
		t.Fatalf("5h window label = %q, want %q", got, "5h")
	}
	if got := statusLimitDuration(60 * 24 * 7); got != "weekly" {
		t.Fatalf("weekly window label = %q, want %q", got, "weekly")
	}
}

func TestStatusUsageWindowsByLabelKeepsNonWhitelistedHourDurations(t *testing.T) {
	windows := statusUsageWindowsByLabel(statusUsagePayload{
		RateLimit: &statusUsageRateLimit{
			PrimaryWindow:   &statusUsageWindow{UsedPercent: 10, LimitWindowSeconds: 3600},
			SecondaryWindow: &statusUsageWindow{UsedPercent: 20, LimitWindowSeconds: 3 * 3600},
		},
		AdditionalRateLimits: []statusUsageExtraBucket{{
			RateLimit: &statusUsageRateLimit{
				PrimaryWindow: &statusUsageWindow{UsedPercent: 30, LimitWindowSeconds: 24 * 3600},
			},
		}},
	})
	got := make([]string, 0, len(windows))
	for _, window := range windows {
		got = append(got, window.Label)
	}
	want := []string{"1h", "3h", "24h"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("window labels = %v, want %v", got, want)
	}
}

func TestStatusUsageWindowsByLabelKeepsDuplicateDurationBuckets(t *testing.T) {
	resetAt := time.Date(2026, time.March, 25, 2, 0, 0, 0, time.UTC).Unix()
	windows := statusUsageWindowsByLabel(statusUsagePayload{
		RateLimit: &statusUsageRateLimit{
			PrimaryWindow: &statusUsageWindow{UsedPercent: 10, LimitWindowSeconds: 5 * 3600, ResetAt: resetAt},
		},
		AdditionalRateLimits: []statusUsageExtraBucket{{
			MeteredFeature: "images",
			LimitName:      "vision",
			RateLimit: &statusUsageRateLimit{
				PrimaryWindow: &statusUsageWindow{UsedPercent: 30, LimitWindowSeconds: 5 * 3600, ResetAt: resetAt},
			},
		}},
	})
	if len(windows) != 2 {
		t.Fatalf("windows len = %d, want 2", len(windows))
	}
	if windows[0].Label != "5h" || windows[1].Label != "5h" {
		t.Fatalf("window labels = %#v", windows)
	}
	if windows[0].Qualifier != "" {
		t.Fatalf("first qualifier = %q, want empty", windows[0].Qualifier)
	}
	if windows[1].Qualifier != "vision / images" {
		t.Fatalf("second qualifier = %q, want %q", windows[1].Qualifier, "vision / images")
	}
}

func TestStatusUsageWindowsByLabelDisambiguatesDuplicateExtraBucketsWithoutUniqueQualifier(t *testing.T) {
	resetAt := time.Date(2026, time.March, 25, 2, 0, 0, 0, time.UTC).Unix()
	windows := statusUsageWindowsByLabel(statusUsagePayload{
		AdditionalRateLimits: []statusUsageExtraBucket{
			{
				RateLimit: &statusUsageRateLimit{
					PrimaryWindow: &statusUsageWindow{UsedPercent: 10, LimitWindowSeconds: 5 * 3600, ResetAt: resetAt},
				},
			},
			{
				RateLimit: &statusUsageRateLimit{
					PrimaryWindow: &statusUsageWindow{UsedPercent: 20, LimitWindowSeconds: 5 * 3600, ResetAt: resetAt},
				},
			},
			{
				MeteredFeature: "images",
				LimitName:      "images",
				RateLimit: &statusUsageRateLimit{
					PrimaryWindow: &statusUsageWindow{UsedPercent: 30, LimitWindowSeconds: 5 * 3600, ResetAt: resetAt},
				},
			},
			{
				MeteredFeature: "images",
				LimitName:      "images",
				RateLimit: &statusUsageRateLimit{
					PrimaryWindow: &statusUsageWindow{UsedPercent: 40, LimitWindowSeconds: 5 * 3600, ResetAt: resetAt},
				},
			},
		},
	})
	got := make([]string, 0, len(windows))
	for _, window := range windows {
		got = append(got, window.Qualifier)
	}
	want := []string{"extra", "extra #2", "images", "images #2"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("qualifiers = %v, want %v", got, want)
	}
}

func TestStatusParentSessionNameResolvesFromPersistenceRoot(t *testing.T) {
	persistenceRoot := t.TempDir()
	containerDir := filepath.Join(persistenceRoot, "sessions", "workspace-a")
	parentStore, err := session.Create(containerDir, "workspace-a", "/tmp/work-a")
	if err != nil {
		t.Fatalf("create parent store: %v", err)
	}
	if err := parentStore.SetName("incident-root"); err != nil {
		t.Fatalf("set parent name: %v", err)
	}
	if got := statusParentSessionName(persistenceRoot, parentStore.Meta().SessionID); got != "incident-root" {
		t.Fatalf("parent session name = %q", got)
	}
}

func TestStatusVisibleAuthSummarySuppressesGenericSubscriptionWhenPlanPresent(t *testing.T) {
	if got := statusVisibleAuthSummary(uiStatusAuthInfo{Summary: "Subscription"}, uiStatusSubscriptionInfo{Summary: "Pro subscription"}); got != "" {
		t.Fatalf("visible auth summary = %q", got)
	}
	if got := statusVisibleAuthSummary(uiStatusAuthInfo{Summary: "user@example.com"}, uiStatusSubscriptionInfo{Summary: "Pro subscription"}); got != "user@example.com" {
		t.Fatalf("visible auth summary = %q", got)
	}
}

func TestStatusSubscriptionResetMetaIncludesRelativeDuration(t *testing.T) {
	now := time.Date(2026, time.March, 24, 20, 0, 0, 0, time.UTC)
	resetAt := now.Add(49*time.Hour + 3*time.Minute)
	got := statusSubscriptionResetMeta(resetAt, now)
	if !strings.Contains(got, "in 2d1h3m") {
		t.Fatalf("reset meta = %q", got)
	}
	if !strings.Contains(got, "at ") {
		t.Fatalf("expected local timestamp in reset meta, got %q", got)
	}
}

func TestStatusOverlayRendersQualifiedDuplicateSubscriptionBuckets(t *testing.T) {
	collector := &stubStatusCollector{snapshot: uiStatusSnapshot{
		CollectedAt: time.Date(2026, time.March, 24, 21, 15, 0, 0, time.UTC),
		Subscription: uiStatusSubscriptionInfo{
			Applicable: true,
			Summary:    "Pro subscription",
			Windows: []uiStatusSubscriptionWindow{
				{Label: "5h", UsedPercent: 10},
				{Label: "5h", Qualifier: "vision / images", UsedPercent: 30},
			},
		},
	}}

	m := newProjectedStaticUIModel(
		WithUIStatusConfig(uiStatusConfig{WorkspaceRoot: "/tmp/workdir"}),
		WithUIStatusCollector(collector),
	)
	m.termWidth = 100
	m.termHeight = 20
	m.windowSizeKnown = true
	m.input = "/status"

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := next.(*uiModel)
	next, _ = updated.Update(statusRefreshDoneMsg{token: testStatusRefreshToken(updated), snapshot: collector.snapshot})
	updated = next.(*uiModel)
	plain := stripANSIAndTrimRight(updated.View())
	if strings.Count(plain, "5h") < 2 {
		t.Fatalf("expected duplicate 5h rows, got %q", plain)
	}
	if !strings.Contains(plain, "vision / images") {
		t.Fatalf("expected qualified bucket rendered, got %q", plain)
	}
}

func TestStatusConfigHidesEmptyOverrideLine(t *testing.T) {
	collector := &stubStatusCollector{snapshot: uiStatusSnapshot{
		CollectedAt: time.Date(2026, time.March, 24, 21, 15, 0, 0, time.UTC),
		Workdir:     "/tmp/workdir",
		Config: uiStatusConfigInfo{
			SettingsPath:   "/Users/test/.builder/config.toml",
			Supervisor:     "edits",
			AutoCompaction: true,
		},
	}}

	m := newProjectedStaticUIModel(
		WithUIStatusConfig(uiStatusConfig{WorkspaceRoot: "/tmp/workdir"}),
		WithUIStatusCollector(collector),
	)
	m.termWidth = 100
	m.termHeight = 20
	m.windowSizeKnown = true
	m.input = "/status"

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := next.(*uiModel)
	next, _ = updated.Update(statusRefreshDoneMsg{token: testStatusRefreshToken(updated), snapshot: collector.snapshot})
	updated = next.(*uiModel)
	plain := stripANSIAndTrimRight(updated.View())
	if strings.Contains(plain, "overrides: none") {
		t.Fatalf("expected empty override line hidden, got %q", plain)
	}
}

func TestStatusCollectorPrefersWorkspaceRootForWorkdir(t *testing.T) {
	workspaceRoot := t.TempDir()
	collector := defaultUIStatusCollector{}
	snapshot, err := collector.Collect(context.Background(), uiStatusRequest{WorkspaceRoot: workspaceRoot})
	if err != nil {
		t.Fatalf("collect status: %v", err)
	}
	if snapshot.Workdir != workspaceRoot {
		t.Fatalf("workdir = %q, want %q", snapshot.Workdir, workspaceRoot)
	}
	if snapshot.Git.Visible {
		t.Fatal("expected non-git temp directory to hide git section")
	}
}

func TestStatusShouldFetchSubscriptionUsageOnlyForDefaultOpenAIOAuth(t *testing.T) {
	oauthState := auth.State{Method: auth.Method{Type: auth.MethodOAuth, OAuth: &auth.OAuthMethod{AccessToken: "access-token", AccountID: "acct-123"}}}
	if !statusShouldFetchSubscriptionUsage(config.Settings{}, oauthState) {
		t.Fatal("expected default OAuth configuration to allow subscription usage fetch")
	}
	for _, baseURL := range []string{
		"https://chatgpt.com",
		"https://chatgpt.com/backend-api",
		"https://chat.openai.com",
		"https://chat.openai.com/backend-api",
	} {
		if !statusShouldFetchSubscriptionUsage(config.Settings{OpenAIBaseURL: baseURL}, oauthState) {
			t.Fatalf("expected official ChatGPT base URL %q to allow subscription usage fetch", baseURL)
		}
	}
	if statusShouldFetchSubscriptionUsage(config.Settings{OpenAIBaseURL: "https://example.com/backend-api"}, oauthState) {
		t.Fatal("expected custom base URL override to disable subscription usage fetch")
	}
	if statusShouldFetchSubscriptionUsage(config.Settings{ProviderOverride: "anthropic"}, oauthState) {
		t.Fatal("expected provider override to disable subscription usage fetch")
	}
	if statusShouldFetchSubscriptionUsage(config.Settings{}, auth.State{}) {
		t.Fatal("expected non-OAuth auth state to disable subscription usage fetch")
	}
}

func TestCollectSubscriptionStatusDoesNotFetchForOverrides(t *testing.T) {
	originalFetcher := statusUsagePayloadFetcher
	defer func() { statusUsagePayloadFetcher = originalFetcher }()
	called := false
	statusUsagePayloadFetcher = func(_ context.Context, baseURL string, _ auth.State) (statusUsagePayload, error) {
		called = true
		if baseURL != statusUsageBaseURL {
			t.Fatalf("usage fetch base URL = %q, want %q", baseURL, statusUsageBaseURL)
		}
		return statusUsagePayload{PlanType: "pro"}, nil
	}
	state := auth.State{Method: auth.Method{Type: auth.MethodOAuth, OAuth: &auth.OAuthMethod{AccessToken: "access-token", AccountID: "acct-123"}}}

	status := collectSubscriptionStatus(context.Background(), uiStatusRequest{Settings: config.Settings{OpenAIBaseURL: "https://example.com/backend-api"}}, state, nil)
	if status.Applicable {
		t.Fatalf("expected overridden base URL to disable subscription status, got %+v", status)
	}
	status = collectSubscriptionStatus(context.Background(), uiStatusRequest{Settings: config.Settings{ProviderOverride: "openai-compatible"}}, state, nil)
	if status.Applicable {
		t.Fatalf("expected provider override to disable subscription status, got %+v", status)
	}
	if called {
		t.Fatal("did not expect subscription usage fetcher to be called for overrides")
	}

	status = collectSubscriptionStatus(context.Background(), uiStatusRequest{Settings: config.Settings{OpenAIBaseURL: "https://chatgpt.com/backend-api"}}, state, nil)
	if !status.Applicable || status.Summary != "Pro subscription" {
		t.Fatalf("expected official ChatGPT base URL to preserve subscription status, got %+v", status)
	}
	if !called {
		t.Fatal("expected subscription usage fetcher to be called for official ChatGPT base URL")
	}
}

func TestCollectSubscriptionStatusUsesFixedUsageEndpointForOfficialChatGPTHost(t *testing.T) {
	originalFetcher := statusUsagePayloadFetcher
	defer func() { statusUsagePayloadFetcher = originalFetcher }()
	called := false
	statusUsagePayloadFetcher = func(_ context.Context, baseURL string, _ auth.State) (statusUsagePayload, error) {
		called = true
		if baseURL != statusUsageBaseURL {
			t.Fatalf("usage fetch base URL = %q, want %q", baseURL, statusUsageBaseURL)
		}
		return statusUsagePayload{
			PlanType: "pro",
			RateLimit: &statusUsageRateLimit{
				PrimaryWindow: &statusUsageWindow{UsedPercent: 12.5, LimitWindowSeconds: 5 * 3600, ResetAt: 1704069000},
			},
		}, nil
	}
	state := auth.State{Method: auth.Method{Type: auth.MethodOAuth, OAuth: &auth.OAuthMethod{AccessToken: "access-token", AccountID: "acct-123"}}}
	status := collectSubscriptionStatus(context.Background(), uiStatusRequest{Settings: config.Settings{OpenAIBaseURL: "https://chatgpt.com"}}, state, nil)
	if !called {
		t.Fatal("expected subscription usage fetcher to be called")
	}
	if !status.Applicable || status.Summary != "Pro subscription" {
		t.Fatalf("expected official ChatGPT host to preserve subscription status, got %+v", status)
	}
	if len(status.Windows) != 1 || status.Windows[0].Label != "5h" {
		t.Fatalf("expected quota window preserved, got %+v", status.Windows)
	}
}

func TestFetchStatusUsagePayloadFetchesWhamUsageWithOAuthHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/backend-api/wham/usage" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer access-token" {
			t.Fatalf("authorization header = %q", got)
		}
		if got := r.Header.Get("ChatGPT-Account-Id"); got != "acct-123" {
			t.Fatalf("ChatGPT-Account-Id header = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"plan_type":"pro",
			"rate_limit":{
				"primary_window":{"used_percent":12.5,"limit_window_seconds":18000,"reset_at":1704069000},
				"secondary_window":{"used_percent":40.0,"limit_window_seconds":604800,"reset_at":1704074400}
			}
		}`))
	}))
	defer server.Close()

	status, err := fetchStatusUsagePayload(context.Background(), server.URL+"/backend-api", auth.State{Method: auth.Method{Type: auth.MethodOAuth, OAuth: &auth.OAuthMethod{AccessToken: "access-token", AccountID: "acct-123"}}})
	if err != nil {
		t.Fatalf("fetch status usage payload: %v", err)
	}

	if status.PlanType != "pro" {
		t.Fatalf("plan type = %q", status.PlanType)
	}
	windows := statusUsageWindowsByLabel(status)
	if len(windows) != 2 {
		t.Fatalf("windows len = %d", len(windows))
	}
	if windows[0].Label != "5h" || windows[1].Label != "weekly" {
		t.Fatalf("windows = %#v", windows)
	}
}

func TestFetchStatusUsagePayloadHandlesUsageErrors(t *testing.T) {
	t.Run("non-2xx", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "boom", http.StatusBadGateway)
		}))
		defer server.Close()

		_, err := fetchStatusUsagePayload(context.Background(), server.URL+"/backend-api", auth.State{Method: auth.Method{Type: auth.MethodOAuth, OAuth: &auth.OAuthMethod{AccessToken: "access-token"}}})
		if err == nil || !strings.Contains(err.Error(), "usage request failed") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("decode error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"plan_type":`))
		}))
		defer server.Close()

		_, err := fetchStatusUsagePayload(context.Background(), server.URL+"/backend-api", auth.State{Method: auth.Method{Type: auth.MethodOAuth, OAuth: &auth.OAuthMethod{AccessToken: "access-token"}}})
		if err == nil || !strings.Contains(err.Error(), "decode usage response") {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestStatusCollectorUsesRefreshedOAuthStateForUsageFetch(t *testing.T) {
	now := time.Date(2026, time.January, 1, 10, 0, 0, 0, time.UTC)
	store := auth.NewMemoryStore(auth.State{
		Scope: auth.ScopeGlobal,
		Method: auth.Method{
			Type: auth.MethodOAuth,
			OAuth: &auth.OAuthMethod{
				AccessToken:  "stale-token",
				RefreshToken: "refresh-token",
				TokenType:    "Bearer",
				Expiry:       now.Add(-time.Minute),
				AccountID:    "acct-456",
			},
		},
	})
	refresher := auth.NewOAuthRefresher(nil, func() time.Time { return now }, 30*time.Second)
	refresher.Refresh = func(context.Context, auth.Method) (auth.Method, error) {
		return auth.Method{
			Type: auth.MethodOAuth,
			OAuth: &auth.OAuthMethod{
				AccessToken:  "fresh-token",
				RefreshToken: "refresh-token",
				TokenType:    "Bearer",
				Expiry:       now.Add(time.Hour),
				AccountID:    "acct-456",
			},
		}, nil
	}
	manager := auth.NewManager(store, refresher, func() time.Time { return now.Add(time.Minute) })
	originalFetcher := statusUsagePayloadFetcher
	defer func() { statusUsagePayloadFetcher = originalFetcher }()
	statusUsagePayloadFetcher = func(_ context.Context, baseURL string, state auth.State) (statusUsagePayload, error) {
		if baseURL != statusUsageBaseURL {
			t.Fatalf("base URL = %q", baseURL)
		}
		authorization, err := state.Method.AuthHeaderValue()
		if err != nil {
			t.Fatalf("auth header value: %v", err)
		}
		if got := authorization; got != "Bearer fresh-token" {
			t.Fatalf("authorization header value = %q", got)
		}
		if got := strings.TrimSpace(state.Method.OAuth.AccountID); got != "acct-456" {
			t.Fatalf("ChatGPT-Account-Id value = %q", got)
		}
		return statusUsagePayload{PlanType: "pro", RateLimit: &statusUsageRateLimit{PrimaryWindow: &statusUsageWindow{UsedPercent: 12.5, LimitWindowSeconds: 18000, ResetAt: 1704069000}}}, nil
	}

	collector := defaultUIStatusCollector{}
	snapshot, err := collector.Collect(context.Background(), uiStatusRequest{
		WorkspaceRoot: t.TempDir(),
		Settings:      config.Settings{},
		AuthManager:   manager,
	})
	if err != nil {
		t.Fatalf("collect status: %v", err)
	}
	if !strings.Contains(snapshot.Auth.Summary, "Subscription") {
		t.Fatalf("auth summary = %q", snapshot.Auth.Summary)
	}
	if snapshot.Subscription.Summary != "Pro subscription" {
		t.Fatalf("subscription summary = %q", snapshot.Subscription.Summary)
	}
	if len(snapshot.Subscription.Windows) != 1 || snapshot.Subscription.Windows[0].Label != "5h" {
		t.Fatalf("windows = %#v", snapshot.Subscription.Windows)
	}
}

func TestStatusCollectorPreservesStoredAuthStateWhenRefreshFails(t *testing.T) {
	now := time.Date(2026, time.January, 1, 10, 0, 0, 0, time.UTC)
	store := auth.NewMemoryStore(auth.State{
		Scope: auth.ScopeGlobal,
		Method: auth.Method{
			Type: auth.MethodOAuth,
			OAuth: &auth.OAuthMethod{
				AccessToken:  "stale-token",
				RefreshToken: "refresh-token",
				TokenType:    "Bearer",
				Expiry:       now.Add(-time.Minute),
				AccountID:    "acct-789",
				Email:        "user@example.com",
			},
		},
		EnvAPIKeyPreference: auth.EnvAPIKeyPreferencePreferSaved,
	})
	refresher := auth.NewOAuthRefresher(nil, func() time.Time { return now }, 30*time.Second)
	refresher.Refresh = func(context.Context, auth.Method) (auth.Method, error) {
		return auth.Method{}, auth.ErrOAuthRefreshFailed
	}
	manager := auth.NewManager(store, refresher, func() time.Time { return now.Add(time.Minute) })

	collector := defaultUIStatusCollector{}
	snapshot, err := collector.Collect(context.Background(), uiStatusRequest{
		WorkspaceRoot: t.TempDir(),
		Settings:      config.Settings{},
		AuthManager:   manager,
	})
	if err != nil {
		t.Fatalf("collect status: %v", err)
	}
	if !strings.Contains(snapshot.Auth.Summary, "user@example.com") {
		t.Fatalf("auth summary = %q", snapshot.Auth.Summary)
	}
	if !snapshot.Subscription.Applicable {
		t.Fatal("expected subscription section to stay applicable")
	}
	if !strings.Contains(snapshot.Subscription.Summary, auth.ErrOAuthRefreshFailed.Error()) {
		t.Fatalf("subscription summary = %q", snapshot.Subscription.Summary)
	}
	if !strings.Contains(snapshot.CollectorWarning, auth.ErrOAuthRefreshFailed.Error()) {
		t.Fatalf("collector warning = %q", snapshot.CollectorWarning)
	}
}

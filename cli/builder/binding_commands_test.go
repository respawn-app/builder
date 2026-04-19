package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"builder/server/auth"
	"builder/server/authflow"
	"builder/server/metadata"
	"builder/server/serve"
	serverstartup "builder/server/startup"
	"builder/shared/config"
	"builder/shared/protocol"
	"builder/shared/serverapi"
)

type bindingCommandTimeoutProjectViewStub struct {
	resolveProjectPath func(context.Context, serverapi.ProjectResolvePathRequest) (serverapi.ProjectResolvePathResponse, error)
	listProjects       func(context.Context, serverapi.ProjectListRequest) (serverapi.ProjectListResponse, error)
	createProject      func(context.Context, serverapi.ProjectCreateRequest) (serverapi.ProjectCreateResponse, error)
	attachWorkspace    func(context.Context, serverapi.ProjectAttachWorkspaceRequest) (serverapi.ProjectAttachWorkspaceResponse, error)
	rebindWorkspace    func(context.Context, serverapi.ProjectRebindWorkspaceRequest) (serverapi.ProjectRebindWorkspaceResponse, error)
}

func (s bindingCommandTimeoutProjectViewStub) ListProjects(ctx context.Context, req serverapi.ProjectListRequest) (serverapi.ProjectListResponse, error) {
	if s.listProjects == nil {
		return serverapi.ProjectListResponse{}, errors.New("unexpected ListProjects call")
	}
	return s.listProjects(ctx, req)
}

func (s bindingCommandTimeoutProjectViewStub) ResolveProjectPath(ctx context.Context, req serverapi.ProjectResolvePathRequest) (serverapi.ProjectResolvePathResponse, error) {
	if s.resolveProjectPath == nil {
		return serverapi.ProjectResolvePathResponse{}, errors.New("unexpected ResolveProjectPath call")
	}
	return s.resolveProjectPath(ctx, req)
}

func (s bindingCommandTimeoutProjectViewStub) CreateProject(ctx context.Context, req serverapi.ProjectCreateRequest) (serverapi.ProjectCreateResponse, error) {
	if s.createProject == nil {
		return serverapi.ProjectCreateResponse{}, errors.New("unexpected CreateProject call")
	}
	return s.createProject(ctx, req)
}

func (s bindingCommandTimeoutProjectViewStub) AttachWorkspaceToProject(ctx context.Context, req serverapi.ProjectAttachWorkspaceRequest) (serverapi.ProjectAttachWorkspaceResponse, error) {
	if s.attachWorkspace == nil {
		return serverapi.ProjectAttachWorkspaceResponse{}, errors.New("unexpected AttachWorkspaceToProject call")
	}
	return s.attachWorkspace(ctx, req)
}

func (s bindingCommandTimeoutProjectViewStub) RebindWorkspace(ctx context.Context, req serverapi.ProjectRebindWorkspaceRequest) (serverapi.ProjectRebindWorkspaceResponse, error) {
	if s.rebindWorkspace == nil {
		return serverapi.ProjectRebindWorkspaceResponse{}, errors.New("unexpected RebindWorkspace call")
	}
	return s.rebindWorkspace(ctx, req)
}

func (bindingCommandTimeoutProjectViewStub) ListSessionsByProject(context.Context, serverapi.SessionListByProjectRequest) (serverapi.SessionListByProjectResponse, error) {
	return serverapi.SessionListByProjectResponse{}, nil
}

func (bindingCommandTimeoutProjectViewStub) GetProjectOverview(context.Context, serverapi.ProjectGetOverviewRequest) (serverapi.ProjectGetOverviewResponse, error) {
	return serverapi.ProjectGetOverviewResponse{}, errors.New("unexpected GetProjectOverview call")
}

type bindingCommandMemoryAuthHandler struct {
	state auth.State
}

func (h bindingCommandMemoryAuthHandler) WrapStore(auth.Store) auth.Store {
	return auth.NewMemoryStore(h.state)
}

func (bindingCommandMemoryAuthHandler) NeedsInteraction(req authflow.InteractionRequest) bool {
	return !req.Gate.Ready
}

func (bindingCommandMemoryAuthHandler) Interact(context.Context, authflow.InteractionRequest) (authflow.InteractionOutcome, error) {
	return authflow.InteractionOutcome{}, auth.ErrAuthNotConfigured
}

func (bindingCommandMemoryAuthHandler) LookupEnv(string) string {
	return ""
}

type bindingCommandAutoOnboarding struct{}

func (bindingCommandAutoOnboarding) EnsureOnboardingReady(_ context.Context, req serverstartup.OnboardingRequest) (config.App, error) {
	path, created, err := config.WriteDefaultSettingsFile()
	if err != nil {
		return config.App{}, err
	}
	reloaded, err := req.ReloadConfig()
	if err != nil {
		return config.App{}, err
	}
	reloaded.Source.CreatedDefaultConfig = created
	reloaded.Source.SettingsPath = path
	reloaded.Source.SettingsFileExists = true
	return reloaded, nil
}

func configureBindingCommandTestServerPort(t *testing.T) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	t.Setenv("BUILDER_SERVER_PORT", fmt.Sprintf("%d", port))
}

func startBindingCommandServer(t *testing.T, workspace string) func() {
	t.Helper()
	cfg, err := config.Load(workspace, config.LoadOptions{})
	if err != nil {
		t.Fatalf("config.Load server workspace: %v", err)
	}
	serve.ReleaseTestListenReservation(config.ServerListenAddress(cfg))
	srv, err := serve.Start(context.Background(), serverstartup.Request{WorkspaceRoot: workspace, WorkspaceRootExplicit: true, Model: "gpt-5"}, bindingCommandMemoryAuthHandler{state: auth.State{
		Scope:     auth.ScopeGlobal,
		Method:    auth.Method{Type: auth.MethodAPIKey, APIKey: &auth.APIKeyMethod{Key: "test-key"}},
		UpdatedAt: time.Now().UTC(),
	}}, bindingCommandAutoOnboarding{})
	if err != nil {
		t.Fatalf("serve.Start: %v", err)
	}
	serveCtx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Serve(serveCtx)
	}()
	waitForBindingCommandServer(t, workspace)
	return func() {
		cancel()
		if err := <-errCh; err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("Serve error: %v", err)
		}
		_ = srv.Close()
	}
}

func waitForBindingCommandServer(t *testing.T, workspace string) {
	t.Helper()
	cfg, err := config.Load(workspace, config.LoadOptions{})
	if err != nil {
		t.Fatalf("config.Load health workspace: %v", err)
	}
	healthURL := config.ServerHTTPBaseURL(cfg) + protocol.HealthPath
	client := &http.Client{Timeout: 250 * time.Millisecond}
	deadline := time.Now().Add(5 * time.Second)
	for {
		resp, err := client.Get(healthURL)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("binding command test server did not become healthy at %s", healthURL)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestProjectSubcommandPrintsBoundProjectID(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	configureBindingCommandTestServerPort(t)

	cfg, err := config.Load(workspace, config.LoadOptions{})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	binding, err := metadata.RegisterBinding(context.Background(), cfg.PersistenceRoot, cfg.WorkspaceRoot)
	if err != nil {
		t.Fatalf("RegisterBinding: %v", err)
	}
	cleanup := startBindingCommandServer(t, workspace)
	defer cleanup()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := projectSubcommand([]string{workspace}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit code = %d, want 0 stderr=%q", code, stderr.String())
	}
	if got := stdout.String(); got != binding.ProjectID+"\n" {
		t.Fatalf("stdout = %q, want %q", got, binding.ProjectID+"\n")
	}
}

func TestProjectSubcommandTreatsNestedDirectoryAsUnregistered(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	nested := filepath.Join(workspace, "subdir")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("MkdirAll nested: %v", err)
	}
	t.Setenv("HOME", home)
	configureBindingCommandTestServerPort(t)

	cfg, err := config.Load(workspace, config.LoadOptions{})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	_, err = metadata.RegisterBinding(context.Background(), cfg.PersistenceRoot, cfg.WorkspaceRoot)
	if err != nil {
		t.Fatalf("RegisterBinding: %v", err)
	}
	cleanup := startBindingCommandServer(t, workspace)
	defer cleanup()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := projectSubcommand([]string{nested}, &stdout, &stderr); code != 1 {
		t.Fatalf("exit code = %d, want 1 stderr=%q", code, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if got := stderr.String(); !bytes.Contains([]byte(got), []byte("workspace is not registered")) {
		t.Fatalf("stderr = %q, want unregistered error", got)
	}
}

func TestAttachSubcommandPathFirstBindsTargetToCurrentProject(t *testing.T) {
	home := t.TempDir()
	source := t.TempDir()
	target := t.TempDir()
	t.Setenv("HOME", home)
	configureBindingCommandTestServerPort(t)

	cfg, err := config.Load(source, config.LoadOptions{})
	if err != nil {
		t.Fatalf("config.Load source: %v", err)
	}
	binding, err := metadata.RegisterBinding(context.Background(), cfg.PersistenceRoot, cfg.WorkspaceRoot)
	if err != nil {
		t.Fatalf("RegisterBinding source: %v", err)
	}
	cleanup := startBindingCommandServer(t, source)
	defer cleanup()

	previousWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(source); err != nil {
		t.Fatalf("Chdir source: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(previousWD) })

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := attachSubcommand([]string{target}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit code = %d, want 0 stderr=%q", code, stderr.String())
	}
	if got := stdout.String(); got != binding.ProjectID+"\n" {
		t.Fatalf("stdout = %q, want %q", got, binding.ProjectID+"\n")
	}

	targetCfg, err := config.Load(target, config.LoadOptions{})
	if err != nil {
		t.Fatalf("config.Load target: %v", err)
	}
	resolved, err := metadata.ResolveBinding(context.Background(), targetCfg.PersistenceRoot, targetCfg.WorkspaceRoot)
	if err != nil {
		t.Fatalf("ResolveBinding target: %v", err)
	}
	if resolved.ProjectID != binding.ProjectID {
		t.Fatalf("target project id = %q, want %q", resolved.ProjectID, binding.ProjectID)
	}
}

func TestAttachSubcommandExplicitProjectOverridesCurrentWorkspace(t *testing.T) {
	home := t.TempDir()
	source := t.TempDir()
	target := t.TempDir()
	working := t.TempDir()
	t.Setenv("HOME", home)
	configureBindingCommandTestServerPort(t)

	cfg, err := config.Load(source, config.LoadOptions{})
	if err != nil {
		t.Fatalf("config.Load source: %v", err)
	}
	binding, err := metadata.RegisterBinding(context.Background(), cfg.PersistenceRoot, cfg.WorkspaceRoot)
	if err != nil {
		t.Fatalf("RegisterBinding source: %v", err)
	}
	cleanup := startBindingCommandServer(t, source)
	defer cleanup()

	previousWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(working); err != nil {
		t.Fatalf("Chdir working: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(previousWD) })

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := attachSubcommand([]string{"--project", binding.ProjectID, target}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit code = %d, want 0 stderr=%q", code, stderr.String())
	}
	if got := stdout.String(); got != binding.ProjectID+"\n" {
		t.Fatalf("stdout = %q, want %q", got, binding.ProjectID+"\n")
	}
}

func TestAttachSubcommandWithoutProjectGuidanceFailsWhenCurrentWorkspaceUnregistered(t *testing.T) {
	home := t.TempDir()
	working := t.TempDir()
	target := t.TempDir()
	t.Setenv("HOME", home)
	configureBindingCommandTestServerPort(t)
	cleanup := startBindingCommandServer(t, working)
	defer cleanup()

	previousWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(working); err != nil {
		t.Fatalf("Chdir working: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(previousWD) })

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := attachSubcommand([]string{target}, &stdout, &stderr); code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if got := stderr.String(); got == "" || !bytes.Contains([]byte(got), []byte("builder project")) || !bytes.Contains([]byte(got), []byte("--project <project-id>")) {
		t.Fatalf("stderr = %q, want recovery guidance", got)
	}
}

func TestAttachSubcommandRejectsUnknownExplicitProjectIDCleanly(t *testing.T) {
	home := t.TempDir()
	target := t.TempDir()
	t.Setenv("HOME", home)
	configureBindingCommandTestServerPort(t)
	cleanup := startBindingCommandServer(t, target)
	defer cleanup()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := attachSubcommand([]string{"--project", "project-missing", target}, &stdout, &stderr); code != 1 {
		t.Fatalf("exit code = %d, want 1 stderr=%q", code, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if got := stderr.String(); !bytes.Contains([]byte(got), []byte("does not exist in this Builder state")) || !bytes.Contains([]byte(got), []byte("project-missing")) {
		t.Fatalf("stderr = %q, want missing project guidance", got)
	}
}

func TestRebindSubcommandPreservesWorkspaceIdentity(t *testing.T) {
	home := t.TempDir()
	oldWorkspace := t.TempDir()
	newParent := t.TempDir()
	newWorkspace := filepath.Join(newParent, "workspace-moved")
	t.Setenv("HOME", home)
	configureBindingCommandTestServerPort(t)

	cfg, err := config.Load(oldWorkspace, config.LoadOptions{})
	if err != nil {
		t.Fatalf("config.Load oldWorkspace: %v", err)
	}
	binding, err := metadata.RegisterBinding(context.Background(), cfg.PersistenceRoot, cfg.WorkspaceRoot)
	if err != nil {
		t.Fatalf("RegisterBinding oldWorkspace: %v", err)
	}
	if err := os.Rename(oldWorkspace, newWorkspace); err != nil {
		t.Fatalf("Rename workspace: %v", err)
	}
	cleanup := startBindingCommandServer(t, newWorkspace)
	defer cleanup()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := rebindSubcommand([]string{oldWorkspace, newWorkspace}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit code = %d, want 0 stderr=%q", code, stderr.String())
	}
	if got := stdout.String(); got != binding.WorkspaceID+"\n" {
		t.Fatalf("stdout = %q, want %q", got, binding.WorkspaceID+"\n")
	}
	newProjectID, err := projectIDForPath(context.Background(), newWorkspace)
	if err != nil {
		t.Fatalf("projectIDForPath newWorkspace: %v", err)
	}
	if newProjectID != binding.ProjectID {
		t.Fatalf("new project id = %q, want %q", newProjectID, binding.ProjectID)
	}
	if _, err := projectIDForPath(context.Background(), oldWorkspace); !errors.Is(err, serverapi.ErrWorkspaceNotRegistered) {
		t.Fatalf("projectIDForPath oldWorkspace error = %v, want ErrWorkspaceNotRegistered", err)
	}
}

func TestRebindSubcommandRejectsInvalidInputs(t *testing.T) {
	home := t.TempDir()
	oldWorkspace := t.TempDir()
	otherWorkspace := t.TempDir()
	missingWorkspace := filepath.Join(t.TempDir(), "missing")
	t.Setenv("HOME", home)
	configureBindingCommandTestServerPort(t)

	cfg, err := config.Load(oldWorkspace, config.LoadOptions{})
	if err != nil {
		t.Fatalf("config.Load oldWorkspace: %v", err)
	}
	_, err = metadata.RegisterBinding(context.Background(), cfg.PersistenceRoot, cfg.WorkspaceRoot)
	if err != nil {
		t.Fatalf("RegisterBinding oldWorkspace: %v", err)
	}
	otherCfg, err := config.Load(otherWorkspace, config.LoadOptions{})
	if err != nil {
		t.Fatalf("config.Load otherWorkspace: %v", err)
	}
	_, err = metadata.RegisterBinding(context.Background(), otherCfg.PersistenceRoot, otherCfg.WorkspaceRoot)
	if err != nil {
		t.Fatalf("RegisterBinding otherWorkspace: %v", err)
	}
	cleanup := startBindingCommandServer(t, oldWorkspace)
	defer cleanup()

	assertRebindError := func(args []string, want string) {
		t.Helper()
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		if code := rebindSubcommand(args, &stdout, &stderr); code != 1 {
			t.Fatalf("exit code = %d, want 1 stderr=%q", code, stderr.String())
		}
		if stdout.Len() != 0 {
			t.Fatalf("stdout = %q, want empty", stdout.String())
		}
		if got := stderr.String(); !bytes.Contains([]byte(got), []byte(want)) {
			t.Fatalf("stderr = %q, want %q", got, want)
		}
	}

	assertRebindError([]string{filepath.Join(t.TempDir(), "unknown-old"), otherWorkspace}, "workspace is not registered")
	assertRebindError([]string{oldWorkspace, missingWorkspace}, "does not exist")
	assertRebindError([]string{oldWorkspace, otherWorkspace}, "already bound")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := rebindSubcommand([]string{oldWorkspace}, &stdout, &stderr); code != 2 {
		t.Fatalf("exit code = %d, want 2 stderr=%q", code, stderr.String())
	}
	if got := stderr.String(); !bytes.Contains([]byte(got), []byte("rebind requires <old-path> and <new-path>")) {
		t.Fatalf("stderr = %q, want usage guidance", got)
	}
}

func TestResolveWorkspaceBindingAppliesRPCTimeout(t *testing.T) {
	originalTimeout := bindingCommandRPCTimeout
	bindingCommandRPCTimeout = 20 * time.Millisecond
	t.Cleanup(func() { bindingCommandRPCTimeout = originalTimeout })

	stub := bindingCommandTimeoutProjectViewStub{
		resolveProjectPath: func(ctx context.Context, req serverapi.ProjectResolvePathRequest) (serverapi.ProjectResolvePathResponse, error) {
			<-ctx.Done()
			return serverapi.ProjectResolvePathResponse{}, ctx.Err()
		},
	}
	start := time.Now()
	_, err := resolveWorkspaceBinding(context.Background(), stub, "/tmp/workspace")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("resolveWorkspaceBinding error = %v, want deadline exceeded", err)
	}
	if elapsed := time.Since(start); elapsed > 250*time.Millisecond {
		t.Fatalf("resolveWorkspaceBinding timeout took too long: %v", elapsed)
	}
}

func TestBindingCommandProjectRPCWrappersApplyTimeout(t *testing.T) {
	originalTimeout := bindingCommandRPCTimeout
	bindingCommandRPCTimeout = 20 * time.Millisecond
	t.Cleanup(func() { bindingCommandRPCTimeout = originalTimeout })

	deadlineErrAfterCancel := func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}
	stub := bindingCommandTimeoutProjectViewStub{
		listProjects: func(ctx context.Context, req serverapi.ProjectListRequest) (serverapi.ProjectListResponse, error) {
			return serverapi.ProjectListResponse{}, deadlineErrAfterCancel(ctx)
		},
		createProject: func(ctx context.Context, req serverapi.ProjectCreateRequest) (serverapi.ProjectCreateResponse, error) {
			return serverapi.ProjectCreateResponse{}, deadlineErrAfterCancel(ctx)
		},
		attachWorkspace: func(ctx context.Context, req serverapi.ProjectAttachWorkspaceRequest) (serverapi.ProjectAttachWorkspaceResponse, error) {
			return serverapi.ProjectAttachWorkspaceResponse{}, deadlineErrAfterCancel(ctx)
		},
		rebindWorkspace: func(ctx context.Context, req serverapi.ProjectRebindWorkspaceRequest) (serverapi.ProjectRebindWorkspaceResponse, error) {
			return serverapi.ProjectRebindWorkspaceResponse{}, deadlineErrAfterCancel(ctx)
		},
	}

	assertDeadlineExceeded := func(name string, err error) {
		t.Helper()
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("%s error = %v, want deadline exceeded", name, err)
		}
	}

	_, err := listProjectsWithTimeout(context.Background(), stub)
	assertDeadlineExceeded("listProjectsWithTimeout", err)
	_, err = createProjectWithTimeout(context.Background(), stub, "project", "/tmp/workspace")
	assertDeadlineExceeded("createProjectWithTimeout", err)
	_, err = attachWorkspaceToProject(context.Background(), stub, "project-1", "/tmp/workspace")
	assertDeadlineExceeded("attachWorkspaceToProject", err)
	_, err = rebindWorkspaceWithTimeout(context.Background(), stub, "/tmp/old", "/tmp/new")
	assertDeadlineExceeded("rebindWorkspaceWithTimeout", err)
}

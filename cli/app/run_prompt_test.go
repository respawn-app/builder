package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	goruntime "runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"builder/server/auth"
	"builder/server/authflow"
	"builder/server/llm"
	"builder/server/runtime"
	"builder/server/serve"
	"builder/server/session"
	serverstartup "builder/server/startup"
	"builder/server/tools/askquestion"
	"builder/shared/client"
	"builder/shared/clientui"
	"builder/shared/config"
	"builder/shared/protocol"
	"builder/shared/serverapi"
	"builder/shared/testopenai"
)

type memoryAuthHandler struct {
	state auth.State
}

type headlessProjectViewStubService struct {
	listProjectsResp serverapi.ProjectListResponse
	listProjectsErr  error
	overviews        map[string]serverapi.ProjectGetOverviewResponse
	overviewErr      error
}

type configuredProjectViewRemoteStub struct {
	identity           protocol.ServerIdentity
	resolveProjectPath func(context.Context, serverapi.ProjectResolvePathRequest) (serverapi.ProjectResolvePathResponse, error)
	listProjects       func(context.Context, serverapi.ProjectListRequest) (serverapi.ProjectListResponse, error)
	getProjectOverview func(context.Context, serverapi.ProjectGetOverviewRequest) (serverapi.ProjectGetOverviewResponse, error)
	closed             atomic.Bool
}

func (s *configuredProjectViewRemoteStub) Close() error {
	if s != nil {
		s.closed.Store(true)
	}
	return nil
}

func (s *configuredProjectViewRemoteStub) Identity() protocol.ServerIdentity {
	if s == nil {
		return protocol.ServerIdentity{}
	}
	return s.identity
}

func (s *configuredProjectViewRemoteStub) ListProjects(ctx context.Context, req serverapi.ProjectListRequest) (serverapi.ProjectListResponse, error) {
	if s != nil && s.listProjects != nil {
		return s.listProjects(ctx, req)
	}
	return serverapi.ProjectListResponse{}, errors.New("unexpected ListProjects call")
}

func (s *configuredProjectViewRemoteStub) ResolveProjectPath(ctx context.Context, req serverapi.ProjectResolvePathRequest) (serverapi.ProjectResolvePathResponse, error) {
	if s != nil && s.resolveProjectPath != nil {
		return s.resolveProjectPath(ctx, req)
	}
	return serverapi.ProjectResolvePathResponse{}, errors.New("unexpected ResolveProjectPath call")
}

func (*configuredProjectViewRemoteStub) CreateProject(context.Context, serverapi.ProjectCreateRequest) (serverapi.ProjectCreateResponse, error) {
	return serverapi.ProjectCreateResponse{}, errors.New("unexpected CreateProject call")
}

func (*configuredProjectViewRemoteStub) AttachWorkspaceToProject(context.Context, serverapi.ProjectAttachWorkspaceRequest) (serverapi.ProjectAttachWorkspaceResponse, error) {
	return serverapi.ProjectAttachWorkspaceResponse{}, errors.New("unexpected AttachWorkspaceToProject call")
}

func (*configuredProjectViewRemoteStub) RebindWorkspace(context.Context, serverapi.ProjectRebindWorkspaceRequest) (serverapi.ProjectRebindWorkspaceResponse, error) {
	return serverapi.ProjectRebindWorkspaceResponse{}, errors.New("unexpected RebindWorkspace call")
}

func (s *configuredProjectViewRemoteStub) GetProjectOverview(ctx context.Context, req serverapi.ProjectGetOverviewRequest) (serverapi.ProjectGetOverviewResponse, error) {
	if s != nil && s.getProjectOverview != nil {
		return s.getProjectOverview(ctx, req)
	}
	return serverapi.ProjectGetOverviewResponse{}, errors.New("unexpected GetProjectOverview call")
}

func (*configuredProjectViewRemoteStub) ListSessionsByProject(context.Context, serverapi.SessionListByProjectRequest) (serverapi.SessionListByProjectResponse, error) {
	return serverapi.SessionListByProjectResponse{}, nil
}

func (s headlessProjectViewStubService) ListProjects(context.Context, serverapi.ProjectListRequest) (serverapi.ProjectListResponse, error) {
	return s.listProjectsResp, s.listProjectsErr
}

func (headlessProjectViewStubService) ResolveProjectPath(context.Context, serverapi.ProjectResolvePathRequest) (serverapi.ProjectResolvePathResponse, error) {
	return serverapi.ProjectResolvePathResponse{}, errors.New("unexpected ResolveProjectPath call")
}

func (headlessProjectViewStubService) CreateProject(context.Context, serverapi.ProjectCreateRequest) (serverapi.ProjectCreateResponse, error) {
	return serverapi.ProjectCreateResponse{}, errors.New("unexpected CreateProject call")
}

func (headlessProjectViewStubService) AttachWorkspaceToProject(context.Context, serverapi.ProjectAttachWorkspaceRequest) (serverapi.ProjectAttachWorkspaceResponse, error) {
	return serverapi.ProjectAttachWorkspaceResponse{}, errors.New("unexpected AttachWorkspaceToProject call")
}

func (headlessProjectViewStubService) RebindWorkspace(context.Context, serverapi.ProjectRebindWorkspaceRequest) (serverapi.ProjectRebindWorkspaceResponse, error) {
	return serverapi.ProjectRebindWorkspaceResponse{}, errors.New("unexpected RebindWorkspace call")
}

func (s headlessProjectViewStubService) GetProjectOverview(_ context.Context, req serverapi.ProjectGetOverviewRequest) (serverapi.ProjectGetOverviewResponse, error) {
	if s.overviewErr != nil {
		return serverapi.ProjectGetOverviewResponse{}, s.overviewErr
	}
	resp, ok := s.overviews[req.ProjectID]
	if !ok {
		return serverapi.ProjectGetOverviewResponse{}, errors.New("missing overview")
	}
	return resp, nil
}

func (headlessProjectViewStubService) ListSessionsByProject(context.Context, serverapi.SessionListByProjectRequest) (serverapi.SessionListByProjectResponse, error) {
	return serverapi.SessionListByProjectResponse{}, nil
}

func (h memoryAuthHandler) WrapStore(auth.Store) auth.Store {
	return auth.NewMemoryStore(h.state)
}

func (memoryAuthHandler) NeedsInteraction(req authflow.InteractionRequest) bool {
	return !req.Gate.Ready
}

func (memoryAuthHandler) Interact(context.Context, authflow.InteractionRequest) (authflow.InteractionOutcome, error) {
	return authflow.InteractionOutcome{}, auth.ErrAuthNotConfigured
}

func (memoryAuthHandler) LookupEnv(string) string {
	return ""
}

type autoOnboarding struct{}

func (autoOnboarding) EnsureOnboardingReady(_ context.Context, req serverstartup.OnboardingRequest) (config.App, error) {
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

func waitForConfiguredRunPromptDaemon(t *testing.T, workspace string) {
	t.Helper()
	loadCfg, err := config.Load(workspace, config.LoadOptions{})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	healthURL := config.ServerHTTPBaseURL(loadCfg) + protocol.HealthPath
	deadline := time.Now().Add(5 * time.Second)
	client := &http.Client{Timeout: 250 * time.Millisecond}
	for {
		resp, err := client.Get(healthURL)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("configured daemon did not become healthy at %s", healthURL)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestEnsureSubagentSessionNameSetsDefault(t *testing.T) {
	containerDir := t.TempDir()
	store, err := session.NewLazy(containerDir, "workspace-x", "/tmp/workspace")
	if err != nil {
		t.Fatalf("new lazy session: %v", err)
	}

	if err := ensureSubagentSessionName(store); err != nil {
		t.Fatalf("ensure subagent session name: %v", err)
	}

	meta := store.Meta()
	want := meta.SessionID + " " + subagentSessionSuffix
	if meta.Name != want {
		t.Fatalf("session name = %q, want %q", meta.Name, want)
	}
}

func TestEnsureSubagentSessionNamePreservesExistingName(t *testing.T) {
	containerDir := t.TempDir()
	store, err := session.NewLazy(containerDir, "workspace-x", "/tmp/workspace")
	if err != nil {
		t.Fatalf("new lazy session: %v", err)
	}
	if err := store.SetName("incident triage"); err != nil {
		t.Fatalf("set name: %v", err)
	}

	if err := ensureSubagentSessionName(store); err != nil {
		t.Fatalf("ensure subagent session name: %v", err)
	}

	if got := store.Meta().Name; got != "incident triage" {
		t.Fatalf("session name = %q, want incident triage", got)
	}
}

func TestWriteRunProgressEventOnlyWritesSelectedKinds(t *testing.T) {
	var out bytes.Buffer

	writeRunProgressEvent(&out, runtime.Event{Kind: runtime.EventAssistantDelta, StepID: "s1", AssistantDelta: "hello"})
	writeRunProgressEvent(&out, runtime.Event{Kind: runtime.EventToolCallStarted, StepID: "s1"})
	writeRunProgressEvent(&out, runtime.Event{Kind: runtime.EventReviewerCompleted, StepID: "s1", Reviewer: &runtime.ReviewerStatus{Outcome: "no_suggestions"}})

	text := out.String()
	if strings.Contains(text, "AssistantDelta") {
		t.Fatalf("unexpected assistant delta in progress output: %q", text)
	}
	if !strings.Contains(text, "Running tool") {
		t.Fatalf("expected tool call started in progress output, got %q", text)
	}
	if !strings.Contains(text, "Review finished") {
		t.Fatalf("expected reviewer completed in progress output, got %q", text)
	}
}

func TestRunPromptAskHandlerReturnsError(t *testing.T) {
	_, err := runPromptAskHandler(askquestion.Request{Question: "Need approval?"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "You can't ask questions") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunPromptWithoutAuthReturnsErrAuthNotConfiguredWithoutReadingStdin(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	registerAppWorkspace(t, workspace)
	t.Setenv("OPENAI_API_KEY", "")

	originalStdin := os.Stdin
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe stdin: %v", err)
	}
	_ = w.Close()
	os.Stdin = r
	t.Cleanup(func() {
		os.Stdin = originalStdin
		_ = r.Close()
	})

	_, err = RunPrompt(context.Background(), Options{WorkspaceRoot: workspace}, "hello", 0, nil)
	if !errors.Is(err, auth.ErrAuthNotConfigured) {
		t.Fatalf("expected auth not configured without stdin prompt, got %v", err)
	}
}

func TestRunPromptUsesConfiguredDaemonWithoutLocalAuth(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	registerAppWorkspace(t, workspace)

	fakeResponses, hits := newFakeResponsesServer(t, []string{"daemon reply"})
	defer fakeResponses.Close()

	srv, err := serve.Start(context.Background(), serverstartup.Request{
		WorkspaceRoot:         workspace,
		WorkspaceRootExplicit: true,
		Model:                 "gpt-5",
		OpenAIBaseURL:         fakeResponses.URL,
		OpenAIBaseURLExplicit: true,
	}, memoryAuthHandler{state: auth.State{
		Scope: auth.ScopeGlobal,
		Method: auth.Method{
			Type:   auth.MethodAPIKey,
			APIKey: &auth.APIKeyMethod{Key: "test-key"},
		},
		UpdatedAt: time.Now().UTC(),
	}}, autoOnboarding{})
	if err != nil {
		t.Fatalf("serve.Start: %v", err)
	}
	defer func() { _ = srv.Close() }()

	serveCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Serve(serveCtx)
	}()

	waitForConfiguredRunPromptDaemon(t, workspace)

	result, err := RunPrompt(context.Background(), Options{WorkspaceRoot: workspace, WorkspaceRootExplicit: true}, "hello through daemon", 0, nil)
	if err != nil {
		t.Fatalf("RunPrompt: %v", err)
	}
	if result.Result != "daemon reply" {
		t.Fatalf("result = %q, want %q", result.Result, "daemon reply")
	}
	if hits.Load() != 1 {
		t.Fatalf("expected daemon-backed llm call once, got %d", hits.Load())
	}

	cancel()
	if serveErr := <-errCh; !errors.Is(serveErr, context.Canceled) {
		t.Fatalf("Serve error = %v, want context canceled", serveErr)
	}
}

func TestRunPromptRejectsIncompatibleConfiguredDaemonAndFallsBackToEmbedded(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	registerAppWorkspace(t, workspace)
	t.Setenv("OPENAI_API_KEY", "test-key")

	fakeResponses, hits := newFakeResponsesServer(t, []string{"embedded fallback reply"})
	defer fakeResponses.Close()

	cleanup := publishConfiguredRemoteForWorkspace(t, workspace, protocol.CapabilityFlags{
		JSONRPCWebSocket: true,
		ProjectAttach:    true,
		SessionAttach:    true,
		SessionPlan:      true,
		SessionLifecycle: true,
		SessionRuntime:   true,
		RuntimeControl:   true,
		PromptControl:    true,
		PromptActivity:   true,
		SessionActivity:  true,
	})
	defer cleanup()

	result, err := RunPrompt(context.Background(), Options{
		WorkspaceRoot:         workspace,
		WorkspaceRootExplicit: true,
		Model:                 "gpt-5",
		OpenAIBaseURL:         fakeResponses.URL,
		OpenAIBaseURLExplicit: true,
	}, "hello through fallback", 0, nil)
	if err != nil {
		t.Fatalf("RunPrompt: %v", err)
	}
	if result.Result != "embedded fallback reply" {
		t.Fatalf("result = %q, want %q", result.Result, "embedded fallback reply")
	}
	if hits.Load() != 1 {
		t.Fatalf("expected embedded fallback llm call once, got %d", hits.Load())
	}
}

func TestStartRunPromptClientFallsBackToEmbeddedWhenDaemonLaunchFails(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	registerAppWorkspace(t, workspace)
	t.Setenv("OPENAI_API_KEY", "test-key")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if testopenai.HandleInputTokenCount(w, r, 1) {
			return
		}
		if r.URL.Path != "/responses" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		testopenai.WriteCompletedResponseStream(w, "embedded fallback", 1, 1)
	}))
	defer server.Close()

	originalLaunch := launchRunPromptDaemon
	t.Cleanup(func() { launchRunPromptDaemon = originalLaunch })
	launchRunPromptDaemon = func(context.Context, Options) (*client.Remote, func() error, bool, error) {
		return nil, nil, false, errors.New("daemon launch failed")
	}

	runClient, closeFn, err := startRunPromptClient(context.Background(), Options{
		WorkspaceRoot:         workspace,
		WorkspaceRootExplicit: true,
		Model:                 "gpt-5",
		OpenAIBaseURL:         server.URL,
		OpenAIBaseURLExplicit: true,
	})
	if err != nil {
		t.Fatalf("startRunPromptClient: %v", err)
	}
	defer func() {
		if closeFn != nil {
			_ = closeFn()
		}
	}()

	response, err := runClient.RunPrompt(context.Background(), serverapi.RunPromptRequest{
		ClientRequestID: "req-embedded-fallback",
		Prompt:          "hello",
	}, nil)
	if err != nil {
		t.Fatalf("RunPrompt: %v", err)
	}
	if response.Result != "embedded fallback" {
		t.Fatalf("result = %q, want embedded fallback", response.Result)
	}
}

func TestOwnedDaemonCloseFallsBackToKillWhenInterruptFails(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("sleep helper is unix-only")
	}
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- cmd.Wait()
	}()
	originalTerminate := terminateOwnedDaemonProcess
	originalKill := forceKillOwnedDaemonProcess
	t.Cleanup(func() {
		terminateOwnedDaemonProcess = originalTerminate
		forceKillOwnedDaemonProcess = originalKill
	})
	killed := false
	terminateOwnedDaemonProcess = func(process *os.Process) error {
		return errors.New("interrupt unsupported")
	}
	forceKillOwnedDaemonProcess = func(process *os.Process) error {
		killed = true
		if process == nil {
			return nil
		}
		return process.Kill()
	}
	closeFn := newOwnedDaemonClose(nil, cmd, errCh)
	if err := closeFn(); err != nil {
		t.Fatalf("closeFn: %v", err)
	}
	if !killed {
		t.Fatal("expected owned daemon close to fall back to kill")
	}
}

func TestRunPromptUsesInvocationOverridesWhenAttachingToConfiguredDaemon(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	registerAppWorkspace(t, workspace)

	defaultResponses, defaultHits := newFakeResponsesServer(t, []string{"daemon default"})
	defer defaultResponses.Close()
	overrideResponses, overrideHits := newFakeResponsesServer(t, []string{"override reply"})
	defer overrideResponses.Close()

	srv, err := serve.Start(context.Background(), serverstartup.Request{
		WorkspaceRoot:         workspace,
		WorkspaceRootExplicit: true,
		Model:                 "gpt-5",
		OpenAIBaseURL:         defaultResponses.URL,
		OpenAIBaseURLExplicit: true,
	}, memoryAuthHandler{state: auth.State{
		Scope: auth.ScopeGlobal,
		Method: auth.Method{
			Type:   auth.MethodAPIKey,
			APIKey: &auth.APIKeyMethod{Key: "test-key"},
		},
		UpdatedAt: time.Now().UTC(),
	}}, autoOnboarding{})
	if err != nil {
		t.Fatalf("serve.Start: %v", err)
	}
	defer func() { _ = srv.Close() }()

	serveCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Serve(serveCtx)
	}()

	waitForConfiguredRunPromptDaemon(t, workspace)

	result, err := RunPrompt(context.Background(), Options{
		WorkspaceRoot:         workspace,
		WorkspaceRootExplicit: true,
		Model:                 "gpt-5",
		OpenAIBaseURL:         overrideResponses.URL,
		OpenAIBaseURLExplicit: true,
	}, "hello through override", 0, nil)
	if err != nil {
		t.Fatalf("RunPrompt: %v", err)
	}
	if result.Result != "override reply" {
		t.Fatalf("result = %q, want %q", result.Result, "override reply")
	}
	if overrideHits.Load() != 1 {
		t.Fatalf("expected override llm call once, got %d", overrideHits.Load())
	}
	if defaultHits.Load() != 0 {
		t.Fatalf("expected daemon default llm endpoint unused, got %d", defaultHits.Load())
	}

	cancel()
	if serveErr := <-errCh; !errors.Is(serveErr, context.Canceled) {
		t.Fatalf("Serve error = %v, want context canceled", serveErr)
	}
}

func TestTryDialMatchingConfiguredRemoteRejectsServerThatDoesNotMatchSpawnedPID(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	registerAppWorkspace(t, workspace)
	cleanup := publishConfiguredRemoteForWorkspace(t, workspace, protocol.CapabilityFlags{RunPrompt: true, AuthBootstrap: true})
	defer cleanup()
	if remote, ok := tryDialMatchingConfiguredRemote(context.Background(), Options{WorkspaceRoot: workspace, WorkspaceRootExplicit: true}, configuredRemoteSupportsRunPrompt, func(identity protocol.ServerIdentity) bool {
		return identity.PID == 111
	}); ok || remote != nil {
		t.Fatalf("expected mismatched pid server to be rejected, got remote=%v ok=%t", remote, ok)
	}
}

func TestTryDialMatchingConfiguredRemoteSkipsUnregisteredWorkspace(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	configureAppTestServerPort(t)
	cleanup := publishConfiguredRemoteForWorkspace(t, workspace, protocol.CapabilityFlags{RunPrompt: true, AuthBootstrap: true})
	defer cleanup()
	if remote, ok := tryDialMatchingConfiguredRemote(context.Background(), Options{WorkspaceRoot: workspace, WorkspaceRootExplicit: true}, configuredRemoteSupportsRunPrompt, nil); ok || remote != nil {
		t.Fatalf("expected unregistered workspace to skip configured remote attach, got remote=%v ok=%t", remote, ok)
	}
}

func TestStartLocalRunPromptDaemonAttemptsLaunchWhenRegistrationMustBeResolvedByServer(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	configureAppTestServerPort(t)

	originalResolve := resolveDaemonExecutablePath
	t.Cleanup(func() { resolveDaemonExecutablePath = originalResolve })

	lookupCalls := 0
	resolveDaemonExecutablePath = func() (string, bool) {
		lookupCalls++
		return "/bin/false", true
	}

	remote, closeFn, ok, err := startLocalRunPromptDaemon(context.Background(), Options{WorkspaceRoot: workspace, WorkspaceRootExplicit: true})
	if err == nil {
		t.Fatal("expected daemon launch attempt to fail for unregistered workspace probe")
	}
	if ok {
		t.Fatal("expected no connected daemon client after failed launch attempt")
	}
	if remote != nil {
		t.Fatalf("expected no remote client, got %v", remote)
	}
	if closeFn != nil {
		t.Fatal("expected no close function when launch is skipped")
	}
	if lookupCalls != 1 {
		t.Fatalf("expected daemon executable lookup once, got %d calls", lookupCalls)
	}
}

func TestStartRunPromptClientUnregisteredWorkspaceReturnsRegistrationError(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	configureAppTestServerPort(t)
	t.Setenv("OPENAI_API_KEY", "test-key")

	runClient, closeFn, err := startRunPromptClient(context.Background(), Options{WorkspaceRoot: workspace, WorkspaceRootExplicit: true})
	if !errors.Is(err, serverapi.ErrWorkspaceNotRegistered) {
		t.Fatalf("startRunPromptClient error = %v, want ErrWorkspaceNotRegistered", err)
	}
	if runClient != nil {
		t.Fatalf("expected no run client, got %v", runClient)
	}
	if closeFn != nil {
		t.Fatal("expected no close function when startup fails")
	}
	if !strings.Contains(err.Error(), "builder project") || !strings.Contains(err.Error(), "builder attach") {
		t.Fatalf("expected recovery guidance in error, got %q", err)
	}
}

func TestSelectSingleRemoteWorkspaceForHeadlessChoosesOnlyWorkspace(t *testing.T) {
	client := client.NewLoopbackProjectViewClient(headlessProjectViewStubService{
		listProjectsResp: serverapi.ProjectListResponse{Projects: []clientui.ProjectSummary{{ProjectID: "project-1"}}},
		overviews: map[string]serverapi.ProjectGetOverviewResponse{
			"project-1": {Overview: clientui.ProjectOverview{Workspaces: []clientui.ProjectWorkspaceSummary{{WorkspaceID: "workspace-1"}}}},
		},
	})

	selection, found, err := selectSingleRemoteWorkspaceForHeadless(context.Background(), client)
	if err != nil {
		t.Fatalf("selectSingleRemoteWorkspaceForHeadless: %v", err)
	}
	if !found {
		t.Fatal("expected single workspace selection")
	}
	if selection.ProjectID != "project-1" || selection.WorkspaceID != "workspace-1" {
		t.Fatalf("unexpected selection: %+v", selection)
	}
}

func TestSelectSingleRemoteWorkspaceForHeadlessIgnoresUnavailableWorkspaces(t *testing.T) {
	client := client.NewLoopbackProjectViewClient(headlessProjectViewStubService{
		listProjectsResp: serverapi.ProjectListResponse{Projects: []clientui.ProjectSummary{{ProjectID: "project-1"}}},
		overviews: map[string]serverapi.ProjectGetOverviewResponse{
			"project-1": {Overview: clientui.ProjectOverview{Workspaces: []clientui.ProjectWorkspaceSummary{
				{WorkspaceID: "workspace-missing", Availability: clientui.ProjectAvailabilityMissing},
				{WorkspaceID: "workspace-1", Availability: clientui.ProjectAvailabilityAvailable},
				{WorkspaceID: "workspace-inaccessible", Availability: clientui.ProjectAvailabilityInaccessible},
			}}},
		},
	})

	selection, found, err := selectSingleRemoteWorkspaceForHeadless(context.Background(), client)
	if err != nil {
		t.Fatalf("selectSingleRemoteWorkspaceForHeadless: %v", err)
	}
	if !found {
		t.Fatal("expected single available workspace selection")
	}
	if selection.ProjectID != "project-1" || selection.WorkspaceID != "workspace-1" {
		t.Fatalf("unexpected selection: %+v", selection)
	}
}

func TestTryDialConfiguredRunPromptRemoteUsesFreshDialTimeoutAfterWorkspaceDiscovery(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)

	originalProjectViewsDial := dialConfiguredProjectViewRemote
	originalRemoteDial := dialConfiguredRemote
	originalAttachTimeout := configuredRemoteAttachTimeout
	originalDiscoveryTimeout := configuredRemoteWorkspaceDiscoveryTimeout
	t.Cleanup(func() {
		dialConfiguredProjectViewRemote = originalProjectViewsDial
		dialConfiguredRemote = originalRemoteDial
		configuredRemoteAttachTimeout = originalAttachTimeout
		configuredRemoteWorkspaceDiscoveryTimeout = originalDiscoveryTimeout
	})

	configuredRemoteAttachTimeout = 20 * time.Millisecond
	configuredRemoteWorkspaceDiscoveryTimeout = 120 * time.Millisecond
	projectViews := &configuredProjectViewRemoteStub{
		identity: protocol.ServerIdentity{Capabilities: protocol.CapabilityFlags{RunPrompt: true, AuthBootstrap: true}},
		resolveProjectPath: func(context.Context, serverapi.ProjectResolvePathRequest) (serverapi.ProjectResolvePathResponse, error) {
			return serverapi.ProjectResolvePathResponse{PathAvailability: clientui.ProjectAvailabilityMissing}, nil
		},
		listProjects: func(context.Context, serverapi.ProjectListRequest) (serverapi.ProjectListResponse, error) {
			return serverapi.ProjectListResponse{Projects: []clientui.ProjectSummary{{ProjectID: "project-1"}}}, nil
		},
		getProjectOverview: func(ctx context.Context, req serverapi.ProjectGetOverviewRequest) (serverapi.ProjectGetOverviewResponse, error) {
			time.Sleep(configuredRemoteAttachTimeout + 10*time.Millisecond)
			if err := ctx.Err(); err != nil {
				return serverapi.ProjectGetOverviewResponse{}, err
			}
			return serverapi.ProjectGetOverviewResponse{Overview: clientui.ProjectOverview{Workspaces: []clientui.ProjectWorkspaceSummary{{WorkspaceID: "workspace-1"}}}}, nil
		},
	}
	dialConfiguredProjectViewRemote = func(context.Context, string) (configuredProjectViewRemote, error) {
		return projectViews, nil
	}
	var dialRemaining time.Duration
	dialConfiguredRemote = func(ctx context.Context, rpcURL string, projectID string, workspaceID string) (*client.Remote, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		deadline, ok := ctx.Deadline()
		if !ok {
			t.Fatal("expected dial context deadline")
		}
		dialRemaining = time.Until(deadline)
		if projectID != "project-1" || workspaceID != "workspace-1" {
			t.Fatalf("unexpected workspace dial target: %s/%s", projectID, workspaceID)
		}
		return new(client.Remote), nil
	}

	remote, ok, err := tryDialConfiguredRunPromptRemote(context.Background(), Options{WorkspaceRoot: workspace, WorkspaceRootExplicit: true})
	if err != nil {
		t.Fatalf("tryDialConfiguredRunPromptRemote: %v", err)
	}
	if !ok {
		t.Fatal("expected configured remote attach to succeed")
	}
	if remote == nil {
		t.Fatal("expected remote client")
	}
	if !projectViews.closed.Load() {
		t.Fatal("expected project view remote to close after workspace selection")
	}
	if dialRemaining <= configuredRemoteAttachTimeout/2 {
		t.Fatalf("expected fresh attach timeout after workspace discovery, remaining=%v attach=%v", dialRemaining, configuredRemoteAttachTimeout)
	}
}

func TestTryDialConfiguredRunPromptRemoteSkipsServerWithoutAuthBootstrapCapability(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)

	originalProjectViewsDial := dialConfiguredProjectViewRemote
	t.Cleanup(func() { dialConfiguredProjectViewRemote = originalProjectViewsDial })

	projectViews := &configuredProjectViewRemoteStub{
		identity: protocol.ServerIdentity{Capabilities: protocol.CapabilityFlags{RunPrompt: true}},
	}
	dialConfiguredProjectViewRemote = func(context.Context, string) (configuredProjectViewRemote, error) {
		return projectViews, nil
	}

	remote, ok, err := tryDialConfiguredRunPromptRemote(context.Background(), Options{WorkspaceRoot: workspace, WorkspaceRootExplicit: true})
	if err != nil {
		t.Fatalf("tryDialConfiguredRunPromptRemote: %v", err)
	}
	if ok || remote != nil {
		t.Fatalf("expected configured remote without auth bootstrap to be skipped, got remote=%v ok=%t", remote, ok)
	}
	if !projectViews.closed.Load() {
		t.Fatal("expected incompatible project view remote to be closed")
	}
}

func TestRunPromptCreatesSessionAndPersistsDurableTranscript(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	registerAppWorkspace(t, workspace)
	t.Setenv("OPENAI_API_KEY", "test-key")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if testopenai.HandleInputTokenCount(w, r, 11) {
			return
		}
		if r.URL.Path != "/responses" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		if got := strings.TrimSpace(r.Header.Get("Authorization")); got == "" {
			t.Fatal("expected authorization header")
		}
		testopenai.WriteCompletedResponseStream(w, "hello from fake", 11, 7)
	}))
	defer server.Close()

	result, err := RunPrompt(context.Background(), Options{
		WorkspaceRoot:         workspace,
		WorkspaceRootExplicit: true,
		Model:                 "gpt-5",
		OpenAIBaseURL:         server.URL,
		OpenAIBaseURLExplicit: true,
	}, "hello from user", 0, nil)
	if err != nil {
		t.Fatalf("RunPrompt: %v", err)
	}
	if result.Result != "hello from fake" {
		t.Fatalf("result = %q, want %q", result.Result, "hello from fake")
	}
	if strings.TrimSpace(result.SessionID) == "" {
		t.Fatal("expected session id")
	}
	if !strings.HasSuffix(result.SessionName, " "+subagentSessionSuffix) {
		t.Fatalf("expected subagent session name, got %q", result.SessionName)
	}

	cfg, err := config.Load(workspace, config.LoadOptions{OpenAIBaseURL: server.URL})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	store := openAuthoritativeAppSession(t, cfg.PersistenceRoot, result.SessionID)
	meta := store.Meta()
	wantWorkspaceRoot, err := config.CanonicalWorkspaceRoot(cfg.WorkspaceRoot)
	if err != nil {
		t.Fatalf("CanonicalWorkspaceRoot: %v", err)
	}
	if meta.WorkspaceRoot != wantWorkspaceRoot {
		t.Fatalf("workspace root = %q, want %q", meta.WorkspaceRoot, wantWorkspaceRoot)
	}
	if meta.FirstPromptPreview != "hello from user" {
		t.Fatalf("first prompt preview = %q, want %q", meta.FirstPromptPreview, "hello from user")
	}
	if meta.Continuation == nil || meta.Continuation.OpenAIBaseURL != server.URL {
		t.Fatalf("unexpected continuation context: %+v", meta.Continuation)
	}

	events, err := store.ReadEvents()
	if err != nil {
		t.Fatalf("read events: %v", err)
	}
	var (
		sawUser      bool
		sawAssistant bool
	)
	for _, evt := range events {
		if evt.Kind != "message" {
			continue
		}
		var msg llm.Message
		if err := json.Unmarshal(evt.Payload, &msg); err != nil {
			t.Fatalf("unmarshal message payload: %v", err)
		}
		if msg.Role == llm.RoleUser && msg.Content == "hello from user" {
			sawUser = true
		}
		if msg.Role == llm.RoleAssistant && msg.Content == "hello from fake" && msg.Phase == llm.MessagePhaseFinal {
			sawAssistant = true
		}
	}
	if !sawUser {
		t.Fatal("expected persisted user message in event log")
	}
	if !sawAssistant {
		t.Fatal("expected persisted final assistant message in event log")
	}
}

func TestHeadlessRunPromptClientResumesExistingSessionByID(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	registerAppWorkspace(t, workspace)
	t.Setenv("OPENAI_API_KEY", "test-key")

	server, hits := newFakeResponsesServer(t, []string{"first response", "second response"})
	defer server.Close()

	created, err := RunPrompt(context.Background(), Options{
		WorkspaceRoot:         workspace,
		WorkspaceRootExplicit: true,
		Model:                 "gpt-5",
		OpenAIBaseURL:         server.URL,
		OpenAIBaseURLExplicit: true,
	}, "first prompt", 0, nil)
	if err != nil {
		t.Fatalf("initial RunPrompt: %v", err)
	}

	boot, err := startEmbeddedServer(context.Background(), Options{
		WorkspaceRoot:         workspace,
		WorkspaceRootExplicit: true,
		SessionID:             created.SessionID,
		Model:                 "gpt-5",
		OpenAIBaseURL:         server.URL,
		OpenAIBaseURLExplicit: true,
	}, newHeadlessAuthInteractor())
	if err != nil {
		t.Fatalf("bootstrap app: %v", err)
	}
	defer func() { _ = boot.Close() }()

	runClient := newHeadlessRunPromptClient(boot)
	resumed, err := runClient.RunPrompt(context.Background(), serverapi.RunPromptRequest{
		ClientRequestID:   "req-resume-1",
		SelectedSessionID: created.SessionID,
		Prompt:            "second prompt",
	}, nil)
	if err != nil {
		t.Fatalf("resumed client RunPrompt: %v", err)
	}
	if resumed.SessionID != created.SessionID {
		t.Fatalf("resumed session id = %q, want %q", resumed.SessionID, created.SessionID)
	}
	if resumed.Result != "second response" {
		t.Fatalf("resumed result = %q, want %q", resumed.Result, "second response")
	}
	if got := hits.Load(); got != 2 {
		t.Fatalf("fake response server hit count = %d, want 2", got)
	}

	store := openAuthoritativeWorkspaceSessionStore(t, workspace, server.URL, created.SessionID)
	messages, err := readStoredMessages(store)
	if err != nil {
		t.Fatalf("read stored messages: %v", err)
	}
	assertMessagePresent(t, messages, llm.RoleUser, "first prompt")
	assertMessagePresent(t, messages, llm.RoleAssistant, "first response")
	assertMessagePresent(t, messages, llm.RoleUser, "second prompt")
	assertMessagePresent(t, messages, llm.RoleAssistant, "second response")
	if got := store.Meta().FirstPromptPreview; got != "first prompt" {
		t.Fatalf("first prompt preview = %q, want %q", got, "first prompt")
	}
}

func TestHeadlessRunPromptClientRestoresContinuationContextFromSelectedSession(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	registerAppWorkspace(t, workspace)
	t.Setenv("OPENAI_API_KEY", "test-key")

	server, hits := newFakeResponsesServer(t, []string{"created via explicit base url", "resumed via continuation"})
	defer server.Close()

	created, err := RunPrompt(context.Background(), Options{
		WorkspaceRoot:         workspace,
		WorkspaceRootExplicit: true,
		Model:                 "gpt-5",
		OpenAIBaseURL:         server.URL,
		OpenAIBaseURLExplicit: true,
	}, "first prompt", 0, nil)
	if err != nil {
		t.Fatalf("initial RunPrompt: %v", err)
	}

	boot, err := startEmbeddedServer(context.Background(), Options{
		WorkspaceRoot:         workspace,
		WorkspaceRootExplicit: true,
		SessionID:             created.SessionID,
		Model:                 "gpt-5",
	}, newHeadlessAuthInteractor())
	if err != nil {
		t.Fatalf("bootstrap app: %v", err)
	}
	defer func() { _ = boot.Close() }()

	runClient := newHeadlessRunPromptClient(boot)
	resumed, err := runClient.RunPrompt(context.Background(), serverapi.RunPromptRequest{
		ClientRequestID:   "req-resume-2",
		SelectedSessionID: created.SessionID,
		Prompt:            "second prompt",
	}, nil)
	if err != nil {
		t.Fatalf("resumed client RunPrompt: %v", err)
	}
	if resumed.Result != "resumed via continuation" {
		t.Fatalf("resumed result = %q, want %q", resumed.Result, "resumed via continuation")
	}
	if got := hits.Load(); got != 2 {
		t.Fatalf("fake response server hit count = %d, want 2", got)
	}

	store := openAuthoritativeWorkspaceSessionStore(t, workspace, server.URL, created.SessionID)
	if store.Meta().Continuation == nil || store.Meta().Continuation.OpenAIBaseURL != server.URL {
		t.Fatalf("unexpected continuation context: %+v", store.Meta().Continuation)
	}
}

func newFakeResponsesServer(t *testing.T, assistantReplies []string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if testopenai.HandleInputTokenCount(w, r, 11) {
			return
		}
		if r.URL.Path != "/responses" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		if got := strings.TrimSpace(r.Header.Get("Authorization")); got == "" {
			t.Fatal("expected authorization header")
		}
		index := int(hits.Add(1)) - 1
		if index >= len(assistantReplies) {
			t.Fatalf("unexpected response request index %d", index)
		}
		testopenai.WriteCompletedResponseStream(w, assistantReplies[index], 11, 7)
	}))
	return server, &hits
}

func openAuthoritativeWorkspaceSessionStore(t *testing.T, workspaceRoot, openAIBaseURL, sessionID string) *session.Store {
	t.Helper()
	loadOpts := config.LoadOptions{}
	if strings.TrimSpace(openAIBaseURL) != "" {
		loadOpts.OpenAIBaseURL = openAIBaseURL
	}
	cfg, err := config.Load(workspaceRoot, loadOpts)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	return openAuthoritativeAppSession(t, cfg.PersistenceRoot, sessionID)
}

func readStoredMessages(store *session.Store) ([]llm.Message, error) {
	events, err := store.ReadEvents()
	if err != nil {
		return nil, err
	}
	messages := make([]llm.Message, 0, len(events))
	for _, evt := range events {
		if evt.Kind != "message" {
			continue
		}
		var msg llm.Message
		if err := json.Unmarshal(evt.Payload, &msg); err != nil {
			return nil, err
		}
		messages = append(messages, msg)
	}
	return messages, nil
}

func assertMessagePresent(t *testing.T, messages []llm.Message, role llm.Role, content string) {
	t.Helper()
	for _, msg := range messages {
		if msg.Role == role && msg.Content == content {
			return
		}
	}
	t.Fatalf("expected message role=%s content=%q in %+v", role, content, messages)
}

package app

import (
	"context"
	"errors"
	"io"
	"net/http/httptest"
	"os"
	"os/signal"
	goruntime "runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"builder/server/auth"
	"builder/server/serve"
	serverstartup "builder/server/startup"
	"builder/server/tools"
	askquestion "builder/server/tools/askquestion"
	shelltool "builder/server/tools/shell"
	"builder/shared/client"
	"builder/shared/clientui"
	"builder/shared/config"
	"builder/shared/discovery"
	"builder/shared/protocol"
	"builder/shared/serverapi"
	"github.com/google/uuid"
	"golang.org/x/net/websocket"
)

func TestStartSessionServerHelperDaemonProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_DAEMON") != "1" {
		return
	}
	workspace := strings.TrimSpace(os.Getenv("GO_HELPER_WORKSPACE_ROOT"))
	if workspace == "" {
		t.Fatal("GO_HELPER_WORKSPACE_ROOT is required")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	srv, err := serve.Start(context.Background(), serverstartup.Request{
		WorkspaceRoot:         workspace,
		WorkspaceRootExplicit: true,
		Model:                 "gpt-5",
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
	if err := srv.Serve(ctx); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("Serve: %v", err)
	}
}

func TestStartSessionServerUsesDiscoveredDaemonForInteractiveFlow(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)

	fakeResponses, hits := newFakeResponsesServer(t, []string{"interactive daemon reply"})
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

	loadCfg, err := config.Load(workspace, config.LoadOptions{})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	_, containerDir, err := config.ResolveWorkspaceContainer(loadCfg)
	if err != nil {
		t.Fatalf("ResolveWorkspaceContainer: %v", err)
	}
	discoveryPath, err := discovery.PathForContainer(containerDir)
	if err != nil {
		t.Fatalf("PathForContainer: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := discovery.Read(discoveryPath); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("discovery record did not appear at %s", discoveryPath)
		}
		time.Sleep(10 * time.Millisecond)
	}

	server, err := startSessionServer(context.Background(), Options{WorkspaceRoot: workspace, WorkspaceRootExplicit: true}, newHeadlessAuthInteractor())
	if err != nil {
		t.Fatalf("startSessionServer: %v", err)
	}
	defer func() { _ = server.Close() }()
	if _, ok := server.(*remoteAppServer); !ok {
		t.Fatalf("expected remote app server, got %T", server)
	}

	planner := newSessionLaunchPlanner(server)
	plan, err := planner.PlanSession(context.Background(), sessionLaunchRequest{Mode: launchModeInteractive, ForceNewSession: true})
	if err != nil {
		t.Fatalf("PlanSession: %v", err)
	}
	runtimePlan, err := planner.PrepareRuntime(context.Background(), plan, io.Discard, "test remote interactive runtime")
	if err != nil {
		t.Fatalf("PrepareRuntime: %v", err)
	}
	defer runtimePlan.Close()

	message, err := runtimePlan.Wiring.runtimeClient.SubmitUserMessage(context.Background(), "hello through interactive daemon")
	if err != nil {
		t.Fatalf("SubmitUserMessage: %v", err)
	}
	if message != "interactive daemon reply" {
		t.Fatalf("assistant message = %q, want %q", message, "interactive daemon reply")
	}
	if hits.Load() != 1 {
		t.Fatalf("expected daemon-backed llm call once, got %d", hits.Load())
	}

	refreshed, err := server.SessionViewClient().GetSessionMainView(context.Background(), serverapi.SessionMainViewRequest{SessionID: plan.SessionID})
	if err != nil {
		t.Fatalf("GetSessionMainView: %v", err)
	}
	if refreshed.MainView.Session.Transcript.CommittedEntryCount == 0 {
		t.Fatalf("expected refreshed transcript metadata, got %+v", refreshed.MainView.Session.Transcript)
	}

	cancel()
	if serveErr := <-errCh; !errors.Is(serveErr, context.Canceled) {
		t.Fatalf("Serve error = %v, want context canceled", serveErr)
	}
}

func TestShouldBypassRemoteStartupForInteractiveOnboardingOnFirstRun(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)

	bypass, err := shouldBypassRemoteStartupForInteractiveOnboarding(Options{WorkspaceRoot: workspace, WorkspaceRootExplicit: true}, &stubAuthInteractor{})
	if err != nil {
		t.Fatalf("shouldBypassRemoteStartupForInteractiveOnboarding: %v", err)
	}
	if !bypass {
		t.Fatal("expected first-run interactive startup to bypass remote onboarding paths")
	}
}

func TestShouldBypassRemoteStartupForInteractiveOnboardingSkipsWhenConfigExists(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	if _, _, err := config.WriteDefaultSettingsFile(); err != nil {
		t.Fatalf("WriteDefaultSettingsFile: %v", err)
	}

	bypass, err := shouldBypassRemoteStartupForInteractiveOnboarding(Options{WorkspaceRoot: workspace, WorkspaceRootExplicit: true}, &stubAuthInteractor{})
	if err != nil {
		t.Fatalf("shouldBypassRemoteStartupForInteractiveOnboarding: %v", err)
	}
	if bypass {
		t.Fatal("expected configured interactive startup to keep remote onboarding paths enabled")
	}
}

func TestStartSessionServerRejectsIncompatibleDiscoveredDaemonAndFallsBack(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)

	fakeResponses, hits := newFakeResponsesServer(t, []string{"embedded fallback reply"})
	defer fakeResponses.Close()

	cleanup := publishDiscoveredRemoteForWorkspace(t, workspace, protocol.CapabilityFlags{
		JSONRPCWebSocket: true,
		ProjectAttach:    true,
		SessionAttach:    true,
		RunPrompt:        true,
		SessionActivity:  true,
		ProcessOutput:    true,
	})
	defer cleanup()

	server, err := startSessionServer(context.Background(), Options{
		WorkspaceRoot:         workspace,
		WorkspaceRootExplicit: true,
		Model:                 "gpt-5",
		OpenAIBaseURL:         fakeResponses.URL,
		OpenAIBaseURLExplicit: true,
	}, newHeadlessAuthInteractorWithEnvKey("test-key"))
	if err != nil {
		t.Fatalf("startSessionServer: %v", err)
	}
	defer func() { _ = server.Close() }()
	if _, ok := server.(*remoteAppServer); ok {
		t.Fatal("expected incompatible discovered daemon to be rejected")
	}

	planner := newSessionLaunchPlanner(server)
	plan, err := planner.PlanSession(context.Background(), sessionLaunchRequest{Mode: launchModeInteractive, ForceNewSession: true})
	if err != nil {
		t.Fatalf("PlanSession: %v", err)
	}
	runtimePlan, err := planner.PrepareRuntime(context.Background(), plan, io.Discard, "test embedded fallback runtime")
	if err != nil {
		t.Fatalf("PrepareRuntime: %v", err)
	}
	defer runtimePlan.Close()

	message, err := runtimePlan.Wiring.runtimeClient.SubmitUserMessage(context.Background(), "hello through embedded fallback")
	if err != nil {
		t.Fatalf("SubmitUserMessage: %v", err)
	}
	if message != "embedded fallback reply" {
		t.Fatalf("assistant message = %q, want %q", message, "embedded fallback reply")
	}
	if hits.Load() != 1 {
		t.Fatalf("expected embedded fallback llm call once, got %d", hits.Load())
	}
}

func TestStartSessionServerRejectsDiscoveredDaemonWithoutProcessOutputCapability(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)

	fakeResponses, hits := newFakeResponsesServer(t, []string{"embedded fallback reply"})
	defer fakeResponses.Close()

	cleanup := publishDiscoveredRemoteForWorkspace(t, workspace, protocol.CapabilityFlags{
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

	server, err := startSessionServer(context.Background(), Options{
		WorkspaceRoot:         workspace,
		WorkspaceRootExplicit: true,
		Model:                 "gpt-5",
		OpenAIBaseURL:         fakeResponses.URL,
		OpenAIBaseURLExplicit: true,
	}, newHeadlessAuthInteractorWithEnvKey("test-key"))
	if err != nil {
		t.Fatalf("startSessionServer: %v", err)
	}
	defer func() { _ = server.Close() }()
	if _, ok := server.(*remoteAppServer); ok {
		t.Fatal("expected discovered daemon without process capability to be rejected")
	}

	planner := newSessionLaunchPlanner(server)
	plan, err := planner.PlanSession(context.Background(), sessionLaunchRequest{Mode: launchModeInteractive, ForceNewSession: true})
	if err != nil {
		t.Fatalf("PlanSession: %v", err)
	}
	runtimePlan, err := planner.PrepareRuntime(context.Background(), plan, io.Discard, "test embedded fallback runtime")
	if err != nil {
		t.Fatalf("PrepareRuntime: %v", err)
	}
	defer runtimePlan.Close()

	message, err := runtimePlan.Wiring.runtimeClient.SubmitUserMessage(context.Background(), "hello after capability fallback")
	if err != nil {
		t.Fatalf("SubmitUserMessage: %v", err)
	}
	if message != "embedded fallback reply" {
		t.Fatalf("assistant message = %q, want %q", message, "embedded fallback reply")
	}
	if hits.Load() != 1 {
		t.Fatalf("expected embedded fallback llm call once, got %d", hits.Load())
	}
}

func TestStartSessionServerRejectsDiscoveredDaemonWithoutTranscriptPagingCapability(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)

	fakeResponses, hits := newFakeResponsesServer(t, []string{"embedded fallback reply"})
	defer fakeResponses.Close()

	cleanup := publishDiscoveredRemoteForWorkspace(t, workspace, protocol.CapabilityFlags{
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
		ProcessOutput:    true,
	})
	defer cleanup()

	server, err := startSessionServer(context.Background(), Options{
		WorkspaceRoot:         workspace,
		WorkspaceRootExplicit: true,
		Model:                 "gpt-5",
		OpenAIBaseURL:         fakeResponses.URL,
		OpenAIBaseURLExplicit: true,
	}, newHeadlessAuthInteractorWithEnvKey("test-key"))
	if err != nil {
		t.Fatalf("startSessionServer: %v", err)
	}
	defer func() { _ = server.Close() }()
	if _, ok := server.(*remoteAppServer); ok {
		t.Fatal("expected discovered daemon without transcript paging capability to be rejected")
	}

	planner := newSessionLaunchPlanner(server)
	plan, err := planner.PlanSession(context.Background(), sessionLaunchRequest{Mode: launchModeInteractive, ForceNewSession: true})
	if err != nil {
		t.Fatalf("PlanSession: %v", err)
	}
	runtimePlan, err := planner.PrepareRuntime(context.Background(), plan, io.Discard, "test embedded fallback runtime")
	if err != nil {
		t.Fatalf("PrepareRuntime: %v", err)
	}
	defer runtimePlan.Close()

	message, err := runtimePlan.Wiring.runtimeClient.SubmitUserMessage(context.Background(), "hello after transcript paging fallback")
	if err != nil {
		t.Fatalf("SubmitUserMessage: %v", err)
	}
	if message != "embedded fallback reply" {
		t.Fatalf("assistant message = %q, want %q", message, "embedded fallback reply")
	}
	if hits.Load() != 1 {
		t.Fatalf("expected embedded fallback llm call once, got %d", hits.Load())
	}
}

func TestRemoteSessionStatusUsesLocalOAuthAuthState(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)

	srv, err := serve.Start(context.Background(), serverstartup.Request{
		WorkspaceRoot:         workspace,
		WorkspaceRootExplicit: true,
		Model:                 "gpt-5",
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
	defer func() {
		cancel()
		if serveErr := <-errCh; !errors.Is(serveErr, context.Canceled) {
			t.Fatalf("Serve error = %v, want context canceled", serveErr)
		}
	}()
	waitForDiscoveryRecord(t, workspace)

	loadCfg, err := config.Load(workspace, config.LoadOptions{})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	store := auth.NewFileStore(config.GlobalAuthConfigPath(loadCfg))
	if err := store.Save(context.Background(), auth.State{
		Scope: auth.ScopeGlobal,
		Method: auth.Method{
			Type: auth.MethodOAuth,
			OAuth: &auth.OAuthMethod{
				AccessToken: "access-token",
				AccountID:   "acct-123",
				Email:       "user@example.com",
			},
		},
		UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("save auth state: %v", err)
	}

	originalFetcher := statusUsagePayloadFetcher
	defer func() { statusUsagePayloadFetcher = originalFetcher }()
	statusUsagePayloadFetcher = func(_ context.Context, baseURL string, state auth.State) (statusUsagePayload, error) {
		if baseURL != statusUsageBaseURL {
			t.Fatalf("base URL = %q", baseURL)
		}
		if state.Method.OAuth == nil || state.Method.OAuth.Email != "user@example.com" || state.Method.OAuth.AccountID != "acct-123" {
			t.Fatalf("unexpected auth state: %+v", state.Method.OAuth)
		}
		return statusUsagePayload{PlanType: "pro"}, nil
	}

	server, err := startSessionServer(context.Background(), Options{WorkspaceRoot: workspace, WorkspaceRootExplicit: true}, newHeadlessAuthInteractor())
	if err != nil {
		t.Fatalf("startSessionServer: %v", err)
	}
	defer func() { _ = server.Close() }()
	if _, ok := server.(*remoteAppServer); !ok {
		t.Fatalf("expected remote app server, got %T", server)
	}

	planner := newSessionLaunchPlanner(server)
	plan, err := planner.PlanSession(context.Background(), sessionLaunchRequest{Mode: launchModeInteractive, ForceNewSession: true})
	if err != nil {
		t.Fatalf("PlanSession: %v", err)
	}
	if plan.StatusConfig.AuthManager == nil {
		t.Fatal("expected status auth manager for remote session")
	}

	collector := defaultUIStatusCollector{}
	snapshot, err := collector.Collect(context.Background(), uiStatusRequest{
		WorkspaceRoot:   plan.StatusConfig.WorkspaceRoot,
		PersistenceRoot: plan.StatusConfig.PersistenceRoot,
		Settings:        plan.StatusConfig.Settings,
		Source:          plan.StatusConfig.Source,
		AuthManager:     plan.StatusConfig.AuthManager,
		AuthStatePath:   plan.StatusConfig.AuthStatePath,
		OwnsServer:      plan.StatusConfig.OwnsServer,
	})
	if err != nil {
		t.Fatalf("collect status: %v", err)
	}
	if got := snapshot.Auth.Summary; got != "user@example.com" {
		t.Fatalf("auth summary = %q", got)
	}
	if got := snapshot.Subscription.Summary; got != "Pro subscription" {
		t.Fatalf("subscription summary = %q", got)
	}
}

func TestStartSessionServerOwnsLaunchedDaemonCloser(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)

	called := false
	originalLaunch := launchSessionServerDaemon
	t.Cleanup(func() { launchSessionServerDaemon = originalLaunch })
	launchSessionServerDaemon = func(context.Context, Options) (*client.Remote, func() error, bool, error) {
		return &client.Remote{}, func() error {
			called = true
			return nil
		}, true, nil
	}

	server, err := startSessionServer(context.Background(), Options{WorkspaceRoot: workspace, WorkspaceRootExplicit: true}, newHeadlessAuthInteractor())
	if err != nil {
		t.Fatalf("startSessionServer: %v", err)
	}
	if _, ok := server.(*remoteAppServer); !ok {
		t.Fatalf("expected remote app server, got %T", server)
	}
	if err := server.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !called {
		t.Fatal("expected launched daemon closer to be invoked")
	}
}

func TestStartSessionServerLaunchedDaemonCloseStopsProcess(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("helper daemon process signal probe is unix-only")
	}
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GO_WANT_HELPER_DAEMON", "1")
	t.Setenv("GO_HELPER_WORKSPACE_ROOT", workspace)

	originalExecPath := resolveDaemonExecutablePath
	originalServeArgs := buildServeArgsFunc
	t.Cleanup(func() {
		resolveDaemonExecutablePath = originalExecPath
		buildServeArgsFunc = originalServeArgs
	})
	resolveDaemonExecutablePath = func() (string, bool) {
		path, err := os.Executable()
		if err != nil {
			t.Fatalf("os.Executable: %v", err)
		}
		return path, true
	}
	buildServeArgsFunc = func(string, Options) []string {
		return []string{"-test.run=^TestStartSessionServerHelperDaemonProcess$"}
	}

	server, err := startSessionServer(context.Background(), Options{WorkspaceRoot: workspace, WorkspaceRootExplicit: true}, newHeadlessAuthInteractor())
	if err != nil {
		t.Fatalf("startSessionServer: %v", err)
	}
	remote, ok := server.(*remoteAppServer)
	if !ok {
		t.Fatalf("expected remote app server, got %T", server)
	}
	discoveryPath := discoveryPathForWorkspace(t, workspace)
	record := waitForDiscoveryRecordAtPath(t, discoveryPath)
	if remote.identity.PID == 0 {
		t.Fatal("expected launched daemon pid")
	}
	if record.Identity.PID != remote.identity.PID {
		t.Fatalf("discovery pid = %d, remote pid = %d", record.Identity.PID, remote.identity.PID)
	}

	if err := server.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	waitForDiscoveryRemoval(t, discoveryPath)
	waitForPIDExit(t, remote.identity.PID)
}

func TestStartSessionServerUsesInvocationOverridesWhenAttachingToDiscoveredDaemon(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)

	defaultResponses, defaultHits := newFakeResponsesServer(t, []string{"interactive daemon default"})
	defer defaultResponses.Close()
	overrideResponses, overrideHits := newFakeResponsesServer(t, []string{"interactive daemon override"})
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
	waitForDiscoveryRecord(t, workspace)

	server, err := startSessionServer(context.Background(), Options{
		WorkspaceRoot:         workspace,
		WorkspaceRootExplicit: true,
		Model:                 "gpt-5",
		OpenAIBaseURL:         overrideResponses.URL,
		OpenAIBaseURLExplicit: true,
	}, newHeadlessAuthInteractor())
	if err != nil {
		t.Fatalf("startSessionServer: %v", err)
	}
	defer func() { _ = server.Close() }()

	planner := newSessionLaunchPlanner(server)
	plan, err := planner.PlanSession(context.Background(), sessionLaunchRequest{Mode: launchModeInteractive, ForceNewSession: true})
	if err != nil {
		t.Fatalf("PlanSession: %v", err)
	}
	runtimePlan, err := planner.PrepareRuntime(context.Background(), plan, io.Discard, "test remote interactive runtime override")
	if err != nil {
		t.Fatalf("PrepareRuntime: %v", err)
	}
	defer runtimePlan.Close()

	message, err := runtimePlan.Wiring.runtimeClient.SubmitUserMessage(context.Background(), "hello through interactive override")
	if err != nil {
		t.Fatalf("SubmitUserMessage: %v", err)
	}
	if message != "interactive daemon override" {
		t.Fatalf("assistant message = %q, want %q", message, "interactive daemon override")
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

func TestStartSessionServerPreservesExplicitCLIToolsWithCLIModelOverride(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)

	srv, err := serve.Start(context.Background(), serverstartup.Request{
		WorkspaceRoot:         workspace,
		WorkspaceRootExplicit: true,
		Model:                 "gpt-5.4",
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
	waitForDiscoveryRecord(t, workspace)

	server, err := startSessionServer(context.Background(), Options{
		WorkspaceRoot:         workspace,
		WorkspaceRootExplicit: true,
		Model:                 "gpt-5.3-codex",
		Tools:                 "shell",
	}, newHeadlessAuthInteractor())
	if err != nil {
		t.Fatalf("startSessionServer: %v", err)
	}
	defer func() { _ = server.Close() }()

	planner := newSessionLaunchPlanner(server)
	plan, err := planner.PlanSession(context.Background(), sessionLaunchRequest{Mode: launchModeInteractive, ForceNewSession: true})
	if err != nil {
		t.Fatalf("PlanSession: %v", err)
	}
	if plan.ActiveSettings.Model != "gpt-5.3-codex" {
		t.Fatalf("model = %q, want gpt-5.3-codex", plan.ActiveSettings.Model)
	}
	if len(plan.EnabledTools) != 1 || plan.EnabledTools[0] != tools.ToolShell {
		t.Fatalf("enabled tools = %+v, want only shell", plan.EnabledTools)
	}

	cancel()
	if serveErr := <-errCh; !errors.Is(serveErr, context.Canceled) {
		t.Fatalf("Serve error = %v, want context canceled", serveErr)
	}
}

func TestStartSessionServerUsesDiscoveredDaemonForPromptRoundTrip(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)

	srv, err := serve.Start(context.Background(), serverstartup.Request{
		WorkspaceRoot:         workspace,
		WorkspaceRootExplicit: true,
		Model:                 "gpt-5",
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
	waitForDiscoveryRecord(t, workspace)

	server, err := startSessionServer(context.Background(), Options{WorkspaceRoot: workspace, WorkspaceRootExplicit: true}, newHeadlessAuthInteractor())
	if err != nil {
		t.Fatalf("startSessionServer: %v", err)
	}
	defer func() { _ = server.Close() }()

	planner := newSessionLaunchPlanner(server)
	plan, err := planner.PlanSession(context.Background(), sessionLaunchRequest{Mode: launchModeInteractive, ForceNewSession: true})
	if err != nil {
		t.Fatalf("PlanSession: %v", err)
	}
	runtimePlan, err := planner.PrepareRuntime(context.Background(), plan, io.Discard, "test remote prompt round trip")
	if err != nil {
		t.Fatalf("PrepareRuntime: %v", err)
	}
	defer runtimePlan.Close()

	askDone := make(chan struct {
		resp askquestion.Response
		err  error
	}, 1)
	go func() {
		resp, err := srv.AwaitPromptResponse(context.Background(), plan.SessionID, askquestion.Request{
			ID:                     "ask-1",
			Question:               "Pick one",
			Suggestions:            []string{"one", "two"},
			RecommendedOptionIndex: 2,
		})
		askDone <- struct {
			resp askquestion.Response
			err  error
		}{resp: resp, err: err}
	}()
	waitForPendingAskResources(t, server.AskViewClient(), plan.SessionID, 1)
	askEvt := waitForRemoteAskEvent(t, runtimePlan.Wiring.askEvents)
	if askEvt.req.ID != "ask-1" || askEvt.req.Question != "Pick one" {
		t.Fatalf("unexpected ask event: %+v", askEvt.req)
	}
	askEvt.reply <- askReply{response: askquestion.Response{RequestID: askEvt.req.ID, SelectedOptionNumber: 2}}
	select {
	case result := <-askDone:
		if result.err != nil {
			t.Fatalf("AwaitPromptResponse ask: %v", result.err)
		}
		if result.resp.RequestID != "ask-1" || result.resp.SelectedOptionNumber != 2 {
			t.Fatalf("unexpected ask response: %+v", result.resp)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for ask response")
	}
	waitForPendingAskResources(t, server.AskViewClient(), plan.SessionID, 0)

	approvalDone := make(chan struct {
		resp askquestion.Response
		err  error
	}, 1)
	go func() {
		resp, err := srv.AwaitPromptResponse(context.Background(), plan.SessionID, askquestion.Request{
			ID:              "approval-1",
			Question:        "Approve it?",
			Approval:        true,
			ApprovalOptions: []askquestion.ApprovalOption{{Decision: askquestion.ApprovalDecisionAllowOnce, Label: "Allow once"}, {Decision: askquestion.ApprovalDecisionDeny, Label: "Deny"}},
		})
		approvalDone <- struct {
			resp askquestion.Response
			err  error
		}{resp: resp, err: err}
	}()
	waitForPendingApprovalResources(t, server.ApprovalViewClient(), plan.SessionID, 1)
	approvalEvt := waitForRemoteAskEvent(t, runtimePlan.Wiring.askEvents)
	if !approvalEvt.req.Approval || approvalEvt.req.ID != "approval-1" {
		t.Fatalf("unexpected approval event: %+v", approvalEvt.req)
	}
	approvalEvt.reply <- askReply{response: askquestion.Response{RequestID: approvalEvt.req.ID, Approval: &askquestion.ApprovalPayload{Decision: askquestion.ApprovalDecisionAllowOnce, Commentary: "trusted"}}}
	select {
	case result := <-approvalDone:
		if result.err != nil {
			t.Fatalf("AwaitPromptResponse approval: %v", result.err)
		}
		if result.resp.RequestID != "approval-1" || result.resp.Approval == nil || result.resp.Approval.Decision != askquestion.ApprovalDecisionAllowOnce || result.resp.Approval.Commentary != "trusted" {
			t.Fatalf("unexpected approval response: %+v", result.resp)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for approval response")
	}
	waitForPendingApprovalResources(t, server.ApprovalViewClient(), plan.SessionID, 0)

	cancel()
	if serveErr := <-errCh; !errors.Is(serveErr, context.Canceled) {
		t.Fatalf("Serve error = %v, want context canceled", serveErr)
	}
}

func TestStartSessionServerUsesDiscoveredDaemonForSessionLifecycleDraftPersistence(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)

	srv, err := serve.Start(context.Background(), serverstartup.Request{
		WorkspaceRoot:         workspace,
		WorkspaceRootExplicit: true,
		Model:                 "gpt-5",
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
	waitForDiscoveryRecord(t, workspace)

	server, err := startSessionServer(context.Background(), Options{WorkspaceRoot: workspace, WorkspaceRootExplicit: true}, newHeadlessAuthInteractor())
	if err != nil {
		t.Fatalf("startSessionServer: %v", err)
	}
	defer func() { _ = server.Close() }()

	planner := newSessionLaunchPlanner(server)
	plan, err := planner.PlanSession(context.Background(), sessionLaunchRequest{Mode: launchModeInteractive, ForceNewSession: true})
	if err != nil {
		t.Fatalf("PlanSession: %v", err)
	}
	if _, err := server.SessionLifecycleClient().PersistInputDraft(context.Background(), serverapi.SessionPersistInputDraftRequest{SessionID: plan.SessionID, Input: "saved draft"}); err != nil {
		t.Fatalf("PersistInputDraft: %v", err)
	}
	if got := sessionLaunchInitialInputFromServer(context.Background(), server, plan.SessionID, "transition draft"); got != "saved draft" {
		t.Fatalf("sessionLaunchInitialInputFromServer = %q, want saved draft", got)
	}
	resolved, err := server.SessionLifecycleClient().ResolveTransition(context.Background(), serverapi.SessionResolveTransitionRequest{
		SessionID: plan.SessionID,
		Transition: serverapi.SessionTransition{
			Action:          "open_session",
			TargetSessionID: plan.SessionID,
			InitialInput:    "transition draft",
		},
	})
	if err != nil {
		t.Fatalf("ResolveTransition: %v", err)
	}
	if !resolved.ShouldContinue || resolved.NextSessionID != plan.SessionID || resolved.InitialInput != "transition draft" {
		t.Fatalf("unexpected resolved transition: %+v", resolved)
	}

	cancel()
	if serveErr := <-errCh; !errors.Is(serveErr, context.Canceled) {
		t.Fatalf("Serve error = %v, want context canceled", serveErr)
	}
}

func TestStartSessionServerListsPendingPromptSnapshotOverRemoteReads(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)

	srv, err := serve.Start(context.Background(), serverstartup.Request{
		WorkspaceRoot:         workspace,
		WorkspaceRootExplicit: true,
		Model:                 "gpt-5",
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
	waitForDiscoveryRecord(t, workspace)

	server, err := startSessionServer(context.Background(), Options{WorkspaceRoot: workspace, WorkspaceRootExplicit: true}, newHeadlessAuthInteractor())
	if err != nil {
		t.Fatalf("startSessionServer: %v", err)
	}
	defer func() { _ = server.Close() }()
	if _, ok := server.(*remoteAppServer); !ok {
		t.Fatalf("expected remote app server, got %T", server)
	}

	planner := newSessionLaunchPlanner(server)
	plan, err := planner.PlanSession(context.Background(), sessionLaunchRequest{Mode: launchModeInteractive, ForceNewSession: true})
	if err != nil {
		t.Fatalf("PlanSession: %v", err)
	}
	runtimePlan, err := planner.PrepareRuntime(context.Background(), plan, io.Discard, "test remote prompt snapshot reads")
	if err != nil {
		t.Fatalf("PrepareRuntime: %v", err)
	}
	defer runtimePlan.Close()

	askDone := make(chan error, 1)
	go func() {
		_, err := srv.AwaitPromptResponse(context.Background(), plan.SessionID, askquestion.Request{ID: "ask-remote-1", Question: "Ask?"})
		askDone <- err
	}()
	approvalDone := make(chan error, 1)
	go func() {
		_, err := srv.AwaitPromptResponse(context.Background(), plan.SessionID, askquestion.Request{
			ID:              "approval-remote-1",
			Question:        "Approve?",
			Approval:        true,
			ApprovalOptions: []askquestion.ApprovalOption{{Decision: askquestion.ApprovalDecisionAllowOnce, Label: "Allow once"}},
		})
		approvalDone <- err
	}()

	waitForPendingAskResources(t, server.AskViewClient(), plan.SessionID, 1)
	waitForPendingApprovalResources(t, server.ApprovalViewClient(), plan.SessionID, 1)
	ids, err := listPendingPromptIDs(context.Background(), plan.SessionID, server.AskViewClient(), server.ApprovalViewClient())
	if err != nil {
		t.Fatalf("listPendingPromptIDs: %v", err)
	}
	if _, ok := ids["ask-remote-1"]; !ok {
		t.Fatalf("pending prompt snapshot missing ask id: %+v", ids)
	}
	if _, ok := ids["approval-remote-1"]; !ok {
		t.Fatalf("pending prompt snapshot missing approval id: %+v", ids)
	}

	first := waitForRemoteAskEvent(t, runtimePlan.Wiring.askEvents)
	second := waitForRemoteAskEvent(t, runtimePlan.Wiring.askEvents)
	for _, evt := range []askEvent{first, second} {
		switch evt.req.ID {
		case "ask-remote-1":
			evt.reply <- askReply{response: askquestion.Response{RequestID: evt.req.ID, Answer: "done"}}
		case "approval-remote-1":
			evt.reply <- askReply{response: askquestion.Response{RequestID: evt.req.ID, Approval: &askquestion.ApprovalPayload{Decision: askquestion.ApprovalDecisionAllowOnce}}}
		default:
			t.Fatalf("unexpected prompt event id %q", evt.req.ID)
		}
	}

	select {
	case err := <-askDone:
		if err != nil {
			t.Fatalf("AwaitPromptResponse ask: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for remote ask response")
	}
	select {
	case err := <-approvalDone:
		if err != nil {
			t.Fatalf("AwaitPromptResponse approval: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for remote approval response")
	}

	ids, err = listPendingPromptIDs(context.Background(), plan.SessionID, server.AskViewClient(), server.ApprovalViewClient())
	if err != nil {
		t.Fatalf("listPendingPromptIDs after resolution: %v", err)
	}
	if len(ids) != 0 {
		t.Fatalf("expected no pending prompt ids after resolution, got %+v", ids)
	}

	cancel()
	if serveErr := <-errCh; !errors.Is(serveErr, context.Canceled) {
		t.Fatalf("Serve error = %v, want context canceled", serveErr)
	}
}

func TestStartSessionServerUsesDiscoveredDaemonForProcessFlows(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)

	srv, err := serve.Start(context.Background(), serverstartup.Request{
		WorkspaceRoot:         workspace,
		WorkspaceRootExplicit: true,
		Model:                 "gpt-5",
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
	srv.Background().SetMinimumExecToBgTime(time.Millisecond)

	serveCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Serve(serveCtx)
	}()
	waitForDiscoveryRecord(t, workspace)

	server, err := startSessionServer(context.Background(), Options{WorkspaceRoot: workspace, WorkspaceRootExplicit: true}, newHeadlessAuthInteractor())
	if err != nil {
		t.Fatalf("startSessionServer: %v", err)
	}
	defer func() { _ = server.Close() }()

	planner := newSessionLaunchPlanner(server)
	plan, err := planner.PlanSession(context.Background(), sessionLaunchRequest{Mode: launchModeInteractive, ForceNewSession: true})
	if err != nil {
		t.Fatalf("PlanSession: %v", err)
	}

	result, err := srv.Background().Start(context.Background(), shelltool.ExecRequest{
		Command:        []string{"/bin/sh", "-lc", "printf 'daemon process output\n'; sleep 5"},
		DisplayCommand: "printf 'daemon process output'; sleep 5",
		Workdir:        workspace,
		YieldTime:      time.Millisecond,
		OwnerSessionID: plan.SessionID,
	})
	if err != nil {
		t.Fatalf("Background().Start: %v", err)
	}
	if !result.Backgrounded {
		t.Fatal("expected backgrounded process")
	}

	proc := waitForRemoteProcess(t, server.ProcessViewClient(), plan.SessionID, result.SessionID)
	if proc.OwnerSessionID != plan.SessionID {
		t.Fatalf("unexpected process owner: %+v", proc)
	}

	getResp, err := server.ProcessViewClient().GetProcess(context.Background(), serverapi.ProcessGetRequest{ProcessID: result.SessionID})
	if err != nil {
		t.Fatalf("GetProcess: %v", err)
	}
	if getResp.Process == nil || getResp.Process.ID != result.SessionID {
		t.Fatalf("unexpected get process response: %+v", getResp.Process)
	}

	outputSub, err := server.ProcessOutputClient().SubscribeProcessOutput(context.Background(), serverapi.ProcessOutputSubscribeRequest{ProcessID: result.SessionID, OffsetBytes: 0})
	if err != nil {
		t.Fatalf("SubscribeProcessOutput: %v", err)
	}
	defer func() { _ = outputSub.Close() }()
	chunk, err := outputSub.Next(context.Background())
	if err != nil {
		t.Fatalf("ProcessOutput Next: %v", err)
	}
	if !strings.Contains(chunk.Text, "daemon process output") {
		t.Fatalf("unexpected process output chunk: %+v", chunk)
	}
	inlineResp := waitForRemoteInlineOutput(t, server.ProcessControlClient(), result.SessionID)
	if !strings.Contains(inlineResp.Output, "daemon process output") {
		t.Fatalf("unexpected inline output: %q", inlineResp.Output)
	}

	if _, err := server.ProcessControlClient().KillProcess(context.Background(), serverapi.ProcessKillRequest{ClientRequestID: uuid.NewString(), ProcessID: result.SessionID}); err != nil {
		t.Fatalf("KillProcess: %v", err)
	}
	waitForRemoteProcessExit(t, server.ProcessViewClient(), result.SessionID)

	cancel()
	if serveErr := <-errCh; !errors.Is(serveErr, context.Canceled) {
		t.Fatalf("Serve error = %v, want context canceled", serveErr)
	}
}

func TestInteractiveSessionServerWorkflowParity(t *testing.T) {
	t.Run("embedded", func(t *testing.T) {
		home := t.TempDir()
		workspace := t.TempDir()
		t.Setenv("HOME", home)
		fakeResponses, _ := newFakeResponsesServer(t, []string{"parity reply"})
		defer fakeResponses.Close()
		server, err := startEmbeddedServer(context.Background(), Options{
			WorkspaceRoot:         workspace,
			WorkspaceRootExplicit: true,
			Model:                 "gpt-5",
			OpenAIBaseURL:         fakeResponses.URL,
			OpenAIBaseURLExplicit: true,
		}, newHeadlessAuthInteractorWithEnvKey("test-key"))
		if err != nil {
			t.Fatalf("startEmbeddedServer: %v", err)
		}
		defer func() { _ = server.Close() }()
		runInteractiveWorkflowScenario(t, server, "parity reply")
	})

	t.Run("daemon", func(t *testing.T) {
		home := t.TempDir()
		workspace := t.TempDir()
		t.Setenv("HOME", home)
		fakeResponses, _ := newFakeResponsesServer(t, []string{"parity reply"})
		defer fakeResponses.Close()

		srv, err := serve.Start(context.Background(), serverstartup.Request{
			WorkspaceRoot:         workspace,
			WorkspaceRootExplicit: true,
			Model:                 "gpt-5",
			OpenAIBaseURL:         fakeResponses.URL,
			OpenAIBaseURLExplicit: true,
		}, memoryAuthHandler{state: auth.State{
			Scope:     auth.ScopeGlobal,
			Method:    auth.Method{Type: auth.MethodAPIKey, APIKey: &auth.APIKeyMethod{Key: "test-key"}},
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
		waitForDiscoveryRecord(t, workspace)

		server, err := startSessionServer(context.Background(), Options{WorkspaceRoot: workspace, WorkspaceRootExplicit: true}, newHeadlessAuthInteractor())
		if err != nil {
			t.Fatalf("startSessionServer: %v", err)
		}
		defer func() { _ = server.Close() }()
		runInteractiveWorkflowScenario(t, server, "parity reply")

		cancel()
		if serveErr := <-errCh; !errors.Is(serveErr, context.Canceled) {
			t.Fatalf("Serve error = %v, want context canceled", serveErr)
		}
	})
}

func discoveryPathForWorkspace(t *testing.T, workspace string) string {
	t.Helper()
	loadCfg, err := config.Load(workspace, config.LoadOptions{})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	_, containerDir, err := config.ResolveWorkspaceContainer(loadCfg)
	if err != nil {
		t.Fatalf("ResolveWorkspaceContainer: %v", err)
	}
	discoveryPath, err := discovery.PathForContainer(containerDir)
	if err != nil {
		t.Fatalf("PathForContainer: %v", err)
	}
	return discoveryPath
}

func waitForDiscoveryRecord(t *testing.T, workspace string) {
	t.Helper()
	_ = waitForDiscoveryRecordAtPath(t, discoveryPathForWorkspace(t, workspace))
}

func waitForDiscoveryRecordAtPath(t *testing.T, discoveryPath string) protocol.DiscoveryRecord {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		record, err := discovery.Read(discoveryPath)
		if err == nil {
			return record
		}
		if time.Now().After(deadline) {
			t.Fatalf("discovery record did not appear at %s", discoveryPath)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func waitForDiscoveryRemoval(t *testing.T, discoveryPath string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := discovery.Read(discoveryPath); err != nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("discovery record still present at %s", discoveryPath)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func waitForPIDExit(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		err := syscall.Kill(pid, 0)
		if err != nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("pid %d still running", pid)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func waitForRemoteAskEvent(t *testing.T, events <-chan askEvent) askEvent {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case evt, ok := <-events:
			if !ok {
				t.Fatal("ask event channel closed")
			}
			if evt.isResolution() {
				continue
			}
			return evt
		case <-deadline:
			t.Fatal("timed out waiting for ask event")
			return askEvent{}
		}
	}
}

func waitForRemoteProcess(t *testing.T, views client.ProcessViewClient, sessionID string, processID string) clientui.BackgroundProcess {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := views.ListProcesses(context.Background(), serverapi.ProcessListRequest{OwnerSessionID: sessionID})
		if err != nil {
			t.Fatalf("ListProcesses: %v", err)
		}
		for _, proc := range resp.Processes {
			if proc.ID == processID {
				return proc
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for process %s in session %s", processID, sessionID)
	return clientui.BackgroundProcess{}
}

func waitForRemoteProcessExit(t *testing.T, views client.ProcessViewClient, processID string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := views.GetProcess(context.Background(), serverapi.ProcessGetRequest{ProcessID: processID})
		if err != nil {
			t.Fatalf("GetProcess: %v", err)
		}
		if resp.Process != nil && !resp.Process.Running {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for process %s to exit", processID)
}

func waitForRemoteInlineOutput(t *testing.T, controls client.ProcessControlClient, processID string) serverapi.ProcessInlineOutputResponse {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := controls.GetInlineOutput(context.Background(), serverapi.ProcessInlineOutputRequest{ProcessID: processID, MaxChars: 1024})
		if err != nil {
			t.Fatalf("GetInlineOutput: %v", err)
		}
		if strings.TrimSpace(resp.Output) != "" {
			return resp
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for inline output from %s", processID)
	return serverapi.ProcessInlineOutputResponse{}
}

func runInteractiveWorkflowScenario(t *testing.T, server embeddedServer, wantReply string) {
	t.Helper()
	planner := newSessionLaunchPlanner(server)
	plan, err := planner.PlanSession(context.Background(), sessionLaunchRequest{Mode: launchModeInteractive, ForceNewSession: true})
	if err != nil {
		t.Fatalf("PlanSession: %v", err)
	}
	runtimePlan, err := planner.PrepareRuntime(context.Background(), plan, io.Discard, "workflow parity")
	if err != nil {
		t.Fatalf("PrepareRuntime: %v", err)
	}
	defer runtimePlan.Close()

	message, err := runtimePlan.Wiring.runtimeClient.SubmitUserMessage(context.Background(), "hello parity")
	if err != nil {
		t.Fatalf("SubmitUserMessage: %v", err)
	}
	if message != wantReply {
		t.Fatalf("assistant message = %q, want %q", message, wantReply)
	}
	if _, err := server.SessionLifecycleClient().PersistInputDraft(context.Background(), serverapi.SessionPersistInputDraftRequest{SessionID: plan.SessionID, Input: "workflow draft"}); err != nil {
		t.Fatalf("PersistInputDraft: %v", err)
	}
	if got := sessionLaunchInitialInputFromServer(context.Background(), server, plan.SessionID, "transition draft"); got != "workflow draft" {
		t.Fatalf("sessionLaunchInitialInputFromServer = %q, want workflow draft", got)
	}
	refreshed, err := server.SessionViewClient().GetSessionMainView(context.Background(), serverapi.SessionMainViewRequest{SessionID: plan.SessionID})
	if err != nil {
		t.Fatalf("GetSessionMainView: %v", err)
	}
	if refreshed.MainView.Session.Transcript.CommittedEntryCount == 0 {
		t.Fatalf("expected transcript metadata, got %+v", refreshed.MainView.Session.Transcript)
	}
}

func newHeadlessAuthInteractorWithEnvKey(key string) authInteractor {
	return &headlessAuthInteractor{lookupEnv: func(env string) string {
		if env == "OPENAI_API_KEY" {
			return key
		}
		return ""
	}}
}

func publishDiscoveredRemoteForWorkspace(t *testing.T, workspace string, caps protocol.CapabilityFlags) func() {
	t.Helper()
	loadCfg, err := config.Load(workspace, config.LoadOptions{})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	_, containerDir, err := config.ResolveWorkspaceContainer(loadCfg)
	if err != nil {
		t.Fatalf("ResolveWorkspaceContainer: %v", err)
	}
	discoveryPath, err := discovery.PathForContainer(containerDir)
	if err != nil {
		t.Fatalf("PathForContainer: %v", err)
	}
	expectedProjectID, err := config.ProjectIDForWorkspaceRoot(loadCfg.WorkspaceRoot)
	if err != nil {
		t.Fatalf("ProjectIDForWorkspaceRoot: %v", err)
	}
	identity := protocol.ServerIdentity{
		ProtocolVersion: protocol.Version,
		ServerID:        "stale-daemon",
		ProjectID:       expectedProjectID,
		WorkspaceRoot:   loadCfg.WorkspaceRoot,
		Capabilities:    caps,
	}
	server := httptest.NewServer(websocket.Handler(func(ws *websocket.Conn) {
		defer func() { _ = ws.Close() }()
		var req protocol.Request
		if err := websocket.JSON.Receive(ws, &req); err != nil {
			return
		}
		if req.Method != protocol.MethodHandshake {
			_ = websocket.JSON.Send(ws, protocol.NewErrorResponse(req.ID, protocol.ErrCodeInvalidRequest, "handshake required"))
			return
		}
		if err := websocket.JSON.Send(ws, protocol.NewSuccessResponse(req.ID, protocol.HandshakeResponse{Identity: identity})); err != nil {
			return
		}
		for {
			if err := websocket.JSON.Receive(ws, &req); err != nil {
				return
			}
			_ = websocket.JSON.Send(ws, protocol.NewErrorResponse(req.ID, protocol.ErrCodeMethodNotFound, "method not found"))
		}
	}))
	rpcURL := "ws" + strings.TrimPrefix(server.URL, "http")
	record := protocol.DiscoveryRecord{
		Identity: identity,
		RPCURL:   rpcURL,
		HTTPURL:  server.URL,
	}
	if err := discovery.Write(discoveryPath, record); err != nil {
		server.Close()
		t.Fatalf("discovery.Write: %v", err)
	}
	return func() {
		server.Close()
		_ = discovery.Remove(discoveryPath)
	}
}

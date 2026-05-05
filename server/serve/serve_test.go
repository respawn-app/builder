package serve

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"builder/server/auth"
	"builder/server/authflow"
	"builder/server/metadata"
	"builder/server/rootlock"
	"builder/server/startup"
	"builder/shared/client"
	"builder/shared/config"
	"builder/shared/protocol"
	"builder/shared/serverapi"
)

type envAuthHandler struct{}

func (envAuthHandler) WrapStore(base auth.Store) auth.Store {
	return authflow.WrapStoreWithEnvAPIKeyOverride(base, testAuthLookupEnv)
}

func (envAuthHandler) NeedsInteraction(req authflow.InteractionRequest) bool {
	return !req.Gate.Ready
}

func (envAuthHandler) Interact(context.Context, authflow.InteractionRequest) (authflow.InteractionOutcome, error) {
	return authflow.InteractionOutcome{}, auth.ErrAuthNotConfigured
}

func (envAuthHandler) LookupEnv(key string) string {
	return testAuthLookupEnv(key)
}

func testAuthLookupEnv(key string) string {
	if key == "OPENAI_API_KEY" {
		return "in-memory-test-key"
	}
	return ""
}

type noopOnboarding struct{}

func (noopOnboarding) EnsureOnboardingReady(_ context.Context, req startup.OnboardingRequest) (config.App, error) {
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

func registerServeWorkspace(t *testing.T, workspace string) {
	t.Helper()
	configureServeTestServerPort(t)
	cfg, err := config.Load(workspace, config.LoadOptions{})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if _, err := metadata.RegisterBinding(context.Background(), cfg.PersistenceRoot, cfg.WorkspaceRoot); err != nil {
		t.Fatalf("RegisterBinding: %v", err)
	}
}

func configureServeTestServerPort(t *testing.T) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve server port: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	ReserveTestListenReservation(listener)
	t.Cleanup(func() { ReleaseTestListenReservation(listener.Addr().String()) })
	t.Setenv("BUILDER_SERVER_HOST", "127.0.0.1")
	t.Setenv("BUILDER_SERVER_PORT", strconv.Itoa(port))
}

func TestStartBuildsStandaloneServerFromCoreStartup(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)

	request := startup.Request{WorkspaceRoot: workspace, WorkspaceRootExplicit: true}
	authHandler := envAuthHandler{}
	onboarding := noopOnboarding{}
	registerServeWorkspace(t, workspace)

	appCore, err := startup.StartCore(context.Background(), request, authHandler, onboarding)
	if err != nil {
		t.Fatalf("StartCore: %v", err)
	}
	coreProjectID := appCore.ProjectID()
	coreProjects, err := appCore.ProjectViewClient().ListProjects(context.Background(), serverapi.ProjectListRequest{})
	if err != nil {
		t.Fatalf("core ListProjects: %v", err)
	}
	if err := appCore.Close(); err != nil {
		t.Fatalf("appCore.Close: %v", err)
	}

	server, err := Start(context.Background(), request, authHandler, onboarding)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = server.Close() }()

	if server.Core == nil {
		t.Fatal("expected standalone server to expose core")
	}
	if server.ProjectID() != coreProjectID {
		t.Fatalf("project id mismatch: server=%q core=%q", server.ProjectID(), coreProjectID)
	}
	if server.ProjectViewClient() == nil || server.SessionViewClient() == nil || server.ProcessViewClient() == nil || server.ProcessOutputClient() == nil || server.RunPromptClient() == nil {
		t.Fatal("expected standalone server to expose core-backed clients")
	}
	serverProjects, err := server.ProjectViewClient().ListProjects(context.Background(), serverapi.ProjectListRequest{})
	if err != nil {
		t.Fatalf("server ListProjects: %v", err)
	}
	if len(coreProjects.Projects) != 1 || len(serverProjects.Projects) != 1 {
		t.Fatalf("unexpected project counts core=%d server=%d", len(coreProjects.Projects), len(serverProjects.Projects))
	}
	if coreProjects.Projects[0].ProjectID != serverProjects.Projects[0].ProjectID {
		t.Fatalf("project listing mismatch core=%+v server=%+v", coreProjects.Projects[0], serverProjects.Projects[0])
	}
}

func TestStartRejectsSecondOwnerForSamePersistenceRoot(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)

	request := startup.Request{WorkspaceRoot: workspace, WorkspaceRootExplicit: true}
	authHandler := envAuthHandler{}
	onboarding := noopOnboarding{}
	registerServeWorkspace(t, workspace)

	first, err := Start(context.Background(), request, authHandler, onboarding)
	if err != nil {
		t.Fatalf("Start first: %v", err)
	}
	defer func() { _ = first.Close() }()

	_, err = Start(context.Background(), request, authHandler, onboarding)
	if !errors.Is(err, rootlock.ErrPersistenceRootBusy) {
		t.Fatalf("Start second error = %v, want ErrPersistenceRootBusy", err)
	}
}

func TestServeWaitsForContextCancellation(t *testing.T) {
	server := &Server{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := server.Serve(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Serve error = %v, want context canceled", err)
	}
}

func TestServeRequiresContext(t *testing.T) {
	server := &Server{}
	if err := server.Serve(nil); err == nil || err.Error() != "context is required" {
		t.Fatalf("Serve error = %v, want missing context error", err)
	}
}

func TestServeExposesConfiguredHealthEndpoints(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)

	request := startup.Request{WorkspaceRoot: workspace, WorkspaceRootExplicit: true}
	authHandler := envAuthHandler{}
	onboarding := noopOnboarding{}
	registerServeWorkspace(t, workspace)

	server, err := Start(context.Background(), request, authHandler, onboarding)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = server.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Serve(ctx)
	}()

	loadCfg, err := config.Load(workspace, config.LoadOptions{})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	healthURL := config.ServerHTTPBaseURL(loadCfg) + protocol.HealthPath
	readyURL := config.ServerHTTPBaseURL(loadCfg) + protocol.ReadinessPath
	deadline := time.Now().Add(5 * time.Second)
	var healthResp *http.Response
	for {
		healthResp, err = http.Get(healthURL)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("GET health: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	defer func() { _ = healthResp.Body.Close() }()
	if healthResp.StatusCode != http.StatusOK {
		t.Fatalf("health status = %d, want 200", healthResp.StatusCode)
	}
	var healthBody map[string]any
	if err := json.NewDecoder(healthResp.Body).Decode(&healthBody); err != nil {
		t.Fatalf("decode health body: %v", err)
	}
	if healthBody["status"] != "ok" {
		t.Fatalf("unexpected health body: %+v", healthBody)
	}

	readyResp, err := http.Get(readyURL)
	if err != nil {
		t.Fatalf("GET ready: %v", err)
	}
	defer func() { _ = readyResp.Body.Close() }()
	if readyResp.StatusCode != http.StatusOK {
		t.Fatalf("readiness status = %d, want 200", readyResp.StatusCode)
	}

	cancel()
	if serveErr := <-errCh; !errors.Is(serveErr, context.Canceled) {
		t.Fatalf("Serve error = %v, want context canceled", serveErr)
	}
}

func TestServeExposesDerivedLocalUnixSocketAndCleansStalePath(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)

	request := startup.Request{WorkspaceRoot: workspace, WorkspaceRootExplicit: true}
	authHandler := envAuthHandler{}
	onboarding := noopOnboarding{}
	registerServeWorkspace(t, workspace)

	loadCfg, err := config.Load(workspace, config.LoadOptions{})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	socketPath, ok, err := config.ServerLocalRPCSocketPath(loadCfg)
	if err != nil {
		t.Fatalf("ServerLocalRPCSocketPath: %v", err)
	}
	if !ok {
		t.Skip("local unix sockets unsupported on this platform")
	}
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o755); err != nil {
		t.Fatalf("MkdirAll socket dir: %v", err)
	}
	staleListener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen stale unix socket: %v", err)
	}
	if err := staleListener.Close(); err != nil {
		t.Fatalf("close stale unix socket: %v", err)
	}

	server, err := Start(context.Background(), request, authHandler, onboarding)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = server.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Serve(ctx)
	}()
	defer func() {
		cancel()
		if serveErr := <-errCh; !errors.Is(serveErr, context.Canceled) {
			t.Fatalf("Serve error = %v, want context canceled", serveErr)
		}
	}()

	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(socketPath); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("unix socket path did not appear: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	for {
		conn, dialErr := net.DialTimeout("unix", socketPath, 100*time.Millisecond)
		if dialErr == nil {
			_ = conn.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("unix socket path did not become dialable: %v", dialErr)
		}
		time.Sleep(10 * time.Millisecond)
	}

	var localRemote *client.Remote
	for {
		localRemote, err = client.DialConfiguredRemote(context.Background(), loadCfg)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("DialConfiguredRemote: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if localRemote.Identity().ServerID == "" {
		t.Fatal("expected configured remote identity")
	}
	_ = localRemote.Close()

	tcpRemote, err := client.DialRemoteURL(context.Background(), config.ServerRPCURL(loadCfg))
	if err != nil {
		t.Fatalf("DialRemoteURL TCP: %v", err)
	}
	_ = tcpRemote.Close()
}

func TestServeDegradesToTCPWhenDerivedLocalSocketFails(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)

	request := startup.Request{WorkspaceRoot: workspace, WorkspaceRootExplicit: true}
	authHandler := envAuthHandler{}
	onboarding := noopOnboarding{}
	registerServeWorkspace(t, workspace)

	originalLocalSocketListener := localSocketListener
	localSocketListener = func(config.App) (net.Listener, func(), bool, error) {
		return nil, nil, false, errors.New("uds setup failed")
	}
	t.Cleanup(func() { localSocketListener = originalLocalSocketListener })

	server, err := Start(context.Background(), request, authHandler, onboarding)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = server.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Serve(ctx)
	}()
	defer func() {
		cancel()
		if serveErr := <-errCh; !errors.Is(serveErr, context.Canceled) {
			t.Fatalf("Serve error = %v, want context canceled", serveErr)
		}
	}()

	loadCfg, err := config.Load(workspace, config.LoadOptions{})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	healthURL := config.ServerHTTPBaseURL(loadCfg) + protocol.HealthPath
	deadline := time.Now().Add(5 * time.Second)
	for {
		resp, err := http.Get(healthURL)
		if err == nil {
			_ = resp.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("GET health: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}

	tcpRemote, err := client.DialRemoteURL(context.Background(), config.ServerRPCURL(loadCfg))
	if err != nil {
		t.Fatalf("DialRemoteURL TCP: %v", err)
	}
	_ = tcpRemote.Close()
}

func TestServeStartsUnauthenticatedAndReportsBootstrapReadiness(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)

	request := startup.Request{WorkspaceRoot: workspace, WorkspaceRootExplicit: true, AllowUnauthenticated: true}
	authHandler := envAuthHandler{}
	onboarding := noopOnboarding{}
	registerServeWorkspace(t, workspace)

	server, err := Start(context.Background(), request, authHandler, onboarding)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = server.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Serve(ctx)
	}()

	loadCfg, err := config.Load(workspace, config.LoadOptions{})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	healthURL := config.ServerHTTPBaseURL(loadCfg) + protocol.HealthPath
	readyURL := config.ServerHTTPBaseURL(loadCfg) + protocol.ReadinessPath
	deadline := time.Now().Add(5 * time.Second)
	var healthResp *http.Response
	for {
		healthResp, err = http.Get(healthURL)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("GET health: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	defer func() { _ = healthResp.Body.Close() }()
	if healthResp.StatusCode != http.StatusOK {
		t.Fatalf("health status = %d, want 200", healthResp.StatusCode)
	}
	var healthBody map[string]any
	if err := json.NewDecoder(healthResp.Body).Decode(&healthBody); err != nil {
		t.Fatalf("decode health body: %v", err)
	}
	if healthBody["auth_ready"] != false {
		t.Fatalf("expected auth_ready=false health payload, got %+v", healthBody)
	}

	readyResp, err := http.Get(readyURL)
	if err != nil {
		t.Fatalf("GET ready: %v", err)
	}
	defer func() { _ = readyResp.Body.Close() }()
	if readyResp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("readiness status = %d, want 503", readyResp.StatusCode)
	}
	var readyBody map[string]any
	if err := json.NewDecoder(readyResp.Body).Decode(&readyBody); err != nil {
		t.Fatalf("decode ready body: %v", err)
	}
	if readyBody["ready"] != false || readyBody["auth_ready"] != false || readyBody["transport_ready"] != true {
		t.Fatalf("unexpected readiness payload: %+v", readyBody)
	}

	cancel()
	if serveErr := <-errCh; !errors.Is(serveErr, context.Canceled) {
		t.Fatalf("Serve error = %v, want context canceled", serveErr)
	}
}

func TestServeFailsWhenConfiguredPortIsOccupied(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	registerServeWorkspace(t, workspace)
	request := startup.Request{WorkspaceRoot: workspace, WorkspaceRootExplicit: true}
	authHandler := envAuthHandler{}
	onboarding := noopOnboarding{}
	server, err := Start(context.Background(), request, authHandler, onboarding)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = server.Close() }()
	loadCfg, err := config.Load(workspace, config.LoadOptions{})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	ReleaseTestListenReservation(config.ServerListenAddress(loadCfg))
	listener, err := net.Listen("tcp", config.ServerListenAddress(loadCfg))
	if err != nil {
		t.Fatalf("occupy configured port: %v", err)
	}
	defer func() { _ = listener.Close() }()
	if err := server.Serve(context.Background()); err == nil {
		t.Fatal("expected serve to fail when configured port is occupied")
	}
}

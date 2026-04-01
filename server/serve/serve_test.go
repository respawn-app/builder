package serve

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"testing"
	"time"

	"builder/server/auth"
	"builder/server/authflow"
	"builder/server/discovery"
	"builder/server/startup"
	"builder/shared/config"
	"builder/shared/protocol"
	"builder/shared/serverapi"
)

type envAuthHandler struct{}

func (envAuthHandler) WrapStore(base auth.Store) auth.Store {
	return authflow.WrapStoreWithEnvAPIKeyOverride(base, os.Getenv)
}

func (envAuthHandler) NeedsInteraction(req authflow.InteractionRequest) bool {
	return !req.Gate.Ready
}

func (envAuthHandler) Interact(context.Context, authflow.InteractionRequest) error {
	return auth.ErrAuthNotConfigured
}

func (envAuthHandler) LookupEnv(key string) string {
	return os.Getenv(key)
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

func TestStartBuildsStandaloneServerFromCoreStartup(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OPENAI_API_KEY", "test-key")

	request := startup.Request{WorkspaceRoot: workspace, WorkspaceRootExplicit: true}
	authHandler := envAuthHandler{}
	onboarding := noopOnboarding{}

	appCore, err := startup.StartCore(context.Background(), request, authHandler, onboarding)
	if err != nil {
		t.Fatalf("StartCore: %v", err)
	}
	defer func() { _ = appCore.Close() }()

	server, err := Start(context.Background(), request, authHandler, onboarding)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = server.Close() }()

	if server.Core == nil {
		t.Fatal("expected standalone server to expose core")
	}
	if server.ProjectID() != appCore.ProjectID() {
		t.Fatalf("project id mismatch: server=%q core=%q", server.ProjectID(), appCore.ProjectID())
	}
	if server.ProjectViewClient() == nil || server.SessionViewClient() == nil || server.ProcessViewClient() == nil || server.ProcessOutputClient() == nil || server.RunPromptClient() == nil {
		t.Fatal("expected standalone server to expose core-backed clients")
	}
	coreProjects, err := appCore.ProjectViewClient().ListProjects(context.Background(), serverapi.ProjectListRequest{})
	if err != nil {
		t.Fatalf("core ListProjects: %v", err)
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

func TestServePublishesDiscoveryAndHealthEndpoints(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OPENAI_API_KEY", "test-key")

	request := startup.Request{WorkspaceRoot: workspace, WorkspaceRootExplicit: true}
	authHandler := envAuthHandler{}
	onboarding := noopOnboarding{}

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

	discoveryPath, err := discovery.PathForContainer(server.ContainerDir())
	if err != nil {
		t.Fatalf("PathForContainer: %v", err)
	}
	var record protocol.DiscoveryRecord
	deadline := time.Now().Add(5 * time.Second)
	for {
		record, err = discovery.Read(discoveryPath)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("read discovery record: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}

	if record.Identity.ProjectID != server.ProjectID() {
		t.Fatalf("project id = %q, want %q", record.Identity.ProjectID, server.ProjectID())
	}
	if record.Identity.WorkspaceRoot != workspace {
		t.Fatalf("workspace root = %q, want %q", record.Identity.WorkspaceRoot, workspace)
	}
	if !record.Identity.Capabilities.JSONRPCWebSocket {
		t.Fatal("expected websocket capability in discovery record")
	}

	healthResp, err := http.Get(record.HealthURL)
	if err != nil {
		t.Fatalf("GET health: %v", err)
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

	readyResp, err := http.Get(record.ReadyURL)
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
	if _, err := os.Stat(discoveryPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected discovery record removed after shutdown, got err=%v", err)
	}
}

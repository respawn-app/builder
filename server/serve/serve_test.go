package serve

import (
	"context"
	"errors"
	"os"
	"testing"

	"builder/server/auth"
	"builder/server/authflow"
	"builder/server/startup"
	"builder/shared/config"
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

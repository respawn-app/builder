package embedded

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"builder/server/auth"
	"builder/server/authflow"
	"builder/server/llm"
	"builder/server/runtime"
	"builder/server/session"
	"builder/server/tools"
	"builder/shared/config"
	"builder/shared/serverapi"
)

type testAuthHandler struct {
	lookupEnv      func(string) string
	interactCalled bool
}

func (h *testAuthHandler) WrapStore(base auth.Store) auth.Store {
	return authflow.WrapStoreWithEnvAPIKeyOverride(base, h.lookupEnv)
}

func (h *testAuthHandler) NeedsInteraction(req authflow.InteractionRequest) bool {
	return !req.Gate.Ready
}

func (h *testAuthHandler) Interact(context.Context, authflow.InteractionRequest) error {
	h.interactCalled = true
	return auth.ErrAuthNotConfigured
}

type testOnboardingHandler struct {
	called bool
	ensure func(context.Context, OnboardingRequest) (config.App, error)
}

func (h *testOnboardingHandler) EnsureOnboardingReady(ctx context.Context, req OnboardingRequest) (config.App, error) {
	h.called = true
	if h.ensure != nil {
		return h.ensure(ctx, req)
	}
	return req.Config, nil
}

func TestStartBuildsEmbeddedServerAndRunsOnboarding(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("OPENAI_API_KEY", "sk-test")
	t.Setenv("BUILDER_OAUTH_ISSUER", "https://attacker.example")
	t.Setenv("BUILDER_OAUTH_CLIENT_ID", "client-test")

	workspace := t.TempDir()
	authHandler := &testAuthHandler{lookupEnv: os.Getenv}
	onboarding := &testOnboardingHandler{
		ensure: func(_ context.Context, req OnboardingRequest) (config.App, error) {
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
		},
	}

	server, err := Start(context.Background(), Request{
		WorkspaceRoot: workspace,
		LookupEnv:     os.Getenv,
	}, StartHooks{Auth: authHandler, Onboarding: onboarding})
	if err != nil {
		t.Fatalf("start embedded server: %v", err)
	}
	t.Cleanup(func() { _ = server.Close() })

	if !onboarding.called {
		t.Fatal("expected onboarding handler to run")
	}
	if got := server.OAuthOptions().Issuer; got != auth.DefaultOpenAIIssuer {
		t.Fatalf("oauth issuer = %q, want %q", got, auth.DefaultOpenAIIssuer)
	}
	if got := server.OAuthOptions().ClientID; got != "client-test" {
		t.Fatalf("oauth client id = %q", got)
	}
	_, wantContainerDir, err := config.ResolveWorkspaceContainer(server.Config())
	if err != nil {
		t.Fatalf("resolve workspace container: %v", err)
	}
	if server.ContainerDir() != wantContainerDir {
		t.Fatalf("container dir = %q, want %q", server.ContainerDir(), wantContainerDir)
	}
	if _, err := os.Stat(filepath.Join(server.ContainerDir())); err != nil {
		t.Fatalf("expected container dir to exist: %v", err)
	}
	if server.RunPromptClient() == nil {
		t.Fatal("expected run prompt client")
	}
}

func TestRunPromptClientRunsLoopbackThroughEmbeddedServer(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OPENAI_API_KEY", "test-key")

	responseServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		if got := strings.TrimSpace(r.Header.Get("Authorization")); got == "" {
			t.Fatal("expected authorization header")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":11,\"output_tokens\":7,\"total_tokens\":18},\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"phase\":\"final\",\"content\":[{\"type\":\"output_text\",\"text\":\"hello from embedded\"}]}]}}\n\n")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
	}))
	defer responseServer.Close()

	server, err := Start(context.Background(), Request{
		WorkspaceRoot:         workspace,
		WorkspaceRootExplicit: true,
		OpenAIBaseURL:         responseServer.URL,
		OpenAIBaseURLExplicit: true,
		LoadOptions: config.LoadOptions{
			Model: "gpt-5",
		},
		LookupEnv: os.Getenv,
	}, StartHooks{
		Auth: &testAuthHandler{lookupEnv: os.Getenv},
		Onboarding: &testOnboardingHandler{
			ensure: func(_ context.Context, req OnboardingRequest) (config.App, error) {
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
			},
		},
	})
	if err != nil {
		t.Fatalf("start embedded server: %v", err)
	}
	defer func() { _ = server.Close() }()

	response, err := server.RunPromptClient().RunPrompt(context.Background(), serverapi.RunPromptRequest{
		ClientRequestID: "embedded-run-1",
		Prompt:          "hello from user",
	}, nil)
	if err != nil {
		t.Fatalf("run prompt via embedded server: %v", err)
	}
	if strings.TrimSpace(response.SessionID) == "" {
		t.Fatal("expected session id")
	}
	if response.Result != "hello from embedded" {
		t.Fatalf("response result = %q", response.Result)
	}

	store, err := session.OpenByID(server.Config().PersistenceRoot, response.SessionID)
	if err != nil {
		t.Fatalf("open session by id: %v", err)
	}
	if store.Meta().Continuation == nil || store.Meta().Continuation.OpenAIBaseURL != responseServer.URL {
		t.Fatalf("unexpected continuation context: %+v", store.Meta().Continuation)
	}
	events, err := store.ReadEvents()
	if err != nil {
		t.Fatalf("read events: %v", err)
	}
	var sawUser bool
	var sawAssistant bool
	for _, evt := range events {
		if evt.Kind != "message" {
			continue
		}
		var msg llm.Message
		if err := json.Unmarshal(evt.Payload, &msg); err != nil {
			t.Fatalf("unmarshal message: %v", err)
		}
		if msg.Role == llm.RoleUser && msg.Content == "hello from user" {
			sawUser = true
		}
		if msg.Role == llm.RoleAssistant && msg.Content == "hello from embedded" {
			sawAssistant = true
		}
	}
	if !sawUser || !sawAssistant {
		t.Fatalf("expected persisted user and assistant messages, sawUser=%t sawAssistant=%t", sawUser, sawAssistant)
	}
}

func TestStartPropagatesAuthFailureBeforeOnboarding(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	workspace := t.TempDir()
	authHandler := &testAuthHandler{lookupEnv: os.Getenv}
	onboarding := &testOnboardingHandler{}

	_, err := Start(context.Background(), Request{WorkspaceRoot: workspace, LookupEnv: os.Getenv}, StartHooks{Auth: authHandler, Onboarding: onboarding})
	if !errors.Is(err, auth.ErrAuthNotConfigured) {
		t.Fatalf("expected auth not configured, got %v", err)
	}
	if !authHandler.interactCalled {
		t.Fatal("expected auth handler interaction")
	}
	if onboarding.called {
		t.Fatal("did not expect onboarding after auth failure")
	}
}

func TestSessionViewClientReadsDormantSessionByIDWithoutMutatingFiles(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OPENAI_API_KEY", "test-key")

	server, err := Start(context.Background(), Request{
		WorkspaceRoot: workspace,
		LookupEnv:     os.Getenv,
	}, StartHooks{
		Auth: &testAuthHandler{lookupEnv: os.Getenv},
		Onboarding: &testOnboardingHandler{
			ensure: func(_ context.Context, req OnboardingRequest) (config.App, error) {
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
			},
		},
	})
	if err != nil {
		t.Fatalf("start embedded server: %v", err)
	}
	defer func() { _ = server.Close() }()

	store, err := session.Create(server.ContainerDir(), "workspace-x", workspace)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := store.SetName("incident triage"); err != nil {
		t.Fatalf("set name: %v", err)
	}
	if _, err := store.AppendEvent("step-1", "message", llm.Message{Role: llm.RoleUser, Content: "hello"}); err != nil {
		t.Fatalf("append user message: %v", err)
	}
	if _, err := store.AppendRunStarted(session.RunRecord{RunID: "run-1", StepID: "step-1", StartedAt: time.Now().UTC().Add(-time.Minute)}); err != nil {
		t.Fatalf("append run start: %v", err)
	}

	sessionPath := filepath.Join(store.Dir(), "session.json")
	eventsPath := filepath.Join(store.Dir(), "events.jsonl")
	beforeSession, err := os.ReadFile(sessionPath)
	if err != nil {
		t.Fatalf("read session file before: %v", err)
	}
	beforeEvents, err := os.ReadFile(eventsPath)
	if err != nil {
		t.Fatalf("read events file before: %v", err)
	}

	resp, err := server.SessionViewClient().GetSessionMainView(context.Background(), serverapi.SessionMainViewRequest{SessionID: store.Meta().SessionID})
	if err != nil {
		t.Fatalf("get session main view: %v", err)
	}
	if resp.MainView.Session.SessionName != "incident triage" || resp.MainView.ActiveRun == nil || resp.MainView.ActiveRun.RunID != "run-1" {
		t.Fatalf("unexpected main view: %+v", resp.MainView)
	}

	afterSession, err := os.ReadFile(sessionPath)
	if err != nil {
		t.Fatalf("read session file after: %v", err)
	}
	afterEvents, err := os.ReadFile(eventsPath)
	if err != nil {
		t.Fatalf("read events file after: %v", err)
	}
	if string(beforeSession) != string(afterSession) {
		t.Fatalf("session file mutated during dormant read")
	}
	if string(beforeEvents) != string(afterEvents) {
		t.Fatalf("events file mutated during dormant read")
	}
}

func TestSessionViewClientUsesRegisteredRuntimeByID(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OPENAI_API_KEY", "test-key")

	server, err := Start(context.Background(), Request{
		WorkspaceRoot: workspace,
		LookupEnv:     os.Getenv,
	}, StartHooks{
		Auth: &testAuthHandler{lookupEnv: os.Getenv},
		Onboarding: &testOnboardingHandler{
			ensure: func(_ context.Context, req OnboardingRequest) (config.App, error) {
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
			},
		},
	})
	if err != nil {
		t.Fatalf("start embedded server: %v", err)
	}
	defer func() { _ = server.Close() }()

	store, err := session.Create(server.ContainerDir(), "workspace-x", workspace)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	eng, err := runtime.New(store, &fakeEmbeddedClient{}, tools.NewRegistry(), runtime.Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	server.RegisterRuntime(store.Meta().SessionID, eng)
	defer server.UnregisterRuntime(store.Meta().SessionID)

	resp, err := server.SessionViewClient().GetSessionMainView(context.Background(), serverapi.SessionMainViewRequest{SessionID: store.Meta().SessionID})
	if err != nil {
		t.Fatalf("get session main view: %v", err)
	}
	if resp.MainView.Session.SessionID != store.Meta().SessionID {
		t.Fatalf("unexpected session main view: %+v", resp.MainView)
	}
}

type fakeEmbeddedClient struct{}

func (*fakeEmbeddedClient) Generate(context.Context, llm.Request) (llm.Response, error) {
	return llm.Response{}, nil
}

func (*fakeEmbeddedClient) ProviderCapabilities(context.Context) (llm.ProviderCapabilities, error) {
	return llm.ProviderCapabilities{ProviderID: "openai", SupportsResponsesAPI: true, IsOpenAIFirstParty: true}, nil
}

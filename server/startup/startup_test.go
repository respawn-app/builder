package startup

import (
	"context"
	"errors"
	"testing"
	"time"

	"builder/server/auth"
	"builder/server/authflow"
	"builder/shared/config"
)

type stubAuthHandler struct {
	lookupEnv func(string) string
	needs     func(authflow.InteractionRequest) bool
	interact  func(context.Context, authflow.InteractionRequest) error
}

func (h stubAuthHandler) WrapStore(base auth.Store) auth.Store {
	return base
}

func (h stubAuthHandler) NeedsInteraction(req authflow.InteractionRequest) bool {
	if h.needs == nil {
		return false
	}
	return h.needs(req)
}

func (h stubAuthHandler) Interact(ctx context.Context, req authflow.InteractionRequest) error {
	if h.interact == nil {
		return nil
	}
	return h.interact(ctx, req)
}

func (h stubAuthHandler) LookupEnv(key string) string {
	if h.lookupEnv == nil {
		return ""
	}
	return h.lookupEnv(key)
}

type stubAuthState struct {
	cfg       config.App
	oauthOpts auth.OpenAIOAuthOptions
	mgr       *auth.Manager
}

func (s stubAuthState) Config() config.App                    { return s.cfg }
func (s stubAuthState) OAuthOptions() auth.OpenAIOAuthOptions { return s.oauthOpts }
func (s stubAuthState) AuthManager() *auth.Manager            { return s.mgr }

func TestEnsureReadyUsesAuthHandlerLookupEnv(t *testing.T) {
	mgr := auth.NewManager(auth.NewMemoryStore(auth.EmptyState()), nil, time.Now)
	sawInteraction := false
	err := EnsureReady(context.Background(), stubAuthState{
		cfg: config.App{Settings: config.Settings{
			Theme:              "dark",
			TUIAlternateScreen: config.TUIAlternateScreenAuto,
		}},
		oauthOpts: auth.OpenAIOAuthOptions{ClientID: "client-test"},
		mgr:       mgr,
	}, stubAuthHandler{
		lookupEnv: func(key string) string {
			if key == "OPENAI_API_KEY" {
				return "sk-env"
			}
			return ""
		},
		needs: func(req authflow.InteractionRequest) bool {
			sawInteraction = true
			if !req.HasEnvAPIKey {
				t.Fatal("expected lookup env api key to be reflected in interaction request")
			}
			if req.Theme != "dark" {
				t.Fatalf("theme = %q, want dark", req.Theme)
			}
			if req.AlternateScreen != config.TUIAlternateScreenAuto {
				t.Fatalf("alternate screen policy = %q, want auto", req.AlternateScreen)
			}
			return true
		},
		interact: func(context.Context, authflow.InteractionRequest) error {
			return auth.ErrAuthNotConfigured
		},
	})
	if !errors.Is(err, auth.ErrAuthNotConfigured) {
		t.Fatalf("expected auth not configured, got %v", err)
	}
	if !sawInteraction {
		t.Fatal("expected ensure ready to invoke auth interaction")
	}
}

func TestEnsureReadyRequiresAuthManager(t *testing.T) {
	err := EnsureReady(context.Background(), stubAuthState{}, stubAuthHandler{})
	if err == nil || err.Error() != "auth manager is required" {
		t.Fatalf("expected missing auth manager error, got %v", err)
	}
}

func TestBuildRequestMapsStartupOptionsAndLookupEnv(t *testing.T) {
	handler := stubAuthHandler{
		lookupEnv: func(key string) string {
			if key == "OPENAI_API_KEY" {
				return "sk-test"
			}
			return ""
		},
	}
	req := buildRequest(Request{
		WorkspaceRoot:         "/tmp/workspace",
		WorkspaceRootExplicit: true,
		SessionID:             "session-123",
		Model:                 "gpt-5",
		ProviderOverride:      "openai",
		ThinkingLevel:         "high",
		Theme:                 "dark",
		ModelTimeoutSeconds:   45,
		ShellTimeoutSeconds:   30,
		Tools:                 "shell,patch",
		OpenAIBaseURL:         "http://example.test/v1",
		OpenAIBaseURLExplicit: true,
	}, handler)

	if req.WorkspaceRoot != "/tmp/workspace" || !req.WorkspaceRootExplicit {
		t.Fatalf("unexpected workspace mapping: %+v", req)
	}
	if req.SessionID != "session-123" {
		t.Fatalf("session id = %q, want session-123", req.SessionID)
	}
	if req.OpenAIBaseURL != "http://example.test/v1" || !req.OpenAIBaseURLExplicit {
		t.Fatalf("unexpected base url mapping: %+v", req)
	}
	if req.LoadOptions.Model != "gpt-5" || req.LoadOptions.ProviderOverride != "openai" || req.LoadOptions.ThinkingLevel != "high" {
		t.Fatalf("unexpected model/provider/thinking mapping: %+v", req.LoadOptions)
	}
	if req.LoadOptions.Theme != "dark" || req.LoadOptions.ModelTimeoutSeconds != 45 || req.LoadOptions.ShellTimeoutSeconds != 30 {
		t.Fatalf("unexpected theme/timeout mapping: %+v", req.LoadOptions)
	}
	if req.LoadOptions.Tools != "shell,patch" {
		t.Fatalf("tools = %q, want shell,patch", req.LoadOptions.Tools)
	}
	if got := req.LookupEnv("OPENAI_API_KEY"); got != "sk-test" {
		t.Fatalf("lookup env returned %q, want sk-test", got)
	}
}

func TestLookupEnvFallsBackToProcessEnvWhenHandlerMissing(t *testing.T) {
	t.Setenv("BUILDER_LOOKUP_ENV_FALLBACK", "fallback-value")
	if got := lookupEnv(nil)("BUILDER_LOOKUP_ENV_FALLBACK"); got != "fallback-value" {
		t.Fatalf("lookup env fallback = %q, want fallback-value", got)
	}
}

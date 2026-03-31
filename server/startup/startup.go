package startup

import (
	"context"
	"errors"
	"os"

	"builder/server/auth"
	"builder/server/authflow"
	"builder/server/embedded"
	"builder/shared/config"
)

type Request struct {
	WorkspaceRoot         string
	WorkspaceRootExplicit bool
	SessionID             string
	Model                 string
	ProviderOverride      string
	ThinkingLevel         string
	Theme                 string
	ModelTimeoutSeconds   int
	ShellTimeoutSeconds   int
	Tools                 string
	OpenAIBaseURL         string
	OpenAIBaseURLExplicit bool
}

type AuthHandler interface {
	WrapStore(base auth.Store) auth.Store
	NeedsInteraction(req authflow.InteractionRequest) bool
	Interact(ctx context.Context, req authflow.InteractionRequest) error
	LookupEnv(key string) string
}

type AuthState interface {
	Config() config.App
	OAuthOptions() auth.OpenAIOAuthOptions
	AuthManager() *auth.Manager
}

func Start(ctx context.Context, req Request, authHandler AuthHandler, onboardingHandler embedded.OnboardingHandler) (*embedded.Server, error) {
	if authHandler == nil {
		return nil, errors.New("auth handler is required")
	}
	return embedded.Start(ctx, buildRequest(req, authHandler), embedded.StartHooks{
		Auth:       authHandler,
		Onboarding: onboardingHandler,
	})
}

func EnsureReady(ctx context.Context, state AuthState, authHandler AuthHandler) error {
	if state == nil {
		return errors.New("auth state is required")
	}
	if state.AuthManager() == nil {
		return errors.New("auth manager is required")
	}
	if authHandler == nil {
		return errors.New("auth handler is required")
	}
	cfg := state.Config()
	return authflow.EnsureReady(
		ctx,
		state.AuthManager(),
		state.OAuthOptions(),
		cfg.Settings.Theme,
		cfg.Settings.TUIAlternateScreen,
		lookupEnv(authHandler),
		authHandler,
	)
}

func buildRequest(req Request, authHandler AuthHandler) embedded.Request {
	return embedded.Request{
		WorkspaceRoot:         req.WorkspaceRoot,
		WorkspaceRootExplicit: req.WorkspaceRootExplicit,
		SessionID:             req.SessionID,
		OpenAIBaseURL:         req.OpenAIBaseURL,
		OpenAIBaseURLExplicit: req.OpenAIBaseURLExplicit,
		LookupEnv:             lookupEnv(authHandler),
		LoadOptions: config.LoadOptions{
			Model:               req.Model,
			ProviderOverride:    req.ProviderOverride,
			ThinkingLevel:       req.ThinkingLevel,
			Theme:               req.Theme,
			ModelTimeoutSeconds: req.ModelTimeoutSeconds,
			ShellTimeoutSeconds: req.ShellTimeoutSeconds,
			Tools:               req.Tools,
		},
	}
}

func lookupEnv(authHandler AuthHandler) func(string) string {
	if authHandler == nil {
		return os.Getenv
	}
	return authHandler.LookupEnv
}

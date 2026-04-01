package startup

import (
	"context"
	"errors"
	"os"

	"builder/server/auth"
	"builder/server/authflow"
	serverbootstrap "builder/server/bootstrap"
	"builder/server/core"
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

type OnboardingHandler interface {
	EnsureOnboardingReady(ctx context.Context, req OnboardingRequest) (config.App, error)
}

type OnboardingRequest struct {
	Config       config.App
	AuthManager  *auth.Manager
	ReloadConfig func() (config.App, error)
}

func Start(ctx context.Context, req Request, authHandler AuthHandler, onboardingHandler OnboardingHandler) (*embedded.Server, error) {
	appCore, err := StartCore(ctx, req, authHandler, onboardingHandler)
	if err != nil {
		return nil, err
	}
	return &embedded.Server{Core: appCore}, nil
}

func StartCore(ctx context.Context, req Request, authHandler AuthHandler, onboardingHandler OnboardingHandler) (*core.Core, error) {
	if authHandler == nil {
		return nil, errors.New("auth handler is required")
	}
	bootstrapReq := buildRequest(req, authHandler)
	resolved, err := serverbootstrap.ResolveConfig(bootstrapReq)
	if err != nil {
		return nil, err
	}
	cfg := resolved.Config
	store := authHandler.WrapStore(auth.NewFileStore(config.GlobalAuthConfigPath(cfg)))
	authSupport, err := serverbootstrap.BuildAuthSupport(store, bootstrapReq.LookupEnv, bootstrapReq.Now)
	if err != nil {
		return nil, err
	}
	if err := authflow.EnsureReady(ctx, authSupport.AuthManager, authSupport.OAuthOptions, cfg.Settings.Theme, cfg.Settings.TUIAlternateScreen, bootstrapReq.LookupEnv, authHandler); err != nil {
		return nil, err
	}
	if onboardingHandler != nil {
		cfg, err = onboardingHandler.EnsureOnboardingReady(ctx, OnboardingRequest{
			Config:      cfg,
			AuthManager: authSupport.AuthManager,
			ReloadConfig: func() (config.App, error) {
				refreshed, err := serverbootstrap.ResolveConfig(bootstrapReq)
				if err != nil {
					return config.App{}, err
				}
				return refreshed.Config, nil
			},
		})
		if err != nil {
			return nil, err
		}
	}
	runtimeSupport, err := serverbootstrap.BuildRuntimeSupport(cfg)
	if err != nil {
		return nil, err
	}
	appCore, err := core.New(cfg, authSupport, runtimeSupport)
	if err != nil {
		_ = runtimeSupport.Background.Close()
		return nil, err
	}
	return appCore, nil
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

func buildRequest(req Request, authHandler AuthHandler) serverbootstrap.Request {
	return serverbootstrap.Request{
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

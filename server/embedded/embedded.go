package embedded

import (
	"context"
	"errors"

	"builder/server/auth"
	"builder/server/authflow"
	"builder/server/authpolicy"
	serverbootstrap "builder/server/bootstrap"
	"builder/server/core"
	"builder/server/runtime"
	"builder/shared/config"
)

type Request = serverbootstrap.Request

type AuthHandler interface {
	WrapStore(base auth.Store) auth.Store
	NeedsInteraction(req authflow.InteractionRequest) bool
	Interact(ctx context.Context, req authflow.InteractionRequest) (authflow.InteractionOutcome, error)
}

type OnboardingHandler interface {
	EnsureOnboardingReady(ctx context.Context, req OnboardingRequest) (config.App, error)
}

type OnboardingRequest struct {
	Config       config.App
	AuthManager  *auth.Manager
	ReloadConfig func() (config.App, error)
}

type StartHooks struct {
	Auth       AuthHandler
	Onboarding OnboardingHandler
}

type BackgroundRouter interface {
	SetActiveSession(sessionID string, engine *runtime.Engine)
	ClearActiveSession(sessionID string)
}

type Server struct {
	*core.Core
}

func Start(ctx context.Context, req Request, hooks StartHooks) (*Server, error) {
	if hooks.Auth == nil {
		return nil, errors.New("auth handler is required")
	}
	resolved, err := serverbootstrap.ResolveConfig(req)
	if err != nil {
		return nil, err
	}
	cfg := resolved.Config
	store := hooks.Auth.WrapStore(auth.NewFileStore(config.GlobalAuthConfigPath(cfg)))
	authSupport, err := serverbootstrap.BuildAuthSupport(store, req.LookupEnv, req.Now)
	if err != nil {
		return nil, err
	}
	if err := authflow.EnsureReady(ctx, authSupport.AuthManager, authSupport.OAuthOptions, cfg.Settings.Theme, req.LookupEnv, authpolicy.RequiresStartupAuth(cfg.Settings), false, hooks.Auth); err != nil {
		return nil, err
	}
	if hooks.Onboarding != nil {
		cfg, err = hooks.Onboarding.EnsureOnboardingReady(ctx, OnboardingRequest{
			Config:      cfg,
			AuthManager: authSupport.AuthManager,
			ReloadConfig: func() (config.App, error) {
				refreshed, err := serverbootstrap.ResolveConfig(req)
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
	return &Server{Core: appCore}, nil
}

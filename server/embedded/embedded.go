package embedded

import (
	"context"
	"errors"

	"builder/server/auth"
	"builder/server/authflow"
	serverbootstrap "builder/server/bootstrap"
	"builder/server/runprompt"
	"builder/server/runtime"
	"builder/server/runtimewire"
	shelltool "builder/server/tools/shell"
	"builder/shared/client"
	"builder/shared/config"
)

type Request = serverbootstrap.Request

type AuthHandler interface {
	WrapStore(base auth.Store) auth.Store
	NeedsInteraction(req authflow.InteractionRequest) bool
	Interact(ctx context.Context, req authflow.InteractionRequest) error
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
	cfg              config.App
	containerDir     string
	oauthOpts        auth.OpenAIOAuthOptions
	authManager      *auth.Manager
	fastModeState    *runtime.FastModeState
	background       *shelltool.Manager
	backgroundRouter *runtimewire.BackgroundEventRouter
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
	if err := authflow.EnsureReady(ctx, authSupport.AuthManager, authSupport.OAuthOptions, cfg.Settings.Theme, cfg.Settings.TUIAlternateScreen, req.LookupEnv, hooks.Auth); err != nil {
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
	_, containerDir, err := config.ResolveWorkspaceContainer(cfg)
	if err != nil {
		return nil, err
	}
	runtimeSupport, err := serverbootstrap.BuildRuntimeSupport(cfg)
	if err != nil {
		return nil, err
	}
	return &Server{
		cfg:              cfg,
		containerDir:     containerDir,
		oauthOpts:        authSupport.OAuthOptions,
		authManager:      authSupport.AuthManager,
		fastModeState:    runtimeSupport.FastModeState,
		background:       runtimeSupport.Background,
		backgroundRouter: runtimeSupport.BackgroundRouter,
	}, nil
}

func (s *Server) Close() error {
	if s == nil || s.background == nil {
		return nil
	}
	return s.background.Close()
}

func (s *Server) Config() config.App {
	if s == nil {
		return config.App{}
	}
	return s.cfg
}

func (s *Server) ContainerDir() string {
	if s == nil {
		return ""
	}
	return s.containerDir
}

func (s *Server) OAuthOptions() auth.OpenAIOAuthOptions {
	if s == nil {
		return auth.OpenAIOAuthOptions{}
	}
	return s.oauthOpts
}

func (s *Server) AuthManager() *auth.Manager {
	if s == nil {
		return nil
	}
	return s.authManager
}

func (s *Server) FastModeState() *runtime.FastModeState {
	if s == nil {
		return nil
	}
	return s.fastModeState
}

func (s *Server) Background() *shelltool.Manager {
	if s == nil {
		return nil
	}
	return s.background
}

func (s *Server) BackgroundRouter() BackgroundRouter {
	if s == nil {
		return nil
	}
	return s.backgroundRouter
}

func (s *Server) RunPromptClient() client.RunPromptClient {
	if s == nil {
		return nil
	}
	return runprompt.NewLoopbackRunPromptClient(runprompt.HeadlessBootstrap{
		Config:           s.cfg,
		ContainerDir:     s.containerDir,
		AuthManager:      s.authManager,
		FastModeState:    s.fastModeState,
		Background:       s.background,
		BackgroundRouter: s.backgroundRouter,
	})
}

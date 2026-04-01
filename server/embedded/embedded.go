package embedded

import (
	"context"
	"errors"

	"builder/server/approvalview"
	"builder/server/askview"
	"builder/server/auth"
	"builder/server/authflow"
	serverbootstrap "builder/server/bootstrap"
	"builder/server/processview"
	"builder/server/projectview"
	"builder/server/registry"
	"builder/server/runprompt"
	"builder/server/runtime"
	"builder/server/runtimewire"
	"builder/server/sessionactivity"
	"builder/server/sessionview"
	askquestion "builder/server/tools/askquestion"
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
	runtimeRegistry  *registry.RuntimeRegistry
	projectID        string
	projectViews     client.ProjectViewClient
	askViews         client.AskViewClient
	approvalViews    client.ApprovalViewClient
	processControls  client.ProcessControlClient
	processViews     client.ProcessViewClient
	sessionViews     client.SessionViewClient
	sessionActivity  client.SessionActivityClient
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
	projectID, err := config.ProjectIDForWorkspaceRoot(cfg.WorkspaceRoot)
	if err != nil {
		return nil, err
	}
	runtimeSupport, err := serverbootstrap.BuildRuntimeSupport(cfg)
	if err != nil {
		return nil, err
	}
	runtimeRegistry := registry.NewRuntimeRegistry()
	projectService, err := projectview.NewService(projectID, cfg.WorkspaceRoot, containerDir)
	if err != nil {
		return nil, err
	}
	askService := askview.NewService(runtimeRegistry)
	approvalService := approvalview.NewService(runtimeRegistry)
	processService := processview.NewService(runtimeSupport.Background)
	return &Server{
		cfg:              cfg,
		containerDir:     containerDir,
		oauthOpts:        authSupport.OAuthOptions,
		authManager:      authSupport.AuthManager,
		fastModeState:    runtimeSupport.FastModeState,
		background:       runtimeSupport.Background,
		backgroundRouter: runtimeSupport.BackgroundRouter,
		runtimeRegistry:  runtimeRegistry,
		projectID:        projectService.ProjectID(),
		projectViews: client.NewLoopbackProjectViewClient(
			projectService,
		),
		askViews: client.NewLoopbackAskViewClient(
			askService,
		),
		approvalViews: client.NewLoopbackApprovalViewClient(
			approvalService,
		),
		processControls: client.NewLoopbackProcessControlClient(
			processService,
		),
		processViews: client.NewLoopbackProcessViewClient(
			processService,
		),
		sessionViews: client.NewLoopbackSessionViewClient(
			sessionview.NewService(registry.NewPersistenceSessionResolver(cfg.PersistenceRoot), runtimeRegistry),
		),
		sessionActivity: client.NewLoopbackSessionActivityClient(
			sessionactivity.NewService(runtimeRegistry),
		),
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

func (s *Server) SessionViewClient() client.SessionViewClient {
	if s == nil {
		return nil
	}
	return s.sessionViews
}

func (s *Server) ProjectID() string {
	if s == nil {
		return ""
	}
	return s.projectID
}

func (s *Server) ProjectViewClient() client.ProjectViewClient {
	if s == nil {
		return nil
	}
	return s.projectViews
}

func (s *Server) AskViewClient() client.AskViewClient {
	if s == nil {
		return nil
	}
	return s.askViews
}

func (s *Server) ApprovalViewClient() client.ApprovalViewClient {
	if s == nil {
		return nil
	}
	return s.approvalViews
}

func (s *Server) ProcessViewClient() client.ProcessViewClient {
	if s == nil {
		return nil
	}
	return s.processViews
}

func (s *Server) ProcessControlClient() client.ProcessControlClient {
	if s == nil {
		return nil
	}
	return s.processControls
}

func (s *Server) SessionActivityClient() client.SessionActivityClient {
	if s == nil {
		return nil
	}
	return s.sessionActivity
}

func (s *Server) RegisterRuntime(sessionID string, engine *runtime.Engine) {
	if s == nil || s.runtimeRegistry == nil {
		return
	}
	s.runtimeRegistry.Register(sessionID, engine)
}

func (s *Server) UnregisterRuntime(sessionID string) {
	if s == nil || s.runtimeRegistry == nil {
		return
	}
	s.runtimeRegistry.Unregister(sessionID)
}

func (s *Server) PublishRuntimeEvent(sessionID string, evt runtime.Event) {
	if s == nil || s.runtimeRegistry == nil {
		return
	}
	s.runtimeRegistry.PublishRuntimeEvent(sessionID, evt)
}

func (s *Server) BeginPendingPrompt(sessionID string, req askquestion.Request) {
	if s == nil || s.runtimeRegistry == nil {
		return
	}
	s.runtimeRegistry.BeginPendingPrompt(sessionID, req)
}

func (s *Server) CompletePendingPrompt(sessionID string, requestID string) {
	if s == nil || s.runtimeRegistry == nil {
		return
	}
	s.runtimeRegistry.CompletePendingPrompt(sessionID, requestID)
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
		RuntimeRegistry:  s.runtimeRegistry,
		BackgroundRouter: s.backgroundRouter,
	})
}

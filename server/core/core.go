package core

import (
	"fmt"

	"builder/server/approvalview"
	"builder/server/askview"
	"builder/server/auth"
	serverbootstrap "builder/server/bootstrap"
	"builder/server/primaryrun"
	"builder/server/processoutput"
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

type Core struct {
	cfg              config.App
	containerDir     string
	oauthOpts        serverbootstrap.AuthSupport
	fastModeState    *runtime.FastModeState
	background       *shelltool.Manager
	backgroundRouter *runtimewire.BackgroundEventRouter
	runtimeRegistry  *registry.RuntimeRegistry
	projectID        string
	projectViews     client.ProjectViewClient
	askViews         client.AskViewClient
	approvalViews    client.ApprovalViewClient
	processControls  client.ProcessControlClient
	processOutput    client.ProcessOutputClient
	processViews     client.ProcessViewClient
	sessionViews     client.SessionViewClient
	sessionActivity  client.SessionActivityClient
	runPrompt        client.RunPromptClient
}

func New(cfg config.App, authSupport serverbootstrap.AuthSupport, runtimeSupport serverbootstrap.RuntimeSupport) (*Core, error) {
	_, containerDir, err := config.ResolveWorkspaceContainer(cfg)
	if err != nil {
		return nil, err
	}
	projectID, err := config.ProjectIDForWorkspaceRoot(cfg.WorkspaceRoot)
	if err != nil {
		return nil, err
	}
	if authSupport.AuthManager == nil {
		return nil, fmt.Errorf("auth manager is required")
	}
	if runtimeSupport.Background == nil {
		return nil, fmt.Errorf("background manager is required")
	}
	runtimeRegistry := registry.NewRuntimeRegistry()
	projectService, err := projectview.NewService(projectID, cfg.WorkspaceRoot, containerDir)
	if err != nil {
		return nil, err
	}
	askService := askview.NewService(runtimeRegistry)
	approvalService := approvalview.NewService(runtimeRegistry)
	processService := processview.NewService(runtimeSupport.Background)
	processOutputService := processoutput.NewService(runtimeSupport.Background, runtimeSupport.Background)
	sessionViewService := sessionview.NewService(registry.NewPersistenceSessionResolver(cfg.PersistenceRoot), runtimeRegistry)
	sessionActivityService := sessionactivity.NewService(runtimeRegistry)
	core := &Core{
		cfg:              cfg,
		containerDir:     containerDir,
		oauthOpts:        authSupport,
		fastModeState:    runtimeSupport.FastModeState,
		background:       runtimeSupport.Background,
		backgroundRouter: runtimeSupport.BackgroundRouter,
		runtimeRegistry:  runtimeRegistry,
		projectID:        projectService.ProjectID(),
		projectViews:     client.NewLoopbackProjectViewClient(projectService),
		askViews:         client.NewLoopbackAskViewClient(askService),
		approvalViews:    client.NewLoopbackApprovalViewClient(approvalService),
		processControls:  client.NewLoopbackProcessControlClient(processService),
		processOutput:    client.NewLoopbackProcessOutputClient(processOutputService),
		processViews:     client.NewLoopbackProcessViewClient(processService),
		sessionViews:     client.NewLoopbackSessionViewClient(sessionViewService),
		sessionActivity:  client.NewLoopbackSessionActivityClient(sessionActivityService),
	}
	core.runPrompt = runprompt.NewLoopbackRunPromptClient(runprompt.HeadlessBootstrap{
		Config:           cfg,
		ContainerDir:     containerDir,
		AuthManager:      authSupport.AuthManager,
		FastModeState:    runtimeSupport.FastModeState,
		Background:       runtimeSupport.Background,
		RuntimeRegistry:  runtimeRegistry,
		BackgroundRouter: runtimeSupport.BackgroundRouter,
	})
	return core, nil
}

func (s *Core) Close() error {
	if s == nil || s.background == nil {
		return nil
	}
	return s.background.Close()
}

func (s *Core) Config() config.App {
	if s == nil {
		return config.App{}
	}
	return s.cfg
}

func (s *Core) ContainerDir() string {
	if s == nil {
		return ""
	}
	return s.containerDir
}

func (s *Core) OAuthOptions() auth.OpenAIOAuthOptions {
	if s == nil {
		return auth.OpenAIOAuthOptions{}
	}
	return s.oauthOpts.OAuthOptions
}

func (s *Core) AuthManager() *auth.Manager {
	if s == nil {
		return nil
	}
	return s.oauthOpts.AuthManager
}

func (s *Core) FastModeState() *runtime.FastModeState {
	if s == nil {
		return nil
	}
	return s.fastModeState
}

func (s *Core) Background() *shelltool.Manager {
	if s == nil {
		return nil
	}
	return s.background
}

func (s *Core) BackgroundRouter() *runtimewire.BackgroundEventRouter {
	if s == nil {
		return nil
	}
	return s.backgroundRouter
}

func (s *Core) SessionViewClient() client.SessionViewClient {
	if s == nil {
		return nil
	}
	return s.sessionViews
}

func (s *Core) ProjectID() string {
	if s == nil {
		return ""
	}
	return s.projectID
}

func (s *Core) ProjectViewClient() client.ProjectViewClient {
	if s == nil {
		return nil
	}
	return s.projectViews
}

func (s *Core) AskViewClient() client.AskViewClient {
	if s == nil {
		return nil
	}
	return s.askViews
}

func (s *Core) ApprovalViewClient() client.ApprovalViewClient {
	if s == nil {
		return nil
	}
	return s.approvalViews
}

func (s *Core) ProcessViewClient() client.ProcessViewClient {
	if s == nil {
		return nil
	}
	return s.processViews
}

func (s *Core) ProcessControlClient() client.ProcessControlClient {
	if s == nil {
		return nil
	}
	return s.processControls
}

func (s *Core) ProcessOutputClient() client.ProcessOutputClient {
	if s == nil {
		return nil
	}
	return s.processOutput
}

func (s *Core) SessionActivityClient() client.SessionActivityClient {
	if s == nil {
		return nil
	}
	return s.sessionActivity
}

func (s *Core) RegisterRuntime(sessionID string, engine *runtime.Engine) {
	if s == nil || s.runtimeRegistry == nil {
		return
	}
	s.runtimeRegistry.Register(sessionID, engine)
}

func (s *Core) UnregisterRuntime(sessionID string, engine *runtime.Engine) {
	if s == nil || s.runtimeRegistry == nil {
		return
	}
	s.runtimeRegistry.Unregister(sessionID, engine)
}

func (s *Core) PublishRuntimeEvent(sessionID string, evt runtime.Event) {
	if s == nil || s.runtimeRegistry == nil {
		return
	}
	s.runtimeRegistry.PublishRuntimeEvent(sessionID, evt)
}

func (s *Core) BeginPendingPrompt(sessionID string, req askquestion.Request) {
	if s == nil || s.runtimeRegistry == nil {
		return
	}
	s.runtimeRegistry.BeginPendingPrompt(sessionID, req)
}

func (s *Core) CompletePendingPrompt(sessionID string, requestID string) {
	if s == nil || s.runtimeRegistry == nil {
		return
	}
	s.runtimeRegistry.CompletePendingPrompt(sessionID, requestID)
}

func (s *Core) AcquirePrimaryRun(sessionID string) (primaryrun.Lease, error) {
	if s == nil || s.runtimeRegistry == nil {
		return nil, primaryrun.ErrActivePrimaryRun
	}
	return s.runtimeRegistry.AcquirePrimaryRun(sessionID)
}

func (s *Core) RunPromptClient() client.RunPromptClient {
	if s == nil {
		return nil
	}
	return s.runPrompt
}

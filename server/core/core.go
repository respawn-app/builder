package core

import (
	"context"
	"errors"
	"fmt"

	"builder/server/approvalview"
	"builder/server/askview"
	"builder/server/auth"
	serverbootstrap "builder/server/bootstrap"
	"builder/server/launch"
	"builder/server/metadata"
	"builder/server/primaryrun"
	"builder/server/processoutput"
	"builder/server/processview"
	"builder/server/projectview"
	"builder/server/promptactivity"
	"builder/server/promptcontrol"
	"builder/server/registry"
	"builder/server/runprompt"
	"builder/server/runtime"
	"builder/server/runtimecontrol"
	"builder/server/runtimewire"
	"builder/server/session"
	"builder/server/sessionactivity"
	"builder/server/sessionlaunch"
	"builder/server/sessionlifecycle"
	"builder/server/sessionruntime"
	"builder/server/sessionview"
	"builder/server/storagemigration"
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
	metadataStore    *metadata.Store
	runtimeRegistry  *registry.RuntimeRegistry
	sessionStores    *registry.SessionStoreRegistry
	projectID        string
	projectViews     client.ProjectViewClient
	askViews         client.AskViewClient
	approvalViews    client.ApprovalViewClient
	processControls  client.ProcessControlClient
	processOutput    client.ProcessOutputClient
	processViews     client.ProcessViewClient
	promptControl    client.PromptControlClient
	promptActivity   client.PromptActivityClient
	runtimeControls  client.RuntimeControlClient
	sessionLaunch    client.SessionLaunchClient
	sessionRuntime   client.SessionRuntimeClient
	sessionViews     client.SessionViewClient
	sessionLifecycle client.SessionLifecycleClient
	sessionActivity  client.SessionActivityClient
	runPrompt        client.RunPromptClient
}

func New(cfg config.App, authSupport serverbootstrap.AuthSupport, runtimeSupport serverbootstrap.RuntimeSupport) (*Core, error) {
	if err := storagemigration.EnsureProjectV1(context.Background(), cfg.PersistenceRoot, nil); err != nil {
		return nil, err
	}
	_, containerDir, err := config.ResolveWorkspaceContainer(cfg)
	if err != nil {
		return nil, err
	}
	metadataStore, err := metadata.Open(cfg.PersistenceRoot)
	if err != nil {
		return nil, err
	}
	if authSupport.AuthManager == nil {
		_ = metadataStore.Close()
		return nil, fmt.Errorf("auth manager is required")
	}
	if runtimeSupport.Background == nil {
		_ = metadataStore.Close()
		return nil, fmt.Errorf("background manager is required")
	}
	binding, err := metadataStore.EnsureWorkspaceBinding(context.Background(), cfg.WorkspaceRoot)
	if err != nil {
		_ = metadataStore.Close()
		return nil, err
	}
	projectSessionDir := config.ProjectSessionsRoot(cfg, binding.ProjectID)
	storeOptions := metadataStore.AuthoritativeSessionStoreOptions()
	runtimeRegistry := registry.NewRuntimeRegistry()
	sessionStoreRegistry := registry.NewSessionStoreRegistry()
	projectService, err := projectview.NewMetadataService(metadataStore, binding.ProjectID, containerDir)
	if err != nil {
		_ = metadataStore.Close()
		return nil, err
	}
	askService := askview.NewService(runtimeRegistry)
	approvalService := approvalview.NewService(runtimeRegistry)
	processService := processview.NewService(runtimeSupport.Background)
	processOutputService := processoutput.NewService(runtimeSupport.Background, runtimeSupport.Background)
	promptControlService := promptcontrol.NewService(runtimeRegistry)
	promptActivityService := promptactivity.NewService(runtimeRegistry)
	runtimeControlService := runtimecontrol.NewService(runtimeRegistry, runtimeRegistry)
	projectViews := client.NewLoopbackProjectViewClient(projectService)
	sessionViewService := sessionview.NewService(registry.NewPersistenceSessionResolver(projectSessionDir, storeOptions...), runtimeRegistry, metadataStore)
	sessionLaunchService := sessionlaunch.NewDeduplicatingService(
		sessionlaunch.ScopeID(cfg, projectSessionDir),
		sessionlaunch.NewService(launch.Planner{Config: cfg, ContainerDir: projectSessionDir, ProjectID: binding.ProjectID, ProjectViews: projectViews, StoreOptions: storeOptions}, sessionStoreRegistry),
	)
	sessionLifecycleService := sessionlifecycle.NewService(projectSessionDir, sessionStoreRegistry, authSupport.AuthManager, storeOptions...)
	sessionRuntimeService := sessionruntime.NewService(cfg.PersistenceRoot, metadataStore, authSupport.AuthManager, runtimeSupport.FastModeState, runtimeSupport.Background, runtimeSupport.BackgroundRouter, runtimeRegistry, sessionStoreRegistry, storeOptions...)
	sessionActivityService := sessionactivity.NewService(runtimeRegistry)
	core := &Core{
		cfg:              cfg,
		containerDir:     containerDir,
		oauthOpts:        authSupport,
		fastModeState:    runtimeSupport.FastModeState,
		background:       runtimeSupport.Background,
		backgroundRouter: runtimeSupport.BackgroundRouter,
		metadataStore:    metadataStore,
		runtimeRegistry:  runtimeRegistry,
		sessionStores:    sessionStoreRegistry,
		projectID:        projectService.ProjectID(),
		projectViews:     projectViews,
		askViews:         client.NewLoopbackAskViewClient(askService),
		approvalViews:    client.NewLoopbackApprovalViewClient(approvalService),
		processControls:  client.NewLoopbackProcessControlClient(processService),
		processOutput:    client.NewLoopbackProcessOutputClient(processOutputService),
		processViews:     client.NewLoopbackProcessViewClient(processService),
		promptControl:    client.NewLoopbackPromptControlClient(promptControlService),
		promptActivity:   client.NewLoopbackPromptActivityClient(promptActivityService),
		runtimeControls:  client.NewLoopbackRuntimeControlClient(runtimeControlService),
		sessionLaunch:    client.NewLoopbackSessionLaunchClient(sessionLaunchService),
		sessionRuntime:   client.NewLoopbackSessionRuntimeClient(sessionRuntimeService),
		sessionViews:     client.NewLoopbackSessionViewClient(sessionViewService),
		sessionLifecycle: client.NewLoopbackSessionLifecycleClient(sessionLifecycleService),
		sessionActivity:  client.NewLoopbackSessionActivityClient(sessionActivityService),
	}
	core.runPrompt = runprompt.NewLoopbackRunPromptClient(runprompt.HeadlessBootstrap{
		Config:           cfg,
		ContainerDir:     projectSessionDir,
		StoreOptions:     storeOptions,
		AuthManager:      authSupport.AuthManager,
		FastModeState:    runtimeSupport.FastModeState,
		Background:       runtimeSupport.Background,
		RuntimeRegistry:  runtimeRegistry,
		BackgroundRouter: runtimeSupport.BackgroundRouter,
	})
	return core, nil
}

func (s *Core) Close() error {
	if s == nil {
		return nil
	}
	var err error
	if s.background != nil {
		err = s.background.Close()
	}
	if s.metadataStore != nil {
		err = errors.Join(err, s.metadataStore.Close())
	}
	return err
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

func (s *Core) RuntimeControlClient() client.RuntimeControlClient {
	if s == nil {
		return nil
	}
	return s.runtimeControls
}

func (s *Core) PromptControlClient() client.PromptControlClient {
	if s == nil {
		return nil
	}
	return s.promptControl
}

func (s *Core) PromptActivityClient() client.PromptActivityClient {
	if s == nil {
		return nil
	}
	return s.promptActivity
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

func (s *Core) SessionLaunchClient() client.SessionLaunchClient {
	if s == nil {
		return nil
	}
	return s.sessionLaunch
}

func (s *Core) SessionRuntimeClient() client.SessionRuntimeClient {
	if s == nil {
		return nil
	}
	return s.sessionRuntime
}

func (s *Core) SessionLifecycleClient() client.SessionLifecycleClient {
	if s == nil {
		return nil
	}
	return s.sessionLifecycle
}

func (s *Core) RegisterSessionStore(store *session.Store) {
	if s == nil || s.sessionStores == nil {
		return
	}
	s.sessionStores.RegisterStore(store)
}

func (s *Core) ResolveSessionStore(sessionID string) (*session.Store, error) {
	if s == nil || s.sessionStores == nil {
		return nil, nil
	}
	return s.sessionStores.ResolveStore(context.Background(), sessionID)
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

func (s *Core) AwaitPromptResponse(ctx context.Context, sessionID string, req askquestion.Request) (askquestion.Response, error) {
	if s == nil || s.runtimeRegistry == nil {
		return askquestion.Response{}, fmt.Errorf("runtime registry is required")
	}
	return s.runtimeRegistry.AwaitPromptResponse(ctx, sessionID, req)
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

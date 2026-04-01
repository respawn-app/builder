package app

import (
	"context"
	"errors"
	"io"

	"builder/server/auth"
	serverembedded "builder/server/embedded"
	"builder/server/launch"
	"builder/server/primaryrun"
	"builder/server/runtime"
	"builder/server/session"
	"builder/server/sessioncontrol"
	askquestion "builder/server/tools/askquestion"
	"builder/shared/client"
	"builder/shared/config"
)

type embeddedServer interface {
	Close() error
	Config() config.App
	ProjectID() string
	ProjectViewClient() client.ProjectViewClient
	RunPromptClient() client.RunPromptClient
	ProcessControlClient() client.ProcessControlClient
	ProcessOutputClient() client.ProcessOutputClient
	ProcessViewClient() client.ProcessViewClient
	SessionLifecycleClient() client.SessionLifecycleClient
	SessionViewClient() client.SessionViewClient
	PlanSession(req sessionLaunchRequest, pick sessionPickerRunner) (sessionLaunchPlan, error)
	PrepareRuntime(plan sessionLaunchPlan, diagnosticWriter io.Writer, startLogLine string) (*runtimeLaunchPlan, error)
	Reauthenticate(ctx context.Context, interactor authInteractor) error
}

type embeddedAppServer struct {
	inner *serverembedded.Server
}

func newEmbeddedAppServer(inner *serverembedded.Server) *embeddedAppServer {
	if inner == nil {
		return nil
	}
	return &embeddedAppServer{inner: inner}
}

func (s *embeddedAppServer) Close() error {
	if s == nil || s.inner == nil {
		return nil
	}
	return s.inner.Close()
}

func (s *embeddedAppServer) Config() config.App {
	if s == nil || s.inner == nil {
		return config.App{}
	}
	return s.inner.Config()
}

func (s *embeddedAppServer) ProjectID() string {
	if s == nil || s.inner == nil {
		return ""
	}
	return s.inner.ProjectID()
}

func (s *embeddedAppServer) ProjectViewClient() client.ProjectViewClient {
	if s == nil || s.inner == nil {
		return nil
	}
	return s.inner.ProjectViewClient()
}

func (s *embeddedAppServer) RunPromptClient() client.RunPromptClient {
	if s == nil || s.inner == nil {
		return nil
	}
	return s.inner.RunPromptClient()
}

func (s *embeddedAppServer) SessionViewClient() client.SessionViewClient {
	if s == nil || s.inner == nil {
		return nil
	}
	return s.inner.SessionViewClient()
}

func (s *embeddedAppServer) SessionLifecycleClient() client.SessionLifecycleClient {
	if s == nil || s.inner == nil {
		return nil
	}
	return s.inner.SessionLifecycleClient()
}

func (s *embeddedAppServer) ProcessViewClient() client.ProcessViewClient {
	if s == nil || s.inner == nil {
		return nil
	}
	return s.inner.ProcessViewClient()
}

func (s *embeddedAppServer) ProcessControlClient() client.ProcessControlClient {
	if s == nil || s.inner == nil {
		return nil
	}
	return s.inner.ProcessControlClient()
}

func (s *embeddedAppServer) ProcessOutputClient() client.ProcessOutputClient {
	if s == nil || s.inner == nil {
		return nil
	}
	return s.inner.ProcessOutputClient()
}

func (s *embeddedAppServer) OAuthOptions() auth.OpenAIOAuthOptions {
	if s == nil || s.inner == nil {
		return auth.OpenAIOAuthOptions{}
	}
	return s.inner.OAuthOptions()
}

func (s *embeddedAppServer) AuthManager() *auth.Manager {
	if s == nil || s.inner == nil {
		return nil
	}
	return s.inner.AuthManager()
}

func (s *embeddedAppServer) ContainerDir() string {
	if s == nil || s.inner == nil {
		return ""
	}
	return s.inner.ContainerDir()
}

func (s *embeddedAppServer) PlanSession(req sessionLaunchRequest, pick sessionPickerRunner) (sessionLaunchPlan, error) {
	if s == nil || s.inner == nil {
		return sessionLaunchPlan{}, errors.New("embedded server is required")
	}
	cfg := s.inner.Config()
	controller := sessioncontrol.Controller{
		Config:       cfg,
		ContainerDir: s.inner.ContainerDir(),
		ProjectID:    s.inner.ProjectID(),
		ProjectViews: s.inner.ProjectViewClient(),
		AuthManager:  s.inner.AuthManager(),
		PickSession: func(summaries []session.Summary, theme string, alternateScreenPolicy config.TUIAlternateScreenPolicy) (launch.SessionSelection, error) {
			runPicker := pick
			if runPicker == nil {
				runPicker = func(summaries []session.Summary, theme string, alternateScreenPolicy config.TUIAlternateScreenPolicy) (sessionPickerResult, error) {
					return runSessionPicker(summaries, theme, alternateScreenPolicy)
				}
			}
			picked, err := runPicker(summaries, theme, alternateScreenPolicy)
			if err != nil {
				return launch.SessionSelection{}, err
			}
			return launch.SessionSelection{Session: picked.Session, CreateNew: picked.CreateNew, Canceled: picked.Canceled}, nil
		},
	}
	serverPlan, err := controller.PlanSession(launch.SessionRequest{
		Mode:              launch.Mode(req.Mode),
		SelectedSessionID: req.SelectedSessionID,
		ForceNewSession:   req.ForceNewSession,
		ParentSessionID:   req.ParentSessionID,
	})
	if err != nil {
		return sessionLaunchPlan{}, err
	}
	return sessionLaunchPlan{
		Mode:                req.Mode,
		Store:               serverPlan.Store,
		ActiveSettings:      serverPlan.ActiveSettings,
		EnabledTools:        serverPlan.EnabledTools,
		ConfiguredModelName: serverPlan.ConfiguredModelName,
		SessionName:         serverPlan.SessionName,
		ModelContractLocked: serverPlan.ModelContractLocked,
		StatusConfig: uiStatusConfig{
			WorkspaceRoot:   cfg.WorkspaceRoot,
			PersistenceRoot: cfg.PersistenceRoot,
			Settings:        serverPlan.ActiveSettings,
			Source:          serverPlan.Source,
			AuthManager:     s.inner.AuthManager(),
			AuthStatePath:   config.GlobalAuthConfigPath(cfg),
		},
		WorkspaceRoot: serverPlan.WorkspaceRoot,
		Source:        serverPlan.Source,
	}, nil
}

func (s *embeddedAppServer) PrepareRuntime(plan sessionLaunchPlan, diagnosticWriter io.Writer, startLogLine string) (*runtimeLaunchPlan, error) {
	if s == nil || s.inner == nil {
		return nil, errors.New("embedded server is required")
	}
	logger, err := newRunLogger(plan.Store.Dir(), func(diag runLoggerDiagnostic) {
		reportRunLoggerDiagnostic(diagnosticWriter, diag)
	})
	if err != nil {
		return nil, err
	}
	logLaunchPlanStart(logger, plan, startLogLine)
	wiring, err := newRuntimeWiringWithBackground(plan.Store, plan.ActiveSettings, plan.EnabledTools, plan.WorkspaceRoot, s.inner.AuthManager(), logger, s.inner.Background(), runtimeWiringOptions{
		FastMode: s.inner.FastModeState(),
		OnAskStart: func(req askquestion.Request) {
			s.inner.BeginPendingPrompt(plan.Store.Meta().SessionID, req)
		},
		OnAskDone: func(req askquestion.Request, _ askquestion.Response, _ error) {
			s.inner.CompletePendingPrompt(plan.Store.Meta().SessionID, req.ID)
		},
		OnEvent: func(evt runtime.Event) {
			s.inner.PublishRuntimeEvent(plan.Store.Meta().SessionID, evt)
		},
	})
	if err != nil {
		_ = logger.Close()
		return nil, err
	}
	if router := s.inner.BackgroundRouter(); router != nil {
		router.SetActiveSession(plan.Store.Meta().SessionID, wiring.engine)
	}
	s.inner.RegisterRuntime(plan.Store.Meta().SessionID, wiring.engine)
	wiring.processControls = s.inner.ProcessControlClient()
	wiring.processOutput = s.inner.ProcessOutputClient()
	wiring.processViews = s.inner.ProcessViewClient()
	wiring.sessionViews = s.inner.SessionViewClient()
	wiring.runtimeClient = primaryrun.NewGatedRuntimeClient(plan.Store.Meta().SessionID, newUIRuntimeClientWithReads(wiring.engine, wiring.sessionViews), s.inner)
	return &runtimeLaunchPlan{
		Logger: logger,
		Wiring: wiring,
		close: func() {
			s.inner.UnregisterRuntime(plan.Store.Meta().SessionID)
			if router := s.inner.BackgroundRouter(); router != nil {
				router.ClearActiveSession(plan.Store.Meta().SessionID)
			}
			_ = wiring.Close()
			_ = logger.Close()
		},
	}, nil
}

func (s *embeddedAppServer) Reauthenticate(ctx context.Context, interactor authInteractor) error {
	if s == nil || s.inner == nil {
		return errors.New("embedded server is required")
	}
	cfg := s.inner.Config()
	return ensureAuthReady(ctx, s.inner.AuthManager(), s.inner.OAuthOptions(), cfg.Settings.Theme, cfg.Settings.TUIAlternateScreen, interactor)
}

var _ embeddedServer = (*embeddedAppServer)(nil)

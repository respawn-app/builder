package app

import (
	"context"
	"errors"
	"io"

	"builder/server/auth"
	serverembedded "builder/server/embedded"
	"builder/server/launch"
	serverlifecycle "builder/server/lifecycle"
	"builder/server/session"
	"builder/server/sessioncontrol"
	"builder/shared/client"
	"builder/shared/config"
)

type embeddedServer interface {
	Close() error
	Config() config.App
	RunPromptClient() client.RunPromptClient
	PlanSession(req sessionLaunchRequest, pick sessionPickerRunner) (sessionLaunchPlan, error)
	PrepareRuntime(plan sessionLaunchPlan, diagnosticWriter io.Writer, startLogLine string) (*runtimeLaunchPlan, error)
	ResolveTransition(ctx context.Context, interactor authInteractor, store *session.Store, transition UITransition) (resolvedSessionAction, error)
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

func (s *embeddedAppServer) RunPromptClient() client.RunPromptClient {
	if s == nil || s.inner == nil {
		return nil
	}
	return s.inner.RunPromptClient()
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
	wiring, err := newRuntimeWiringWithBackground(plan.Store, plan.ActiveSettings, plan.EnabledTools, plan.WorkspaceRoot, s.inner.AuthManager(), logger, s.inner.Background(), runtimeWiringOptions{FastMode: s.inner.FastModeState()})
	if err != nil {
		_ = logger.Close()
		return nil, err
	}
	if router := s.inner.BackgroundRouter(); router != nil {
		router.SetActiveSession(plan.Store.Meta().SessionID, wiring.engine)
	}
	return &runtimeLaunchPlan{
		Logger: logger,
		Wiring: wiring,
		close: func() {
			if router := s.inner.BackgroundRouter(); router != nil {
				router.ClearActiveSession(plan.Store.Meta().SessionID)
			}
			_ = wiring.Close()
			_ = logger.Close()
		},
	}, nil
}

func (s *embeddedAppServer) ResolveTransition(ctx context.Context, interactor authInteractor, store *session.Store, transition UITransition) (resolvedSessionAction, error) {
	if s == nil || s.inner == nil {
		return resolvedSessionAction{}, errors.New("embedded server is required")
	}
	controller := sessioncontrol.Controller{
		Config:       s.inner.Config(),
		ContainerDir: s.inner.ContainerDir(),
		AuthManager:  s.inner.AuthManager(),
		Reauth: func(ctx context.Context) error {
			cfg := s.inner.Config()
			return ensureAuthReady(ctx, s.inner.AuthManager(), s.inner.OAuthOptions(), cfg.Settings.Theme, cfg.Settings.TUIAlternateScreen, interactor)
		},
	}
	resolved, err := controller.ResolveTransition(ctx, store, serverlifecycle.Transition{
		Action:               serverlifecycle.Action(transition.Action),
		InitialPrompt:        transition.InitialPrompt,
		InitialInput:         transition.InitialInput,
		TargetSessionID:      transition.TargetSessionID,
		ForkUserMessageIndex: transition.ForkUserMessageIndex,
		ParentSessionID:      transition.ParentSessionID,
	})
	if err != nil {
		return resolvedSessionAction{}, err
	}
	return resolvedSessionAction{
		NextSessionID:   resolved.NextSessionID,
		InitialPrompt:   resolved.InitialPrompt,
		InitialInput:    resolved.InitialInput,
		ParentSessionID: resolved.ParentSessionID,
		ForceNewSession: resolved.ForceNewSession,
		ShouldContinue:  resolved.ShouldContinue,
	}, nil
}

var _ embeddedServer = (*embeddedAppServer)(nil)

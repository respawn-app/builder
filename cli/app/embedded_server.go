package app

import (
	"context"
	"errors"
	"io"

	"builder/server/auth"
	serverembedded "builder/server/embedded"
	"builder/server/primaryrun"
	"builder/server/runtime"
	"builder/server/session"
	askquestion "builder/server/tools/askquestion"
	"builder/shared/client"
	"builder/shared/config"
)

type embeddedServer interface {
	Close() error
	Config() config.App
	AuthManager() *auth.Manager
	ProjectID() string
	ProjectViewClient() client.ProjectViewClient
	RunPromptClient() client.RunPromptClient
	ProcessControlClient() client.ProcessControlClient
	ProcessOutputClient() client.ProcessOutputClient
	ProcessViewClient() client.ProcessViewClient
	SessionLaunchClient() client.SessionLaunchClient
	SessionLifecycleClient() client.SessionLifecycleClient
	SessionViewClient() client.SessionViewClient
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

func (s *embeddedAppServer) AuthManager() *auth.Manager {
	if s == nil || s.inner == nil {
		return nil
	}
	return s.inner.AuthManager()
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

func (s *embeddedAppServer) SessionLaunchClient() client.SessionLaunchClient {
	if s == nil || s.inner == nil {
		return nil
	}
	return s.inner.SessionLaunchClient()
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

func (s *embeddedAppServer) ContainerDir() string {
	if s == nil || s.inner == nil {
		return ""
	}
	return s.inner.ContainerDir()
}

func (s *embeddedAppServer) PrepareRuntime(plan sessionLaunchPlan, diagnosticWriter io.Writer, startLogLine string) (*runtimeLaunchPlan, error) {
	if s == nil || s.inner == nil {
		return nil, errors.New("embedded server is required")
	}
	store, err := s.inner.ResolveSessionStore(plan.SessionID)
	if err != nil {
		return nil, err
	}
	if store == nil {
		store, err = session.OpenByID(s.inner.Config().PersistenceRoot, plan.SessionID)
		if err != nil {
			return nil, err
		}
		s.inner.RegisterSessionStore(store)
	}
	logger, err := newRunLogger(store.Dir(), func(diag runLoggerDiagnostic) {
		reportRunLoggerDiagnostic(diagnosticWriter, diag)
	})
	if err != nil {
		return nil, err
	}
	logLaunchPlanStart(logger, plan, startLogLine)
	wiring, err := newRuntimeWiringWithBackground(store, plan.ActiveSettings, plan.EnabledTools, plan.WorkspaceRoot, s.inner.AuthManager(), logger, s.inner.Background(), runtimeWiringOptions{
		FastMode: s.inner.FastModeState(),
		OnAskStart: func(req askquestion.Request) {
			s.inner.BeginPendingPrompt(plan.SessionID, req)
		},
		OnAskDone: func(req askquestion.Request, _ askquestion.Response, _ error) {
			s.inner.CompletePendingPrompt(plan.SessionID, req.ID)
		},
		OnEvent: func(evt runtime.Event) {
			s.inner.PublishRuntimeEvent(plan.SessionID, evt)
		},
	})
	if err != nil {
		_ = logger.Close()
		return nil, err
	}
	if router := s.inner.BackgroundRouter(); router != nil {
		router.SetActiveSession(plan.SessionID, wiring.engine)
	}
	s.inner.RegisterRuntime(plan.SessionID, wiring.engine)
	wiring.processControls = s.inner.ProcessControlClient()
	wiring.processOutput = s.inner.ProcessOutputClient()
	wiring.processViews = s.inner.ProcessViewClient()
	wiring.sessionViews = s.inner.SessionViewClient()
	wiring.runtimeClient = primaryrun.NewGatedRuntimeClient(plan.SessionID, newUIRuntimeClientWithReads(wiring.engine, wiring.sessionViews), s.inner)
	return &runtimeLaunchPlan{
		Logger: logger,
		Wiring: wiring,
		close: func() {
			s.inner.UnregisterRuntime(plan.SessionID)
			if router := s.inner.BackgroundRouter(); router != nil {
				router.ClearActiveSession(plan.SessionID)
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

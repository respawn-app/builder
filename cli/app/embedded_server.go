package app

import (
	"context"
	"errors"
	"io"

	"builder/server/auth"
	serverembedded "builder/server/embedded"
	"builder/shared/client"
	"builder/shared/config"
	"builder/shared/serverapi"
	"github.com/google/uuid"
)

type embeddedServer interface {
	Close() error
	Config() config.App
	AuthManager() *auth.Manager
	ProjectID() string
	ApprovalViewClient() client.ApprovalViewClient
	AskViewClient() client.AskViewClient
	PromptControlClient() client.PromptControlClient
	PromptActivityClient() client.PromptActivityClient
	ProjectViewClient() client.ProjectViewClient
	RunPromptClient() client.RunPromptClient
	ProcessControlClient() client.ProcessControlClient
	ProcessOutputClient() client.ProcessOutputClient
	ProcessViewClient() client.ProcessViewClient
	RuntimeControlClient() client.RuntimeControlClient
	SessionActivityClient() client.SessionActivityClient
	SessionLaunchClient() client.SessionLaunchClient
	SessionLifecycleClient() client.SessionLifecycleClient
	SessionRuntimeClient() client.SessionRuntimeClient
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

func (s *embeddedAppServer) AskViewClient() client.AskViewClient {
	if s == nil || s.inner == nil {
		return nil
	}
	return s.inner.AskViewClient()
}

func (s *embeddedAppServer) ApprovalViewClient() client.ApprovalViewClient {
	if s == nil || s.inner == nil {
		return nil
	}
	return s.inner.ApprovalViewClient()
}

func (s *embeddedAppServer) PromptControlClient() client.PromptControlClient {
	if s == nil || s.inner == nil {
		return nil
	}
	return s.inner.PromptControlClient()
}

func (s *embeddedAppServer) PromptActivityClient() client.PromptActivityClient {
	if s == nil || s.inner == nil {
		return nil
	}
	return s.inner.PromptActivityClient()
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

func (s *embeddedAppServer) SessionActivityClient() client.SessionActivityClient {
	if s == nil || s.inner == nil {
		return nil
	}
	return s.inner.SessionActivityClient()
}

func (s *embeddedAppServer) SessionRuntimeClient() client.SessionRuntimeClient {
	if s == nil || s.inner == nil {
		return nil
	}
	return s.inner.SessionRuntimeClient()
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

func (s *embeddedAppServer) RuntimeControlClient() client.RuntimeControlClient {
	if s == nil || s.inner == nil {
		return nil
	}
	return s.inner.RuntimeControlClient()
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
	return prepareSharedRuntime(s, plan, diagnosticWriter, startLogLine)
}

func prepareSharedRuntime(server embeddedServer, plan sessionLaunchPlan, diagnosticWriter io.Writer, startLogLine string) (*runtimeLaunchPlan, error) {
	if server == nil {
		return nil, errors.New("server is required")
	}
	toolIDs := make([]string, 0, len(plan.EnabledTools))
	for _, id := range plan.EnabledTools {
		toolIDs = append(toolIDs, string(id))
	}
	activateReq := serverapi.SessionRuntimeActivateRequest{
		ClientRequestID: uuid.NewString(),
		SessionID:       plan.SessionID,
		ActiveSettings:  plan.ActiveSettings,
		EnabledToolIDs:  toolIDs,
		WorkspaceRoot:   plan.WorkspaceRoot,
		Source:          plan.Source,
	}
	if err := server.SessionRuntimeClient().ActivateSessionRuntime(context.Background(), activateReq); err != nil {
		return nil, err
	}
	sub, err := server.SessionActivityClient().SubscribeSessionActivity(context.Background(), serverapi.SessionActivitySubscribeRequest{SessionID: plan.SessionID})
	if err != nil {
		_ = server.SessionRuntimeClient().ReleaseSessionRuntime(context.Background(), serverapi.SessionRuntimeReleaseRequest{ClientRequestID: uuid.NewString(), SessionID: plan.SessionID})
		return nil, err
	}
	promptSub, err := server.PromptActivityClient().SubscribePromptActivity(context.Background(), serverapi.PromptActivitySubscribeRequest{SessionID: plan.SessionID})
	if err != nil {
		_ = sub.Close()
		_ = server.SessionRuntimeClient().ReleaseSessionRuntime(context.Background(), serverapi.SessionRuntimeReleaseRequest{ClientRequestID: uuid.NewString(), SessionID: plan.SessionID})
		return nil, err
	}
	runtimeEvents, stopRuntimeEvents := startSessionActivityEvents(context.Background(), sub)
	askEvents, stopAskEvents := startPendingPromptEvents(context.Background(), promptSub, server.PromptControlClient())
	logger := &runLogger{}
	_ = diagnosticWriter
	logger.Logf("%s", startLogLine)
	wiring := &runtimeWiring{
		runtimeEvents:   runtimeEvents,
		askEvents:       askEvents,
		background:      nil,
		runtimeClient:   newUIRuntimeClientWithReads(plan.SessionID, server.SessionViewClient(), server.RuntimeControlClient()),
		promptControl:   server.PromptControlClient(),
		runtimeControls: server.RuntimeControlClient(),
		processControls: server.ProcessControlClient(),
		processOutput:   server.ProcessOutputClient(),
		processViews:    server.ProcessViewClient(),
		approvalViews:   server.ApprovalViewClient(),
		askViews:        server.AskViewClient(),
		sessionActivity: server.SessionActivityClient(),
		sessionViews:    server.SessionViewClient(),
	}
	return &runtimeLaunchPlan{
		Logger: logger,
		Wiring: wiring,
		close: func() {
			stopAskEvents()
			stopRuntimeEvents()
			_ = server.SessionRuntimeClient().ReleaseSessionRuntime(context.Background(), serverapi.SessionRuntimeReleaseRequest{ClientRequestID: uuid.NewString(), SessionID: plan.SessionID})
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

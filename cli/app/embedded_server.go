package app

import (
	"context"
	"errors"
	"io"
	"time"

	"builder/server/auth"
	serverembedded "builder/server/embedded"
	"builder/shared/client"
	"builder/shared/config"
	"builder/shared/serverapi"
	"github.com/google/uuid"
)

const runtimeReleaseTimeout = 3 * time.Second

type embeddedServer interface {
	Close() error
	OwnsServer() bool
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
	PrepareRuntime(ctx context.Context, plan sessionLaunchPlan, diagnosticWriter io.Writer, startLogLine string) (*runtimeLaunchPlan, error)
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

func (s *embeddedAppServer) OwnsServer() bool {
	return s != nil && s.inner != nil
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

func (s *embeddedAppServer) PrepareRuntime(ctx context.Context, plan sessionLaunchPlan, diagnosticWriter io.Writer, startLogLine string) (*runtimeLaunchPlan, error) {
	if s == nil || s.inner == nil {
		return nil, errors.New("embedded server is required")
	}
	return prepareSharedRuntime(ctx, s, plan, diagnosticWriter, startLogLine)
}

func prepareSharedRuntime(ctx context.Context, server embeddedServer, plan sessionLaunchPlan, diagnosticWriter io.Writer, startLogLine string) (*runtimeLaunchPlan, error) {
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
	if err := server.SessionRuntimeClient().ActivateSessionRuntime(ctx, activateReq); err != nil {
		return nil, err
	}
	sub, err := server.SessionActivityClient().SubscribeSessionActivity(ctx, serverapi.SessionActivitySubscribeRequest{SessionID: plan.SessionID})
	if err != nil {
		releaseSharedRuntime(server.SessionRuntimeClient(), plan.SessionID)
		return nil, err
	}
	promptSub, err := server.PromptActivityClient().SubscribePromptActivity(ctx, serverapi.PromptActivitySubscribeRequest{SessionID: plan.SessionID})
	if err != nil {
		_ = sub.Close()
		releaseSharedRuntime(server.SessionRuntimeClient(), plan.SessionID)
		return nil, err
	}
	logger := &runLogger{}
	_ = diagnosticWriter
	logger.Logf("%s", startLogLine)
	runtimeEvents, stopRuntimeEvents := startSessionActivityEvents(ctx, sub, func(ctx context.Context) (serverapi.SessionActivitySubscription, error) {
		return server.SessionActivityClient().SubscribeSessionActivity(ctx, serverapi.SessionActivitySubscribeRequest{SessionID: plan.SessionID})
	}, func(line string) {
		logger.Logf("%s", line)
	})
	askEvents, stopAskEvents := startPendingPromptEvents(ctx, promptSub, func(ctx context.Context) (serverapi.PromptActivitySubscription, error) {
		return server.PromptActivityClient().SubscribePromptActivity(ctx, serverapi.PromptActivitySubscribeRequest{SessionID: plan.SessionID})
	}, func(ctx context.Context) (map[string]struct{}, error) {
		return listPendingPromptIDs(ctx, plan.SessionID, server.AskViewClient(), server.ApprovalViewClient())
	}, server.PromptControlClient())
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
			releaseSharedRuntime(server.SessionRuntimeClient(), plan.SessionID)
		},
	}, nil
}

func releaseSharedRuntime(client serverapi.SessionRuntimeService, sessionID string) {
	if client == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), runtimeReleaseTimeout)
	defer cancel()
	_ = client.ReleaseSessionRuntime(ctx, serverapi.SessionRuntimeReleaseRequest{ClientRequestID: uuid.NewString(), SessionID: sessionID})
}

func listPendingPromptIDs(ctx context.Context, sessionID string, askViews client.AskViewClient, approvalViews client.ApprovalViewClient) (map[string]struct{}, error) {
	ids := make(map[string]struct{})
	if askViews != nil {
		resp, err := askViews.ListPendingAsksBySession(ctx, serverapi.AskListPendingBySessionRequest{SessionID: sessionID})
		if err != nil {
			return nil, err
		}
		for _, ask := range resp.Asks {
			ids[ask.AskID] = struct{}{}
		}
	}
	if approvalViews != nil {
		resp, err := approvalViews.ListPendingApprovalsBySession(ctx, serverapi.ApprovalListPendingBySessionRequest{SessionID: sessionID})
		if err != nil {
			return nil, err
		}
		for _, approval := range resp.Approvals {
			ids[approval.ApprovalID] = struct{}{}
		}
	}
	return ids, nil
}

func (s *embeddedAppServer) Reauthenticate(ctx context.Context, interactor authInteractor) error {
	if s == nil || s.inner == nil {
		return errors.New("embedded server is required")
	}
	cfg := s.inner.Config()
	return ensureAuthReady(ctx, s.inner.AuthManager(), s.inner.OAuthOptions(), cfg.Settings.Theme, cfg.Settings.TUIAlternateScreen, interactor)
}

var _ embeddedServer = (*embeddedAppServer)(nil)

package app

import (
	"context"
	"errors"
	"io"
	"strings"
	"time"

	"builder/server/auth"
	serverbootstrap "builder/server/bootstrap"
	"builder/shared/client"
	"builder/shared/config"
	"builder/shared/protocol"
)

type remoteAppServer struct {
	remote   *client.Remote
	identity protocol.ServerIdentity
	cfg      config.App
}

func newRemoteAppServer(remote *client.Remote, cfg config.App) *remoteAppServer {
	if remote == nil {
		return nil
	}
	return &remoteAppServer{remote: remote, identity: remote.Identity(), cfg: cfg}
}

func (s *remoteAppServer) Close() error {
	if s == nil || s.remote == nil {
		return nil
	}
	return s.remote.Close()
}
func (s *remoteAppServer) Config() config.App {
	if s == nil {
		return config.App{}
	}
	return s.cfg
}
func (s *remoteAppServer) AuthManager() *auth.Manager { return nil }
func (s *remoteAppServer) ProjectID() string {
	if s == nil {
		return ""
	}
	return strings.TrimSpace(s.identity.ProjectID)
}
func (s *remoteAppServer) AskViewClient() client.AskViewClient {
	if s == nil {
		return nil
	}
	return s.remote
}
func (s *remoteAppServer) ApprovalViewClient() client.ApprovalViewClient {
	if s == nil {
		return nil
	}
	return s.remote
}
func (s *remoteAppServer) PromptControlClient() client.PromptControlClient {
	if s == nil {
		return nil
	}
	return s.remote
}
func (s *remoteAppServer) PromptActivityClient() client.PromptActivityClient {
	if s == nil {
		return nil
	}
	return s.remote
}
func (s *remoteAppServer) ProjectViewClient() client.ProjectViewClient {
	if s == nil {
		return nil
	}
	return s.remote
}
func (s *remoteAppServer) RunPromptClient() client.RunPromptClient {
	if s == nil {
		return nil
	}
	return s.remote
}
func (s *remoteAppServer) ProcessControlClient() client.ProcessControlClient {
	if s == nil {
		return nil
	}
	return s.remote
}
func (s *remoteAppServer) ProcessOutputClient() client.ProcessOutputClient {
	if s == nil {
		return nil
	}
	return s.remote
}
func (s *remoteAppServer) ProcessViewClient() client.ProcessViewClient {
	if s == nil {
		return nil
	}
	return s.remote
}
func (s *remoteAppServer) RuntimeControlClient() client.RuntimeControlClient {
	if s == nil {
		return nil
	}
	return s.remote
}
func (s *remoteAppServer) SessionActivityClient() client.SessionActivityClient {
	if s == nil {
		return nil
	}
	return s.remote
}
func (s *remoteAppServer) SessionLaunchClient() client.SessionLaunchClient {
	if s == nil {
		return nil
	}
	return s.remote
}
func (s *remoteAppServer) SessionLifecycleClient() client.SessionLifecycleClient {
	if s == nil {
		return nil
	}
	return s.remote
}
func (s *remoteAppServer) SessionRuntimeClient() client.SessionRuntimeClient {
	if s == nil {
		return nil
	}
	return s.remote
}
func (s *remoteAppServer) SessionViewClient() client.SessionViewClient {
	if s == nil {
		return nil
	}
	return s.remote
}
func (s *remoteAppServer) PrepareRuntime(ctx context.Context, plan sessionLaunchPlan, diagnosticWriter io.Writer, startLogLine string) (*runtimeLaunchPlan, error) {
	if s == nil || s.remote == nil {
		return nil, errors.New("remote server is required")
	}
	return prepareSharedRuntime(ctx, s, plan, diagnosticWriter, startLogLine)
}

func (s *remoteAppServer) Reauthenticate(ctx context.Context, interactor authInteractor) error {
	if s == nil {
		return errors.New("remote server is required")
	}
	if interactor == nil {
		return errors.New("auth interactor is required")
	}
	authSupport, err := serverbootstrap.BuildAuthSupport(
		interactor.WrapStore(auth.NewFileStore(config.GlobalAuthConfigPath(s.cfg))),
		interactor.LookupEnv,
		time.Now,
	)
	if err != nil {
		return err
	}
	return ensureAuthReady(ctx, authSupport.AuthManager, authSupport.OAuthOptions, s.cfg.Settings.Theme, s.cfg.Settings.TUIAlternateScreen, interactor)
}

package serve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	"builder/server/core"
	"builder/server/startup"
	"builder/server/transport"
	"builder/shared/discovery"
	"builder/shared/protocol"
)

type Server struct {
	*core.Core
	ready bool
}

func Start(ctx context.Context, req startup.Request, authHandler startup.AuthHandler, onboardingHandler startup.OnboardingHandler) (*Server, error) {
	appCore, err := startup.StartCore(ctx, req, authHandler, onboardingHandler)
	if err != nil {
		return nil, err
	}
	return &Server{Core: appCore}, nil
}

func Run(ctx context.Context, req startup.Request, authHandler startup.AuthHandler, onboardingHandler startup.OnboardingHandler) error {
	server, err := Start(ctx, req, authHandler, onboardingHandler)
	if err != nil {
		return err
	}
	defer func() { _ = server.Close() }()
	return server.Serve(ctx)
}

func (s *Server) Serve(ctx context.Context) error {
	if ctx == nil {
		return errors.New("context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil || s.Core == nil {
		return errors.New("server core is required")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("listen local control endpoint: %w", err)
	}
	defer func() { _ = listener.Close() }()

	baseURL := "http://" + listener.Addr().String()
	identity := protocol.ServerIdentity{
		ProtocolVersion: protocol.Version,
		ServerID:        fmt.Sprintf("%s:%d", s.ProjectID(), os.Getpid()),
		ProjectID:       s.ProjectID(),
		WorkspaceRoot:   s.Config().WorkspaceRoot,
		PID:             os.Getpid(),
		Capabilities: protocol.CapabilityFlags{
			JSONRPCWebSocket:  true,
			ProjectAttach:     true,
			SessionAttach:     true,
			HealthEndpoint:    true,
			ReadinessEndpoint: true,
			RunPrompt:         true,
			SessionPlan:       true,
			SessionLifecycle:  true,
			SessionRuntime:    true,
			RuntimeControl:    true,
			PromptControl:     true,
			PromptActivity:    true,
			SessionActivity:   true,
			ProcessOutput:     true,
		},
	}
	gateway, err := transport.NewGateway(s.Core, identity)
	if err != nil {
		return err
	}
	record := protocol.DiscoveryRecord{
		Identity:  identity,
		HTTPURL:   baseURL,
		RPCURL:    "ws://" + listener.Addr().String() + protocol.RPCPath,
		HealthURL: baseURL + protocol.HealthPath,
		ReadyURL:  baseURL + protocol.ReadinessPath,
		StartedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	discoveryPath, err := discovery.PathForContainer(s.ContainerDir())
	if err != nil {
		return err
	}
	if err := discovery.Write(discoveryPath, record); err != nil {
		return err
	}
	defer func() { _ = discovery.Remove(discoveryPath) }()

	mux := http.NewServeMux()
	mux.HandleFunc(protocol.HealthPath, func(w http.ResponseWriter, _ *http.Request) {
		writeStatusJSON(w, http.StatusOK, map[string]any{
			"status":         "ok",
			"project_id":     s.ProjectID(),
			"workspace_root": s.Config().WorkspaceRoot,
		})
	})
	s.ready = true
	mux.HandleFunc(protocol.ReadinessPath, func(w http.ResponseWriter, _ *http.Request) {
		status := http.StatusServiceUnavailable
		body := map[string]any{"ready": false}
		if s.ready {
			status = http.StatusOK
			body = map[string]any{
				"ready":          true,
				"project_id":     s.ProjectID(),
				"workspace_root": s.Config().WorkspaceRoot,
			}
		}
		writeStatusJSON(w, status, body)
	})
	mux.Handle(protocol.RPCPath, gateway.Handler())

	httpServer := &http.Server{Handler: mux}
	errCh := make(chan error, 1)
	go func() {
		serveErr := httpServer.Serve(listener)
		if serveErr == nil || errors.Is(serveErr, http.ErrServerClosed) {
			errCh <- nil
			return
		}
		errCh <- serveErr
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
		<-errCh
		return ctx.Err()
	case serveErr := <-errCh:
		return serveErr
	}
}

func writeStatusJSON(w http.ResponseWriter, status int, body map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

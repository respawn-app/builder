package serve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"builder/server/core"
	"builder/server/startup"
	"builder/server/transport"
	"builder/shared/config"
	"builder/shared/protocol"
)

type Server struct {
	*core.Core
	ready atomic.Bool
}

var (
	testListenReservationsMu sync.Mutex
	testListenReservations   = map[string]net.Listener{}
)

// ReserveTestListenReservation keeps a test-owned listener alive until the
// configured daemon bind path is ready to claim the same address.
func ReserveTestListenReservation(listener net.Listener) {
	if listener == nil {
		return
	}
	addr := strings.TrimSpace(listener.Addr().String())
	if addr == "" {
		_ = listener.Close()
		return
	}
	testListenReservationsMu.Lock()
	if existing := testListenReservations[addr]; existing != nil {
		_ = existing.Close()
	}
	testListenReservations[addr] = listener
	testListenReservationsMu.Unlock()
}

func ReleaseTestListenReservation(addr string) {
	trimmed := strings.TrimSpace(addr)
	if trimmed == "" {
		return
	}
	testListenReservationsMu.Lock()
	listener := testListenReservations[trimmed]
	delete(testListenReservations, trimmed)
	testListenReservationsMu.Unlock()
	if listener != nil {
		_ = listener.Close()
	}
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
	listenAddress := config.ServerListenAddress(s.Config())
	ReleaseTestListenReservation(listenAddress)
	listener, err := net.Listen("tcp", listenAddress)
	if err != nil {
		return fmt.Errorf("listen local control endpoint: %w", err)
	}
	defer func() { _ = listener.Close() }()

	identity := protocol.ServerIdentity{
		ProtocolVersion: protocol.Version,
		ServerID:        fmt.Sprintf("builder:%d", os.Getpid()),
		PID:             os.Getpid(),
		Capabilities: protocol.CapabilityFlags{
			JSONRPCWebSocket:        true,
			ProjectAttach:           true,
			SessionAttach:           true,
			HealthEndpoint:          true,
			ReadinessEndpoint:       true,
			RunPrompt:               true,
			SessionPlan:             true,
			SessionLifecycle:        true,
			SessionTranscriptPaging: true,
			SessionRuntime:          true,
			RuntimeControl:          true,
			PromptControl:           true,
			PromptActivity:          true,
			SessionActivity:         true,
			ProcessOutput:           true,
		},
	}
	gateway, err := transport.NewGateway(s.Core, identity)
	if err != nil {
		return err
	}

	mux := http.NewServeMux()
	mux.HandleFunc(protocol.HealthPath, func(w http.ResponseWriter, _ *http.Request) {
		writeStatusJSON(w, http.StatusOK, map[string]any{
			"status":    "ok",
			"server_id": identity.ServerID,
			"pid":       identity.PID,
		})
	})
	s.ready.Store(true)
	mux.HandleFunc(protocol.ReadinessPath, func(w http.ResponseWriter, _ *http.Request) {
		status := http.StatusServiceUnavailable
		body := map[string]any{"ready": false}
		if s.ready.Load() {
			status = http.StatusOK
			body = map[string]any{
				"ready":     true,
				"server_id": identity.ServerID,
				"pid":       identity.PID,
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

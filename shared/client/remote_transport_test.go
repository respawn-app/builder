package client

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"builder/shared/config"
	"builder/shared/protocol"
	"builder/shared/serverapi"
	"golang.org/x/net/websocket"
)

func TestDialConfiguredRemotePrefersLocalUnixSocket(t *testing.T) {
	cfg := config.App{PersistenceRoot: t.TempDir(), Settings: config.Settings{ServerHost: "127.0.0.1", ServerPort: 1}}
	socketPath, ok, err := config.ServerLocalRPCSocketPath(cfg)
	if err != nil {
		t.Fatalf("ServerLocalRPCSocketPath: %v", err)
	}
	if !ok {
		t.Skip("local unix sockets unsupported on this platform")
	}
	shutdown := startUnixWebSocketServer(t, socketPath, func(ws *websocket.Conn) {
		defer func() { _ = ws.Close() }()
		handshakeRemoteConn(t, ws)
		serveProjectListRPC(t, ws)
	})
	defer shutdown()

	remote, err := DialConfiguredRemote(context.Background(), cfg)
	if err != nil {
		t.Fatalf("DialConfiguredRemote: %v", err)
	}
	defer func() { _ = remote.Close() }()

	if _, err := remote.ListProjects(context.Background(), serverapi.ProjectListRequest{}); err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
}

func TestDialConfiguredRemoteFallsBackToTCPWhenLocalUnixSocketMissing(t *testing.T) {
	server := httptest.NewServer(websocket.Handler(func(ws *websocket.Conn) {
		defer func() { _ = ws.Close() }()
		handshakeRemoteConn(t, ws)
		serveProjectListRPC(t, ws)
	}))
	defer server.Close()

	cfg := testRemoteConfigFromServerURL(t, t.TempDir(), server.URL)
	socketPath, ok, err := config.ServerLocalRPCSocketPath(cfg)
	if err != nil {
		t.Fatalf("ServerLocalRPCSocketPath: %v", err)
	}
	if ok {
		_ = os.Remove(socketPath)
	}

	remote, err := DialConfiguredRemote(context.Background(), cfg)
	if err != nil {
		t.Fatalf("DialConfiguredRemote: %v", err)
	}
	defer func() { _ = remote.Close() }()

	if _, err := remote.ListProjects(context.Background(), serverapi.ProjectListRequest{}); err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
}

func TestDialConfiguredRemoteFallsBackToTCPWhenLocalUnixHandshakeStalls(t *testing.T) {
	server := httptest.NewServer(websocket.Handler(func(ws *websocket.Conn) {
		defer func() { _ = ws.Close() }()
		handshakeRemoteConn(t, ws)
		serveProjectListRPC(t, ws)
	}))
	defer server.Close()

	cfg := testRemoteConfigFromServerURL(t, t.TempDir(), server.URL)
	socketPath, ok, err := config.ServerLocalRPCSocketPath(cfg)
	if err != nil {
		t.Fatalf("ServerLocalRPCSocketPath: %v", err)
	}
	if !ok {
		t.Skip("local unix sockets unsupported on this platform")
	}
	stallListener, stallAccepted := startUnixStallingListener(t, socketPath, 5*time.Second)
	defer func() { _ = stallListener.Close() }()
	defer func() { _ = os.Remove(socketPath) }()

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	start := time.Now()
	remote, err := DialConfiguredRemote(ctx, cfg)
	if err != nil {
		t.Fatalf("DialConfiguredRemote: %v", err)
	}
	defer func() { _ = remote.Close() }()
	if elapsed := time.Since(start); elapsed >= 500*time.Millisecond {
		t.Fatalf("DialConfiguredRemote elapsed = %v, want < 500ms", elapsed)
	}
	select {
	case <-stallAccepted:
	case <-time.After(time.Second):
		t.Fatal("expected stalled unix listener accept")
	}
	if _, err := remote.ListProjects(context.Background(), serverapi.ProjectListRequest{}); err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
}

func TestRemoteCanceledUnaryRequestKeepsPersistentControlConnection(t *testing.T) {
	var connectionCount atomic.Int32
	firstRequestSeen := make(chan string, 1)
	server := httptest.NewServer(websocket.Handler(func(ws *websocket.Conn) {
		connectionCount.Add(1)
		defer func() { _ = ws.Close() }()
		handshakeRemoteConn(t, ws)
		firstRequestID := ""
		for {
			var req protocol.Request
			if err := websocket.JSON.Receive(ws, &req); err != nil {
				return
			}
			switch req.Method {
			case protocol.MethodProjectList:
				firstRequestID = req.ID
				firstRequestSeen <- firstRequestID
			case protocol.MethodProjectResolvePath:
				if firstRequestID == "" {
					t.Fatal("expected first request id before second call")
				}
				if err := websocket.JSON.Send(ws, protocol.NewSuccessResponse(req.ID, serverapi.ProjectResolvePathResponse{CanonicalRoot: "/tmp/workspace-a"})); err != nil {
					t.Fatalf("send second response: %v", err)
				}
				if err := websocket.JSON.Send(ws, protocol.NewSuccessResponse(firstRequestID, serverapi.ProjectListResponse{})); err != nil {
					t.Fatalf("send late first response: %v", err)
				}
				return
			default:
				t.Fatalf("unexpected unary method %q", req.Method)
			}
		}
	}))
	defer server.Close()

	remote, err := DialRemoteURL(context.Background(), "ws"+server.URL[len("http"):])
	if err != nil {
		t.Fatalf("DialRemoteURL: %v", err)
	}
	defer func() { _ = remote.Close() }()

	cancelCtx, cancel := context.WithCancel(context.Background())
	firstErr := make(chan error, 1)
	go func() {
		_, err := remote.ListProjects(cancelCtx, serverapi.ProjectListRequest{})
		firstErr <- err
	}()

	select {
	case <-firstRequestSeen:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for first unary request")
	}
	cancel()
	if err := <-firstErr; !errors.Is(err, context.Canceled) {
		t.Fatalf("ListProjects error = %v, want context canceled", err)
	}

	resolveResp, err := remote.ResolveProjectPath(context.Background(), serverapi.ProjectResolvePathRequest{Path: "/tmp/workspace-a"})
	if err != nil {
		t.Fatalf("ResolveProjectPath: %v", err)
	}
	if resolveResp.CanonicalRoot != "/tmp/workspace-a" {
		t.Fatalf("CanonicalRoot = %q, want /tmp/workspace-a", resolveResp.CanonicalRoot)
	}
	if got := connectionCount.Load(); got != 1 {
		t.Fatalf("connectionCount = %d, want 1", got)
	}
}

func TestRemoteReconnectsUnaryControlConnectionAfterDrop(t *testing.T) {
	var connectionCount atomic.Int32
	server := httptest.NewServer(websocket.Handler(func(ws *websocket.Conn) {
		connIndex := connectionCount.Add(1)
		defer func() { _ = ws.Close() }()
		handshakeRemoteConn(t, ws)
		if connIndex == 1 {
			var req protocol.Request
			if err := websocket.JSON.Receive(ws, &req); err != nil {
				t.Fatalf("receive first request: %v", err)
			}
			if req.Method != protocol.MethodProjectList {
				t.Fatalf("first method = %q, want %q", req.Method, protocol.MethodProjectList)
			}
			if err := websocket.JSON.Send(ws, protocol.NewSuccessResponse(req.ID, serverapi.ProjectListResponse{})); err != nil {
				t.Fatalf("send first response: %v", err)
			}
			return
		}
		var req protocol.Request
		if err := websocket.JSON.Receive(ws, &req); err != nil {
			t.Fatalf("receive second request: %v", err)
		}
		if req.Method != protocol.MethodProjectResolvePath {
			t.Fatalf("second method = %q, want %q", req.Method, protocol.MethodProjectResolvePath)
		}
		if err := websocket.JSON.Send(ws, protocol.NewSuccessResponse(req.ID, serverapi.ProjectResolvePathResponse{CanonicalRoot: "/tmp/reconnected"})); err != nil {
			t.Fatalf("send second response: %v", err)
		}
	}))
	defer server.Close()

	remote, err := DialRemoteURL(context.Background(), "ws"+server.URL[len("http"):])
	if err != nil {
		t.Fatalf("DialRemoteURL: %v", err)
	}
	defer func() { _ = remote.Close() }()

	if _, err := remote.ListProjects(context.Background(), serverapi.ProjectListRequest{}); err != nil {
		t.Fatalf("ListProjects: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		remote.mu.Lock()
		controlDone := remote.control == nil || remote.control.IsDone()
		remote.mu.Unlock()
		if controlDone {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for dropped control connection")
		}
		time.Sleep(10 * time.Millisecond)
	}

	resp, err := remote.ResolveProjectPath(context.Background(), serverapi.ProjectResolvePathRequest{Path: "/tmp/reconnected"})
	if err != nil {
		t.Fatalf("ResolveProjectPath after reconnect: %v", err)
	}
	if resp.CanonicalRoot != "/tmp/reconnected" {
		t.Fatalf("CanonicalRoot = %q, want /tmp/reconnected", resp.CanonicalRoot)
	}
	if got := connectionCount.Load(); got != 2 {
		t.Fatalf("connectionCount = %d, want 2", got)
	}
}

func startUnixWebSocketServer(t *testing.T, socketPath string, handler func(*websocket.Conn)) func() {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	_ = os.Remove(socketPath)
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("Listen unix: %v", err)
	}
	httpServer := &http.Server{Handler: websocket.Handler(handler)}
	errCh := make(chan error, 1)
	go func() { errCh <- httpServer.Serve(listener) }()
	return func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
		select {
		case err := <-errCh:
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				t.Fatalf("unix websocket server: %v", err)
			}
		default:
		}
		_ = os.Remove(socketPath)
	}
}

func startUnixStallingListener(t *testing.T, socketPath string, stall time.Duration) (net.Listener, <-chan struct{}) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	_ = os.Remove(socketPath)
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("Listen unix: %v", err)
	}
	accepted := make(chan struct{}, 1)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			accepted <- struct{}{}
			go func(conn net.Conn) {
				defer func() { _ = conn.Close() }()
				time.Sleep(stall)
			}(conn)
		}
	}()
	return listener, accepted
}

func testRemoteConfigFromServerURL(t *testing.T, persistenceRoot string, serverURL string) config.App {
	t.Helper()
	parsed, err := url.Parse(serverURL)
	if err != nil {
		t.Fatalf("Parse server URL: %v", err)
	}
	host, portValue, err := net.SplitHostPort(parsed.Host)
	if err != nil {
		t.Fatalf("SplitHostPort: %v", err)
	}
	port, err := strconv.Atoi(portValue)
	if err != nil {
		t.Fatalf("Atoi port: %v", err)
	}
	return config.App{PersistenceRoot: persistenceRoot, Settings: config.Settings{ServerHost: host, ServerPort: port}}
}

func handshakeRemoteConn(t *testing.T, ws *websocket.Conn) {
	t.Helper()
	var req protocol.Request
	if err := websocket.JSON.Receive(ws, &req); err != nil {
		t.Fatalf("receive handshake: %v", err)
	}
	if req.Method != protocol.MethodHandshake {
		t.Fatalf("handshake method = %q", req.Method)
	}
	if err := websocket.JSON.Send(ws, protocol.NewSuccessResponse(req.ID, protocol.HandshakeResponse{Identity: protocol.ServerIdentity{ProtocolVersion: protocol.Version, ServerID: "server-1"}})); err != nil {
		t.Fatalf("send handshake response: %v", err)
	}
}

func serveProjectListRPC(t *testing.T, ws *websocket.Conn) {
	t.Helper()
	for {
		var req protocol.Request
		if err := websocket.JSON.Receive(ws, &req); err != nil {
			return
		}
		if req.Method != protocol.MethodProjectList {
			t.Fatalf("project list method = %q", req.Method)
		}
		if err := websocket.JSON.Send(ws, protocol.NewSuccessResponse(req.ID, serverapi.ProjectListResponse{})); err != nil {
			t.Fatalf("send project list response: %v", err)
		}
	}
}

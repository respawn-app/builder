package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"builder/shared/clientui"
	"builder/shared/protocol"
	"builder/shared/rpcwire"
	"builder/shared/serverapi"
)

func BenchmarkRemoteGetSessionMainViewPersistent(b *testing.B) {
	benchmarkRemoteGetSessionMainViewPersistent(b, rpcwire.NewWebSocketTransport())
}

func BenchmarkRemoteGetSessionMainViewPersistentGWS(b *testing.B) {
	benchmarkRemoteGetSessionMainViewPersistent(b, rpcwire.NewGWSTransport())
}

func BenchmarkLegacyRedialGetSessionMainView(b *testing.B) {
	benchmarkLegacyRedialGetSessionMainView(b, rpcwire.NewWebSocketTransport())
}

func BenchmarkLegacyRedialGetSessionMainViewGWS(b *testing.B) {
	benchmarkLegacyRedialGetSessionMainView(b, rpcwire.NewGWSTransport())
}

func BenchmarkRemoteGetSessionTranscriptPagePersistent(b *testing.B) {
	benchmarkRemoteGetSessionTranscriptPagePersistent(b, rpcwire.NewWebSocketTransport())
}

func BenchmarkRemoteGetSessionTranscriptPagePersistentGWS(b *testing.B) {
	benchmarkRemoteGetSessionTranscriptPagePersistent(b, rpcwire.NewGWSTransport())
}

func BenchmarkLegacyRedialGetSessionTranscriptPage(b *testing.B) {
	benchmarkLegacyRedialGetSessionTranscriptPage(b, rpcwire.NewWebSocketTransport())
}

func BenchmarkLegacyRedialGetSessionTranscriptPageGWS(b *testing.B) {
	benchmarkLegacyRedialGetSessionTranscriptPage(b, rpcwire.NewGWSTransport())
}

func BenchmarkDialRemoteAttachProjectWorkspace(b *testing.B) {
	benchmarkDialRemoteAttachProjectWorkspace(b, rpcwire.NewWebSocketTransport())
}

func BenchmarkDialRemoteAttachProjectWorkspaceGWS(b *testing.B) {
	benchmarkDialRemoteAttachProjectWorkspace(b, rpcwire.NewGWSTransport())
}

func BenchmarkRemoteGetSessionMainViewPersistentLocalSocket(b *testing.B) {
	benchmarkRemoteGetSessionMainViewPersistentLocalSocket(b, rpcwire.NewWebSocketTransport())
}

func BenchmarkRemoteGetSessionMainViewPersistentLocalSocketGWS(b *testing.B) {
	benchmarkRemoteGetSessionMainViewPersistentLocalSocket(b, rpcwire.NewGWSTransport())
}

func BenchmarkRemoteGetSessionTranscriptPagePersistentLocalSocket(b *testing.B) {
	benchmarkRemoteGetSessionTranscriptPagePersistentLocalSocket(b, rpcwire.NewWebSocketTransport())
}

func BenchmarkRemoteGetSessionTranscriptPagePersistentLocalSocketGWS(b *testing.B) {
	benchmarkRemoteGetSessionTranscriptPagePersistentLocalSocket(b, rpcwire.NewGWSTransport())
}

func BenchmarkDialHandshakeLocalSocket(b *testing.B) {
	benchmarkDialHandshakeLocalSocket(b, rpcwire.NewWebSocketTransport())
}

func BenchmarkDialHandshakeLocalSocketGWS(b *testing.B) {
	benchmarkDialHandshakeLocalSocket(b, rpcwire.NewGWSTransport())
}

func BenchmarkAttachProjectWorkspaceLocalSocket(b *testing.B) {
	benchmarkAttachProjectWorkspaceLocalSocket(b, rpcwire.NewWebSocketTransport())
}

func BenchmarkAttachProjectWorkspaceLocalSocketGWS(b *testing.B) {
	benchmarkAttachProjectWorkspaceLocalSocket(b, rpcwire.NewGWSTransport())
}

func BenchmarkRemoteGetSessionMainViewPersistentParallelLocalSocket(b *testing.B) {
	benchmarkRemoteGetSessionMainViewPersistentParallelLocalSocket(b, rpcwire.NewWebSocketTransport())
}

func BenchmarkRemoteGetSessionMainViewPersistentParallelLocalSocketGWS(b *testing.B) {
	benchmarkRemoteGetSessionMainViewPersistentParallelLocalSocket(b, rpcwire.NewGWSTransport())
}

func BenchmarkSubscribeSessionActivityLocalSocket(b *testing.B) {
	benchmarkSubscribeSessionActivityLocalSocket(b, rpcwire.NewWebSocketTransport())
}

func BenchmarkSubscribeSessionActivityLocalSocketGWS(b *testing.B) {
	benchmarkSubscribeSessionActivityLocalSocket(b, rpcwire.NewGWSTransport())
}

func benchmarkRemoteGetSessionMainViewPersistent(b *testing.B, transport benchmarkTransport) {
	server := benchmarkUnaryTCPServer(b, transport)
	defer server.Close()
	benchmarkRemoteGetSessionMainViewPersistentOnServer(b, transport, server.endpoint)
}

func benchmarkRemoteGetSessionMainViewPersistentLocalSocket(b *testing.B, transport benchmarkTransport) {
	server := benchmarkUnaryUnixServer(b, transport)
	defer server.Close()
	benchmarkRemoteGetSessionMainViewPersistentOnServer(b, transport, server.endpoint)
}

func benchmarkRemoteGetSessionMainViewPersistentOnServer(b *testing.B, transport rpcwire.ClientTransport, endpoint rpcwire.Endpoint) {
	remote := benchmarkRemote(b, transport, endpoint)
	defer func() { _ = remote.Close() }()
	request := serverapi.SessionMainViewRequest{SessionID: "session-1"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := remote.GetSessionMainView(context.Background(), request); err != nil {
			b.Fatalf("GetSessionMainView: %v", err)
		}
	}
}

func benchmarkLegacyRedialGetSessionMainView(b *testing.B, transport benchmarkTransport) {
	server := benchmarkUnaryTCPServer(b, transport)
	defer server.Close()
	request := serverapi.SessionMainViewRequest{SessionID: "session-1"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		remote := benchmarkRemote(b, transport, server.endpoint)
		if _, err := remote.GetSessionMainView(context.Background(), request); err != nil {
			_ = remote.Close()
			b.Fatalf("GetSessionMainView: %v", err)
		}
		_ = remote.Close()
	}
}

func benchmarkRemoteGetSessionTranscriptPagePersistent(b *testing.B, transport benchmarkTransport) {
	server := benchmarkUnaryTCPServer(b, transport)
	defer server.Close()
	benchmarkRemoteGetSessionTranscriptPagePersistentOnServer(b, transport, server.endpoint)
}

func benchmarkRemoteGetSessionTranscriptPagePersistentLocalSocket(b *testing.B, transport benchmarkTransport) {
	server := benchmarkUnaryUnixServer(b, transport)
	defer server.Close()
	benchmarkRemoteGetSessionTranscriptPagePersistentOnServer(b, transport, server.endpoint)
}

func benchmarkRemoteGetSessionTranscriptPagePersistentOnServer(b *testing.B, transport rpcwire.ClientTransport, endpoint rpcwire.Endpoint) {
	remote := benchmarkRemote(b, transport, endpoint)
	defer func() { _ = remote.Close() }()
	request := serverapi.SessionTranscriptPageRequest{SessionID: "session-1"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := remote.GetSessionTranscriptPage(context.Background(), request); err != nil {
			b.Fatalf("GetSessionTranscriptPage: %v", err)
		}
	}
}

func benchmarkLegacyRedialGetSessionTranscriptPage(b *testing.B, transport benchmarkTransport) {
	server := benchmarkUnaryTCPServer(b, transport)
	defer server.Close()
	request := serverapi.SessionTranscriptPageRequest{SessionID: "session-1"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		remote := benchmarkRemote(b, transport, server.endpoint)
		if _, err := remote.GetSessionTranscriptPage(context.Background(), request); err != nil {
			_ = remote.Close()
			b.Fatalf("GetSessionTranscriptPage: %v", err)
		}
		_ = remote.Close()
	}
}

func benchmarkDialRemoteAttachProjectWorkspace(b *testing.B, transport benchmarkTransport) {
	server := benchmarkUnaryTCPServer(b, transport)
	defer server.Close()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		remote := benchmarkRemote(b, transport, server.endpoint)
		_ = remote.Close()
	}
}

func benchmarkDialHandshakeLocalSocket(b *testing.B, transport benchmarkTransport) {
	server := benchmarkUnaryUnixServer(b, transport)
	defer server.Close()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		conn := benchmarkDialHandshakeConn(b, transport, server.endpoint)
		_ = conn.Close()
	}
}

func benchmarkAttachProjectWorkspaceLocalSocket(b *testing.B, transport benchmarkTransport) {
	server := benchmarkUnaryUnixServer(b, transport)
	defer server.Close()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		conn := benchmarkDialHandshakeConn(b, transport, server.endpoint)
		b.StartTimer()
		if err := attachProjectRPC(context.Background(), conn, "project-1", "", "/tmp/workspace-a"); err != nil {
			_ = conn.Close()
			b.Fatalf("attachProjectRPC: %v", err)
		}
		b.StopTimer()
		_ = conn.Close()
		b.StartTimer()
	}
	if b.N > 0 {
		b.StopTimer()
	}
}

func benchmarkRemoteGetSessionMainViewPersistentParallelLocalSocket(b *testing.B, transport benchmarkTransport) {
	server := benchmarkUnaryUnixServer(b, transport)
	defer server.Close()
	remote := benchmarkRemote(b, transport, server.endpoint)
	defer func() { _ = remote.Close() }()
	request := serverapi.SessionMainViewRequest{SessionID: "session-1"}
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, err := remote.GetSessionMainView(context.Background(), request); err != nil {
				b.Fatalf("GetSessionMainView: %v", err)
			}
		}
	})
}

func benchmarkSubscribeSessionActivityLocalSocket(b *testing.B, transport benchmarkTransport) {
	server := benchmarkSessionActivityUnixServer(b, transport)
	defer server.Close()
	remote := benchmarkRemote(b, transport, server.endpoint)
	defer func() { _ = remote.Close() }()
	request := serverapi.SessionActivitySubscribeRequest{SessionID: "session-1"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		subscription, err := remote.SubscribeSessionActivity(context.Background(), request)
		if err != nil {
			b.Fatalf("SubscribeSessionActivity: %v", err)
		}
		if _, err := subscription.Next(context.Background()); err != nil {
			_ = subscription.Close()
			b.Fatalf("SessionActivity.Next: %v", err)
		}
		_ = subscription.Close()
	}
}

func benchmarkDialHandshakeConn(tb testing.TB, transport rpcwire.ClientTransport, endpoint rpcwire.Endpoint) rpcwire.Conn {
	tb.Helper()
	conn, err := transport.Dial(context.Background(), endpoint)
	if err != nil {
		tb.Fatalf("Dial: %v", err)
	}
	if _, err := handshakeRPC(context.Background(), conn); err != nil {
		_ = conn.Close()
		tb.Fatalf("handshakeRPC: %v", err)
	}
	return conn
}

type benchmarkTransport interface {
	rpcwire.ClientTransport
	rpcwire.ServerTransport
}

type benchmarkServer struct {
	endpoint rpcwire.Endpoint
	close    func()
}

func (s benchmarkServer) Close() {
	if s.close != nil {
		s.close()
	}
}

func benchmarkUnaryTCPServer(tb testing.TB, transport benchmarkTransport) benchmarkServer {
	tb.Helper()
	server := httptest.NewServer(transport.Handler(func(ctx context.Context, conn rpcwire.Conn) {
		benchmarkServeFrames(tb, ctx, conn)
	}))
	endpoint, err := rpcwire.ParseWebSocketEndpoint("ws" + server.URL[len("http"):])
	if err != nil {
		server.Close()
		tb.Fatalf("ParseWebSocketEndpoint: %v", err)
	}
	return benchmarkServer{endpoint: endpoint, close: server.Close}
}

func benchmarkUnaryUnixServer(tb testing.TB, transport benchmarkTransport) benchmarkServer {
	tb.Helper()
	if runtime.GOOS == "windows" {
		tb.Skip("unix sockets unavailable")
	}
	socketPath := benchmarkUnixSocketPath()
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		tb.Fatalf("Listen unix: %v", err)
	}
	httpServer := &http.Server{Handler: transport.Handler(func(ctx context.Context, conn rpcwire.Conn) {
		benchmarkServeFrames(tb, ctx, conn)
	})}
	errCh := make(chan error, 1)
	go func() { errCh <- httpServer.Serve(listener) }()
	endpoint, err := rpcwire.NewUnixEndpoint(socketPath, protocol.RPCPath)
	if err != nil {
		_ = httpServer.Close()
		_ = listener.Close()
		_ = os.Remove(socketPath)
		tb.Fatalf("NewUnixEndpoint: %v", err)
	}
	return benchmarkServer{
		endpoint: endpoint,
		close: func() {
			if err := httpServer.Close(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				tb.Fatalf("Close unix benchmark server: %v", err)
			}
			if err := <-errCh; err != nil && !errors.Is(err, http.ErrServerClosed) {
				tb.Fatalf("Serve unix benchmark server: %v", err)
			}
			if err := os.Remove(socketPath); err != nil && !errors.Is(err, os.ErrNotExist) {
				tb.Fatalf("Remove unix benchmark socket: %v", err)
			}
		},
	}
}

func benchmarkSessionActivityUnixServer(tb testing.TB, transport benchmarkTransport) benchmarkServer {
	tb.Helper()
	if runtime.GOOS == "windows" {
		tb.Skip("unix sockets unavailable")
	}
	socketPath := benchmarkUnixSocketPath()
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		tb.Fatalf("Listen unix: %v", err)
	}
	httpServer := &http.Server{Handler: transport.Handler(func(ctx context.Context, conn rpcwire.Conn) {
		benchmarkServeSessionActivity(tb, ctx, conn)
	})}
	errCh := make(chan error, 1)
	go func() { errCh <- httpServer.Serve(listener) }()
	endpoint, err := rpcwire.NewUnixEndpoint(socketPath, protocol.RPCPath)
	if err != nil {
		_ = httpServer.Close()
		_ = listener.Close()
		_ = os.Remove(socketPath)
		tb.Fatalf("NewUnixEndpoint: %v", err)
	}
	return benchmarkServer{
		endpoint: endpoint,
		close: func() {
			if err := httpServer.Close(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				tb.Fatalf("Close unix benchmark server: %v", err)
			}
			if err := <-errCh; err != nil && !errors.Is(err, http.ErrServerClosed) {
				tb.Fatalf("Serve unix benchmark server: %v", err)
			}
			if err := os.Remove(socketPath); err != nil && !errors.Is(err, os.ErrNotExist) {
				tb.Fatalf("Remove unix benchmark socket: %v", err)
			}
		},
	}
}

func benchmarkUnixSocketPath() string {
	return filepath.Join(os.TempDir(), fmt.Sprintf("builder-bench-%d.sock", time.Now().UnixNano()))
}

func benchmarkServeFrames(tb testing.TB, ctx context.Context, conn rpcwire.Conn) {
	tb.Helper()
	for event := range conn.Events() {
		if event.Err != nil {
			return
		}
		request := event.Frame.Request()
		var response protocol.Response
		switch request.Method {
		case protocol.MethodHandshake:
			response = protocol.NewSuccessResponse(request.ID, protocol.HandshakeResponse{Identity: protocol.ServerIdentity{ProtocolVersion: protocol.Version, ServerID: "bench-server"}})
		case protocol.MethodAttachProject:
			response = protocol.NewSuccessResponse(request.ID, protocol.AttachResponse{Kind: "project", ProjectID: "project-1", WorkspaceRoot: "/tmp/workspace-a"})
		case protocol.MethodSessionGetMainView:
			response = protocol.NewSuccessResponse(request.ID, serverapi.SessionMainViewResponse{MainView: clientui.RuntimeMainView{Session: clientui.RuntimeSessionView{SessionID: "session-1"}}})
		case protocol.MethodSessionGetTranscriptPage:
			response = protocol.NewSuccessResponse(request.ID, serverapi.SessionTranscriptPageResponse{Transcript: clientui.TranscriptPage{Entries: []clientui.ChatEntry{{Role: "assistant", Text: "done"}}}})
		default:
			tb.Fatalf("unexpected benchmark method %q", request.Method)
		}
		if err := conn.Send(ctx, rpcwire.FrameFromResponse(response)); err != nil {
			return
		}
	}
}

func benchmarkServeSessionActivity(tb testing.TB, ctx context.Context, conn rpcwire.Conn) {
	tb.Helper()
	for event := range conn.Events() {
		if event.Err != nil {
			return
		}
		request := event.Frame.Request()
		switch request.Method {
		case protocol.MethodHandshake:
			if err := conn.Send(ctx, rpcwire.FrameFromResponse(protocol.NewSuccessResponse(request.ID, protocol.HandshakeResponse{Identity: protocol.ServerIdentity{ProtocolVersion: protocol.Version, ServerID: "bench-server"}}))); err != nil {
				return
			}
		case protocol.MethodAttachProject:
			if err := conn.Send(ctx, rpcwire.FrameFromResponse(protocol.NewSuccessResponse(request.ID, protocol.AttachResponse{Kind: "project", ProjectID: "project-1", WorkspaceRoot: "/tmp/workspace-a"}))); err != nil {
				return
			}
		case protocol.MethodAttachSession:
			if err := conn.Send(ctx, rpcwire.FrameFromResponse(protocol.NewSuccessResponse(request.ID, protocol.AttachResponse{Kind: "session", SessionID: "session-1", ProjectID: "project-1", WorkspaceRoot: "/tmp/workspace-a"}))); err != nil {
				return
			}
		case protocol.MethodSessionSubscribeActivity:
			if err := conn.Send(ctx, rpcwire.FrameFromResponse(protocol.NewSuccessResponse(request.ID, protocol.SubscribeResponse{Stream: protocol.MethodSessionActivityEvent}))); err != nil {
				return
			}
			notification := rpcwire.Frame{
				JSONRPC: protocol.JSONRPCVersion,
				Method:  protocol.MethodSessionActivityEvent,
				Params:  mustBenchmarkMarshal(tb, protocol.SessionActivityEventParams{Event: clientui.Event{Kind: clientui.EventRunStateChanged, RunState: &clientui.RunState{Busy: true, RunID: "run-1"}}}),
			}
			_ = conn.Send(ctx, notification)
			return
		default:
			tb.Fatalf("unexpected session activity benchmark method %q", request.Method)
		}
	}
}

func benchmarkRemote(tb testing.TB, transport rpcwire.ClientTransport, endpoint rpcwire.Endpoint) *Remote {
	tb.Helper()
	remote, err := dialRemoteWithTransport(context.Background(), remoteDialPlan{endpoints: []rpcwire.Endpoint{endpoint}}, transport, "project-1", "", "/tmp/workspace-a")
	if err != nil {
		tb.Fatalf("dialRemoteWithTransport: %v", err)
	}
	return remote
}

func mustBenchmarkMarshal(tb testing.TB, value any) []byte {
	tb.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		tb.Fatalf("json.Marshal: %v", err)
	}
	return data
}

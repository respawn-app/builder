package client

import (
	"context"
	"net/http/httptest"
	"testing"

	"builder/shared/clientui"
	"builder/shared/protocol"
	"builder/shared/serverapi"
	"golang.org/x/net/websocket"
)

func BenchmarkRemoteGetSessionMainViewPersistent(b *testing.B) {
	server := benchmarkUnaryServer(b)
	defer server.Close()
	remote, err := DialRemoteURLForProjectWorkspace(context.Background(), benchmarkServerRPCURL(server), "project-1", "/tmp/workspace-a")
	if err != nil {
		b.Fatalf("DialRemoteURLForProjectWorkspace: %v", err)
	}
	defer func() { _ = remote.Close() }()
	req := serverapi.SessionMainViewRequest{SessionID: "session-1"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := remote.GetSessionMainView(context.Background(), req); err != nil {
			b.Fatalf("GetSessionMainView: %v", err)
		}
	}
}

func BenchmarkLegacyRedialGetSessionMainView(b *testing.B) {
	server := benchmarkUnaryServer(b)
	defer server.Close()
	req := serverapi.SessionMainViewRequest{SessionID: "session-1"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		remote, err := DialRemoteURLForProjectWorkspace(context.Background(), benchmarkServerRPCURL(server), "project-1", "/tmp/workspace-a")
		if err != nil {
			b.Fatalf("DialRemoteURLForProjectWorkspace: %v", err)
		}
		if _, err := remote.GetSessionMainView(context.Background(), req); err != nil {
			_ = remote.Close()
			b.Fatalf("GetSessionMainView: %v", err)
		}
		_ = remote.Close()
	}
}

func BenchmarkRemoteGetSessionTranscriptPagePersistent(b *testing.B) {
	server := benchmarkUnaryServer(b)
	defer server.Close()
	remote, err := DialRemoteURLForProjectWorkspace(context.Background(), benchmarkServerRPCURL(server), "project-1", "/tmp/workspace-a")
	if err != nil {
		b.Fatalf("DialRemoteURLForProjectWorkspace: %v", err)
	}
	defer func() { _ = remote.Close() }()
	req := serverapi.SessionTranscriptPageRequest{SessionID: "session-1"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := remote.GetSessionTranscriptPage(context.Background(), req); err != nil {
			b.Fatalf("GetSessionTranscriptPage: %v", err)
		}
	}
}

func BenchmarkLegacyRedialGetSessionTranscriptPage(b *testing.B) {
	server := benchmarkUnaryServer(b)
	defer server.Close()
	req := serverapi.SessionTranscriptPageRequest{SessionID: "session-1"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		remote, err := DialRemoteURLForProjectWorkspace(context.Background(), benchmarkServerRPCURL(server), "project-1", "/tmp/workspace-a")
		if err != nil {
			b.Fatalf("DialRemoteURLForProjectWorkspace: %v", err)
		}
		if _, err := remote.GetSessionTranscriptPage(context.Background(), req); err != nil {
			_ = remote.Close()
			b.Fatalf("GetSessionTranscriptPage: %v", err)
		}
		_ = remote.Close()
	}
}

func BenchmarkDialRemoteAttachProjectWorkspace(b *testing.B) {
	server := benchmarkUnaryServer(b)
	defer server.Close()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		remote, err := DialRemoteURLForProjectWorkspace(context.Background(), benchmarkServerRPCURL(server), "project-1", "/tmp/workspace-a")
		if err != nil {
			b.Fatalf("DialRemoteURLForProjectWorkspace: %v", err)
		}
		_ = remote.Close()
	}
}

func benchmarkUnaryServer(tb testing.TB) *httptest.Server {
	tb.Helper()
	return httptest.NewServer(websocket.Handler(func(ws *websocket.Conn) {
		defer func() { _ = ws.Close() }()
		benchmarkHandshakeAndServe(tb, ws)
	}))
}

func benchmarkHandshakeAndServe(tb testing.TB, ws *websocket.Conn) {
	tb.Helper()
	var req protocol.Request
	if err := websocket.JSON.Receive(ws, &req); err != nil {
		tb.Fatalf("receive handshake: %v", err)
	}
	if req.Method != protocol.MethodHandshake {
		tb.Fatalf("handshake method = %q", req.Method)
	}
	if err := websocket.JSON.Send(ws, protocol.NewSuccessResponse(req.ID, protocol.HandshakeResponse{Identity: protocol.ServerIdentity{ProtocolVersion: protocol.Version, ServerID: "bench-server"}})); err != nil {
		tb.Fatalf("send handshake response: %v", err)
	}
	for {
		if err := websocket.JSON.Receive(ws, &req); err != nil {
			return
		}
		switch req.Method {
		case protocol.MethodAttachProject:
			if err := websocket.JSON.Send(ws, protocol.NewSuccessResponse(req.ID, protocol.AttachResponse{Kind: "project", ProjectID: "project-1", WorkspaceRoot: "/tmp/workspace-a"})); err != nil {
				tb.Fatalf("send attach response: %v", err)
			}
		case protocol.MethodSessionGetMainView:
			if err := websocket.JSON.Send(ws, protocol.NewSuccessResponse(req.ID, serverapi.SessionMainViewResponse{MainView: clientui.RuntimeMainView{Session: clientui.RuntimeSessionView{SessionID: "session-1"}}})); err != nil {
				tb.Fatalf("send main view response: %v", err)
			}
		case protocol.MethodSessionGetTranscriptPage:
			if err := websocket.JSON.Send(ws, protocol.NewSuccessResponse(req.ID, serverapi.SessionTranscriptPageResponse{Transcript: clientui.TranscriptPage{Entries: []clientui.ChatEntry{{Role: "assistant", Text: "done"}}}})); err != nil {
				tb.Fatalf("send transcript response: %v", err)
			}
		default:
			tb.Fatalf("unexpected benchmark method %q", req.Method)
		}
	}
}

func benchmarkServerRPCURL(server *httptest.Server) string {
	return "ws" + server.URL[len("http"):]
}

package transport

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"builder/server/auth"
	serverbootstrap "builder/server/bootstrap"
	"builder/server/core"
	"builder/server/runtime"
	askquestion "builder/server/tools/askquestion"
	shelltool "builder/server/tools/shell"
	"builder/shared/clientui"
	"builder/shared/protocol"
	"builder/shared/serverapi"
	"golang.org/x/net/websocket"
)

func TestGatewayHandshakeAndProjectList(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)

	resolved, err := serverbootstrap.ResolveConfig(serverbootstrap.Request{WorkspaceRoot: workspace})
	if err != nil {
		t.Fatalf("ResolveConfig: %v", err)
	}
	authSupport, err := serverbootstrap.BuildAuthSupport(auth.NewMemoryStore(auth.EmptyState()), nil, nil)
	if err != nil {
		t.Fatalf("BuildAuthSupport: %v", err)
	}
	runtimeSupport, err := serverbootstrap.BuildRuntimeSupport(resolved.Config)
	if err != nil {
		t.Fatalf("BuildRuntimeSupport: %v", err)
	}
	t.Cleanup(func() { _ = runtimeSupport.Background.Close() })
	appCore, err := core.New(resolved.Config, authSupport, runtimeSupport)
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	gateway, err := NewGateway(appCore, protocol.ServerIdentity{ProtocolVersion: protocol.Version, ServerID: "server-1", ProjectID: appCore.ProjectID(), WorkspaceRoot: workspace})
	if err != nil {
		t.Fatalf("NewGateway: %v", err)
	}
	server := httptest.NewServer(gateway.Handler())
	defer server.Close()

	wsURL := "ws" + server.URL[len("http"):]
	conn, err := websocket.Dial(wsURL, "", server.URL)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	if err := websocket.JSON.Send(conn, protocol.Request{JSONRPC: protocol.JSONRPCVersion, ID: "1", Method: protocol.MethodHandshake, Params: mustJSON(t, protocol.HandshakeRequest{ProtocolVersion: protocol.Version})}); err != nil {
		t.Fatalf("send handshake: %v", err)
	}
	var handshakeResp protocol.Response
	if err := websocket.JSON.Receive(conn, &handshakeResp); err != nil {
		t.Fatalf("receive handshake: %v", err)
	}
	if handshakeResp.Error != nil {
		t.Fatalf("handshake error: %+v", handshakeResp.Error)
	}
	var handshake protocol.HandshakeResponse
	if err := json.Unmarshal(handshakeResp.Result, &handshake); err != nil {
		t.Fatalf("decode handshake result: %v", err)
	}
	if handshake.Identity.ProjectID != appCore.ProjectID() || handshake.Identity.WorkspaceRoot != workspace {
		t.Fatalf("unexpected handshake: %+v", handshake.Identity)
	}

	if err := websocket.JSON.Send(conn, protocol.Request{JSONRPC: protocol.JSONRPCVersion, ID: "2", Method: protocol.MethodProjectList, Params: mustJSON(t, serverapi.ProjectListRequest{})}); err != nil {
		t.Fatalf("send project list: %v", err)
	}
	var projectListResp protocol.Response
	if err := websocket.JSON.Receive(conn, &projectListResp); err != nil {
		t.Fatalf("receive project list: %v", err)
	}
	if projectListResp.Error != nil {
		t.Fatalf("project list error: %+v", projectListResp.Error)
	}
	var projects serverapi.ProjectListResponse
	if err := json.Unmarshal(projectListResp.Result, &projects); err != nil {
		t.Fatalf("decode project list: %v", err)
	}
	if len(projects.Projects) != 1 || projects.Projects[0].ProjectID != appCore.ProjectID() {
		t.Fatalf("unexpected project list: %+v", projects.Projects)
	}
}

func TestGatewayRejectsMethodsBeforeHandshake(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)

	resolved, err := serverbootstrap.ResolveConfig(serverbootstrap.Request{WorkspaceRoot: workspace})
	if err != nil {
		t.Fatalf("ResolveConfig: %v", err)
	}
	authSupport, err := serverbootstrap.BuildAuthSupport(auth.NewMemoryStore(auth.EmptyState()), nil, nil)
	if err != nil {
		t.Fatalf("BuildAuthSupport: %v", err)
	}
	runtimeSupport, err := serverbootstrap.BuildRuntimeSupport(resolved.Config)
	if err != nil {
		t.Fatalf("BuildRuntimeSupport: %v", err)
	}
	t.Cleanup(func() { _ = runtimeSupport.Background.Close() })
	appCore, err := core.New(resolved.Config, authSupport, runtimeSupport)
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	gateway, err := NewGateway(appCore, protocol.ServerIdentity{ProtocolVersion: protocol.Version, ServerID: "server-1", ProjectID: appCore.ProjectID(), WorkspaceRoot: workspace})
	if err != nil {
		t.Fatalf("NewGateway: %v", err)
	}
	server := httptest.NewServer(gateway.Handler())
	defer server.Close()

	wsURL := "ws" + server.URL[len("http"):]
	conn, err := websocket.Dial(wsURL, "", server.URL)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	if err := websocket.JSON.Send(conn, protocol.Request{JSONRPC: protocol.JSONRPCVersion, ID: "1", Method: protocol.MethodProjectList, Params: mustJSON(t, serverapi.ProjectListRequest{})}); err != nil {
		t.Fatalf("send project list: %v", err)
	}
	var resp protocol.Response
	if err := websocket.JSON.Receive(conn, &resp); err != nil {
		t.Fatalf("receive response: %v", err)
	}
	if resp.Error == nil || resp.Error.Code != protocol.ErrCodeInvalidRequest {
		t.Fatalf("expected handshake-required error, got %+v", resp.Error)
	}
}

func TestGatewaySessionActivitySubscriptionStreamsEventsAndCompletion(t *testing.T) {
	appCore, server := newGatewayTestServer(t)
	defer server.Close()

	appCore.RegisterRuntime("session-1", &runtime.Engine{})
	defer appCore.UnregisterRuntime("session-1")

	conn := dialGateway(t, server)
	defer func() { _ = conn.Close() }()
	handshakeGateway(t, conn)
	callGateway(t, conn, "attach", protocol.MethodAttachSession, protocol.AttachSessionRequest{SessionID: "session-1"}, nil)
	callGateway(t, conn, "subscribe", protocol.MethodSessionSubscribeActivity, serverapi.SessionActivitySubscribeRequest{SessionID: "session-1"}, nil)

	appCore.PublishRuntimeEvent("session-1", runtime.Event{Kind: runtime.EventConversationUpdated, StepID: "step-1"})
	var notif protocol.Request
	if err := websocket.JSON.Receive(conn, &notif); err != nil {
		t.Fatalf("receive notification: %v", err)
	}
	if notif.Method != protocol.MethodSessionActivityEvent {
		t.Fatalf("notification method = %q", notif.Method)
	}
	var event protocol.SessionActivityEventParams
	if err := json.Unmarshal(notif.Params, &event); err != nil {
		t.Fatalf("decode event params: %v", err)
	}
	if event.Event.Kind != "conversation_updated" || event.Event.StepID != "step-1" {
		t.Fatalf("unexpected event: %+v", event.Event)
	}

	appCore.UnregisterRuntime("session-1")
	if err := websocket.JSON.Receive(conn, &notif); err != nil {
		t.Fatalf("receive completion: %v", err)
	}
	if notif.Method != protocol.MethodSessionActivityComplete {
		t.Fatalf("completion method = %q", notif.Method)
	}
	var complete protocol.StreamCompleteParams
	if err := json.Unmarshal(notif.Params, &complete); err != nil {
		t.Fatalf("decode completion: %v", err)
	}
	if complete.Code != 0 || complete.Message != "" {
		t.Fatalf("unexpected completion params: %+v", complete)
	}
}

func TestGatewayProcessOutputSubscriptionStreamsOutputAndCompletion(t *testing.T) {
	appCore, server := newGatewayTestServer(t)
	defer server.Close()
	appCore.Background().SetMinimumExecToBgTime(time.Millisecond)

	result, err := appCore.Background().Start(context.Background(), shelltool.ExecRequest{
		Command:        []string{"/bin/sh", "-lc", "printf 'hello\\n'; sleep 0.05"},
		DisplayCommand: "printf 'hello\\n'; sleep 0.05",
		Workdir:        appCore.Config().WorkspaceRoot,
		YieldTime:      time.Millisecond,
	})
	if err != nil {
		t.Fatalf("start process: %v", err)
	}

	conn := dialGateway(t, server)
	defer func() { _ = conn.Close() }()
	handshakeGateway(t, conn)
	callGateway(t, conn, "subscribe", protocol.MethodProcessSubscribeOutput, serverapi.ProcessOutputSubscribeRequest{ProcessID: result.SessionID, OffsetBytes: 0}, nil)

	var notif protocol.Request
	if err := websocket.JSON.Receive(conn, &notif); err != nil {
		t.Fatalf("receive output: %v", err)
	}
	if notif.Method != protocol.MethodProcessOutputEvent {
		t.Fatalf("output method = %q", notif.Method)
	}
	var chunk protocol.ProcessOutputEventParams
	if err := json.Unmarshal(notif.Params, &chunk); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	if chunk.Chunk.ProcessID != result.SessionID || chunk.Chunk.OffsetBytes != 0 || chunk.Chunk.Text != "hello\n" {
		t.Fatalf("unexpected process output chunk: %+v", chunk.Chunk)
	}

	if err := websocket.JSON.Receive(conn, &notif); err != nil {
		t.Fatalf("receive completion: %v", err)
	}
	if notif.Method != protocol.MethodProcessOutputComplete {
		t.Fatalf("completion method = %q", notif.Method)
	}
	var complete protocol.StreamCompleteParams
	if err := json.Unmarshal(notif.Params, &complete); err != nil {
		t.Fatalf("decode completion: %v", err)
	}
	if complete.Code != 0 || complete.Message != "" {
		t.Fatalf("unexpected completion params: %+v", complete)
	}
}

func TestGatewayPromptActivitySubscriptionStreamsPendingResolvedAndCompletion(t *testing.T) {
	appCore, server := newGatewayTestServer(t)
	defer server.Close()

	appCore.RegisterRuntime("session-1", &runtime.Engine{})
	defer appCore.UnregisterRuntime("session-1")
	appCore.BeginPendingPrompt("session-1", askquestion.Request{ID: "ask-1", Question: "Proceed?", Suggestions: []string{"Yes", "No"}})

	conn := dialGateway(t, server)
	defer func() { _ = conn.Close() }()
	handshakeGateway(t, conn)
	callGateway(t, conn, "attach", protocol.MethodAttachSession, protocol.AttachSessionRequest{SessionID: "session-1"}, nil)
	callGateway(t, conn, "subscribe", protocol.MethodPromptSubscribeActivity, serverapi.PromptActivitySubscribeRequest{SessionID: "session-1"}, nil)

	var notif protocol.Request
	if err := websocket.JSON.Receive(conn, &notif); err != nil {
		t.Fatalf("receive prompt pending: %v", err)
	}
	if notif.Method != protocol.MethodPromptActivityEvent {
		t.Fatalf("prompt event method = %q", notif.Method)
	}
	var pending protocol.PromptActivityEventParams
	if err := json.Unmarshal(notif.Params, &pending); err != nil {
		t.Fatalf("decode prompt pending: %v", err)
	}
	if pending.Event.Type != clientui.PendingPromptEventPending || pending.Event.PromptID != "ask-1" || pending.Event.Question != "Proceed?" {
		t.Fatalf("unexpected pending prompt event: %+v", pending.Event)
	}

	appCore.CompletePendingPrompt("session-1", "ask-1")
	if err := websocket.JSON.Receive(conn, &notif); err != nil {
		t.Fatalf("receive prompt resolved: %v", err)
	}
	if notif.Method != protocol.MethodPromptActivityEvent {
		t.Fatalf("resolved prompt method = %q", notif.Method)
	}
	var resolved protocol.PromptActivityEventParams
	if err := json.Unmarshal(notif.Params, &resolved); err != nil {
		t.Fatalf("decode prompt resolved: %v", err)
	}
	if resolved.Event.Type != clientui.PendingPromptEventResolved || resolved.Event.PromptID != "ask-1" {
		t.Fatalf("unexpected resolved prompt event: %+v", resolved.Event)
	}

	appCore.UnregisterRuntime("session-1")
	if err := websocket.JSON.Receive(conn, &notif); err != nil {
		t.Fatalf("receive prompt completion: %v", err)
	}
	if notif.Method != protocol.MethodPromptActivityComplete {
		t.Fatalf("prompt completion method = %q", notif.Method)
	}
	var complete protocol.StreamCompleteParams
	if err := json.Unmarshal(notif.Params, &complete); err != nil {
		t.Fatalf("decode prompt completion: %v", err)
	}
	if complete.Code != 0 || complete.Message != "" {
		t.Fatalf("unexpected prompt completion params: %+v", complete)
	}
}

func newGatewayTestServer(t *testing.T) (*core.Core, *httptest.Server) {
	t.Helper()
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)

	resolved, err := serverbootstrap.ResolveConfig(serverbootstrap.Request{WorkspaceRoot: workspace})
	if err != nil {
		t.Fatalf("ResolveConfig: %v", err)
	}
	authSupport, err := serverbootstrap.BuildAuthSupport(auth.NewMemoryStore(auth.EmptyState()), nil, nil)
	if err != nil {
		t.Fatalf("BuildAuthSupport: %v", err)
	}
	runtimeSupport, err := serverbootstrap.BuildRuntimeSupport(resolved.Config)
	if err != nil {
		t.Fatalf("BuildRuntimeSupport: %v", err)
	}
	t.Cleanup(func() { _ = runtimeSupport.Background.Close() })
	appCore, err := core.New(resolved.Config, authSupport, runtimeSupport)
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	gateway, err := NewGateway(appCore, protocol.ServerIdentity{ProtocolVersion: protocol.Version, ServerID: "server-1", ProjectID: appCore.ProjectID(), WorkspaceRoot: workspace})
	if err != nil {
		t.Fatalf("NewGateway: %v", err)
	}
	return appCore, httptest.NewServer(gateway.Handler())
}

func dialGateway(t *testing.T, server *httptest.Server) *websocket.Conn {
	t.Helper()
	wsURL := "ws" + server.URL[len("http"):]
	conn, err := websocket.Dial(wsURL, "", server.URL)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	return conn
}

func handshakeGateway(t *testing.T, conn *websocket.Conn) {
	t.Helper()
	callGateway(t, conn, "1", protocol.MethodHandshake, protocol.HandshakeRequest{ProtocolVersion: protocol.Version}, nil)
}

func callGateway(t *testing.T, conn *websocket.Conn, id string, method string, params any, out any) {
	t.Helper()
	if err := websocket.JSON.Send(conn, protocol.Request{JSONRPC: protocol.JSONRPCVersion, ID: id, Method: method, Params: mustJSON(t, params)}); err != nil {
		t.Fatalf("send %s: %v", method, err)
	}
	var resp protocol.Response
	if err := websocket.JSON.Receive(conn, &resp); err != nil {
		t.Fatalf("receive %s: %v", method, err)
	}
	if resp.Error != nil {
		t.Fatalf("%s error: %+v", method, resp.Error)
	}
	if out != nil && len(resp.Result) > 0 {
		if err := json.Unmarshal(resp.Result, out); err != nil {
			t.Fatalf("decode %s: %v", method, err)
		}
	}
}

func mustJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	return data
}

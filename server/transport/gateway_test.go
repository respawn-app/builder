package transport

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"builder/server/auth"
	serverbootstrap "builder/server/bootstrap"
	"builder/server/core"
	"builder/server/llm"
	"builder/server/metadata"
	"builder/server/runtime"
	"builder/server/session"
	"builder/server/tools"
	askquestion "builder/server/tools/askquestion"
	shelltool "builder/server/tools/shell"
	remoteclient "builder/shared/client"
	"builder/shared/clientui"
	"builder/shared/config"
	"builder/shared/protocol"
	"builder/shared/serverapi"
	"builder/shared/toolspec"

	"golang.org/x/net/websocket"
)

func registerGatewayWorkspace(t *testing.T, workspace string) {
	t.Helper()
	configureGatewayTestServerPort(t)
	resolved, err := serverbootstrap.ResolveConfig(serverbootstrap.Request{WorkspaceRoot: workspace})
	if err != nil {
		t.Fatalf("ResolveConfig: %v", err)
	}
	if _, err := metadata.RegisterBinding(context.Background(), resolved.Config.PersistenceRoot, resolved.Config.WorkspaceRoot); err != nil {
		t.Fatalf("RegisterBinding: %v", err)
	}
}

func configureGatewayTestServerPort(t *testing.T) {
	t.Helper()
	port := 56000 + int(gatewayTestPortCounter.Add(1))
	t.Setenv("BUILDER_SERVER_HOST", "127.0.0.1")
	t.Setenv("BUILDER_SERVER_PORT", strconv.Itoa(port))
}

var gatewayTestPortCounter atomic.Uint32

func newGatewayTestAuthSupport(t *testing.T, ready bool) serverbootstrap.AuthSupport {
	t.Helper()
	store := auth.NewMemoryStore(auth.EmptyState())
	authSupport, err := serverbootstrap.BuildAuthSupport(store, nil, nil)
	if err != nil {
		t.Fatalf("BuildAuthSupport: %v", err)
	}
	if ready {
		if _, err := authSupport.AuthManager.SwitchMethod(context.Background(), auth.Method{
			Type:   auth.MethodAPIKey,
			APIKey: &auth.APIKeyMethod{Key: "test-key"},
		}, true); err != nil {
			t.Fatalf("SwitchMethod: %v", err)
		}
	}
	return authSupport
}

func activateGatewayController(t *testing.T, appCore *core.Core, sessionID string) string {
	t.Helper()
	settings := appCore.Config().Settings
	if strings.TrimSpace(settings.Model) == "" {
		settings.Model = "gpt-5"
	}
	if strings.TrimSpace(settings.ProviderOverride) == "" && strings.TrimSpace(settings.OpenAIBaseURL) == "" {
		settings.ProviderOverride = "openai"
	}
	resp, err := appCore.SessionRuntimeClient().ActivateSessionRuntime(context.Background(), serverapi.SessionRuntimeActivateRequest{
		ClientRequestID: "activate-" + strings.TrimSpace(sessionID),
		SessionID:       strings.TrimSpace(sessionID),
		ActiveSettings:  settings,
		Source:          appCore.Config().Source,
	})
	if err != nil {
		t.Fatalf("ActivateSessionRuntime: %v", err)
	}
	return resp.LeaseID
}

func releaseGatewayController(t *testing.T, appCore *core.Core, sessionID string, leaseID string) {
	t.Helper()
	if strings.TrimSpace(leaseID) == "" {
		return
	}
	if _, err := appCore.SessionRuntimeClient().ReleaseSessionRuntime(context.Background(), serverapi.SessionRuntimeReleaseRequest{
		ClientRequestID: "release-" + strings.TrimSpace(sessionID),
		SessionID:       strings.TrimSpace(sessionID),
		LeaseID:         strings.TrimSpace(leaseID),
	}); err != nil {
		t.Fatalf("ReleaseSessionRuntime: %v", err)
	}
}

func TestGatewayHandshakeAndProjectList(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	registerGatewayWorkspace(t, workspace)

	resolved, err := serverbootstrap.ResolveConfig(serverbootstrap.Request{WorkspaceRoot: workspace})
	if err != nil {
		t.Fatalf("ResolveConfig: %v", err)
	}
	authSupport := newGatewayTestAuthSupport(t, true)
	runtimeSupport, err := serverbootstrap.BuildRuntimeSupport(resolved.Config)
	if err != nil {
		t.Fatalf("BuildRuntimeSupport: %v", err)
	}
	t.Cleanup(func() { _ = runtimeSupport.Background.Close() })
	appCore, err := core.New(resolved.Config, authSupport, runtimeSupport)
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	gateway, err := NewGateway(appCore, protocol.ServerIdentity{ProtocolVersion: protocol.Version, ServerID: "server-1"})
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
	if handshake.Identity.ProtocolVersion != protocol.Version || handshake.Identity.ServerID != "server-1" {
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
	registerGatewayWorkspace(t, workspace)

	resolved, err := serverbootstrap.ResolveConfig(serverbootstrap.Request{WorkspaceRoot: workspace})
	if err != nil {
		t.Fatalf("ResolveConfig: %v", err)
	}
	authSupport := newGatewayTestAuthSupport(t, true)
	runtimeSupport, err := serverbootstrap.BuildRuntimeSupport(resolved.Config)
	if err != nil {
		t.Fatalf("BuildRuntimeSupport: %v", err)
	}
	t.Cleanup(func() { _ = runtimeSupport.Background.Close() })
	appCore, err := core.New(resolved.Config, authSupport, runtimeSupport)
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	gateway, err := NewGateway(appCore, protocol.ServerIdentity{ProtocolVersion: protocol.Version, ServerID: "server-1"})
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

func TestGatewayPreAuthMethodPolicy(t *testing.T) {
	tests := []struct {
		name         string
		method       string
		requiresAuth bool
	}{
		{name: "handshake", method: protocol.MethodHandshake, requiresAuth: false},
		{name: "bootstrap status", method: protocol.MethodAuthGetBootstrapStatus, requiresAuth: false},
		{name: "bootstrap complete", method: protocol.MethodAuthCompleteBootstrap, requiresAuth: false},
		{name: "project list", method: protocol.MethodProjectList, requiresAuth: false},
		{name: "project attach workspace", method: protocol.MethodProjectAttachWorkspace, requiresAuth: true},
		{name: "attach project", method: protocol.MethodAttachProject, requiresAuth: false},
		{name: "attach session", method: protocol.MethodAttachSession, requiresAuth: false},
		{name: "session transcript page", method: protocol.MethodSessionGetTranscriptPage, requiresAuth: false},
		{name: "process list", method: protocol.MethodProcessList, requiresAuth: false},
		{name: "run get", method: protocol.MethodRunGet, requiresAuth: false},
		{name: "session plan", method: protocol.MethodSessionPlan, requiresAuth: true},
		{name: "persist input draft", method: protocol.MethodSessionPersistInputDraft, requiresAuth: true},
		{name: "runtime submit", method: protocol.MethodRuntimeSubmitUserMessage, requiresAuth: true},
		{name: "run prompt", method: protocol.MethodRunPrompt, requiresAuth: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := &Gateway{}
			got := g.methodRequiresServerAuth(tt.method)
			if got != tt.requiresAuth {
				t.Fatalf("methodRequiresServerAuth(%q) = %t, want %t", tt.method, got, tt.requiresAuth)
			}
		})
	}
}

func TestGatewayAuthBootstrapStatusAllowedBeforeAttach(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	registerGatewayWorkspace(t, workspace)

	resolved, err := serverbootstrap.ResolveConfig(serverbootstrap.Request{WorkspaceRoot: workspace})
	if err != nil {
		t.Fatalf("ResolveConfig: %v", err)
	}
	authSupport := newGatewayTestAuthSupport(t, false)
	runtimeSupport, err := serverbootstrap.BuildRuntimeSupport(resolved.Config)
	if err != nil {
		t.Fatalf("BuildRuntimeSupport: %v", err)
	}
	t.Cleanup(func() { _ = runtimeSupport.Background.Close() })
	appCore, err := core.New(resolved.Config, authSupport, runtimeSupport)
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	defer func() { _ = appCore.Close() }()
	gateway, err := NewGateway(appCore, protocol.ServerIdentity{ProtocolVersion: protocol.Version, ServerID: "server-1"})
	if err != nil {
		t.Fatalf("NewGateway: %v", err)
	}
	server := httptest.NewServer(gateway.Handler())
	defer server.Close()

	conn := dialGateway(t, server)
	defer func() { _ = conn.Close() }()
	handshakeGateway(t, conn)

	var status serverapi.AuthGetBootstrapStatusResponse
	callGateway(t, conn, "status-1", protocol.MethodAuthGetBootstrapStatus, serverapi.AuthGetBootstrapStatusRequest{}, &status)
	if status.AuthReady {
		t.Fatal("expected unauthenticated bootstrap status")
	}
	if !status.AuthBootstrapSupported {
		t.Fatal("expected auth bootstrap to be supported")
	}
	if !containsString(status.AllowedPreAuthMethods, protocol.MethodProjectList) {
		t.Fatalf("allowed pre-auth methods = %+v, want %q", status.AllowedPreAuthMethods, protocol.MethodProjectList)
	}
	if !containsString(status.AllowedPreAuthMethods, protocol.MethodAuthCompleteBootstrap) {
		t.Fatalf("allowed pre-auth methods = %+v, want %q", status.AllowedPreAuthMethods, protocol.MethodAuthCompleteBootstrap)
	}
	if !sameStringSet(status.AllowedPreAuthMethods, protocol.AllowedPreAuthMethods()) {
		t.Fatalf("allowed pre-auth methods = %+v, want %+v", status.AllowedPreAuthMethods, protocol.AllowedPreAuthMethods())
	}
}

func TestGatewayAuthBootstrapAPIKeyCompletionEnablesAuthRequiredMethods(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	registerGatewayWorkspace(t, workspace)

	resolved, err := serverbootstrap.ResolveConfig(serverbootstrap.Request{WorkspaceRoot: workspace})
	if err != nil {
		t.Fatalf("ResolveConfig: %v", err)
	}
	authSupport := newGatewayTestAuthSupport(t, false)
	runtimeSupport, err := serverbootstrap.BuildRuntimeSupport(resolved.Config)
	if err != nil {
		t.Fatalf("BuildRuntimeSupport: %v", err)
	}
	t.Cleanup(func() { _ = runtimeSupport.Background.Close() })
	appCore, err := core.New(resolved.Config, authSupport, runtimeSupport)
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	defer func() { _ = appCore.Close() }()
	gateway, err := NewGateway(appCore, protocol.ServerIdentity{ProtocolVersion: protocol.Version, ServerID: "server-1"})
	if err != nil {
		t.Fatalf("NewGateway: %v", err)
	}
	server := httptest.NewServer(gateway.Handler())
	defer server.Close()

	conn := dialGateway(t, server)
	defer func() { _ = conn.Close() }()
	handshakeGateway(t, conn)

	callGateway(t, conn, "attach-project", protocol.MethodAttachProject, protocol.AttachProjectRequest{ProjectID: appCore.ProjectID()}, nil)
	if err := websocket.JSON.Send(conn, protocol.Request{JSONRPC: protocol.JSONRPCVersion, ID: "run-1", Method: protocol.MethodRunPrompt, Params: mustJSON(t, serverapi.RunPromptRequest{})}); err != nil {
		t.Fatalf("send run.prompt: %v", err)
	}
	var runResp protocol.Response
	if err := websocket.JSON.Receive(conn, &runResp); err != nil {
		t.Fatalf("receive run.prompt: %v", err)
	}
	if runResp.Error == nil || runResp.Error.Code != protocol.ErrCodeAuthRequired {
		t.Fatalf("run.prompt error = %+v, want auth required", runResp.Error)
	}

	callGateway(t, conn, "complete-1", protocol.MethodAuthCompleteBootstrap, serverapi.AuthCompleteBootstrapRequest{
		Mode:   serverapi.AuthBootstrapModeAPIKey,
		APIKey: "server-key",
	}, nil)
	var status serverapi.AuthGetBootstrapStatusResponse
	callGateway(t, conn, "status-2", protocol.MethodAuthGetBootstrapStatus, serverapi.AuthGetBootstrapStatusRequest{}, &status)
	if !status.AuthReady {
		t.Fatal("expected bootstrap completion to configure server auth")
	}
	state, err := authSupport.AuthManager.StoredState(context.Background())
	if err != nil {
		t.Fatalf("StoredState: %v", err)
	}
	if state.Method.APIKey == nil || state.Method.APIKey.Key != "server-key" {
		t.Fatalf("unexpected stored auth method: %+v", state.Method)
	}

	if err := websocket.JSON.Send(conn, protocol.Request{JSONRPC: protocol.JSONRPCVersion, ID: "complete-2", Method: protocol.MethodAuthCompleteBootstrap, Params: mustJSON(t, serverapi.AuthCompleteBootstrapRequest{Mode: serverapi.AuthBootstrapModeAPIKey, APIKey: "server-key-2"})}); err != nil {
		t.Fatalf("send second auth.completeBootstrap: %v", err)
	}
	var secondCompleteResp protocol.Response
	if err := websocket.JSON.Receive(conn, &secondCompleteResp); err != nil {
		t.Fatalf("receive second auth.completeBootstrap: %v", err)
	}
	if secondCompleteResp.Error != nil {
		t.Fatalf("second auth.completeBootstrap error = %+v, want success", secondCompleteResp.Error)
	}
	var secondComplete serverapi.AuthCompleteBootstrapResponse
	if err := json.Unmarshal(secondCompleteResp.Result, &secondComplete); err != nil {
		t.Fatalf("decode second auth.completeBootstrap result: %v", err)
	}
	if !secondComplete.AuthReady || secondComplete.MethodType != string(auth.MethodAPIKey) {
		t.Fatalf("unexpected second auth.completeBootstrap result: %+v", secondComplete)
	}
	state, err = authSupport.AuthManager.StoredState(context.Background())
	if err != nil {
		t.Fatalf("StoredState after second complete: %v", err)
	}
	if state.Method.APIKey == nil || state.Method.APIKey.Key != "server-key" {
		t.Fatalf("unexpected stored auth method after retry: %+v", state.Method)
	}
}

func TestGatewayRejectsProjectWorkspaceMutationBeforeServerAuthReady(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	registerGatewayWorkspace(t, workspace)

	resolved, err := serverbootstrap.ResolveConfig(serverbootstrap.Request{WorkspaceRoot: workspace})
	if err != nil {
		t.Fatalf("ResolveConfig: %v", err)
	}
	authSupport := newGatewayTestAuthSupport(t, false)
	runtimeSupport, err := serverbootstrap.BuildRuntimeSupport(resolved.Config)
	if err != nil {
		t.Fatalf("BuildRuntimeSupport: %v", err)
	}
	t.Cleanup(func() { _ = runtimeSupport.Background.Close() })
	appCore, err := core.New(resolved.Config, authSupport, runtimeSupport)
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	defer func() { _ = appCore.Close() }()
	gateway, err := NewGateway(appCore, protocol.ServerIdentity{ProtocolVersion: protocol.Version, ServerID: "server-1"})
	if err != nil {
		t.Fatalf("NewGateway: %v", err)
	}
	server := httptest.NewServer(gateway.Handler())
	defer server.Close()

	conn := dialGateway(t, server)
	defer func() { _ = conn.Close() }()
	handshakeGateway(t, conn)
	callGateway(t, conn, "attach-project", protocol.MethodAttachProject, protocol.AttachProjectRequest{ProjectID: appCore.ProjectID()}, nil)

	if err := websocket.JSON.Send(conn, protocol.Request{JSONRPC: protocol.JSONRPCVersion, ID: "attach-workspace", Method: protocol.MethodProjectAttachWorkspace, Params: mustJSON(t, serverapi.ProjectAttachWorkspaceRequest{ProjectID: appCore.ProjectID(), WorkspaceRoot: "/tmp/workspace"})}); err != nil {
		t.Fatalf("send project.attachWorkspace: %v", err)
	}
	var resp protocol.Response
	if err := websocket.JSON.Receive(conn, &resp); err != nil {
		t.Fatalf("receive project.attachWorkspace: %v", err)
	}
	if resp.Error == nil || resp.Error.Code != protocol.ErrCodeAuthRequired {
		t.Fatalf("project.attachWorkspace error = %+v, want auth required", resp.Error)
	}
}

func TestGatewayRejectsSessionActivitySubscriptionBeforeServerAuthReady(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	registerGatewayWorkspace(t, workspace)

	resolved, err := serverbootstrap.ResolveConfig(serverbootstrap.Request{WorkspaceRoot: workspace})
	if err != nil {
		t.Fatalf("ResolveConfig: %v", err)
	}
	authSupport := newGatewayTestAuthSupport(t, false)
	runtimeSupport, err := serverbootstrap.BuildRuntimeSupport(resolved.Config)
	if err != nil {
		t.Fatalf("BuildRuntimeSupport: %v", err)
	}
	t.Cleanup(func() { _ = runtimeSupport.Background.Close() })
	appCore, err := core.New(resolved.Config, authSupport, runtimeSupport)
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	defer func() { _ = appCore.Close() }()
	store := createGatewayAuthoritativeSession(t, appCore)
	appCore.RegisterSessionStore(store)
	gateway, err := NewGateway(appCore, protocol.ServerIdentity{ProtocolVersion: protocol.Version, ServerID: "server-1"})
	if err != nil {
		t.Fatalf("NewGateway: %v", err)
	}
	server := httptest.NewServer(gateway.Handler())
	defer server.Close()

	conn := dialGateway(t, server)
	defer func() { _ = conn.Close() }()
	handshakeGateway(t, conn)

	callGateway(t, conn, "attach-project", protocol.MethodAttachProject, protocol.AttachProjectRequest{ProjectID: appCore.ProjectID()}, nil)
	callGateway(t, conn, "attach-session", protocol.MethodAttachSession, protocol.AttachSessionRequest{SessionID: store.Meta().SessionID}, nil)
	if err := websocket.JSON.Send(conn, protocol.Request{JSONRPC: protocol.JSONRPCVersion, ID: "subscribe", Method: protocol.MethodSessionSubscribeActivity, Params: mustJSON(t, serverapi.SessionActivitySubscribeRequest{SessionID: store.Meta().SessionID})}); err != nil {
		t.Fatalf("send session activity subscribe: %v", err)
	}
	var resp protocol.Response
	if err := websocket.JSON.Receive(conn, &resp); err != nil {
		t.Fatalf("receive session activity subscribe: %v", err)
	}
	if resp.Error == nil || resp.Error.Code != protocol.ErrCodeAuthRequired {
		t.Fatalf("session activity subscribe error = %+v, want auth required", resp.Error)
	}
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func sameStringSet(left []string, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	counts := make(map[string]int, len(left))
	for _, item := range left {
		counts[item]++
	}
	for _, item := range right {
		counts[item]--
		if counts[item] < 0 {
			return false
		}
	}
	for _, count := range counts {
		if count != 0 {
			return false
		}
	}
	return true
}

func TestGatewayRejectsSessionAccessOutsideAttachedProject(t *testing.T) {
	home := t.TempDir()
	workspaceA := t.TempDir()
	workspaceB := t.TempDir()
	t.Setenv("HOME", home)
	configureGatewayTestServerPort(t)

	resolvedA, err := serverbootstrap.ResolveConfig(serverbootstrap.Request{WorkspaceRoot: workspaceA})
	if err != nil {
		t.Fatalf("ResolveConfig A: %v", err)
	}
	bindingA, err := metadata.RegisterBinding(context.Background(), resolvedA.Config.PersistenceRoot, resolvedA.Config.WorkspaceRoot)
	if err != nil {
		t.Fatalf("RegisterBinding A: %v", err)
	}
	resolvedB, err := serverbootstrap.ResolveConfig(serverbootstrap.Request{WorkspaceRoot: workspaceB})
	if err != nil {
		t.Fatalf("ResolveConfig B: %v", err)
	}
	bindingB, err := metadata.RegisterBinding(context.Background(), resolvedB.Config.PersistenceRoot, resolvedB.Config.WorkspaceRoot)
	if err != nil {
		t.Fatalf("RegisterBinding B: %v", err)
	}
	metadataStore, err := metadata.Open(resolvedA.Config.PersistenceRoot)
	if err != nil {
		t.Fatalf("metadata.Open: %v", err)
	}
	defer func() { _ = metadataStore.Close() }()
	foreignSession, err := session.Create(
		config.ProjectSessionsRoot(resolvedB.Config, bindingB.ProjectID),
		"workspace-b",
		resolvedB.Config.WorkspaceRoot,
		metadataStore.AuthoritativeSessionStoreOptions()...,
	)
	if err != nil {
		t.Fatalf("session.Create foreign: %v", err)
	}

	authSupport := newGatewayTestAuthSupport(t, true)
	runtimeSupport, err := serverbootstrap.BuildRuntimeSupport(resolvedA.Config)
	if err != nil {
		t.Fatalf("BuildRuntimeSupport: %v", err)
	}
	defer func() { _ = runtimeSupport.Background.Close() }()
	appCore, err := core.New(resolvedA.Config, authSupport, runtimeSupport)
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	defer func() { _ = appCore.Close() }()
	gateway, err := NewGateway(appCore, protocol.ServerIdentity{ProtocolVersion: protocol.Version, ServerID: "server-1"})
	if err != nil {
		t.Fatalf("NewGateway: %v", err)
	}
	server := httptest.NewServer(gateway.Handler())
	defer server.Close()

	remote, err := remoteclient.DialRemoteURLForProject(context.Background(), "ws"+server.URL[len("http"):], bindingA.ProjectID)
	if err != nil {
		t.Fatalf("DialRemoteURLForProject: %v", err)
	}
	defer func() { _ = remote.Close() }()

	if _, err := remote.GetSessionMainView(context.Background(), serverapi.SessionMainViewRequest{SessionID: foreignSession.Meta().SessionID}); err == nil {
		t.Fatal("expected foreign-project session view access to be rejected")
	}
	if _, err := remote.PersistInputDraft(context.Background(), serverapi.SessionPersistInputDraftRequest{ClientRequestID: "persist-foreign", SessionID: foreignSession.Meta().SessionID, ControllerLeaseID: "lease-foreign", Input: "should fail"}); err == nil {
		t.Fatal("expected foreign-project session mutation to be rejected")
	}
	if _, err := remote.RetargetSessionWorkspace(context.Background(), serverapi.SessionRetargetWorkspaceRequest{ClientRequestID: "retarget-foreign", SessionID: foreignSession.Meta().SessionID, WorkspaceRoot: resolvedA.Config.WorkspaceRoot}); err == nil {
		t.Fatal("expected foreign-project session retarget to be rejected")
	}
	if bindingA.ProjectID == bindingB.ProjectID {
		t.Fatalf("expected distinct project ids, both=%q", bindingA.ProjectID)
	}
}

func TestGatewayAllowsOptionalSessionLifecycleRequestsWithoutSessionID(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	registerGatewayWorkspace(t, workspace)

	resolved, err := serverbootstrap.ResolveConfig(serverbootstrap.Request{WorkspaceRoot: workspace})
	if err != nil {
		t.Fatalf("ResolveConfig: %v", err)
	}
	binding, err := metadata.ResolveBinding(context.Background(), resolved.Config.PersistenceRoot, resolved.Config.WorkspaceRoot)
	if err != nil {
		t.Fatalf("ResolveBinding: %v", err)
	}
	authSupport := newGatewayTestAuthSupport(t, true)
	runtimeSupport, err := serverbootstrap.BuildRuntimeSupport(resolved.Config)
	if err != nil {
		t.Fatalf("BuildRuntimeSupport: %v", err)
	}
	defer func() { _ = runtimeSupport.Background.Close() }()
	appCore, err := core.New(resolved.Config, authSupport, runtimeSupport)
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	defer func() { _ = appCore.Close() }()
	gateway, err := NewGateway(appCore, protocol.ServerIdentity{ProtocolVersion: protocol.Version, ServerID: "server-1"})
	if err != nil {
		t.Fatalf("NewGateway: %v", err)
	}
	server := httptest.NewServer(gateway.Handler())
	defer server.Close()

	remote, err := remoteclient.DialRemoteURLForProject(context.Background(), "ws"+server.URL[len("http"):], binding.ProjectID)
	if err != nil {
		t.Fatalf("DialRemoteURLForProject: %v", err)
	}
	defer func() { _ = remote.Close() }()

	initialInput, err := remote.GetInitialInput(context.Background(), serverapi.SessionInitialInputRequest{TransitionInput: "draft text"})
	if err != nil {
		t.Fatalf("GetInitialInput: %v", err)
	}
	if initialInput.Input != "draft text" {
		t.Fatalf("initial input = %q, want draft text", initialInput.Input)
	}

	resolvedTransition, err := remote.ResolveTransition(context.Background(), serverapi.SessionResolveTransitionRequest{
		ClientRequestID: "new-session-no-current-session",
		Transition: serverapi.SessionTransition{
			Action:        "new_session",
			InitialPrompt: "hello",
		},
	})
	if err != nil {
		t.Fatalf("ResolveTransition: %v", err)
	}
	if !resolvedTransition.ShouldContinue || !resolvedTransition.ForceNewSession {
		t.Fatalf("unexpected transition response: %+v", resolvedTransition)
	}
	if resolvedTransition.InitialPrompt != "hello" {
		t.Fatalf("initial prompt = %q, want hello", resolvedTransition.InitialPrompt)
	}
}

func TestGatewayProjectReattachClearsStaleSessionAttachment(t *testing.T) {
	home := t.TempDir()
	workspaceA := t.TempDir()
	workspaceB := t.TempDir()
	t.Setenv("HOME", home)
	configureGatewayTestServerPort(t)

	resolvedA, err := serverbootstrap.ResolveConfig(serverbootstrap.Request{WorkspaceRoot: workspaceA})
	if err != nil {
		t.Fatalf("ResolveConfig A: %v", err)
	}
	bindingA, err := metadata.RegisterBinding(context.Background(), resolvedA.Config.PersistenceRoot, resolvedA.Config.WorkspaceRoot)
	if err != nil {
		t.Fatalf("RegisterBinding A: %v", err)
	}
	resolvedB, err := serverbootstrap.ResolveConfig(serverbootstrap.Request{WorkspaceRoot: workspaceB})
	if err != nil {
		t.Fatalf("ResolveConfig B: %v", err)
	}
	bindingB, err := metadata.RegisterBinding(context.Background(), resolvedB.Config.PersistenceRoot, resolvedB.Config.WorkspaceRoot)
	if err != nil {
		t.Fatalf("RegisterBinding B: %v", err)
	}

	authSupport := newGatewayTestAuthSupport(t, true)
	runtimeSupport, err := serverbootstrap.BuildRuntimeSupport(resolvedA.Config)
	if err != nil {
		t.Fatalf("BuildRuntimeSupport: %v", err)
	}
	defer func() { _ = runtimeSupport.Background.Close() }()
	appCore, err := core.New(resolvedA.Config, authSupport, runtimeSupport)
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	defer func() { _ = appCore.Close() }()
	storeA := createGatewayAuthoritativeSession(t, appCore)
	appCore.RegisterSessionStore(storeA)
	gateway, err := NewGateway(appCore, protocol.ServerIdentity{ProtocolVersion: protocol.Version, ServerID: "server-1"})
	if err != nil {
		t.Fatalf("NewGateway: %v", err)
	}
	server := httptest.NewServer(gateway.Handler())
	defer server.Close()

	conn := dialGateway(t, server)
	defer func() { _ = conn.Close() }()
	handshakeGateway(t, conn)
	callGateway(t, conn, "attach-project-a", protocol.MethodAttachProject, protocol.AttachProjectRequest{ProjectID: bindingA.ProjectID}, nil)
	callGateway(t, conn, "attach-session-a", protocol.MethodAttachSession, protocol.AttachSessionRequest{SessionID: storeA.Meta().SessionID}, nil)
	callGateway(t, conn, "attach-project-b", protocol.MethodAttachProject, protocol.AttachProjectRequest{ProjectID: bindingB.ProjectID}, nil)

	if err := websocket.JSON.Send(conn, protocol.Request{JSONRPC: protocol.JSONRPCVersion, ID: "subscribe", Method: protocol.MethodSessionSubscribeActivity, Params: mustJSON(t, serverapi.SessionActivitySubscribeRequest{SessionID: storeA.Meta().SessionID})}); err != nil {
		t.Fatalf("send subscribe: %v", err)
	}
	var resp protocol.Response
	if err := websocket.JSON.Receive(conn, &resp); err != nil {
		t.Fatalf("receive subscribe response: %v", err)
	}
	if resp.Error == nil || resp.Error.Code != protocol.ErrCodeInvalidRequest {
		t.Fatalf("expected session-attach-required error after project reattach, got %+v", resp.Error)
	}
}

func TestGatewayRejectsAttachProjectWorkspaceOutsideProject(t *testing.T) {
	home := t.TempDir()
	workspaceA := t.TempDir()
	workspaceB := t.TempDir()
	t.Setenv("HOME", home)
	configureGatewayTestServerPort(t)

	resolvedA, err := serverbootstrap.ResolveConfig(serverbootstrap.Request{WorkspaceRoot: workspaceA})
	if err != nil {
		t.Fatalf("ResolveConfig A: %v", err)
	}
	bindingA, err := metadata.RegisterBinding(context.Background(), resolvedA.Config.PersistenceRoot, resolvedA.Config.WorkspaceRoot)
	if err != nil {
		t.Fatalf("RegisterBinding A: %v", err)
	}
	resolvedB, err := serverbootstrap.ResolveConfig(serverbootstrap.Request{WorkspaceRoot: workspaceB})
	if err != nil {
		t.Fatalf("ResolveConfig B: %v", err)
	}
	if _, err := metadata.RegisterBinding(context.Background(), resolvedB.Config.PersistenceRoot, resolvedB.Config.WorkspaceRoot); err != nil {
		t.Fatalf("RegisterBinding B: %v", err)
	}

	authSupport := newGatewayTestAuthSupport(t, true)
	runtimeSupport, err := serverbootstrap.BuildRuntimeSupport(resolvedA.Config)
	if err != nil {
		t.Fatalf("BuildRuntimeSupport: %v", err)
	}
	defer func() { _ = runtimeSupport.Background.Close() }()
	appCore, err := core.New(resolvedA.Config, authSupport, runtimeSupport)
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	defer func() { _ = appCore.Close() }()
	gateway, err := NewGateway(appCore, protocol.ServerIdentity{ProtocolVersion: protocol.Version, ServerID: "server-1"})
	if err != nil {
		t.Fatalf("NewGateway: %v", err)
	}
	server := httptest.NewServer(gateway.Handler())
	defer server.Close()

	conn := dialGateway(t, server)
	defer func() { _ = conn.Close() }()
	handshakeGateway(t, conn)
	if err := websocket.JSON.Send(conn, protocol.Request{JSONRPC: protocol.JSONRPCVersion, ID: "attach-project", Method: protocol.MethodAttachProject, Params: mustJSON(t, protocol.AttachProjectRequest{ProjectID: bindingA.ProjectID, WorkspaceRoot: resolvedB.Config.WorkspaceRoot})}); err != nil {
		t.Fatalf("send attach-project: %v", err)
	}
	var resp protocol.Response
	if err := websocket.JSON.Receive(conn, &resp); err != nil {
		t.Fatalf("receive attach-project: %v", err)
	}
	if resp.Error == nil || !strings.Contains(resp.Error.Message, "not bound to project") {
		t.Fatalf("expected workspace/project mismatch error, got %+v", resp.Error)
	}
}

func TestGatewayRequiresExplicitWorkspaceSelectionForMultiWorkspaceProject(t *testing.T) {
	home := t.TempDir()
	workspaceA := t.TempDir()
	workspaceB := t.TempDir()
	t.Setenv("HOME", home)
	configureGatewayTestServerPort(t)

	resolvedA, err := serverbootstrap.ResolveConfig(serverbootstrap.Request{WorkspaceRoot: workspaceA})
	if err != nil {
		t.Fatalf("ResolveConfig A: %v", err)
	}
	bindingA, err := metadata.RegisterBinding(context.Background(), resolvedA.Config.PersistenceRoot, resolvedA.Config.WorkspaceRoot)
	if err != nil {
		t.Fatalf("RegisterBinding A: %v", err)
	}
	metadataStore, err := metadata.Open(resolvedA.Config.PersistenceRoot)
	if err != nil {
		t.Fatalf("metadata.Open: %v", err)
	}
	defer func() { _ = metadataStore.Close() }()
	bindingB, err := metadataStore.AttachWorkspaceToProject(context.Background(), bindingA.ProjectID, workspaceB)
	if err != nil {
		t.Fatalf("AttachWorkspaceToProject B: %v", err)
	}

	authSupport := newGatewayTestAuthSupport(t, true)
	runtimeSupport, err := serverbootstrap.BuildRuntimeSupport(resolvedA.Config)
	if err != nil {
		t.Fatalf("BuildRuntimeSupport: %v", err)
	}
	defer func() { _ = runtimeSupport.Background.Close() }()
	appCore, err := core.New(resolvedA.Config, authSupport, runtimeSupport)
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	defer func() { _ = appCore.Close() }()
	gateway, err := NewGateway(appCore, protocol.ServerIdentity{ProtocolVersion: protocol.Version, ServerID: "server-1"})
	if err != nil {
		t.Fatalf("NewGateway: %v", err)
	}
	server := httptest.NewServer(gateway.Handler())
	defer server.Close()

	conn := dialGateway(t, server)
	defer func() { _ = conn.Close() }()
	handshakeGateway(t, conn)
	if err := websocket.JSON.Send(conn, protocol.Request{JSONRPC: protocol.JSONRPCVersion, ID: "attach-project", Method: protocol.MethodAttachProject, Params: mustJSON(t, protocol.AttachProjectRequest{ProjectID: bindingA.ProjectID})}); err != nil {
		t.Fatalf("send attach-project: %v", err)
	}
	var resp protocol.Response
	if err := websocket.JSON.Receive(conn, &resp); err != nil {
		t.Fatalf("receive attach-project: %v", err)
	}
	if resp.Error == nil || !strings.Contains(resp.Error.Message, "requires explicit workspace selection") {
		t.Fatalf("expected explicit workspace selection error, got %+v", resp.Error)
	}

	callGateway(t, conn, "attach-project-explicit", protocol.MethodAttachProject, protocol.AttachProjectRequest{ProjectID: bindingA.ProjectID, WorkspaceID: bindingB.WorkspaceID}, nil)
	var planResp serverapi.SessionPlanResponse
	callGateway(t, conn, "session-plan", protocol.MethodSessionPlan, serverapi.SessionPlanRequest{
		ClientRequestID: "plan-after-explicit-workspace",
		Mode:            serverapi.SessionLaunchModeInteractive,
		ForceNewSession: true,
	}, &planResp)
	if got, want := planResp.Plan.WorkspaceRoot, bindingB.CanonicalRoot; got != want {
		t.Fatalf("planned workspace root = %q, want %q", got, want)
	}
}

func TestGatewayAttachSessionClearsWorkspaceOverrideForLaterPlans(t *testing.T) {
	home := t.TempDir()
	workspaceA := t.TempDir()
	workspaceB := t.TempDir()
	t.Setenv("HOME", home)
	configureGatewayTestServerPort(t)

	resolvedB, err := serverbootstrap.ResolveConfig(serverbootstrap.Request{WorkspaceRoot: workspaceB})
	if err != nil {
		t.Fatalf("ResolveConfig B: %v", err)
	}
	bindingB, err := metadata.RegisterBinding(context.Background(), resolvedB.Config.PersistenceRoot, resolvedB.Config.WorkspaceRoot)
	if err != nil {
		t.Fatalf("RegisterBinding B: %v", err)
	}
	resolvedA, err := serverbootstrap.ResolveConfig(serverbootstrap.Request{WorkspaceRoot: workspaceA})
	if err != nil {
		t.Fatalf("ResolveConfig A: %v", err)
	}
	metadataStore, err := metadata.Open(resolvedA.Config.PersistenceRoot)
	if err != nil {
		t.Fatalf("metadata.Open: %v", err)
	}
	defer func() { _ = metadataStore.Close() }()
	if _, err := metadataStore.AttachWorkspaceToProject(context.Background(), bindingB.ProjectID, resolvedA.Config.WorkspaceRoot); err != nil {
		t.Fatalf("AttachWorkspaceToProject: %v", err)
	}

	authSupport := newGatewayTestAuthSupport(t, true)
	runtimeSupport, err := serverbootstrap.BuildRuntimeSupport(resolvedA.Config)
	if err != nil {
		t.Fatalf("BuildRuntimeSupport: %v", err)
	}
	defer func() { _ = runtimeSupport.Background.Close() }()
	appCore, err := core.New(resolvedA.Config, authSupport, runtimeSupport)
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	defer func() { _ = appCore.Close() }()

	storeB, err := session.Create(
		config.ProjectSessionsRoot(resolvedA.Config, bindingB.ProjectID),
		"workspace-b",
		resolvedB.Config.WorkspaceRoot,
		metadataStore.AuthoritativeSessionStoreOptions()...,
	)
	if err != nil {
		t.Fatalf("session.Create workspace B: %v", err)
	}
	if err := storeB.EnsureDurable(); err != nil {
		t.Fatalf("EnsureDurable workspace B: %v", err)
	}

	gateway, err := NewGateway(appCore, protocol.ServerIdentity{ProtocolVersion: protocol.Version, ServerID: "server-1"})
	if err != nil {
		t.Fatalf("NewGateway: %v", err)
	}
	server := httptest.NewServer(gateway.Handler())
	defer server.Close()

	conn := dialGateway(t, server)
	defer func() { _ = conn.Close() }()
	handshakeGateway(t, conn)
	callGateway(t, conn, "attach-project", protocol.MethodAttachProject, protocol.AttachProjectRequest{ProjectID: bindingB.ProjectID, WorkspaceRoot: resolvedA.Config.WorkspaceRoot}, nil)
	callGateway(t, conn, "attach-session", protocol.MethodAttachSession, protocol.AttachSessionRequest{SessionID: storeB.Meta().SessionID}, nil)

	var planResp serverapi.SessionPlanResponse
	callGateway(t, conn, "session-plan", protocol.MethodSessionPlan, serverapi.SessionPlanRequest{
		ClientRequestID: "new-after-attach-session",
		Mode:            serverapi.SessionLaunchModeInteractive,
		ForceNewSession: true,
	}, &planResp)
	wantWorkspaceRoot, err := config.CanonicalWorkspaceRoot(resolvedB.Config.WorkspaceRoot)
	if err != nil {
		t.Fatalf("CanonicalWorkspaceRoot B: %v", err)
	}
	if got, want := planResp.Plan.WorkspaceRoot, wantWorkspaceRoot; got != want {
		t.Fatalf("planned workspace root = %q, want %q", got, want)
	}
}

func TestGatewayScopesProcessAPIsToAttachedProject(t *testing.T) {
	home := t.TempDir()
	workspaceA := t.TempDir()
	workspaceB := t.TempDir()
	t.Setenv("HOME", home)
	configureGatewayTestServerPort(t)

	resolvedA, err := serverbootstrap.ResolveConfig(serverbootstrap.Request{WorkspaceRoot: workspaceA})
	if err != nil {
		t.Fatalf("ResolveConfig A: %v", err)
	}
	bindingA, err := metadata.RegisterBinding(context.Background(), resolvedA.Config.PersistenceRoot, resolvedA.Config.WorkspaceRoot)
	if err != nil {
		t.Fatalf("RegisterBinding A: %v", err)
	}
	resolvedB, err := serverbootstrap.ResolveConfig(serverbootstrap.Request{WorkspaceRoot: workspaceB})
	if err != nil {
		t.Fatalf("ResolveConfig B: %v", err)
	}
	bindingB, err := metadata.RegisterBinding(context.Background(), resolvedB.Config.PersistenceRoot, resolvedB.Config.WorkspaceRoot)
	if err != nil {
		t.Fatalf("RegisterBinding B: %v", err)
	}
	metadataStore, err := metadata.Open(resolvedA.Config.PersistenceRoot)
	if err != nil {
		t.Fatalf("metadata.Open: %v", err)
	}
	defer func() { _ = metadataStore.Close() }()

	authSupport := newGatewayTestAuthSupport(t, true)
	runtimeSupport, err := serverbootstrap.BuildRuntimeSupport(resolvedA.Config)
	if err != nil {
		t.Fatalf("BuildRuntimeSupport: %v", err)
	}
	defer func() { _ = runtimeSupport.Background.Close() }()
	appCore, err := core.New(resolvedA.Config, authSupport, runtimeSupport)
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	defer func() { _ = appCore.Close() }()
	appCore.Background().SetMinimumExecToBgTime(time.Millisecond)

	storeA := createGatewayAuthoritativeSession(t, appCore)
	appCore.RegisterSessionStore(storeA)
	storeB, err := session.Create(
		config.ProjectSessionsRoot(resolvedB.Config, bindingB.ProjectID),
		"workspace-b",
		resolvedB.Config.WorkspaceRoot,
		metadataStore.AuthoritativeSessionStoreOptions()...,
	)
	if err != nil {
		t.Fatalf("session.Create foreign: %v", err)
	}
	if err := storeB.EnsureDurable(); err != nil {
		t.Fatalf("EnsureDurable foreign: %v", err)
	}

	ownResult, err := appCore.Background().Start(context.Background(), shelltool.ExecRequest{
		Command:        []string{"/bin/sh", "-lc", "printf own\\n; sleep 1"},
		DisplayCommand: "printf own; sleep 1",
		OwnerSessionID: storeA.Meta().SessionID,
		Workdir:        appCore.Config().WorkspaceRoot,
		YieldTime:      time.Millisecond,
	})
	if err != nil {
		t.Fatalf("start own process: %v", err)
	}
	foreignResult, err := appCore.Background().Start(context.Background(), shelltool.ExecRequest{
		Command:        []string{"/bin/sh", "-lc", "printf foreign\\n; sleep 1"},
		DisplayCommand: "printf foreign; sleep 1",
		OwnerSessionID: storeB.Meta().SessionID,
		Workdir:        resolvedB.Config.WorkspaceRoot,
		YieldTime:      time.Millisecond,
	})
	if err != nil {
		t.Fatalf("start foreign process: %v", err)
	}

	gateway, err := NewGateway(appCore, protocol.ServerIdentity{ProtocolVersion: protocol.Version, ServerID: "server-1"})
	if err != nil {
		t.Fatalf("NewGateway: %v", err)
	}
	server := httptest.NewServer(gateway.Handler())
	defer server.Close()

	remote, err := remoteclient.DialRemoteURLForProject(context.Background(), "ws"+server.URL[len("http"):], bindingA.ProjectID)
	if err != nil {
		t.Fatalf("DialRemoteURLForProject: %v", err)
	}
	defer func() { _ = remote.Close() }()

	listed, err := remote.ListProcesses(context.Background(), serverapi.ProcessListRequest{})
	if err != nil {
		t.Fatalf("ListProcesses: %v", err)
	}
	if len(listed.Processes) != 1 || listed.Processes[0].ID != ownResult.SessionID {
		t.Fatalf("expected only own project process, got %+v", listed.Processes)
	}
	if _, err := remote.GetProcess(context.Background(), serverapi.ProcessGetRequest{ProcessID: foreignResult.SessionID}); err == nil {
		t.Fatal("expected foreign process get to be rejected")
	}
	if _, err := remote.GetInlineOutput(context.Background(), serverapi.ProcessInlineOutputRequest{ProcessID: foreignResult.SessionID, MaxChars: 128}); err == nil {
		t.Fatal("expected foreign process inline output to be rejected")
	}
	if _, err := remote.KillProcess(context.Background(), serverapi.ProcessKillRequest{ClientRequestID: "kill-foreign", ProcessID: foreignResult.SessionID}); err == nil {
		t.Fatal("expected foreign process kill to be rejected")
	}
	if _, err := remote.SubscribeProcessOutput(context.Background(), serverapi.ProcessOutputSubscribeRequest{ProcessID: foreignResult.SessionID, OffsetBytes: 0}); err == nil {
		t.Fatal("expected foreign process output subscription to be rejected")
	}
	if _, err := remote.GetProcess(context.Background(), serverapi.ProcessGetRequest{ProcessID: ownResult.SessionID}); err != nil {
		t.Fatalf("expected own process get to succeed, got %v", err)
	}
	if bindingA.ProjectID == bindingB.ProjectID {
		t.Fatalf("expected distinct project ids, both=%q", bindingA.ProjectID)
	}
}

func TestGatewaySessionActivitySubscriptionStreamsEventsAndCompletion(t *testing.T) {
	appCore, server := newGatewayTestServer(t)
	defer server.Close()
	store := createGatewayAuthoritativeSession(t, appCore)
	appCore.RegisterSessionStore(store)

	engine := &runtime.Engine{}
	appCore.RegisterRuntime(store.Meta().SessionID, engine)
	defer appCore.UnregisterRuntime(store.Meta().SessionID, engine)

	conn := dialGateway(t, server)
	defer func() { _ = conn.Close() }()
	handshakeGateway(t, conn)
	callGateway(t, conn, "attach", protocol.MethodAttachSession, protocol.AttachSessionRequest{SessionID: store.Meta().SessionID}, nil)
	callGateway(t, conn, "subscribe", protocol.MethodSessionSubscribeActivity, serverapi.SessionActivitySubscribeRequest{SessionID: store.Meta().SessionID}, nil)

	appCore.PublishRuntimeEvent(store.Meta().SessionID, runtime.Event{Kind: runtime.EventConversationUpdated, StepID: "step-1"})
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

	appCore.PublishRuntimeEvent(store.Meta().SessionID, runtime.Event{
		Kind:     runtime.EventToolCallStarted,
		ToolCall: &llm.ToolCall{ID: "call-1", Name: "shell"},
	})
	if err := websocket.JSON.Receive(conn, &notif); err != nil {
		t.Fatalf("receive tool event: %v", err)
	}
	if notif.Method != protocol.MethodSessionActivityEvent {
		t.Fatalf("tool event method = %q", notif.Method)
	}
	if err := json.Unmarshal(notif.Params, &event); err != nil {
		t.Fatalf("decode tool event params: %v", err)
	}
	if len(event.Event.TranscriptEntries) != 1 {
		t.Fatalf("tool transcript entries len = %d, want 1", len(event.Event.TranscriptEntries))
	}
	if event.Event.TranscriptEntries[0].Role != "tool_call" {
		t.Fatalf("tool transcript role = %q, want tool_call", event.Event.TranscriptEntries[0].Role)
	}
	if event.Event.TranscriptEntries[0].Text != "tool call" {
		t.Fatalf("expected raw gateway passthrough to preserve unformatted tool call text, got %+v", event.Event.TranscriptEntries[0])
	}

	appCore.UnregisterRuntime(store.Meta().SessionID, engine)
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

func TestGatewayRemoteSessionActivityRecoversToolCallTextWithoutPresentation(t *testing.T) {
	appCore, server := newGatewayTestServer(t)
	defer server.Close()
	store := createGatewayAuthoritativeSession(t, appCore)
	appCore.RegisterSessionStore(store)

	engine := &runtime.Engine{}
	appCore.RegisterRuntime(store.Meta().SessionID, engine)
	defer appCore.UnregisterRuntime(store.Meta().SessionID, engine)

	remote, err := remoteclient.DialRemoteURLForProject(context.Background(), "ws"+server.URL[len("http"):], appCore.ProjectID())
	if err != nil {
		t.Fatalf("DialRemote: %v", err)
	}
	defer func() { _ = remote.Close() }()

	sub, err := remote.SubscribeSessionActivity(context.Background(), serverapi.SessionActivitySubscribeRequest{SessionID: store.Meta().SessionID})
	if err != nil {
		t.Fatalf("SubscribeSessionActivity: %v", err)
	}
	defer func() { _ = sub.Close() }()

	appCore.PublishRuntimeEvent(store.Meta().SessionID, runtime.Event{
		Kind:     runtime.EventToolCallStarted,
		ToolCall: &llm.ToolCall{ID: "call-1", Name: string(toolspec.ToolShell), Input: json.RawMessage(`{"command":"pwd"}`)},
	})

	evt, err := sub.Next(context.Background())
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if evt.Kind != clientui.EventToolCallStarted {
		t.Fatalf("event kind = %q, want %q", evt.Kind, clientui.EventToolCallStarted)
	}
	if len(evt.TranscriptEntries) != 1 {
		t.Fatalf("transcript entries len = %d, want 1", len(evt.TranscriptEntries))
	}
	entry := evt.TranscriptEntries[0]
	if entry.Role != "tool_call" || entry.Text != "pwd" {
		t.Fatalf("unexpected remote transcript entry: %+v", entry)
	}
	if entry.ToolCall == nil || !entry.ToolCall.IsShell || entry.ToolCall.Command != "pwd" {
		t.Fatalf("expected recovered shell metadata, got %+v", entry.ToolCall)
	}
}

func TestGatewayRemoteSessionActivityStreamsDirectSubmittedUserMessage(t *testing.T) {
	appCore, server := newGatewayTestServer(t)
	defer server.Close()

	store := createGatewayAuthoritativeSession(t, appCore)
	controllerLeaseID := activateGatewayController(t, appCore, store.Meta().SessionID)
	defer releaseGatewayController(t, appCore, store.Meta().SessionID, controllerLeaseID)
	eng, err := runtime.New(store, gatewayTestLLMClient{response: llm.Response{Assistant: llm.Message{Role: llm.RoleAssistant, Content: "done"}, Usage: llm.Usage{WindowTokens: 200000}}}, tools.NewRegistry(), runtime.Config{Model: "gpt-5", OnEvent: func(evt runtime.Event) {
		appCore.PublishRuntimeEvent(store.Meta().SessionID, evt)
	}})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	appCore.RegisterSessionStore(store)
	appCore.RegisterRuntime(store.Meta().SessionID, eng)
	defer appCore.UnregisterRuntime(store.Meta().SessionID, eng)

	remote, err := remoteclient.DialRemoteURLForProject(context.Background(), "ws"+server.URL[len("http"):], appCore.ProjectID())
	if err != nil {
		t.Fatalf("DialRemote: %v", err)
	}
	defer func() { _ = remote.Close() }()

	sub, err := remote.SubscribeSessionActivity(context.Background(), serverapi.SessionActivitySubscribeRequest{SessionID: store.Meta().SessionID})
	if err != nil {
		t.Fatalf("SubscribeSessionActivity: %v", err)
	}
	defer func() { _ = sub.Close() }()

	if _, err := remote.SubmitUserMessage(context.Background(), serverapi.RuntimeSubmitUserMessageRequest{ClientRequestID: "submit-say-hi", SessionID: store.Meta().SessionID, ControllerLeaseID: controllerLeaseID, Text: "say hi"}); err != nil {
		t.Fatalf("SubmitUserMessage: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var evt clientui.Event
	for {
		next, err := sub.Next(ctx)
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		if next.Kind != clientui.EventUserMessageFlushed {
			continue
		}
		evt = next
		break
	}
	if evt.UserMessage != "say hi" {
		t.Fatalf("user message = %q, want say hi", evt.UserMessage)
	}
	if len(evt.TranscriptEntries) != 1 {
		t.Fatalf("transcript entries len = %d, want 1", len(evt.TranscriptEntries))
	}
	if evt.TranscriptEntries[0].Role != "user" || evt.TranscriptEntries[0].Text != "say hi" {
		t.Fatalf("unexpected transcript entry: %+v", evt.TranscriptEntries[0])
	}
}

func TestGatewayRemoteSessionActivityPreservesActiveSubmitOrderingUsingAssistantDeltaProgress(t *testing.T) {
	appCore, server := newGatewayTestServer(t)
	defer server.Close()

	store := createGatewayAuthoritativeSession(t, appCore)
	controllerLeaseID := activateGatewayController(t, appCore, store.Meta().SessionID)
	defer releaseGatewayController(t, appCore, store.Meta().SessionID, controllerLeaseID)
	eng, err := runtime.New(store, &gatewayTestStreamingClient{}, tools.NewRegistry(gatewayTestShellTool{}), runtime.Config{Model: "gpt-5", OnEvent: func(evt runtime.Event) {
		appCore.PublishRuntimeEvent(store.Meta().SessionID, evt)
	}})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	appCore.RegisterSessionStore(store)
	appCore.RegisterRuntime(store.Meta().SessionID, eng)
	defer appCore.UnregisterRuntime(store.Meta().SessionID, eng)

	remote, err := remoteclient.DialRemoteURLForProject(context.Background(), "ws"+server.URL[len("http"):], appCore.ProjectID())
	if err != nil {
		t.Fatalf("DialRemote: %v", err)
	}
	defer func() { _ = remote.Close() }()

	sub, err := remote.SubscribeSessionActivity(context.Background(), serverapi.SessionActivitySubscribeRequest{SessionID: store.Meta().SessionID})
	if err != nil {
		t.Fatalf("SubscribeSessionActivity: %v", err)
	}
	defer func() { _ = sub.Close() }()

	submitDone := make(chan error, 1)
	go func() {
		_, submitErr := remote.SubmitUserMessage(context.Background(), serverapi.RuntimeSubmitUserMessageRequest{ClientRequestID: "submit-run-tools", SessionID: store.Meta().SessionID, ControllerLeaseID: controllerLeaseID, Text: "run tools"})
		submitDone <- submitErr
	}()

	// Remote session activity exposes both assistant_delta progress and the persisted
	// commentary assistant transcript entry for the first assistant/tool-call turn.
	// The commentary assistant event must stay distinct from the tool call event and
	// must not carry duplicated tool calls.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	sequence := make([]string, 0, 6)
	commentaryTranscriptSeen := false
	for len(sequence) < 6 {
		evt, err := sub.Next(ctx)
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		for _, entry := range evt.TranscriptEntries {
			if entry.Role == "assistant" && entry.Phase == string(llm.MessagePhaseCommentary) {
				commentaryTranscriptSeen = true
			}
		}
		switch evt.Kind {
		case clientui.EventUserMessageFlushed:
			if len(evt.TranscriptEntries) != 1 || evt.TranscriptEntries[0].Role != "user" || evt.TranscriptEntries[0].Text != "run tools" {
				t.Fatalf("unexpected flushed user transcript entries: %+v", evt.TranscriptEntries)
			}
			sequence = append(sequence, "user")
		case clientui.EventAssistantDelta:
			if evt.AssistantDelta == "" {
				continue
			}
			sequence = append(sequence, "assistant_progress")
		case clientui.EventAssistantMessage:
			if len(evt.TranscriptEntries) != 1 {
				t.Fatalf("assistant transcript entries len = %d, want 1", len(evt.TranscriptEntries))
			}
			entry := evt.TranscriptEntries[0]
			if entry.Phase == string(llm.MessagePhaseCommentary) {
				if entry.Role != "assistant" || entry.Text != "Inspecting now" {
					t.Fatalf("unexpected commentary assistant transcript entry: %+v", entry)
				}
				sequence = append(sequence, "commentary")
				continue
			}
			if entry.Role != "assistant" || entry.Text != "done" || entry.Phase != string(llm.MessagePhaseFinal) {
				t.Fatalf("unexpected final assistant transcript entry: %+v", entry)
			}
			sequence = append(sequence, "final")
		case clientui.EventToolCallStarted:
			if len(evt.TranscriptEntries) != 1 || evt.TranscriptEntries[0].Role != "tool_call" || evt.TranscriptEntries[0].Text != "pwd" {
				t.Fatalf("unexpected tool call transcript entries: %+v", evt.TranscriptEntries)
			}
			sequence = append(sequence, "tool_call")
		case clientui.EventToolCallCompleted:
			if len(evt.TranscriptEntries) != 1 || evt.TranscriptEntries[0].Role != "tool_result_ok" || evt.TranscriptEntries[0].ToolCallID != "call-1" {
				t.Fatalf("unexpected tool result transcript entries: %+v", evt.TranscriptEntries)
			}
			sequence = append(sequence, "tool_result")
		}
	}
	if !commentaryTranscriptSeen {
		t.Fatalf("expected remote session activity to include commentary transcript entry for the tool-call turn, got sequence=%v", sequence)
	}
	want := []string{"user", "assistant_progress", "commentary", "tool_call", "tool_result", "final"}
	if len(sequence) != len(want) {
		t.Fatalf("sequence len = %d, want %d (%v)", len(sequence), len(want), sequence)
	}
	for i := range want {
		if sequence[i] != want[i] {
			t.Fatalf("sequence[%d] = %q, want %q (full=%v)", i, sequence[i], want[i], sequence)
		}
	}
	select {
	case err := <-submitDone:
		if err != nil {
			t.Fatalf("SubmitUserMessage: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for submit to complete")
	}
}

type gatewayTestLLMClient struct {
	response llm.Response
}

func (c gatewayTestLLMClient) Generate(context.Context, llm.Request) (llm.Response, error) {
	return c.response, nil
}

type gatewayTestStreamingClient struct {
	mu    sync.Mutex
	calls int
}

func (c *gatewayTestStreamingClient) Generate(context.Context, llm.Request) (llm.Response, error) {
	return llm.Response{}, nil
}

func (c *gatewayTestStreamingClient) GenerateStreamWithEvents(_ context.Context, _ llm.Request, callbacks llm.StreamCallbacks) (llm.Response, error) {
	c.mu.Lock()
	call := c.calls
	c.calls++
	c.mu.Unlock()
	if call == 0 {
		if callbacks.OnAssistantDelta != nil {
			callbacks.OnAssistantDelta("inspecting")
		}
		return llm.Response{
			Assistant: llm.Message{Role: llm.RoleAssistant, Content: "Inspecting now", Phase: llm.MessagePhaseCommentary},
			ToolCalls: []llm.ToolCall{{ID: "call-1", Name: string(toolspec.ToolShell), Input: json.RawMessage(`{"command":"pwd"}`)}},
			Usage:     llm.Usage{WindowTokens: 200000},
		}, nil
	}
	return llm.Response{
		Assistant: llm.Message{Role: llm.RoleAssistant, Content: "done", Phase: llm.MessagePhaseFinal},
		Usage:     llm.Usage{WindowTokens: 200000},
	}, nil
}

func (c *gatewayTestStreamingClient) ProviderCapabilities(context.Context) (llm.ProviderCapabilities, error) {
	return llm.ProviderCapabilities{
		ProviderID:                    "openai",
		SupportsResponsesAPI:          true,
		SupportsResponsesCompact:      true,
		SupportsReasoningEncrypted:    true,
		SupportsServerSideContextEdit: true,
		IsOpenAIFirstParty:            true,
	}, nil
}

type gatewayTestShellTool struct{}

func (gatewayTestShellTool) Name() toolspec.ID { return toolspec.ToolShell }

func (gatewayTestShellTool) Call(_ context.Context, call tools.Call) (tools.Result, error) {
	return tools.Result{CallID: call.ID, Name: call.Name, Output: json.RawMessage(`{"output":"/tmp\n"}`)}, nil
}

func TestGatewayProcessOutputSubscriptionStreamsOutputAndCompletion(t *testing.T) {
	appCore, server := newGatewayTestServer(t)
	defer server.Close()
	appCore.Background().SetMinimumExecToBgTime(time.Millisecond)
	store := createGatewayAuthoritativeSession(t, appCore)
	appCore.RegisterSessionStore(store)

	result, err := appCore.Background().Start(context.Background(), shelltool.ExecRequest{
		Command:        []string{"/bin/sh", "-lc", "printf 'hello\\n'; sleep 0.05"},
		DisplayCommand: "printf 'hello\\n'; sleep 0.05",
		OwnerSessionID: store.Meta().SessionID,
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
	store := createGatewayAuthoritativeSession(t, appCore)
	appCore.RegisterSessionStore(store)

	engine := &runtime.Engine{}
	appCore.RegisterRuntime(store.Meta().SessionID, engine)
	defer appCore.UnregisterRuntime(store.Meta().SessionID, engine)
	appCore.BeginPendingPrompt(store.Meta().SessionID, askquestion.Request{ID: "ask-1", Question: "Proceed?", Suggestions: []string{"Yes", "No"}})

	conn := dialGateway(t, server)
	defer func() { _ = conn.Close() }()
	handshakeGateway(t, conn)
	callGateway(t, conn, "attach", protocol.MethodAttachSession, protocol.AttachSessionRequest{SessionID: store.Meta().SessionID}, nil)
	callGateway(t, conn, "subscribe", protocol.MethodPromptSubscribeActivity, serverapi.PromptActivitySubscribeRequest{SessionID: store.Meta().SessionID}, nil)

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

	appCore.CompletePendingPrompt(store.Meta().SessionID, "ask-1")
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

	appCore.UnregisterRuntime(store.Meta().SessionID, engine)
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
	registerGatewayWorkspace(t, workspace)

	resolved, err := serverbootstrap.ResolveConfig(serverbootstrap.Request{WorkspaceRoot: workspace})
	if err != nil {
		t.Fatalf("ResolveConfig: %v", err)
	}
	authSupport := newGatewayTestAuthSupport(t, true)
	runtimeSupport, err := serverbootstrap.BuildRuntimeSupport(resolved.Config)
	if err != nil {
		t.Fatalf("BuildRuntimeSupport: %v", err)
	}
	t.Cleanup(func() { _ = runtimeSupport.Background.Close() })
	appCore, err := core.New(resolved.Config, authSupport, runtimeSupport)
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	gateway, err := NewGateway(appCore, protocol.ServerIdentity{ProtocolVersion: protocol.Version, ServerID: "server-1"})
	if err != nil {
		t.Fatalf("NewGateway: %v", err)
	}
	return appCore, httptest.NewServer(gateway.Handler())
}

func createGatewayAuthoritativeSession(t *testing.T, appCore *core.Core) *session.Store {
	t.Helper()
	metadataStore, err := metadata.Open(appCore.Config().PersistenceRoot)
	if err != nil {
		t.Fatalf("metadata.Open: %v", err)
	}
	t.Cleanup(func() { _ = metadataStore.Close() })
	store, err := session.Create(
		config.ProjectSessionsRoot(appCore.Config(), appCore.ProjectID()),
		filepath.Base(appCore.Config().WorkspaceRoot),
		appCore.Config().WorkspaceRoot,
		metadataStore.AuthoritativeSessionStoreOptions()...,
	)
	if err != nil {
		t.Fatalf("session.Create: %v", err)
	}
	if err := store.EnsureDurable(); err != nil {
		t.Fatalf("EnsureDurable: %v", err)
	}
	return store
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

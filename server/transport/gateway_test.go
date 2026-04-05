package transport

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"builder/server/auth"
	serverbootstrap "builder/server/bootstrap"
	"builder/server/core"
	"builder/server/llm"
	"builder/server/runtime"
	"builder/server/session"
	"builder/server/tools"
	askquestion "builder/server/tools/askquestion"
	shelltool "builder/server/tools/shell"
	remoteclient "builder/shared/client"
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

	engine := &runtime.Engine{}
	appCore.RegisterRuntime("session-1", engine)
	defer appCore.UnregisterRuntime("session-1", engine)

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

	appCore.PublishRuntimeEvent("session-1", runtime.Event{
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

	appCore.UnregisterRuntime("session-1", engine)
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

	engine := &runtime.Engine{}
	appCore.RegisterRuntime("session-1", engine)
	defer appCore.UnregisterRuntime("session-1", engine)

	remote, err := remoteclient.DialRemote(context.Background(), protocol.DiscoveryRecord{
		RPCURL:   "ws" + server.URL[len("http"):],
		Identity: protocol.ServerIdentity{ProjectID: appCore.ProjectID()},
	})
	if err != nil {
		t.Fatalf("DialRemote: %v", err)
	}
	defer func() { _ = remote.Close() }()

	sub, err := remote.SubscribeSessionActivity(context.Background(), serverapi.SessionActivitySubscribeRequest{SessionID: "session-1"})
	if err != nil {
		t.Fatalf("SubscribeSessionActivity: %v", err)
	}
	defer func() { _ = sub.Close() }()

	appCore.PublishRuntimeEvent("session-1", runtime.Event{
		Kind:     runtime.EventToolCallStarted,
		ToolCall: &llm.ToolCall{ID: "call-1", Name: string(tools.ToolShell), Input: json.RawMessage(`{"command":"pwd"}`)},
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

	store, err := session.Create(t.TempDir(), "ws", t.TempDir())
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	eng, err := runtime.New(store, gatewayTestLLMClient{response: llm.Response{Assistant: llm.Message{Role: llm.RoleAssistant, Content: "done"}, Usage: llm.Usage{WindowTokens: 200000}}}, tools.NewRegistry(), runtime.Config{Model: "gpt-5", OnEvent: func(evt runtime.Event) {
		appCore.PublishRuntimeEvent(store.Meta().SessionID, evt)
	}})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	appCore.RegisterSessionStore(store)
	appCore.RegisterRuntime(store.Meta().SessionID, eng)
	defer appCore.UnregisterRuntime(store.Meta().SessionID, eng)

	remote, err := remoteclient.DialRemote(context.Background(), protocol.DiscoveryRecord{
		RPCURL:   "ws" + server.URL[len("http"):],
		Identity: protocol.ServerIdentity{ProjectID: appCore.ProjectID()},
	})
	if err != nil {
		t.Fatalf("DialRemote: %v", err)
	}
	defer func() { _ = remote.Close() }()

	sub, err := remote.SubscribeSessionActivity(context.Background(), serverapi.SessionActivitySubscribeRequest{SessionID: store.Meta().SessionID})
	if err != nil {
		t.Fatalf("SubscribeSessionActivity: %v", err)
	}
	defer func() { _ = sub.Close() }()

	if _, err := remote.SubmitUserMessage(context.Background(), serverapi.RuntimeSubmitUserMessageRequest{SessionID: store.Meta().SessionID, Text: "say hi"}); err != nil {
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

	store, err := session.Create(t.TempDir(), "ws", t.TempDir())
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	eng, err := runtime.New(store, &gatewayTestStreamingClient{}, tools.NewRegistry(gatewayTestShellTool{}), runtime.Config{Model: "gpt-5", OnEvent: func(evt runtime.Event) {
		appCore.PublishRuntimeEvent(store.Meta().SessionID, evt)
	}})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	appCore.RegisterSessionStore(store)
	appCore.RegisterRuntime(store.Meta().SessionID, eng)
	defer appCore.UnregisterRuntime(store.Meta().SessionID, eng)

	remote, err := remoteclient.DialRemote(context.Background(), protocol.DiscoveryRecord{
		RPCURL:   "ws" + server.URL[len("http"):],
		Identity: protocol.ServerIdentity{ProjectID: appCore.ProjectID()},
	})
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
		_, submitErr := remote.SubmitUserMessage(context.Background(), serverapi.RuntimeSubmitUserMessageRequest{SessionID: store.Meta().SessionID, Text: "run tools"})
		submitDone <- submitErr
	}()

	// Remote session activity currently exposes live assistant progress via assistant_delta,
	// but it does not surface the persisted commentary transcript entry for the first
	// assistant/tool-call turn. This locks the strongest migrated-path guarantee today:
	// user submit -> assistant-visible progress -> tool call -> tool result -> final assistant.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	sequence := make([]string, 0, 5)
	commentaryTranscriptSeen := false
	for len(sequence) < 5 {
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
		case clientui.EventAssistantMessage:
			if len(evt.TranscriptEntries) != 1 {
				t.Fatalf("assistant transcript entries len = %d, want 1", len(evt.TranscriptEntries))
			}
			entry := evt.TranscriptEntries[0]
			if entry.Role != "assistant" || entry.Text != "done" || entry.Phase != string(llm.MessagePhaseFinal) {
				t.Fatalf("unexpected final assistant transcript entry: %+v", entry)
			}
			sequence = append(sequence, "final")
		}
	}
	if commentaryTranscriptSeen {
		t.Fatalf("expected remote session activity to omit commentary transcript entries for the tool-call turn, got sequence=%v", sequence)
	}
	want := []string{"user", "assistant_progress", "tool_call", "tool_result", "final"}
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
			ToolCalls: []llm.ToolCall{{ID: "call-1", Name: string(tools.ToolShell), Input: json.RawMessage(`{"command":"pwd"}`)}},
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

func (gatewayTestShellTool) Name() tools.ID { return tools.ToolShell }

func (gatewayTestShellTool) Call(_ context.Context, call tools.Call) (tools.Result, error) {
	return tools.Result{CallID: call.ID, Name: call.Name, Output: json.RawMessage(`{"output":"/tmp\n"}`)}, nil
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

	engine := &runtime.Engine{}
	appCore.RegisterRuntime("session-1", engine)
	defer appCore.UnregisterRuntime("session-1", engine)
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

	appCore.UnregisterRuntime("session-1", engine)
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

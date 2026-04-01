package client

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"testing"
	"time"

	"builder/shared/protocol"
	"builder/shared/serverapi"
	"golang.org/x/net/websocket"
)

func TestRemoteRunPromptPublishesProgressNotifications(t *testing.T) {
	server := httptest.NewServer(websocket.Handler(func(ws *websocket.Conn) {
		defer func() { _ = ws.Close() }()
		var req protocol.Request
		if err := websocket.JSON.Receive(ws, &req); err != nil {
			t.Fatalf("receive handshake: %v", err)
		}
		if req.Method != protocol.MethodHandshake {
			t.Fatalf("handshake method = %q", req.Method)
		}
		if err := websocket.JSON.Send(ws, protocol.NewSuccessResponse(req.ID, protocol.HandshakeResponse{Identity: protocol.ServerIdentity{ProtocolVersion: protocol.Version, ServerID: "server-1", ProjectID: "project-1"}})); err != nil {
			t.Fatalf("send handshake response: %v", err)
		}
		if err := websocket.JSON.Receive(ws, &req); err != nil {
			if errors.Is(err, io.EOF) {
				return
			}
			t.Fatalf("receive run prompt: %v", err)
		}
		if req.Method != protocol.MethodRunPrompt {
			t.Fatalf("run prompt method = %q", req.Method)
		}
		if err := websocket.JSON.Send(ws, protocol.Request{JSONRPC: protocol.JSONRPCVersion, Method: protocol.MethodRunPromptProgress, Params: mustJSON(t, serverapi.RunPromptProgress{Kind: serverapi.RunPromptProgressKindStatus, Message: "Running tool"})}); err != nil {
			t.Fatalf("send progress: %v", err)
		}
		if err := websocket.JSON.Send(ws, protocol.Request{JSONRPC: protocol.JSONRPCVersion, Method: protocol.MethodRunPromptProgress, Params: mustJSON(t, serverapi.RunPromptProgress{Kind: serverapi.RunPromptProgressKindStatus, Message: "Tool finished"})}); err != nil {
			t.Fatalf("send progress: %v", err)
		}
		if err := websocket.JSON.Send(ws, protocol.NewSuccessResponse(req.ID, serverapi.RunPromptResponse{SessionID: "session-1", SessionName: "Session 1", Result: "done"})); err != nil {
			t.Fatalf("send response: %v", err)
		}
	}))
	defer server.Close()

	remote, err := DialRemote(context.Background(), protocol.DiscoveryRecord{RPCURL: "ws" + server.URL[len("http"):], Identity: protocol.ServerIdentity{ProjectID: "project-1"}})
	if err != nil {
		t.Fatalf("DialRemote: %v", err)
	}
	defer func() { _ = remote.Close() }()

	var updates []serverapi.RunPromptProgress
	resp, err := remote.RunPrompt(context.Background(), serverapi.RunPromptRequest{ClientRequestID: "req-1", Prompt: "hello"}, serverapi.RunPromptProgressFunc(func(progress serverapi.RunPromptProgress) {
		updates = append(updates, progress)
	}))
	if err != nil {
		t.Fatalf("RunPrompt: %v", err)
	}
	if resp.SessionID != "session-1" || resp.Result != "done" {
		t.Fatalf("unexpected run prompt response: %+v", resp)
	}
	if len(updates) != 2 || updates[0].Message != "Running tool" || updates[1].Message != "Tool finished" {
		t.Fatalf("unexpected progress updates: %+v", updates)
	}
}

func TestRemoteSessionActivitySubscriptionNextHonorsCanceledContext(t *testing.T) {
	server := httptest.NewServer(websocket.Handler(func(ws *websocket.Conn) {
		defer func() { _ = ws.Close() }()
		var req protocol.Request
		if err := websocket.JSON.Receive(ws, &req); err != nil {
			return
		}
		if err := websocket.JSON.Send(ws, protocol.NewSuccessResponse(req.ID, protocol.HandshakeResponse{Identity: protocol.ServerIdentity{ProtocolVersion: protocol.Version, ServerID: "server-1", ProjectID: "project-1"}})); err != nil {
			return
		}
		if err := websocket.JSON.Receive(ws, &req); err != nil {
			return
		}
		if err := websocket.JSON.Send(ws, protocol.NewSuccessResponse(req.ID, protocol.AttachResponse{Kind: "session", SessionID: "session-1"})); err != nil {
			return
		}
		if err := websocket.JSON.Receive(ws, &req); err != nil {
			return
		}
		if err := websocket.JSON.Send(ws, protocol.NewSuccessResponse(req.ID, protocol.SubscribeResponse{})); err != nil {
			return
		}
		<-time.After(2 * time.Second)
	}))
	defer server.Close()

	remote, err := DialRemote(context.Background(), protocol.DiscoveryRecord{RPCURL: "ws" + server.URL[len("http"):], Identity: protocol.ServerIdentity{ProjectID: "project-1"}})
	if err != nil {
		t.Fatalf("DialRemote: %v", err)
	}
	defer func() { _ = remote.Close() }()

	sub, err := remote.SubscribeSessionActivity(context.Background(), serverapi.SessionActivitySubscribeRequest{SessionID: "session-1"})
	if err != nil {
		t.Fatalf("SubscribeSessionActivity: %v", err)
	}
	defer func() { _ = sub.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		_, err := sub.Next(ctx)
		errCh <- err
	}()

	<-time.After(50 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Next error = %v, want context canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Next to honor cancellation")
	}
}

func mustJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return data
}

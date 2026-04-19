package rpcwire

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"builder/shared/protocol"
)

func TestWebSocketTransportRoundTrip(t *testing.T) {
	transport := NewWebSocketTransport()
	serverErr := make(chan error, 1)
	server := httptest.NewServer(transport.Handler(func(ctx context.Context, conn Conn) {
		select {
		case event, ok := <-conn.Events():
			if !ok {
				serverErr <- context.Canceled
				return
			}
			if event.Err != nil {
				serverErr <- event.Err
				return
			}
			request := event.Frame.Request()
			response := protocol.NewSuccessResponse(request.ID, struct {
				Status string `json:"status"`
			}{Status: "ok"})
			serverErr <- conn.Send(ctx, FrameFromResponse(response))
		case <-ctx.Done():
			serverErr <- ctx.Err()
		}
	}))
	defer server.Close()

	endpoint, err := ParseWebSocketEndpoint("ws" + server.URL[len("http"):])
	if err != nil {
		t.Fatalf("ParseWebSocketEndpoint: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := transport.Dial(ctx, endpoint)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	request := protocol.Request{JSONRPC: protocol.JSONRPCVersion, ID: "req-1", Method: "test.ping"}
	if err := conn.Send(ctx, FrameFromRequest(request)); err != nil {
		t.Fatalf("Send: %v", err)
	}

	select {
	case event, ok := <-conn.Events():
		if !ok {
			t.Fatal("Events closed before response")
		}
		if event.Err != nil {
			t.Fatalf("Events error: %v", event.Err)
		}
		response := event.Frame.Response()
		if response.ID != request.ID {
			t.Fatalf("Response ID = %q, want %q", response.ID, request.ID)
		}
		var payload struct {
			Status string `json:"status"`
		}
		if err := json.Unmarshal(response.Result, &payload); err != nil {
			t.Fatalf("Unmarshal response: %v", err)
		}
		if payload.Status != "ok" {
			t.Fatalf("Response payload = %#v, want status ok", payload)
		}
	case <-ctx.Done():
		t.Fatalf("Timed out waiting for response: %v", ctx.Err())
	}

	if err := <-serverErr; err != nil {
		t.Fatalf("Server handler: %v", err)
	}
}

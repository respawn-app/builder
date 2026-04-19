package rpcwire

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
)

func BenchmarkTransportRoundTripTCP(b *testing.B) {
	benchmarkTransportRoundTripTCP(b, NewWebSocketTransport())
}

func BenchmarkTransportRoundTripTCPGWS(b *testing.B) {
	benchmarkTransportRoundTripTCP(b, NewGWSTransport())
}

func BenchmarkTransportRoundTripLocalSocket(b *testing.B) {
	benchmarkTransportRoundTripLocalSocket(b, NewWebSocketTransport())
}

func BenchmarkTransportRoundTripLocalSocketGWS(b *testing.B) {
	benchmarkTransportRoundTripLocalSocket(b, NewGWSTransport())
}

func BenchmarkTransportDialTCP(b *testing.B) {
	benchmarkTransportDialTCP(b, NewWebSocketTransport())
}

func BenchmarkTransportDialTCPGWS(b *testing.B) {
	benchmarkTransportDialTCP(b, NewGWSTransport())
}

func BenchmarkTransportDialLocalSocket(b *testing.B) {
	benchmarkTransportDialLocalSocket(b, NewWebSocketTransport())
}

func BenchmarkTransportDialLocalSocketGWS(b *testing.B) {
	benchmarkTransportDialLocalSocket(b, NewGWSTransport())
}

func benchmarkTransportRoundTripTCP(b *testing.B, transport benchmarkTransport) {
	server := benchmarkEchoTCPServer(b, transport)
	defer server.Close()
	benchmarkTransportRoundTripOnEndpoint(b, transport, server.endpoint)
}

func benchmarkTransportRoundTripLocalSocket(b *testing.B, transport benchmarkTransport) {
	server := benchmarkEchoUnixServer(b, transport)
	defer server.Close()
	benchmarkTransportRoundTripOnEndpoint(b, transport, server.endpoint)
}

func benchmarkTransportRoundTripOnEndpoint(b *testing.B, transport ClientTransport, endpoint Endpoint) {
	conn := benchmarkTransportDialConn(b, transport, endpoint)
	defer func() { _ = conn.Close() }()
	payload := json.RawMessage(`{"ok":true}`)
	frame := Frame{JSONRPC: "2.0", ID: "bench-echo", Method: "bench.echo", Params: payload}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := conn.Send(context.Background(), frame); err != nil {
			b.Fatalf("Send: %v", err)
		}
		event, err := benchmarkReceiveEvent(context.Background(), conn)
		if err != nil {
			b.Fatalf("Receive: %v", err)
		}
		if event.Frame.ID != frame.ID {
			b.Fatalf("Response ID = %q, want %q", event.Frame.ID, frame.ID)
		}
	}
}

func benchmarkTransportDialTCP(b *testing.B, transport benchmarkTransport) {
	server := benchmarkEchoTCPServer(b, transport)
	defer server.Close()
	benchmarkTransportDialOnEndpoint(b, transport, server.endpoint)
}

func benchmarkTransportDialLocalSocket(b *testing.B, transport benchmarkTransport) {
	server := benchmarkEchoUnixServer(b, transport)
	defer server.Close()
	benchmarkTransportDialOnEndpoint(b, transport, server.endpoint)
}

func benchmarkTransportDialOnEndpoint(b *testing.B, transport ClientTransport, endpoint Endpoint) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		conn := benchmarkTransportDialConn(b, transport, endpoint)
		_ = conn.Close()
	}
}

type benchmarkTransport interface {
	ClientTransport
	ServerTransport
}

type benchmarkEchoServer struct {
	endpoint Endpoint
	close    func()
}

func (s benchmarkEchoServer) Close() {
	if s.close != nil {
		s.close()
	}
}

func benchmarkEchoTCPServer(tb testing.TB, transport benchmarkTransport) benchmarkEchoServer {
	tb.Helper()
	server := httptest.NewServer(transport.Handler(func(ctx context.Context, conn Conn) {
		benchmarkEchoLoop(ctx, conn)
	}))
	endpoint, err := ParseWebSocketEndpoint("ws" + server.URL[len("http"):])
	if err != nil {
		server.Close()
		tb.Fatalf("ParseWebSocketEndpoint: %v", err)
	}
	return benchmarkEchoServer{endpoint: endpoint, close: server.Close}
}

func benchmarkEchoUnixServer(tb testing.TB, transport benchmarkTransport) benchmarkEchoServer {
	tb.Helper()
	if runtime.GOOS == "windows" {
		tb.Skip("unix sockets unavailable")
	}
	socketPath := benchmarkEchoUnixSocketPath()
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		tb.Fatalf("Listen unix: %v", err)
	}
	httpServer := &http.Server{Handler: transport.Handler(func(ctx context.Context, conn Conn) {
		benchmarkEchoLoop(ctx, conn)
	})}
	errCh := make(chan error, 1)
	go func() { errCh <- httpServer.Serve(listener) }()
	endpoint, err := NewUnixEndpoint(socketPath, "/")
	if err != nil {
		_ = httpServer.Close()
		_ = listener.Close()
		_ = os.Remove(socketPath)
		tb.Fatalf("NewUnixEndpoint: %v", err)
	}
	return benchmarkEchoServer{
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

func benchmarkEchoLoop(ctx context.Context, conn Conn) {
	for event := range conn.Events() {
		if event.Err != nil {
			return
		}
		response := Frame{JSONRPC: event.Frame.JSONRPC, ID: event.Frame.ID, Result: event.Frame.Params}
		if err := conn.Send(ctx, response); err != nil {
			return
		}
	}
}

func benchmarkTransportDialConn(tb testing.TB, transport ClientTransport, endpoint Endpoint) Conn {
	tb.Helper()
	conn, err := transport.Dial(context.Background(), endpoint)
	if err != nil {
		tb.Fatalf("Dial: %v", err)
	}
	return conn
}

func benchmarkReceiveEvent(ctx context.Context, conn Conn) (Event, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case event, ok := <-conn.Events():
		if !ok {
			return Event{}, errors.New("connection closed")
		}
		if event.Err != nil {
			return Event{}, event.Err
		}
		return event, nil
	case <-ctx.Done():
		return Event{}, ctx.Err()
	}
}

func benchmarkEchoUnixSocketPath() string {
	return filepath.Join(os.TempDir(), fmt.Sprintf("rpcwire-bench-%d.sock", time.Now().UnixNano()))
}

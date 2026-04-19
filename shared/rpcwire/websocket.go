package rpcwire

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"builder/shared/protocol"
	"golang.org/x/net/websocket"
)

type Frame struct {
	JSONRPC string                  `json:"jsonrpc"`
	ID      string                  `json:"id,omitempty"`
	Method  string                  `json:"method,omitempty"`
	Params  json.RawMessage         `json:"params,omitempty"`
	Result  json.RawMessage         `json:"result,omitempty"`
	Error   *protocol.ResponseError `json:"error,omitempty"`
}

type Event struct {
	Frame Frame
	Err   error
}

type Conn interface {
	Send(context.Context, Frame) error
	Events() <-chan Event
	Close() error
}

type ClientTransport interface {
	Dial(context.Context, Endpoint) (Conn, error)
}

type ServerTransport interface {
	Handler(func(context.Context, Conn)) http.Handler
}

type WebSocketTransport struct{}

func NewWebSocketTransport() WebSocketTransport {
	return WebSocketTransport{}
}

func (WebSocketTransport) Dial(ctx context.Context, endpoint Endpoint) (Conn, error) {
	config, err := websocket.NewConfig(endpoint.ServerURL, endpoint.OriginURL)
	if err != nil {
		return nil, err
	}
	var wsConn *websocket.Conn
	switch endpoint.Transport {
	case TransportUnix:
		rawConn, err := (&net.Dialer{}).DialContext(ctx, "unix", endpoint.Address)
		if err != nil {
			return nil, err
		}
		wsConn, err = dialUnixWebSocketContext(ctx, config, rawConn)
		if err != nil {
			_ = rawConn.Close()
			return nil, err
		}
	default:
		wsConn, err = config.DialContext(ctx)
		if err != nil {
			return nil, err
		}
	}
	return newWebSocketConn(wsConn), nil
}

func dialUnixWebSocketContext(ctx context.Context, config *websocket.Config, rawConn net.Conn) (*websocket.Conn, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if deadline, ok := ctx.Deadline(); ok {
		if err := rawConn.SetDeadline(deadline); err != nil {
			return nil, err
		}
	}
	stopCancel := make(chan struct{})
	var cancelOnce sync.Once
	go func() {
		select {
		case <-ctx.Done():
			cancelOnce.Do(func() {
				_ = rawConn.SetDeadline(time.Now())
				_ = rawConn.Close()
			})
		case <-stopCancel:
		}
	}()
	conn, err := websocket.NewClient(config, rawConn)
	close(stopCancel)
	if err != nil {
		if cerr := ctx.Err(); cerr != nil {
			return nil, cerr
		}
		return nil, err
	}
	if err := rawConn.SetDeadline(time.Time{}); err != nil {
		_ = conn.Close()
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return conn, nil
}

func (WebSocketTransport) Handler(handler func(context.Context, Conn)) http.Handler {
	return websocket.Handler(func(ws *websocket.Conn) {
		conn := newWebSocketConn(ws)
		defer func() { _ = conn.Close() }()
		handler(ws.Request().Context(), conn)
	})
}

type websocketConn struct {
	ws             *websocket.Conn
	events         chan Event
	closed         chan struct{}
	closeRequested atomic.Bool
	closeOnce      sync.Once
	writeMu        sync.Mutex
}

func newWebSocketConn(ws *websocket.Conn) *websocketConn {
	conn := &websocketConn{
		ws:     ws,
		events: make(chan Event, 16),
		closed: make(chan struct{}),
	}
	go conn.readLoop()
	return conn
}

func (c *websocketConn) Send(ctx context.Context, frame Frame) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if err := websocket.JSON.Send(c.ws, frame); err != nil {
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		return err
	}
	return nil
}

func (c *websocketConn) Events() <-chan Event {
	return c.events
}

func (c *websocketConn) Close() error {
	if c == nil {
		return nil
	}
	var err error
	c.closeOnce.Do(func() {
		c.closeRequested.Store(true)
		close(c.closed)
		err = c.ws.Close()
	})
	return err
}

func (c *websocketConn) readLoop() {
	defer close(c.events)
	for {
		var frame Frame
		err := websocket.JSON.Receive(c.ws, &frame)
		if err != nil {
			if !c.closeRequested.Load() {
				select {
				case c.events <- Event{Err: err}:
				case <-c.closed:
				}
			}
			return
		}
		select {
		case c.events <- Event{Frame: frame}:
		case <-c.closed:
			return
		}
	}
}

func FrameFromRequest(req protocol.Request) Frame {
	return Frame{
		JSONRPC: req.JSONRPC,
		ID:      req.ID,
		Method:  req.Method,
		Params:  req.Params,
	}
}

func FrameFromResponse(resp protocol.Response) Frame {
	return Frame{
		JSONRPC: resp.JSONRPC,
		ID:      resp.ID,
		Result:  resp.Result,
		Error:   resp.Error,
	}
}

func (f Frame) Request() protocol.Request {
	return protocol.Request{
		JSONRPC: f.JSONRPC,
		ID:      f.ID,
		Method:  f.Method,
		Params:  f.Params,
	}
}

func (f Frame) Response() protocol.Response {
	return protocol.Response{
		JSONRPC: f.JSONRPC,
		ID:      f.ID,
		Result:  f.Result,
		Error:   f.Error,
	}
}

package rpcwire

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lxzan/gws"
)

const defaultGWSHandshakeTimeout = 5 * time.Second

type GWSTransport struct{}

func NewGWSTransport() GWSTransport {
	return GWSTransport{}
}

func (GWSTransport) Dial(ctx context.Context, endpoint Endpoint) (Conn, error) {
	rawConn, err := dialGWSEndpoint(ctx, endpoint)
	if err != nil {
		return nil, err
	}
	adapter := newGWSConn()
	socket, _, err := dialGWSClientContext(ctx, rawConn, endpoint, adapter)
	if err != nil {
		_ = rawConn.Close()
		return nil, err
	}
	adapter.attach(socket)
	go socket.ReadLoop()
	return adapter, nil
}

func (GWSTransport) Handler(handler func(context.Context, Conn)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		adapter := newGWSConn()
		upgrader := gws.NewUpgrader(adapter, &gws.ServerOption{HandshakeTimeout: gwsHandshakeTimeout(r.Context())})
		socket, err := upgrader.Upgrade(w, r)
		if err != nil {
			return
		}
		adapter.attach(socket)
		defer func() { _ = adapter.Close() }()
		go socket.ReadLoop()
		handler(r.Context(), adapter)
	})
}

type gwsConn struct {
	socket         *gws.Conn
	events         chan Event
	closed         chan struct{}
	closeRequested atomic.Bool
	closeOnce      sync.Once
	failOnce       sync.Once
	writeMu        sync.Mutex
}

func newGWSConn() *gwsConn {
	return &gwsConn{
		events: make(chan Event, 16),
		closed: make(chan struct{}),
	}
}

func (c *gwsConn) attach(socket *gws.Conn) {
	c.socket = socket
}

func (c *gwsConn) Send(ctx context.Context, frame Frame) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if c.socket == nil {
		return errors.New("gws connection is required")
	}
	data, err := json.Marshal(frame)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if deadline, ok := ctx.Deadline(); ok {
		if err := c.socket.SetWriteDeadline(deadline); err != nil {
			return err
		}
		defer func() { _ = c.socket.SetWriteDeadline(time.Time{}) }()
	}
	if err := c.socket.WriteMessage(gws.OpcodeText, data); err != nil {
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		return err
	}
	return nil
}

func (c *gwsConn) Events() <-chan Event {
	return c.events
}

func (c *gwsConn) Close() error {
	if c == nil {
		return nil
	}
	var err error
	c.closeOnce.Do(func() {
		c.closeRequested.Store(true)
		close(c.closed)
		if c.socket != nil {
			err = c.socket.NetConn().Close()
		}
		c.closeEvents()
	})
	return err
}

func (c *gwsConn) OnOpen(_ *gws.Conn) {}

func (c *gwsConn) OnClose(_ *gws.Conn, err error) {
	if c.closeRequested.Load() {
		return
	}
	if err == nil {
		err = io.EOF
	}
	c.publishError(err)
	_ = c.Close()
}

func (c *gwsConn) OnPing(_ *gws.Conn, _ []byte) {}

func (c *gwsConn) OnPong(_ *gws.Conn, _ []byte) {}

func (c *gwsConn) OnMessage(_ *gws.Conn, message *gws.Message) {
	defer func() { _ = message.Close() }()
	var frame Frame
	if err := json.Unmarshal(message.Bytes(), &frame); err != nil {
		c.publishError(err)
		_ = c.Close()
		return
	}
	select {
	case c.events <- Event{Frame: frame}:
	case <-c.closed:
	}
}

func (c *gwsConn) publishError(err error) {
	if err == nil {
		return
	}
	select {
	case c.events <- Event{Err: err}:
	case <-c.closed:
	}
}

func (c *gwsConn) closeEvents() {
	c.failOnce.Do(func() {
		close(c.events)
	})
}

func dialGWSEndpoint(ctx context.Context, endpoint Endpoint) (net.Conn, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	network := "tcp"
	if endpoint.Transport == TransportUnix {
		network = "unix"
	}
	return (&net.Dialer{}).DialContext(ctx, network, endpoint.Address)
}

func dialGWSClientContext(ctx context.Context, rawConn net.Conn, endpoint Endpoint, adapter *gwsConn) (*gws.Conn, *http.Response, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	option := &gws.ClientOption{
		Addr:             endpoint.ServerURL,
		RequestHeader:    http.Header{"Origin": []string{endpoint.OriginURL}},
		HandshakeTimeout: gwsHandshakeTimeout(ctx),
	}
	type result struct {
		socket *gws.Conn
		resp   *http.Response
		err    error
	}
	resultCh := make(chan result, 1)
	go func() {
		socket, resp, err := gws.NewClientFromConn(adapter, option, rawConn)
		resultCh <- result{socket: socket, resp: resp, err: err}
	}()
	select {
	case <-ctx.Done():
		_ = rawConn.Close()
		result := <-resultCh
		if result.socket != nil {
			_ = result.socket.NetConn().Close()
		}
		return nil, nil, ctx.Err()
	case result := <-resultCh:
		if err := ctx.Err(); err != nil {
			if result.socket != nil {
				_ = result.socket.NetConn().Close()
			}
			return nil, nil, err
		}
		return result.socket, result.resp, result.err
	}
}

func gwsHandshakeTimeout(ctx context.Context) time.Duration {
	if ctx == nil {
		return defaultGWSHandshakeTimeout
	}
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining > 0 {
			return remaining
		}
		return time.Millisecond
	}
	return defaultGWSHandshakeTimeout
}

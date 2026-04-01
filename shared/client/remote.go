package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"

	"builder/shared/clientui"
	"builder/shared/protocol"
	"builder/shared/serverapi"
	"golang.org/x/net/websocket"
)

type Remote struct {
	record   protocol.DiscoveryRecord
	identity protocol.ServerIdentity
	closed   atomic.Bool
}

func DialRemote(ctx context.Context, record protocol.DiscoveryRecord) (*Remote, error) {
	rpcURL := strings.TrimSpace(record.RPCURL)
	if rpcURL == "" {
		return nil, errors.New("rpc_url is required")
	}
	conn, cleanup, err := dialRPC(ctx, rpcURL)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	identity, err := handshakeRPC(ctx, conn)
	if err != nil {
		return nil, err
	}
	return &Remote{record: record, identity: identity}, nil
}

func (c *Remote) Close() error {
	if c == nil {
		return nil
	}
	c.closed.Store(true)
	return nil
}

func (c *Remote) Identity() protocol.ServerIdentity {
	if c == nil {
		return protocol.ServerIdentity{}
	}
	return c.identity
}

func (c *Remote) ListProjects(ctx context.Context, req serverapi.ProjectListRequest) (serverapi.ProjectListResponse, error) {
	var resp serverapi.ProjectListResponse
	return resp, c.call(ctx, protocol.MethodProjectList, req, &resp)
}

func (c *Remote) GetProjectOverview(ctx context.Context, req serverapi.ProjectGetOverviewRequest) (serverapi.ProjectGetOverviewResponse, error) {
	var resp serverapi.ProjectGetOverviewResponse
	return resp, c.call(ctx, protocol.MethodProjectGetOverview, req, &resp)
}

func (c *Remote) ListSessionsByProject(ctx context.Context, req serverapi.SessionListByProjectRequest) (serverapi.SessionListByProjectResponse, error) {
	var resp serverapi.SessionListByProjectResponse
	return resp, c.call(ctx, protocol.MethodSessionListByProject, req, &resp)
}

func (c *Remote) GetSessionMainView(ctx context.Context, req serverapi.SessionMainViewRequest) (serverapi.SessionMainViewResponse, error) {
	var resp serverapi.SessionMainViewResponse
	return resp, c.call(ctx, protocol.MethodSessionGetMainView, req, &resp)
}

func (c *Remote) GetRun(ctx context.Context, req serverapi.RunGetRequest) (serverapi.RunGetResponse, error) {
	var resp serverapi.RunGetResponse
	return resp, c.call(ctx, protocol.MethodRunGet, req, &resp)
}

func (c *Remote) ListProcesses(ctx context.Context, req serverapi.ProcessListRequest) (serverapi.ProcessListResponse, error) {
	var resp serverapi.ProcessListResponse
	return resp, c.call(ctx, protocol.MethodProcessList, req, &resp)
}

func (c *Remote) GetProcess(ctx context.Context, req serverapi.ProcessGetRequest) (serverapi.ProcessGetResponse, error) {
	var resp serverapi.ProcessGetResponse
	return resp, c.call(ctx, protocol.MethodProcessGet, req, &resp)
}

func (c *Remote) KillProcess(ctx context.Context, req serverapi.ProcessKillRequest) (serverapi.ProcessKillResponse, error) {
	var resp serverapi.ProcessKillResponse
	return resp, c.call(ctx, protocol.MethodProcessKill, req, &resp)
}

func (c *Remote) GetInlineOutput(ctx context.Context, req serverapi.ProcessInlineOutputRequest) (serverapi.ProcessInlineOutputResponse, error) {
	var resp serverapi.ProcessInlineOutputResponse
	return resp, c.call(ctx, protocol.MethodProcessInlineOutput, req, &resp)
}

func (c *Remote) ListPendingAsksBySession(ctx context.Context, req serverapi.AskListPendingBySessionRequest) (serverapi.AskListPendingBySessionResponse, error) {
	var resp serverapi.AskListPendingBySessionResponse
	return resp, c.call(ctx, protocol.MethodAskListPending, req, &resp)
}

func (c *Remote) ListPendingApprovalsBySession(ctx context.Context, req serverapi.ApprovalListPendingBySessionRequest) (serverapi.ApprovalListPendingBySessionResponse, error) {
	var resp serverapi.ApprovalListPendingBySessionResponse
	return resp, c.call(ctx, protocol.MethodApprovalListPending, req, &resp)
}

func (c *Remote) RunPrompt(ctx context.Context, req serverapi.RunPromptRequest, progress serverapi.RunPromptProgressSink) (serverapi.RunPromptResponse, error) {
	if err := c.ensureOpen(); err != nil {
		return serverapi.RunPromptResponse{}, err
	}
	conn, cleanup, err := dialRPC(ctx, c.record.RPCURL)
	if err != nil {
		return serverapi.RunPromptResponse{}, err
	}
	defer cleanup()
	if _, err := handshakeRPC(ctx, conn); err != nil {
		return serverapi.RunPromptResponse{}, err
	}
	params, err := json.Marshal(req)
	if err != nil {
		return serverapi.RunPromptResponse{}, err
	}
	const requestID = "run-prompt"
	if err := sendWithContext(ctx, conn, protocol.Request{JSONRPC: protocol.JSONRPCVersion, ID: requestID, Method: protocol.MethodRunPrompt, Params: params}); err != nil {
		return serverapi.RunPromptResponse{}, err
	}
	for {
		frame, err := receiveFrame(ctx, conn)
		if err != nil {
			return serverapi.RunPromptResponse{}, err
		}
		if frame.Method == protocol.MethodRunPromptProgress {
			if progress != nil {
				var update serverapi.RunPromptProgress
				if err := json.Unmarshal(frame.Params, &update); err != nil {
					return serverapi.RunPromptResponse{}, err
				}
				progress.PublishRunPromptProgress(update)
			}
			continue
		}
		if frame.ID != requestID {
			return serverapi.RunPromptResponse{}, fmt.Errorf("unexpected rpc frame id %q", frame.ID)
		}
		if frame.Error != nil {
			return serverapi.RunPromptResponse{}, protocolError(frame.Error)
		}
		var resp serverapi.RunPromptResponse
		if len(frame.Result) > 0 {
			if err := json.Unmarshal(frame.Result, &resp); err != nil {
				return serverapi.RunPromptResponse{}, err
			}
		}
		return resp, nil
	}
}

func (c *Remote) SubscribeSessionActivity(ctx context.Context, req serverapi.SessionActivitySubscribeRequest) (serverapi.SessionActivitySubscription, error) {
	if err := c.ensureOpen(); err != nil {
		return nil, err
	}
	conn, cleanup, err := dialRPC(ctx, c.record.RPCURL)
	if err != nil {
		return nil, err
	}
	if _, err := handshakeRPC(ctx, conn); err != nil {
		cleanup()
		return nil, err
	}
	if err := callRPC(ctx, conn, "attach-session", protocol.MethodAttachSession, protocol.AttachSessionRequest{SessionID: req.SessionID}, nil); err != nil {
		cleanup()
		return nil, err
	}
	var ack protocol.SubscribeResponse
	if err := callRPC(ctx, conn, "subscribe-session-activity", protocol.MethodSessionSubscribeActivity, req, &ack); err != nil {
		cleanup()
		return nil, err
	}
	return &remoteSessionActivitySubscription{conn: conn, close: cleanup}, nil
}

func (c *Remote) SubscribeProcessOutput(ctx context.Context, req serverapi.ProcessOutputSubscribeRequest) (serverapi.ProcessOutputSubscription, error) {
	if err := c.ensureOpen(); err != nil {
		return nil, err
	}
	conn, cleanup, err := dialRPC(ctx, c.record.RPCURL)
	if err != nil {
		return nil, err
	}
	if _, err := handshakeRPC(ctx, conn); err != nil {
		cleanup()
		return nil, err
	}
	var ack protocol.SubscribeResponse
	if err := callRPC(ctx, conn, "subscribe-process-output", protocol.MethodProcessSubscribeOutput, req, &ack); err != nil {
		cleanup()
		return nil, err
	}
	return &remoteProcessOutputSubscription{conn: conn, close: cleanup}, nil
}

func (c *Remote) call(ctx context.Context, method string, params any, out any) error {
	if err := c.ensureOpen(); err != nil {
		return err
	}
	conn, cleanup, err := dialRPC(ctx, c.record.RPCURL)
	if err != nil {
		return err
	}
	defer cleanup()
	if _, err := handshakeRPC(ctx, conn); err != nil {
		return err
	}
	return callRPC(ctx, conn, method, method, params, out)
}

func (c *Remote) ensureOpen() error {
	if c == nil {
		return errors.New("remote client is required")
	}
	if c.closed.Load() {
		return errors.New("remote client is closed")
	}
	return nil
}

type remoteSessionActivitySubscription struct {
	conn  *websocket.Conn
	close func()
	once  sync.Once
}

func (s *remoteSessionActivitySubscription) Next(ctx context.Context) (clientui.Event, error) {
	notif, err := receiveNotification(ctx, s.conn)
	if err != nil {
		return clientui.Event{}, serverapi.NormalizeStreamError(err)
	}
	switch notif.Method {
	case protocol.MethodSessionActivityEvent:
		var params protocol.SessionActivityEventParams
		if err := json.Unmarshal(notif.Params, &params); err != nil {
			return clientui.Event{}, errors.Join(serverapi.ErrStreamFailed, err)
		}
		return params.Event, nil
	case protocol.MethodSessionActivityComplete:
		var params protocol.StreamCompleteParams
		if err := json.Unmarshal(notif.Params, &params); err != nil {
			return clientui.Event{}, errors.Join(serverapi.ErrStreamFailed, err)
		}
		_ = s.Close()
		return clientui.Event{}, streamCompleteError(params)
	default:
		return clientui.Event{}, errors.Join(serverapi.ErrStreamFailed, fmt.Errorf("unexpected notification method %q", notif.Method))
	}
}

func (s *remoteSessionActivitySubscription) Close() error {
	if s == nil {
		return nil
	}
	s.once.Do(func() {
		if s.close != nil {
			s.close()
		}
	})
	return nil
}

type remoteProcessOutputSubscription struct {
	conn  *websocket.Conn
	close func()
	once  sync.Once
}

func (s *remoteProcessOutputSubscription) Next(ctx context.Context) (clientui.ProcessOutputChunk, error) {
	notif, err := receiveNotification(ctx, s.conn)
	if err != nil {
		return clientui.ProcessOutputChunk{}, serverapi.NormalizeStreamError(err)
	}
	switch notif.Method {
	case protocol.MethodProcessOutputEvent:
		var params protocol.ProcessOutputEventParams
		if err := json.Unmarshal(notif.Params, &params); err != nil {
			return clientui.ProcessOutputChunk{}, errors.Join(serverapi.ErrStreamFailed, err)
		}
		return params.Chunk, nil
	case protocol.MethodProcessOutputComplete:
		var params protocol.StreamCompleteParams
		if err := json.Unmarshal(notif.Params, &params); err != nil {
			return clientui.ProcessOutputChunk{}, errors.Join(serverapi.ErrStreamFailed, err)
		}
		_ = s.Close()
		return clientui.ProcessOutputChunk{}, streamCompleteError(params)
	default:
		return clientui.ProcessOutputChunk{}, errors.Join(serverapi.ErrStreamFailed, fmt.Errorf("unexpected notification method %q", notif.Method))
	}
}

func (s *remoteProcessOutputSubscription) Close() error {
	if s == nil {
		return nil
	}
	s.once.Do(func() {
		if s.close != nil {
			s.close()
		}
	})
	return nil
}

func dialRPC(ctx context.Context, rpcURL string) (*websocket.Conn, func(), error) {
	config, err := websocket.NewConfig(strings.TrimSpace(rpcURL), websocketOrigin(rpcURL))
	if err != nil {
		return nil, nil, err
	}
	conn, err := websocket.DialConfig(config)
	if err != nil {
		return nil, nil, err
	}
	var once sync.Once
	stop := make(chan struct{})
	cleanup := func() {
		once.Do(func() {
			close(stop)
			_ = conn.Close()
		})
	}
	go func() {
		select {
		case <-ctx.Done():
			cleanup()
		case <-stop:
		}
	}()
	return conn, cleanup, nil
}

func websocketOrigin(rpcURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(rpcURL))
	if err != nil {
		return "http://127.0.0.1"
	}
	scheme := "http"
	if parsed.Scheme == "wss" {
		scheme = "https"
	}
	return (&url.URL{Scheme: scheme, Host: parsed.Host}).String()
}

func handshakeRPC(ctx context.Context, conn *websocket.Conn) (protocol.ServerIdentity, error) {
	var resp protocol.HandshakeResponse
	if err := callRPC(ctx, conn, "handshake", protocol.MethodHandshake, protocol.HandshakeRequest{ProtocolVersion: protocol.Version}, &resp); err != nil {
		return protocol.ServerIdentity{}, err
	}
	return resp.Identity, nil
}

func callRPC(ctx context.Context, conn *websocket.Conn, requestID string, method string, params any, out any) error {
	data, err := json.Marshal(params)
	if err != nil {
		return err
	}
	if err := sendWithContext(ctx, conn, protocol.Request{JSONRPC: protocol.JSONRPCVersion, ID: requestID, Method: method, Params: data}); err != nil {
		return err
	}
	var resp protocol.Response
	if err := receiveWithContext(ctx, conn, &resp); err != nil {
		return err
	}
	if resp.Error != nil {
		return protocolError(resp.Error)
	}
	if out == nil || len(resp.Result) == 0 {
		return nil
	}
	return json.Unmarshal(resp.Result, out)
}

func receiveNotification(ctx context.Context, conn *websocket.Conn) (protocol.Request, error) {
	var notif protocol.Request
	if err := receiveWithContext(ctx, conn, &notif); err != nil {
		return protocol.Request{}, err
	}
	if strings.TrimSpace(notif.JSONRPC) != protocol.JSONRPCVersion {
		return protocol.Request{}, errors.Join(serverapi.ErrStreamFailed, fmt.Errorf("unexpected jsonrpc version %q", notif.JSONRPC))
	}
	return notif, nil
}

type rpcFrame struct {
	JSONRPC string                  `json:"jsonrpc"`
	ID      string                  `json:"id,omitempty"`
	Method  string                  `json:"method,omitempty"`
	Params  json.RawMessage         `json:"params,omitempty"`
	Result  json.RawMessage         `json:"result,omitempty"`
	Error   *protocol.ResponseError `json:"error,omitempty"`
}

func receiveFrame(ctx context.Context, conn *websocket.Conn) (rpcFrame, error) {
	var frame rpcFrame
	if err := receiveWithContext(ctx, conn, &frame); err != nil {
		return rpcFrame{}, err
	}
	return frame, nil
}

func sendWithContext(ctx context.Context, conn *websocket.Conn, value any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := websocket.JSON.Send(conn, value); err != nil {
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		return err
	}
	return nil
}

func receiveWithContext(ctx context.Context, conn *websocket.Conn, out any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := websocket.JSON.Receive(conn, out); err != nil {
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		return err
	}
	return nil
}

func protocolError(resp *protocol.ResponseError) error {
	if resp == nil {
		return nil
	}
	message := strings.TrimSpace(resp.Message)
	if message == "" {
		message = "protocol request failed"
	}
	switch resp.Code {
	case protocol.ErrCodeStreamGap:
		return errors.Join(serverapi.ErrStreamGap, errors.New(message))
	case protocol.ErrCodeStreamUnavailable:
		return errors.Join(serverapi.ErrStreamUnavailable, errors.New(message))
	case protocol.ErrCodeStreamFailed:
		return errors.Join(serverapi.ErrStreamFailed, errors.New(message))
	default:
		return errors.New(message)
	}
}

func streamCompleteError(params protocol.StreamCompleteParams) error {
	if params.Code == 0 && strings.TrimSpace(params.Message) == "" {
		return io.EOF
	}
	return protocolError(&protocol.ResponseError{Code: params.Code, Message: params.Message})
}

var _ ProjectViewClient = (*Remote)(nil)
var _ SessionViewClient = (*Remote)(nil)
var _ ProcessViewClient = (*Remote)(nil)
var _ ProcessControlClient = (*Remote)(nil)
var _ ProcessOutputClient = (*Remote)(nil)
var _ SessionActivityClient = (*Remote)(nil)
var _ RunPromptClient = (*Remote)(nil)
var _ AskViewClient = (*Remote)(nil)
var _ ApprovalViewClient = (*Remote)(nil)

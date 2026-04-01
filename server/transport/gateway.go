package transport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"builder/server/core"
	"builder/shared/protocol"
	"builder/shared/serverapi"
	"golang.org/x/net/websocket"
)

type Gateway struct {
	core     *core.Core
	identity protocol.ServerIdentity
}

type connectionState struct {
	handshakeDone   bool
	attachedProject string
	attachedSession string
}

func NewGateway(appCore *core.Core, identity protocol.ServerIdentity) (*Gateway, error) {
	if appCore == nil {
		return nil, errors.New("server core is required")
	}
	if strings.TrimSpace(identity.ProtocolVersion) == "" {
		return nil, errors.New("server identity is required")
	}
	return &Gateway{core: appCore, identity: identity}, nil
}

func (g *Gateway) Handler() http.Handler {
	return websocket.Handler(g.handleConn)
}

func (g *Gateway) handleConn(ws *websocket.Conn) {
	defer func() { _ = ws.Close() }()
	state := &connectionState{}
	ctx := ws.Request().Context()
	for {
		var req protocol.Request
		if err := websocket.JSON.Receive(ws, &req); err != nil {
			return
		}
		if req.Method == protocol.MethodRunPrompt {
			if !g.serveRunPrompt(ws, ctx, state, req) {
				return
			}
			continue
		}
		if isSubscriptionMethod(req.Method) {
			g.serveSubscription(ws, ctx, state, req)
			return
		}
		resp := g.dispatch(ctx, state, req)
		if err := websocket.JSON.Send(ws, resp); err != nil {
			return
		}
	}
}

func (g *Gateway) serveRunPrompt(ws *websocket.Conn, ctx context.Context, state *connectionState, req protocol.Request) bool {
	if err := req.Validate(); err != nil {
		return sendResponse(ws, protocol.NewErrorResponse(req.ID, protocol.ErrCodeInvalidRequest, err.Error()))
	}
	if !state.handshakeDone {
		return sendResponse(ws, protocol.NewErrorResponse(req.ID, protocol.ErrCodeInvalidRequest, "handshake is required before other methods"))
	}
	params, err := decodeParams[serverapi.RunPromptRequest](req.Params)
	if err != nil {
		return sendResponse(ws, protocol.NewErrorResponse(req.ID, protocol.ErrCodeInvalidParams, err.Error()))
	}
	progress := serverapi.RunPromptProgressFunc(func(update serverapi.RunPromptProgress) {
		_ = sendNotification(ws, protocol.MethodRunPromptProgress, update)
	})
	resp, err := g.core.RunPromptClient().RunPrompt(ctx, params, progress)
	if err != nil {
		return sendResponse(ws, responseForError(req.ID, err))
	}
	return sendResponse(ws, protocol.NewSuccessResponse(req.ID, resp))
}

func (g *Gateway) dispatch(ctx context.Context, state *connectionState, req protocol.Request) protocol.Response {
	if err := req.Validate(); err != nil {
		return protocol.NewErrorResponse(req.ID, protocol.ErrCodeInvalidRequest, err.Error())
	}
	if req.Method != protocol.MethodHandshake && !state.handshakeDone {
		return protocol.NewErrorResponse(req.ID, protocol.ErrCodeInvalidRequest, "handshake is required before other methods")
	}
	switch req.Method {
	case protocol.MethodHandshake:
		params, err := decodeParams[protocol.HandshakeRequest](req.Params)
		if err != nil {
			return protocol.NewErrorResponse(req.ID, protocol.ErrCodeInvalidParams, err.Error())
		}
		if err := params.Validate(); err != nil {
			return protocol.NewErrorResponse(req.ID, protocol.ErrCodeInvalidParams, err.Error())
		}
		if params.ProtocolVersion != protocol.Version {
			return protocol.NewErrorResponse(req.ID, protocol.ErrCodeInvalidRequest, fmt.Sprintf("unsupported protocol version %q", params.ProtocolVersion))
		}
		state.handshakeDone = true
		return protocol.NewSuccessResponse(req.ID, protocol.HandshakeResponse{Identity: g.identity})
	case protocol.MethodAttachProject:
		return decodeAndHandle(req, func(params protocol.AttachProjectRequest) (protocol.AttachResponse, error) {
			if err := params.Validate(); err != nil {
				return protocol.AttachResponse{}, err
			}
			if params.ProjectID != g.identity.ProjectID {
				return protocol.AttachResponse{}, fmt.Errorf("project %q is not hosted by this server", params.ProjectID)
			}
			state.attachedProject = params.ProjectID
			return protocol.AttachResponse{Kind: "project", ProjectID: params.ProjectID}, nil
		})
	case protocol.MethodAttachSession:
		return decodeAndHandle(req, func(params protocol.AttachSessionRequest) (protocol.AttachResponse, error) {
			if err := params.Validate(); err != nil {
				return protocol.AttachResponse{}, err
			}
			state.attachedSession = params.SessionID
			return protocol.AttachResponse{Kind: "session", SessionID: params.SessionID}, nil
		})
	case protocol.MethodProjectList:
		return decodeAndHandle(req, func(params serverapi.ProjectListRequest) (serverapi.ProjectListResponse, error) {
			return g.core.ProjectViewClient().ListProjects(ctx, params)
		})
	case protocol.MethodProjectGetOverview:
		return decodeAndHandle(req, func(params serverapi.ProjectGetOverviewRequest) (serverapi.ProjectGetOverviewResponse, error) {
			return g.core.ProjectViewClient().GetProjectOverview(ctx, params)
		})
	case protocol.MethodSessionListByProject:
		return decodeAndHandle(req, func(params serverapi.SessionListByProjectRequest) (serverapi.SessionListByProjectResponse, error) {
			return g.core.ProjectViewClient().ListSessionsByProject(ctx, params)
		})
	case protocol.MethodSessionPlan:
		return decodeAndHandle(req, func(params serverapi.SessionPlanRequest) (serverapi.SessionPlanResponse, error) {
			return g.core.SessionLaunchClient().PlanSession(ctx, params)
		})
	case protocol.MethodSessionGetMainView:
		return decodeAndHandle(req, func(params serverapi.SessionMainViewRequest) (serverapi.SessionMainViewResponse, error) {
			return g.core.SessionViewClient().GetSessionMainView(ctx, params)
		})
	case protocol.MethodSessionGetInitialInput:
		return decodeAndHandle(req, func(params serverapi.SessionInitialInputRequest) (serverapi.SessionInitialInputResponse, error) {
			return g.core.SessionLifecycleClient().GetInitialInput(ctx, params)
		})
	case protocol.MethodSessionPersistInputDraft:
		return decodeAndHandle(req, func(params serverapi.SessionPersistInputDraftRequest) (serverapi.SessionPersistInputDraftResponse, error) {
			return g.core.SessionLifecycleClient().PersistInputDraft(ctx, params)
		})
	case protocol.MethodSessionResolveTransition:
		return decodeAndHandle(req, func(params serverapi.SessionResolveTransitionRequest) (serverapi.SessionResolveTransitionResponse, error) {
			return g.core.SessionLifecycleClient().ResolveTransition(ctx, params)
		})
	case protocol.MethodSessionRuntimeActivate:
		return decodeAndHandle(req, func(params serverapi.SessionRuntimeActivateRequest) (struct{}, error) {
			return struct{}{}, g.core.SessionRuntimeClient().ActivateSessionRuntime(ctx, params)
		})
	case protocol.MethodSessionRuntimeRelease:
		return decodeAndHandle(req, func(params serverapi.SessionRuntimeReleaseRequest) (struct{}, error) {
			return struct{}{}, g.core.SessionRuntimeClient().ReleaseSessionRuntime(ctx, params)
		})
	case protocol.MethodRunGet:
		return decodeAndHandle(req, func(params serverapi.RunGetRequest) (serverapi.RunGetResponse, error) {
			return g.core.SessionViewClient().GetRun(ctx, params)
		})
	case protocol.MethodRuntimeSetSessionName:
		return decodeAndHandle(req, func(params serverapi.RuntimeSetSessionNameRequest) (struct{}, error) {
			return struct{}{}, g.core.RuntimeControlClient().SetSessionName(ctx, params)
		})
	case protocol.MethodRuntimeSetThinkingLevel:
		return decodeAndHandle(req, func(params serverapi.RuntimeSetThinkingLevelRequest) (struct{}, error) {
			return struct{}{}, g.core.RuntimeControlClient().SetThinkingLevel(ctx, params)
		})
	case protocol.MethodRuntimeSetFastModeEnabled:
		return decodeAndHandle(req, func(params serverapi.RuntimeSetFastModeEnabledRequest) (serverapi.RuntimeSetFastModeEnabledResponse, error) {
			return g.core.RuntimeControlClient().SetFastModeEnabled(ctx, params)
		})
	case protocol.MethodRuntimeSetReviewerEnabled:
		return decodeAndHandle(req, func(params serverapi.RuntimeSetReviewerEnabledRequest) (serverapi.RuntimeSetReviewerEnabledResponse, error) {
			return g.core.RuntimeControlClient().SetReviewerEnabled(ctx, params)
		})
	case protocol.MethodRuntimeSetAutoCompactionEnabled:
		return decodeAndHandle(req, func(params serverapi.RuntimeSetAutoCompactionEnabledRequest) (serverapi.RuntimeSetAutoCompactionEnabledResponse, error) {
			return g.core.RuntimeControlClient().SetAutoCompactionEnabled(ctx, params)
		})
	case protocol.MethodRuntimeAppendLocalEntry:
		return decodeAndHandle(req, func(params serverapi.RuntimeAppendLocalEntryRequest) (struct{}, error) {
			return struct{}{}, g.core.RuntimeControlClient().AppendLocalEntry(ctx, params)
		})
	case protocol.MethodRuntimeShouldCompactBeforeUserMessage:
		return decodeAndHandle(req, func(params serverapi.RuntimeShouldCompactBeforeUserMessageRequest) (serverapi.RuntimeShouldCompactBeforeUserMessageResponse, error) {
			return g.core.RuntimeControlClient().ShouldCompactBeforeUserMessage(ctx, params)
		})
	case protocol.MethodRuntimeSubmitUserMessage:
		return decodeAndHandle(req, func(params serverapi.RuntimeSubmitUserMessageRequest) (serverapi.RuntimeSubmitUserMessageResponse, error) {
			return g.core.RuntimeControlClient().SubmitUserMessage(ctx, params)
		})
	case protocol.MethodRuntimeSubmitUserShellCommand:
		return decodeAndHandle(req, func(params serverapi.RuntimeSubmitUserShellCommandRequest) (struct{}, error) {
			return struct{}{}, g.core.RuntimeControlClient().SubmitUserShellCommand(ctx, params)
		})
	case protocol.MethodRuntimeCompactContext:
		return decodeAndHandle(req, func(params serverapi.RuntimeCompactContextRequest) (struct{}, error) {
			return struct{}{}, g.core.RuntimeControlClient().CompactContext(ctx, params)
		})
	case protocol.MethodRuntimeCompactContextForPreSubmit:
		return decodeAndHandle(req, func(params serverapi.RuntimeCompactContextForPreSubmitRequest) (struct{}, error) {
			return struct{}{}, g.core.RuntimeControlClient().CompactContextForPreSubmit(ctx, params)
		})
	case protocol.MethodRuntimeHasQueuedUserWork:
		return decodeAndHandle(req, func(params serverapi.RuntimeHasQueuedUserWorkRequest) (serverapi.RuntimeHasQueuedUserWorkResponse, error) {
			return g.core.RuntimeControlClient().HasQueuedUserWork(ctx, params)
		})
	case protocol.MethodRuntimeSubmitQueuedUserMessages:
		return decodeAndHandle(req, func(params serverapi.RuntimeSubmitQueuedUserMessagesRequest) (serverapi.RuntimeSubmitQueuedUserMessagesResponse, error) {
			return g.core.RuntimeControlClient().SubmitQueuedUserMessages(ctx, params)
		})
	case protocol.MethodRuntimeInterrupt:
		return decodeAndHandle(req, func(params serverapi.RuntimeInterruptRequest) (struct{}, error) {
			return struct{}{}, g.core.RuntimeControlClient().Interrupt(ctx, params)
		})
	case protocol.MethodRuntimeQueueUserMessage:
		return decodeAndHandle(req, func(params serverapi.RuntimeQueueUserMessageRequest) (struct{}, error) {
			return struct{}{}, g.core.RuntimeControlClient().QueueUserMessage(ctx, params)
		})
	case protocol.MethodRuntimeDiscardQueuedUserMessagesMatching:
		return decodeAndHandle(req, func(params serverapi.RuntimeDiscardQueuedUserMessagesMatchingRequest) (serverapi.RuntimeDiscardQueuedUserMessagesMatchingResponse, error) {
			return g.core.RuntimeControlClient().DiscardQueuedUserMessagesMatching(ctx, params)
		})
	case protocol.MethodRuntimeRecordPromptHistory:
		return decodeAndHandle(req, func(params serverapi.RuntimeRecordPromptHistoryRequest) (struct{}, error) {
			return struct{}{}, g.core.RuntimeControlClient().RecordPromptHistory(ctx, params)
		})
	case protocol.MethodProcessList:
		return decodeAndHandle(req, func(params serverapi.ProcessListRequest) (serverapi.ProcessListResponse, error) {
			return g.core.ProcessViewClient().ListProcesses(ctx, params)
		})
	case protocol.MethodProcessGet:
		return decodeAndHandle(req, func(params serverapi.ProcessGetRequest) (serverapi.ProcessGetResponse, error) {
			return g.core.ProcessViewClient().GetProcess(ctx, params)
		})
	case protocol.MethodProcessKill:
		return decodeAndHandle(req, func(params serverapi.ProcessKillRequest) (serverapi.ProcessKillResponse, error) {
			return g.core.ProcessControlClient().KillProcess(ctx, params)
		})
	case protocol.MethodProcessInlineOutput:
		return decodeAndHandle(req, func(params serverapi.ProcessInlineOutputRequest) (serverapi.ProcessInlineOutputResponse, error) {
			return g.core.ProcessControlClient().GetInlineOutput(ctx, params)
		})
	case protocol.MethodAskListPending:
		return decodeAndHandle(req, func(params serverapi.AskListPendingBySessionRequest) (serverapi.AskListPendingBySessionResponse, error) {
			return g.core.AskViewClient().ListPendingAsksBySession(ctx, params)
		})
	case protocol.MethodAskAnswer:
		return decodeAndHandle(req, func(params serverapi.AskAnswerRequest) (struct{}, error) {
			return struct{}{}, g.core.PromptControlClient().AnswerAsk(ctx, params)
		})
	case protocol.MethodApprovalListPending:
		return decodeAndHandle(req, func(params serverapi.ApprovalListPendingBySessionRequest) (serverapi.ApprovalListPendingBySessionResponse, error) {
			return g.core.ApprovalViewClient().ListPendingApprovalsBySession(ctx, params)
		})
	case protocol.MethodApprovalAnswer:
		return decodeAndHandle(req, func(params serverapi.ApprovalAnswerRequest) (struct{}, error) {
			return struct{}{}, g.core.PromptControlClient().AnswerApproval(ctx, params)
		})
	case protocol.MethodRunPrompt:
		return decodeAndHandle(req, func(params serverapi.RunPromptRequest) (serverapi.RunPromptResponse, error) {
			return g.core.RunPromptClient().RunPrompt(ctx, params, nil)
		})
	default:
		return protocol.NewErrorResponse(req.ID, protocol.ErrCodeMethodNotFound, fmt.Sprintf("method %q not found", req.Method))
	}
}

func (g *Gateway) serveSubscription(ws *websocket.Conn, ctx context.Context, state *connectionState, req protocol.Request) {
	if err := req.Validate(); err != nil {
		_ = websocket.JSON.Send(ws, protocol.NewErrorResponse(req.ID, protocol.ErrCodeInvalidRequest, err.Error()))
		return
	}
	if !state.handshakeDone {
		_ = websocket.JSON.Send(ws, protocol.NewErrorResponse(req.ID, protocol.ErrCodeInvalidRequest, "handshake is required before other methods"))
		return
	}
	switch req.Method {
	case protocol.MethodSessionSubscribeActivity:
		params, err := decodeParams[serverapi.SessionActivitySubscribeRequest](req.Params)
		if err != nil {
			_ = websocket.JSON.Send(ws, protocol.NewErrorResponse(req.ID, protocol.ErrCodeInvalidParams, err.Error()))
			return
		}
		if err := params.Validate(); err != nil {
			_ = websocket.JSON.Send(ws, protocol.NewErrorResponse(req.ID, protocol.ErrCodeInvalidParams, err.Error()))
			return
		}
		if state.attachedSession != params.SessionID {
			_ = websocket.JSON.Send(ws, protocol.NewErrorResponse(req.ID, protocol.ErrCodeInvalidRequest, "session attach is required before subscribing"))
			return
		}
		sub, err := g.core.SessionActivityClient().SubscribeSessionActivity(ctx, params)
		if err != nil {
			_ = websocket.JSON.Send(ws, responseForError(req.ID, err))
			return
		}
		defer func() { _ = sub.Close() }()
		if err := websocket.JSON.Send(ws, protocol.NewSuccessResponse(req.ID, protocol.SubscribeResponse{Stream: protocol.MethodSessionActivityEvent})); err != nil {
			return
		}
		for {
			evt, err := sub.Next(ctx)
			if err != nil {
				_ = sendNotification(ws, protocol.MethodSessionActivityComplete, streamCompleteParams(err))
				return
			}
			if err := sendNotification(ws, protocol.MethodSessionActivityEvent, protocol.SessionActivityEventParams{Event: evt}); err != nil {
				return
			}
		}
	case protocol.MethodProcessSubscribeOutput:
		params, err := decodeParams[serverapi.ProcessOutputSubscribeRequest](req.Params)
		if err != nil {
			_ = websocket.JSON.Send(ws, protocol.NewErrorResponse(req.ID, protocol.ErrCodeInvalidParams, err.Error()))
			return
		}
		if err := params.Validate(); err != nil {
			_ = websocket.JSON.Send(ws, protocol.NewErrorResponse(req.ID, protocol.ErrCodeInvalidParams, err.Error()))
			return
		}
		sub, err := g.core.ProcessOutputClient().SubscribeProcessOutput(ctx, params)
		if err != nil {
			_ = websocket.JSON.Send(ws, responseForError(req.ID, err))
			return
		}
		defer func() { _ = sub.Close() }()
		if err := websocket.JSON.Send(ws, protocol.NewSuccessResponse(req.ID, protocol.SubscribeResponse{Stream: protocol.MethodProcessOutputEvent})); err != nil {
			return
		}
		for {
			chunk, err := sub.Next(ctx)
			if err != nil {
				_ = sendNotification(ws, protocol.MethodProcessOutputComplete, streamCompleteParams(err))
				return
			}
			if err := sendNotification(ws, protocol.MethodProcessOutputEvent, protocol.ProcessOutputEventParams{Chunk: chunk}); err != nil {
				return
			}
		}
	case protocol.MethodPromptSubscribeActivity:
		params, err := decodeParams[serverapi.PromptActivitySubscribeRequest](req.Params)
		if err != nil {
			_ = websocket.JSON.Send(ws, protocol.NewErrorResponse(req.ID, protocol.ErrCodeInvalidParams, err.Error()))
			return
		}
		if err := params.Validate(); err != nil {
			_ = websocket.JSON.Send(ws, protocol.NewErrorResponse(req.ID, protocol.ErrCodeInvalidParams, err.Error()))
			return
		}
		if state.attachedSession != params.SessionID {
			_ = websocket.JSON.Send(ws, protocol.NewErrorResponse(req.ID, protocol.ErrCodeInvalidRequest, "session attach is required before subscribing"))
			return
		}
		sub, err := g.core.PromptActivityClient().SubscribePromptActivity(ctx, params)
		if err != nil {
			_ = websocket.JSON.Send(ws, responseForError(req.ID, err))
			return
		}
		defer func() { _ = sub.Close() }()
		if err := websocket.JSON.Send(ws, protocol.NewSuccessResponse(req.ID, protocol.SubscribeResponse{Stream: protocol.MethodPromptActivityEvent})); err != nil {
			return
		}
		for {
			evt, err := sub.Next(ctx)
			if err != nil {
				_ = sendNotification(ws, protocol.MethodPromptActivityComplete, streamCompleteParams(err))
				return
			}
			if err := sendNotification(ws, protocol.MethodPromptActivityEvent, protocol.PromptActivityEventParams{Event: evt}); err != nil {
				return
			}
		}
	default:
		_ = websocket.JSON.Send(ws, protocol.NewErrorResponse(req.ID, protocol.ErrCodeMethodNotFound, fmt.Sprintf("method %q not found", req.Method)))
	}
}

func decodeAndHandle[TReq any, TResp any](req protocol.Request, handler func(TReq) (TResp, error)) protocol.Response {
	params, err := decodeParams[TReq](req.Params)
	if err != nil {
		return protocol.NewErrorResponse(req.ID, protocol.ErrCodeInvalidParams, err.Error())
	}
	resp, err := handler(params)
	if err != nil {
		return responseForError(req.ID, err)
	}
	return protocol.NewSuccessResponse(req.ID, resp)
}

func isSubscriptionMethod(method string) bool {
	switch method {
	case protocol.MethodSessionSubscribeActivity, protocol.MethodPromptSubscribeActivity, protocol.MethodProcessSubscribeOutput:
		return true
	default:
		return false
	}
}

func sendNotification(ws *websocket.Conn, method string, params any) error {
	data, err := json.Marshal(params)
	if err != nil {
		return err
	}
	return websocket.JSON.Send(ws, protocol.Request{JSONRPC: protocol.JSONRPCVersion, Method: method, Params: data})
}

func sendResponse(ws *websocket.Conn, resp protocol.Response) bool {
	return websocket.JSON.Send(ws, resp) == nil
}

func responseForError(id string, err error) protocol.Response {
	code, message := protocolError(err)
	return protocol.NewErrorResponse(id, code, message)
}

func protocolError(err error) (int, string) {
	if err == nil {
		return protocol.ErrCodeInternalError, "internal error"
	}
	message := strings.TrimSpace(err.Error())
	if errors.Is(err, serverapi.ErrStreamGap) {
		return protocol.ErrCodeStreamGap, message
	}
	if errors.Is(err, serverapi.ErrStreamUnavailable) {
		return protocol.ErrCodeStreamUnavailable, message
	}
	if errors.Is(err, serverapi.ErrStreamFailed) {
		return protocol.ErrCodeStreamFailed, message
	}
	return protocol.ErrCodeInternalError, message
}

func streamCompleteParams(err error) protocol.StreamCompleteParams {
	if err == nil || errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return protocol.StreamCompleteParams{}
	}
	code, message := protocolError(err)
	return protocol.StreamCompleteParams{Code: code, Message: message}
}

func decodeParams[T any](raw json.RawMessage) (T, error) {
	var zero T
	if len(raw) == 0 {
		return zero, nil
	}
	var out T
	if err := json.Unmarshal(raw, &out); err != nil {
		return zero, fmt.Errorf("decode params: %w", err)
	}
	return out, nil
}

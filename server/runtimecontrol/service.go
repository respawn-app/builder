package runtimecontrol

import (
	"context"
	"fmt"
	"strings"

	"builder/server/primaryrun"
	"builder/server/requestmemo"
	"builder/server/runtime"
	"builder/shared/serverapi"
)

type RuntimeResolver interface {
	ResolveRuntime(ctx context.Context, sessionID string) (*runtime.Engine, error)
}

type ControllerLeaseVerifier interface {
	RequireControllerLease(ctx context.Context, sessionID string, leaseID string) error
}

type Service struct {
	runtimes RuntimeResolver
	gate     primaryrun.Gate
	control  ControllerLeaseVerifier
	submits  *requestmemo.Memo[userTextMemoRequest, serverapi.RuntimeSubmitUserMessageResponse]
	queues   *requestmemo.Memo[userTextMemoRequest, struct{}]
	shells   *requestmemo.Memo[submitUserShellCommandMemoRequest, struct{}]
}

type userTextMemoRequest struct {
	SessionID         string
	ControllerLeaseID string
	Text              string
}

type submitUserShellCommandMemoRequest struct {
	SessionID         string
	ControllerLeaseID string
	Command           string
}

func NewService(runtimes RuntimeResolver, gate primaryrun.Gate) *Service {
	return &Service{
		runtimes: runtimes,
		gate:     gate,
		submits:  requestmemo.New[userTextMemoRequest, serverapi.RuntimeSubmitUserMessageResponse](),
		queues:   requestmemo.New[userTextMemoRequest, struct{}](),
		shells:   requestmemo.New[submitUserShellCommandMemoRequest, struct{}](),
	}
}

func (s *Service) WithControllerLeaseVerifier(verifier ControllerLeaseVerifier) *Service {
	if s == nil {
		return nil
	}
	s.control = verifier
	return s
}

func (s *Service) requireControllerLease(ctx context.Context, sessionID string, leaseID string) error {
	if s == nil || s.control == nil {
		return nil
	}
	return s.control.RequireControllerLease(ctx, sessionID, leaseID)
}

func (s *Service) resolve(ctx context.Context, sessionID string) (*runtime.Engine, error) {
	if s == nil || s.runtimes == nil {
		return nil, fmt.Errorf("runtime resolver is required")
	}
	engine, err := s.runtimes.ResolveRuntime(ctx, strings.TrimSpace(sessionID))
	if err != nil {
		return nil, err
	}
	if engine == nil {
		return nil, fmt.Errorf("runtime for session %q is unavailable", strings.TrimSpace(sessionID))
	}
	return engine, nil
}

func (s *Service) SetSessionName(ctx context.Context, req serverapi.RuntimeSetSessionNameRequest) error {
	if err := req.Validate(); err != nil {
		return err
	}
	if err := s.requireControllerLease(ctx, req.SessionID, req.ControllerLeaseID); err != nil {
		return err
	}
	engine, err := s.resolve(ctx, req.SessionID)
	if err != nil {
		return err
	}
	return engine.SetSessionName(req.Name)
}

func (s *Service) SetThinkingLevel(ctx context.Context, req serverapi.RuntimeSetThinkingLevelRequest) error {
	if err := req.Validate(); err != nil {
		return err
	}
	if err := s.requireControllerLease(ctx, req.SessionID, req.ControllerLeaseID); err != nil {
		return err
	}
	engine, err := s.resolve(ctx, req.SessionID)
	if err != nil {
		return err
	}
	return engine.SetThinkingLevel(req.Level)
}

func (s *Service) SetFastModeEnabled(ctx context.Context, req serverapi.RuntimeSetFastModeEnabledRequest) (serverapi.RuntimeSetFastModeEnabledResponse, error) {
	if err := req.Validate(); err != nil {
		return serverapi.RuntimeSetFastModeEnabledResponse{}, err
	}
	if err := s.requireControllerLease(ctx, req.SessionID, req.ControllerLeaseID); err != nil {
		return serverapi.RuntimeSetFastModeEnabledResponse{}, err
	}
	engine, err := s.resolve(ctx, req.SessionID)
	if err != nil {
		return serverapi.RuntimeSetFastModeEnabledResponse{}, err
	}
	changed, err := engine.SetFastModeEnabled(req.Enabled)
	if err != nil {
		return serverapi.RuntimeSetFastModeEnabledResponse{}, err
	}
	return serverapi.RuntimeSetFastModeEnabledResponse{Changed: changed}, nil
}

func (s *Service) SetReviewerEnabled(ctx context.Context, req serverapi.RuntimeSetReviewerEnabledRequest) (serverapi.RuntimeSetReviewerEnabledResponse, error) {
	if err := req.Validate(); err != nil {
		return serverapi.RuntimeSetReviewerEnabledResponse{}, err
	}
	if err := s.requireControllerLease(ctx, req.SessionID, req.ControllerLeaseID); err != nil {
		return serverapi.RuntimeSetReviewerEnabledResponse{}, err
	}
	engine, err := s.resolve(ctx, req.SessionID)
	if err != nil {
		return serverapi.RuntimeSetReviewerEnabledResponse{}, err
	}
	changed, mode, err := engine.SetReviewerEnabled(req.Enabled)
	if err != nil {
		return serverapi.RuntimeSetReviewerEnabledResponse{}, err
	}
	return serverapi.RuntimeSetReviewerEnabledResponse{Changed: changed, Mode: mode}, nil
}

func (s *Service) SetAutoCompactionEnabled(ctx context.Context, req serverapi.RuntimeSetAutoCompactionEnabledRequest) (serverapi.RuntimeSetAutoCompactionEnabledResponse, error) {
	if err := req.Validate(); err != nil {
		return serverapi.RuntimeSetAutoCompactionEnabledResponse{}, err
	}
	if err := s.requireControllerLease(ctx, req.SessionID, req.ControllerLeaseID); err != nil {
		return serverapi.RuntimeSetAutoCompactionEnabledResponse{}, err
	}
	engine, err := s.resolve(ctx, req.SessionID)
	if err != nil {
		return serverapi.RuntimeSetAutoCompactionEnabledResponse{}, err
	}
	changed, enabled := engine.SetAutoCompactionEnabled(req.Enabled)
	return serverapi.RuntimeSetAutoCompactionEnabledResponse{Changed: changed, Enabled: enabled}, nil
}

func (s *Service) AppendLocalEntry(ctx context.Context, req serverapi.RuntimeAppendLocalEntryRequest) error {
	if err := req.Validate(); err != nil {
		return err
	}
	if err := s.requireControllerLease(ctx, req.SessionID, req.ControllerLeaseID); err != nil {
		return err
	}
	engine, err := s.resolve(ctx, req.SessionID)
	if err != nil {
		return err
	}
	engine.AppendLocalEntry(req.Role, req.Text)
	return nil
}

func (s *Service) ShouldCompactBeforeUserMessage(ctx context.Context, req serverapi.RuntimeShouldCompactBeforeUserMessageRequest) (serverapi.RuntimeShouldCompactBeforeUserMessageResponse, error) {
	if err := req.Validate(); err != nil {
		return serverapi.RuntimeShouldCompactBeforeUserMessageResponse{}, err
	}
	engine, err := s.resolve(ctx, req.SessionID)
	if err != nil {
		return serverapi.RuntimeShouldCompactBeforeUserMessageResponse{}, err
	}
	shouldCompact, err := engine.ShouldCompactBeforeUserMessage(ctx, req.Text)
	if err != nil {
		return serverapi.RuntimeShouldCompactBeforeUserMessageResponse{}, err
	}
	return serverapi.RuntimeShouldCompactBeforeUserMessageResponse{ShouldCompact: shouldCompact}, nil
}

func (s *Service) SubmitUserMessage(ctx context.Context, req serverapi.RuntimeSubmitUserMessageRequest) (serverapi.RuntimeSubmitUserMessageResponse, error) {
	if err := req.Validate(); err != nil {
		return serverapi.RuntimeSubmitUserMessageResponse{}, err
	}
	memoReq := userTextMemoRequest{
		SessionID:         strings.TrimSpace(req.SessionID),
		ControllerLeaseID: strings.TrimSpace(req.ControllerLeaseID),
		Text:              req.Text,
	}
	return s.submits.Do(ctx, strings.TrimSpace(req.ClientRequestID), memoReq, sameUserTextMemoRequest, func(ctx context.Context) (serverapi.RuntimeSubmitUserMessageResponse, error) {
		if err := s.requireControllerLease(ctx, memoReq.SessionID, memoReq.ControllerLeaseID); err != nil {
			return serverapi.RuntimeSubmitUserMessageResponse{}, err
		}
		lease, err := s.acquirePrimaryRun(memoReq.SessionID)
		if err != nil {
			return serverapi.RuntimeSubmitUserMessageResponse{}, err
		}
		defer lease.Release()
		engine, err := s.resolve(ctx, memoReq.SessionID)
		if err != nil {
			return serverapi.RuntimeSubmitUserMessageResponse{}, err
		}
		msg, err := engine.SubmitUserMessage(ctx, memoReq.Text)
		if err != nil {
			return serverapi.RuntimeSubmitUserMessageResponse{}, err
		}
		return serverapi.RuntimeSubmitUserMessageResponse{Message: msg.Content}, nil
	})
}

func (s *Service) SubmitUserShellCommand(ctx context.Context, req serverapi.RuntimeSubmitUserShellCommandRequest) error {
	if err := req.Validate(); err != nil {
		return err
	}
	memoReq := submitUserShellCommandMemoRequest{
		SessionID:         strings.TrimSpace(req.SessionID),
		ControllerLeaseID: strings.TrimSpace(req.ControllerLeaseID),
		Command:           req.Command,
	}
	_, err := s.shells.Do(ctx, strings.TrimSpace(req.ClientRequestID), memoReq, sameSubmitUserShellCommandMemoRequest, func(ctx context.Context) (struct{}, error) {
		if err := s.requireControllerLease(ctx, memoReq.SessionID, memoReq.ControllerLeaseID); err != nil {
			return struct{}{}, err
		}
		lease, err := s.acquirePrimaryRun(memoReq.SessionID)
		if err != nil {
			return struct{}{}, err
		}
		defer lease.Release()
		engine, err := s.resolve(ctx, memoReq.SessionID)
		if err != nil {
			return struct{}{}, err
		}
		_, err = engine.SubmitUserShellCommand(ctx, memoReq.Command)
		return struct{}{}, err
	})
	return err
}

func (s *Service) CompactContext(ctx context.Context, req serverapi.RuntimeCompactContextRequest) error {
	if err := req.Validate(); err != nil {
		return err
	}
	if err := s.requireControllerLease(ctx, req.SessionID, req.ControllerLeaseID); err != nil {
		return err
	}
	engine, err := s.resolve(ctx, req.SessionID)
	if err != nil {
		return err
	}
	return engine.CompactContext(ctx, req.Args)
}

func (s *Service) CompactContextForPreSubmit(ctx context.Context, req serverapi.RuntimeCompactContextForPreSubmitRequest) error {
	if err := req.Validate(); err != nil {
		return err
	}
	if err := s.requireControllerLease(ctx, req.SessionID, req.ControllerLeaseID); err != nil {
		return err
	}
	engine, err := s.resolve(ctx, req.SessionID)
	if err != nil {
		return err
	}
	return engine.CompactContextForPreSubmit(ctx)
}

func (s *Service) HasQueuedUserWork(ctx context.Context, req serverapi.RuntimeHasQueuedUserWorkRequest) (serverapi.RuntimeHasQueuedUserWorkResponse, error) {
	if err := req.Validate(); err != nil {
		return serverapi.RuntimeHasQueuedUserWorkResponse{}, err
	}
	engine, err := s.resolve(ctx, req.SessionID)
	if err != nil {
		return serverapi.RuntimeHasQueuedUserWorkResponse{}, err
	}
	return serverapi.RuntimeHasQueuedUserWorkResponse{HasQueuedUserWork: engine.HasQueuedUserWork()}, nil
}

func (s *Service) SubmitQueuedUserMessages(ctx context.Context, req serverapi.RuntimeSubmitQueuedUserMessagesRequest) (serverapi.RuntimeSubmitQueuedUserMessagesResponse, error) {
	if err := req.Validate(); err != nil {
		return serverapi.RuntimeSubmitQueuedUserMessagesResponse{}, err
	}
	if err := s.requireControllerLease(ctx, req.SessionID, req.ControllerLeaseID); err != nil {
		return serverapi.RuntimeSubmitQueuedUserMessagesResponse{}, err
	}
	lease, err := s.acquirePrimaryRun(req.SessionID)
	if err != nil {
		return serverapi.RuntimeSubmitQueuedUserMessagesResponse{}, err
	}
	defer lease.Release()
	engine, err := s.resolve(ctx, req.SessionID)
	if err != nil {
		return serverapi.RuntimeSubmitQueuedUserMessagesResponse{}, err
	}
	msg, err := engine.SubmitQueuedUserMessages(ctx)
	if err != nil {
		return serverapi.RuntimeSubmitQueuedUserMessagesResponse{}, err
	}
	return serverapi.RuntimeSubmitQueuedUserMessagesResponse{Message: msg.Content}, nil
}

func (s *Service) Interrupt(ctx context.Context, req serverapi.RuntimeInterruptRequest) error {
	if err := req.Validate(); err != nil {
		return err
	}
	if err := s.requireControllerLease(ctx, req.SessionID, req.ControllerLeaseID); err != nil {
		return err
	}
	engine, err := s.resolve(ctx, req.SessionID)
	if err != nil {
		return err
	}
	return engine.Interrupt()
}

func (s *Service) QueueUserMessage(ctx context.Context, req serverapi.RuntimeQueueUserMessageRequest) error {
	if err := req.Validate(); err != nil {
		return err
	}
	memoReq := userTextMemoRequest{
		SessionID:         strings.TrimSpace(req.SessionID),
		ControllerLeaseID: strings.TrimSpace(req.ControllerLeaseID),
		Text:              req.Text,
	}
	_, err := s.queues.Do(ctx, strings.TrimSpace(req.ClientRequestID), memoReq, sameUserTextMemoRequest, func(ctx context.Context) (struct{}, error) {
		if err := s.requireControllerLease(ctx, memoReq.SessionID, memoReq.ControllerLeaseID); err != nil {
			return struct{}{}, err
		}
		engine, err := s.resolve(ctx, memoReq.SessionID)
		if err != nil {
			return struct{}{}, err
		}
		engine.QueueUserMessage(memoReq.Text)
		return struct{}{}, nil
	})
	return err
}

func (s *Service) DiscardQueuedUserMessagesMatching(ctx context.Context, req serverapi.RuntimeDiscardQueuedUserMessagesMatchingRequest) (serverapi.RuntimeDiscardQueuedUserMessagesMatchingResponse, error) {
	if err := req.Validate(); err != nil {
		return serverapi.RuntimeDiscardQueuedUserMessagesMatchingResponse{}, err
	}
	if err := s.requireControllerLease(ctx, req.SessionID, req.ControllerLeaseID); err != nil {
		return serverapi.RuntimeDiscardQueuedUserMessagesMatchingResponse{}, err
	}
	engine, err := s.resolve(ctx, req.SessionID)
	if err != nil {
		return serverapi.RuntimeDiscardQueuedUserMessagesMatchingResponse{}, err
	}
	return serverapi.RuntimeDiscardQueuedUserMessagesMatchingResponse{Discarded: engine.DiscardQueuedUserMessagesMatching(req.Text)}, nil
}

func (s *Service) RecordPromptHistory(ctx context.Context, req serverapi.RuntimeRecordPromptHistoryRequest) error {
	if err := req.Validate(); err != nil {
		return err
	}
	if err := s.requireControllerLease(ctx, req.SessionID, req.ControllerLeaseID); err != nil {
		return err
	}
	engine, err := s.resolve(ctx, req.SessionID)
	if err != nil {
		return err
	}
	return engine.RecordPromptHistory(req.Text)
}

func (s *Service) acquirePrimaryRun(sessionID string) (primaryrun.Lease, error) {
	if s == nil || s.gate == nil {
		return primaryrun.LeaseFunc(func() {}), nil
	}
	return s.gate.AcquirePrimaryRun(strings.TrimSpace(sessionID))
}

func sameUserTextMemoRequest(a userTextMemoRequest, b userTextMemoRequest) bool {
	return a.SessionID == b.SessionID &&
		a.ControllerLeaseID == b.ControllerLeaseID &&
		a.Text == b.Text
}

func sameSubmitUserShellCommandMemoRequest(a submitUserShellCommandMemoRequest, b submitUserShellCommandMemoRequest) bool {
	return a.SessionID == b.SessionID &&
		a.ControllerLeaseID == b.ControllerLeaseID &&
		a.Command == b.Command
}

var _ serverapi.RuntimeControlService = (*Service)(nil)

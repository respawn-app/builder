package runtimecontrol

import (
	"context"
	"errors"
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
	runtimes       RuntimeResolver
	gate           primaryrun.Gate
	control        ControllerLeaseVerifier
	sessionNames   *requestmemo.Memo[sessionStringMemoRequest, struct{}]
	thinkingLevels *requestmemo.Memo[sessionStringMemoRequest, struct{}]
	fastModes      *requestmemo.Memo[sessionBoolMemoRequest, serverapi.RuntimeSetFastModeEnabledResponse]
	reviewers      *requestmemo.Memo[sessionBoolMemoRequest, serverapi.RuntimeSetReviewerEnabledResponse]
	autoCompacts   *requestmemo.Memo[sessionBoolMemoRequest, serverapi.RuntimeSetAutoCompactionEnabledResponse]
	submits        *requestmemo.Memo[sessionTextMemoRequest, serverapi.RuntimeSubmitUserMessageResponse]
	queues         *requestmemo.Memo[sessionTextMemoRequest, struct{}]
	shells         *requestmemo.Memo[sessionCommandMemoRequest, struct{}]

	localEntries         *requestmemo.Memo[localEntryMemoRequest, struct{}]
	compactions          *requestmemo.Memo[sessionStringMemoRequest, struct{}]
	preSubmitCompactions *requestmemo.Memo[sessionOnlyMemoRequest, struct{}]
	queuedSubmits        *requestmemo.Memo[sessionOnlyMemoRequest, serverapi.RuntimeSubmitQueuedUserMessagesResponse]
	interrupts           *requestmemo.Memo[sessionOnlyMemoRequest, struct{}]
	queuedDiscards       *requestmemo.Memo[sessionTextMemoRequest, serverapi.RuntimeDiscardQueuedUserMessagesMatchingResponse]
	promptHistory        *requestmemo.Memo[sessionTextMemoRequest, struct{}]
}

type sessionStringMemoRequest struct {
	SessionID string
	Value     string
}

type sessionBoolMemoRequest struct {
	SessionID string
	Enabled   bool
}

type sessionTextMemoRequest struct {
	SessionID string
	Text      string
}

type sessionCommandMemoRequest struct {
	SessionID string
	Command   string
}

type sessionOnlyMemoRequest struct {
	SessionID string
}

type localEntryMemoRequest struct {
	SessionID string
	Role      string
	Text      string
}

func NewService(runtimes RuntimeResolver, gate primaryrun.Gate) *Service {
	return &Service{
		runtimes:       runtimes,
		gate:           gate,
		sessionNames:   requestmemo.New[sessionStringMemoRequest, struct{}](),
		thinkingLevels: requestmemo.New[sessionStringMemoRequest, struct{}](),
		fastModes:      requestmemo.New[sessionBoolMemoRequest, serverapi.RuntimeSetFastModeEnabledResponse](),
		reviewers:      requestmemo.New[sessionBoolMemoRequest, serverapi.RuntimeSetReviewerEnabledResponse](),
		autoCompacts:   requestmemo.New[sessionBoolMemoRequest, serverapi.RuntimeSetAutoCompactionEnabledResponse](),
		submits:        requestmemo.New[sessionTextMemoRequest, serverapi.RuntimeSubmitUserMessageResponse](),
		queues:         requestmemo.New[sessionTextMemoRequest, struct{}](),
		shells:         requestmemo.New[sessionCommandMemoRequest, struct{}](),

		localEntries:         requestmemo.New[localEntryMemoRequest, struct{}](),
		compactions:          requestmemo.New[sessionStringMemoRequest, struct{}](),
		preSubmitCompactions: requestmemo.New[sessionOnlyMemoRequest, struct{}](),
		queuedSubmits:        requestmemo.New[sessionOnlyMemoRequest, serverapi.RuntimeSubmitQueuedUserMessagesResponse](),
		interrupts:           requestmemo.New[sessionOnlyMemoRequest, struct{}](),
		queuedDiscards:       requestmemo.New[sessionTextMemoRequest, serverapi.RuntimeDiscardQueuedUserMessagesMatchingResponse](),
		promptHistory:        requestmemo.New[sessionTextMemoRequest, struct{}](),
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
		return nil, errors.Join(serverapi.ErrRuntimeUnavailable, fmt.Errorf("runtime for session %q is unavailable", strings.TrimSpace(sessionID)))
	}
	return engine, nil
}

func (s *Service) SetSessionName(ctx context.Context, req serverapi.RuntimeSetSessionNameRequest) error {
	if err := req.Validate(); err != nil {
		return err
	}
	memoReq := sessionStringMemoRequest{SessionID: strings.TrimSpace(req.SessionID), Value: req.Name}
	_, err := s.sessionNames.Do(ctx, strings.TrimSpace(req.ClientRequestID), memoReq, sameSessionStringMemoRequest, func(ctx context.Context) (struct{}, error) {
		if err := s.requireControllerLease(ctx, req.SessionID, req.ControllerLeaseID); err != nil {
			return struct{}{}, err
		}
		engine, err := s.resolve(ctx, req.SessionID)
		if err != nil {
			return struct{}{}, err
		}
		return struct{}{}, engine.SetSessionName(req.Name)
	})
	return err
}

func (s *Service) SetThinkingLevel(ctx context.Context, req serverapi.RuntimeSetThinkingLevelRequest) error {
	if err := req.Validate(); err != nil {
		return err
	}
	memoReq := sessionStringMemoRequest{SessionID: strings.TrimSpace(req.SessionID), Value: req.Level}
	_, err := s.thinkingLevels.Do(ctx, strings.TrimSpace(req.ClientRequestID), memoReq, sameSessionStringMemoRequest, func(ctx context.Context) (struct{}, error) {
		if err := s.requireControllerLease(ctx, req.SessionID, req.ControllerLeaseID); err != nil {
			return struct{}{}, err
		}
		engine, err := s.resolve(ctx, req.SessionID)
		if err != nil {
			return struct{}{}, err
		}
		return struct{}{}, engine.SetThinkingLevel(req.Level)
	})
	return err
}

func (s *Service) SetFastModeEnabled(ctx context.Context, req serverapi.RuntimeSetFastModeEnabledRequest) (serverapi.RuntimeSetFastModeEnabledResponse, error) {
	if err := req.Validate(); err != nil {
		return serverapi.RuntimeSetFastModeEnabledResponse{}, err
	}
	memoReq := sessionBoolMemoRequest{SessionID: strings.TrimSpace(req.SessionID), Enabled: req.Enabled}
	return s.fastModes.Do(ctx, strings.TrimSpace(req.ClientRequestID), memoReq, sameSessionBoolMemoRequest, func(ctx context.Context) (serverapi.RuntimeSetFastModeEnabledResponse, error) {
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
	})
}

func (s *Service) SetReviewerEnabled(ctx context.Context, req serverapi.RuntimeSetReviewerEnabledRequest) (serverapi.RuntimeSetReviewerEnabledResponse, error) {
	if err := req.Validate(); err != nil {
		return serverapi.RuntimeSetReviewerEnabledResponse{}, err
	}
	memoReq := sessionBoolMemoRequest{SessionID: strings.TrimSpace(req.SessionID), Enabled: req.Enabled}
	return s.reviewers.Do(ctx, strings.TrimSpace(req.ClientRequestID), memoReq, sameSessionBoolMemoRequest, func(ctx context.Context) (serverapi.RuntimeSetReviewerEnabledResponse, error) {
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
	})
}

func (s *Service) SetAutoCompactionEnabled(ctx context.Context, req serverapi.RuntimeSetAutoCompactionEnabledRequest) (serverapi.RuntimeSetAutoCompactionEnabledResponse, error) {
	if err := req.Validate(); err != nil {
		return serverapi.RuntimeSetAutoCompactionEnabledResponse{}, err
	}
	memoReq := sessionBoolMemoRequest{SessionID: strings.TrimSpace(req.SessionID), Enabled: req.Enabled}
	return s.autoCompacts.Do(ctx, strings.TrimSpace(req.ClientRequestID), memoReq, sameSessionBoolMemoRequest, func(ctx context.Context) (serverapi.RuntimeSetAutoCompactionEnabledResponse, error) {
		if err := s.requireControllerLease(ctx, req.SessionID, req.ControllerLeaseID); err != nil {
			return serverapi.RuntimeSetAutoCompactionEnabledResponse{}, err
		}
		engine, err := s.resolve(ctx, req.SessionID)
		if err != nil {
			return serverapi.RuntimeSetAutoCompactionEnabledResponse{}, err
		}
		changed, enabled := engine.SetAutoCompactionEnabled(req.Enabled)
		return serverapi.RuntimeSetAutoCompactionEnabledResponse{Changed: changed, Enabled: enabled}, nil
	})
}

func (s *Service) AppendLocalEntry(ctx context.Context, req serverapi.RuntimeAppendLocalEntryRequest) error {
	if err := req.Validate(); err != nil {
		return err
	}
	memoReq := localEntryMemoRequest{SessionID: strings.TrimSpace(req.SessionID), Role: strings.TrimSpace(req.Role), Text: req.Text}
	_, err := s.localEntries.Do(ctx, strings.TrimSpace(req.ClientRequestID), memoReq, sameLocalEntryMemoRequest, func(ctx context.Context) (struct{}, error) {
		if err := s.requireControllerLease(ctx, req.SessionID, req.ControllerLeaseID); err != nil {
			return struct{}{}, err
		}
		engine, err := s.resolve(ctx, req.SessionID)
		if err != nil {
			return struct{}{}, err
		}
		engine.AppendLocalEntry(req.Role, req.Text)
		return struct{}{}, nil
	})
	return err
}

func (s *Service) AppendSessionEntry(ctx context.Context, sessionID string, role string, text string) error {
	trimmedSessionID := strings.TrimSpace(sessionID)
	trimmedRole := strings.TrimSpace(role)
	trimmedText := strings.TrimSpace(text)
	if trimmedSessionID == "" {
		return fmt.Errorf("session id is required")
	}
	if trimmedRole == "" {
		return fmt.Errorf("role is required")
	}
	if trimmedText == "" {
		return fmt.Errorf("text is required")
	}
	engine, err := s.resolve(ctx, trimmedSessionID)
	if err != nil {
		return err
	}
	engine.AppendLocalEntry(trimmedRole, trimmedText)
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
	memoReq := sessionTextMemoRequest{SessionID: strings.TrimSpace(req.SessionID), Text: req.Text}
	return s.submits.Do(ctx, strings.TrimSpace(req.ClientRequestID), memoReq, sameSessionTextMemoRequest, func(ctx context.Context) (serverapi.RuntimeSubmitUserMessageResponse, error) {
		if err := s.requireControllerLease(ctx, req.SessionID, req.ControllerLeaseID); err != nil {
			return serverapi.RuntimeSubmitUserMessageResponse{}, err
		}
		lease, err := s.acquirePrimaryRun(memoReq.SessionID)
		if err != nil {
			return serverapi.RuntimeSubmitUserMessageResponse{}, err
		}
		defer lease.Release()
		engine, err := s.resolve(ctx, req.SessionID)
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
	memoReq := sessionCommandMemoRequest{SessionID: strings.TrimSpace(req.SessionID), Command: req.Command}
	_, err := s.shells.Do(ctx, strings.TrimSpace(req.ClientRequestID), memoReq, sameSessionCommandMemoRequest, func(ctx context.Context) (struct{}, error) {
		if err := s.requireControllerLease(ctx, req.SessionID, req.ControllerLeaseID); err != nil {
			return struct{}{}, err
		}
		lease, err := s.acquirePrimaryRun(memoReq.SessionID)
		if err != nil {
			return struct{}{}, err
		}
		defer lease.Release()
		engine, err := s.resolve(ctx, req.SessionID)
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
	memoReq := sessionStringMemoRequest{SessionID: strings.TrimSpace(req.SessionID), Value: req.Args}
	_, err := s.compactions.Do(ctx, strings.TrimSpace(req.ClientRequestID), memoReq, sameSessionStringMemoRequest, func(ctx context.Context) (struct{}, error) {
		if err := s.requireControllerLease(ctx, req.SessionID, req.ControllerLeaseID); err != nil {
			return struct{}{}, err
		}
		engine, err := s.resolve(ctx, req.SessionID)
		if err != nil {
			return struct{}{}, err
		}
		return struct{}{}, engine.CompactContext(ctx, req.Args)
	})
	return err
}

func (s *Service) CompactContextForPreSubmit(ctx context.Context, req serverapi.RuntimeCompactContextForPreSubmitRequest) error {
	if err := req.Validate(); err != nil {
		return err
	}
	memoReq := sessionOnlyMemoRequest{SessionID: strings.TrimSpace(req.SessionID)}
	_, err := s.preSubmitCompactions.Do(ctx, strings.TrimSpace(req.ClientRequestID), memoReq, sameSessionOnlyMemoRequest, func(ctx context.Context) (struct{}, error) {
		if err := s.requireControllerLease(ctx, req.SessionID, req.ControllerLeaseID); err != nil {
			return struct{}{}, err
		}
		engine, err := s.resolve(ctx, req.SessionID)
		if err != nil {
			return struct{}{}, err
		}
		return struct{}{}, engine.CompactContextForPreSubmit(ctx)
	})
	return err
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
	memoReq := sessionOnlyMemoRequest{SessionID: strings.TrimSpace(req.SessionID)}
	return s.queuedSubmits.Do(ctx, strings.TrimSpace(req.ClientRequestID), memoReq, sameSessionOnlyMemoRequest, func(ctx context.Context) (serverapi.RuntimeSubmitQueuedUserMessagesResponse, error) {
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
	})
}

func (s *Service) Interrupt(ctx context.Context, req serverapi.RuntimeInterruptRequest) error {
	if err := req.Validate(); err != nil {
		return err
	}
	memoReq := sessionOnlyMemoRequest{SessionID: strings.TrimSpace(req.SessionID)}
	_, err := s.interrupts.Do(ctx, strings.TrimSpace(req.ClientRequestID), memoReq, sameSessionOnlyMemoRequest, func(ctx context.Context) (struct{}, error) {
		if err := s.requireControllerLease(ctx, req.SessionID, req.ControllerLeaseID); err != nil {
			return struct{}{}, err
		}
		engine, err := s.resolve(ctx, req.SessionID)
		if err != nil {
			return struct{}{}, err
		}
		return struct{}{}, engine.Interrupt()
	})
	return err
}

func (s *Service) QueueUserMessage(ctx context.Context, req serverapi.RuntimeQueueUserMessageRequest) error {
	if err := req.Validate(); err != nil {
		return err
	}
	memoReq := sessionTextMemoRequest{SessionID: strings.TrimSpace(req.SessionID), Text: req.Text}
	_, err := s.queues.Do(ctx, strings.TrimSpace(req.ClientRequestID), memoReq, sameSessionTextMemoRequest, func(ctx context.Context) (struct{}, error) {
		if err := s.requireControllerLease(ctx, req.SessionID, req.ControllerLeaseID); err != nil {
			return struct{}{}, err
		}
		engine, err := s.resolve(ctx, req.SessionID)
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
	memoReq := sessionTextMemoRequest{SessionID: strings.TrimSpace(req.SessionID), Text: req.Text}
	return s.queuedDiscards.Do(ctx, strings.TrimSpace(req.ClientRequestID), memoReq, sameSessionTextMemoRequest, func(ctx context.Context) (serverapi.RuntimeDiscardQueuedUserMessagesMatchingResponse, error) {
		if err := s.requireControllerLease(ctx, req.SessionID, req.ControllerLeaseID); err != nil {
			return serverapi.RuntimeDiscardQueuedUserMessagesMatchingResponse{}, err
		}
		engine, err := s.resolve(ctx, req.SessionID)
		if err != nil {
			return serverapi.RuntimeDiscardQueuedUserMessagesMatchingResponse{}, err
		}
		return serverapi.RuntimeDiscardQueuedUserMessagesMatchingResponse{Discarded: engine.DiscardQueuedUserMessagesMatching(req.Text)}, nil
	})
}

func (s *Service) RecordPromptHistory(ctx context.Context, req serverapi.RuntimeRecordPromptHistoryRequest) error {
	if err := req.Validate(); err != nil {
		return err
	}
	memoReq := sessionTextMemoRequest{SessionID: strings.TrimSpace(req.SessionID), Text: req.Text}
	_, err := s.promptHistory.Do(ctx, strings.TrimSpace(req.ClientRequestID), memoReq, sameSessionTextMemoRequest, func(ctx context.Context) (struct{}, error) {
		if err := s.requireControllerLease(ctx, req.SessionID, req.ControllerLeaseID); err != nil {
			return struct{}{}, err
		}
		engine, err := s.resolve(ctx, req.SessionID)
		if err != nil {
			return struct{}{}, err
		}
		return struct{}{}, engine.RecordPromptHistory(req.Text)
	})
	return err
}

func (s *Service) acquirePrimaryRun(sessionID string) (primaryrun.Lease, error) {
	if s == nil || s.gate == nil {
		return primaryrun.LeaseFunc(func() {}), nil
	}
	return s.gate.AcquirePrimaryRun(strings.TrimSpace(sessionID))
}

func sameSessionTextMemoRequest(a sessionTextMemoRequest, b sessionTextMemoRequest) bool {
	return a.SessionID == b.SessionID && a.Text == b.Text
}

func sameSessionStringMemoRequest(a sessionStringMemoRequest, b sessionStringMemoRequest) bool {
	return a.SessionID == b.SessionID && a.Value == b.Value
}

func sameSessionBoolMemoRequest(a sessionBoolMemoRequest, b sessionBoolMemoRequest) bool {
	return a.SessionID == b.SessionID && a.Enabled == b.Enabled
}

func sameSessionCommandMemoRequest(a sessionCommandMemoRequest, b sessionCommandMemoRequest) bool {
	return a.SessionID == b.SessionID && a.Command == b.Command
}

func sameSessionOnlyMemoRequest(a sessionOnlyMemoRequest, b sessionOnlyMemoRequest) bool {
	return a.SessionID == b.SessionID
}

func sameLocalEntryMemoRequest(a localEntryMemoRequest, b localEntryMemoRequest) bool {
	return a.SessionID == b.SessionID && a.Role == b.Role && a.Text == b.Text
}

var _ serverapi.RuntimeControlService = (*Service)(nil)

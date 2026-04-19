package promptcontrol

import (
	"context"
	"errors"

	"builder/server/requestmemo"
	askquestion "builder/server/tools/askquestion"
	"builder/shared/clientui"
	"builder/shared/serverapi"
)

type PendingPromptResponder interface {
	SubmitPromptResponse(sessionID string, resp askquestion.Response, err error) error
}

type ControllerLeaseVerifier interface {
	RequireControllerLease(ctx context.Context, sessionID string, leaseID string) error
}

type Service struct {
	prompts   PendingPromptResponder
	control   ControllerLeaseVerifier
	asks      *requestmemo.Memo[askAnswerMemoRequest, struct{}]
	approvals *requestmemo.Memo[approvalAnswerMemoRequest, struct{}]
}

type askAnswerMemoRequest struct {
	SessionID            string
	ControllerLeaseID    string
	AskID                string
	ErrorMessage         string
	Answer               string
	SelectedOptionNumber int
	FreeformAnswer       string
}

type approvalAnswerMemoRequest struct {
	SessionID         string
	ControllerLeaseID string
	ApprovalID        string
	ErrorMessage      string
	Decision          clientui.ApprovalDecision
	Commentary        string
}

func NewService(prompts PendingPromptResponder) *Service {
	return &Service{
		prompts:   prompts,
		asks:      requestmemo.New[askAnswerMemoRequest, struct{}](),
		approvals: requestmemo.New[approvalAnswerMemoRequest, struct{}](),
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

func (s *Service) AnswerAsk(ctx context.Context, req serverapi.AskAnswerRequest) error {
	if err := req.Validate(); err != nil {
		return err
	}
	if s == nil || s.prompts == nil {
		return errors.New("prompt responder is required")
	}
	memoReq := askAnswerMemoRequest{
		SessionID:            req.SessionID,
		ControllerLeaseID:    req.ControllerLeaseID,
		AskID:                req.AskID,
		ErrorMessage:         req.ErrorMessage,
		Answer:               req.Answer,
		SelectedOptionNumber: req.SelectedOptionNumber,
		FreeformAnswer:       req.FreeformAnswer,
	}
	_, err := s.asks.Do(ctx, req.ClientRequestID, memoReq, sameAskAnswerMemoRequest, func(ctx context.Context) (struct{}, error) {
		if err := s.requireControllerLease(ctx, req.SessionID, req.ControllerLeaseID); err != nil {
			return struct{}{}, err
		}
		if req.ErrorMessage != "" {
			return struct{}{}, s.prompts.SubmitPromptResponse(req.SessionID, askquestion.Response{RequestID: req.AskID}, errors.New(req.ErrorMessage))
		}
		return struct{}{}, s.prompts.SubmitPromptResponse(req.SessionID, askquestion.Response{
			RequestID:            req.AskID,
			Answer:               req.Answer,
			SelectedOptionNumber: req.SelectedOptionNumber,
			FreeformAnswer:       req.FreeformAnswer,
		}, nil)
	})
	return err
}

func (s *Service) AnswerApproval(ctx context.Context, req serverapi.ApprovalAnswerRequest) error {
	if err := req.Validate(); err != nil {
		return err
	}
	if s == nil || s.prompts == nil {
		return errors.New("prompt responder is required")
	}
	memoReq := approvalAnswerMemoRequest{
		SessionID:         req.SessionID,
		ControllerLeaseID: req.ControllerLeaseID,
		ApprovalID:        req.ApprovalID,
		ErrorMessage:      req.ErrorMessage,
		Decision:          req.Decision,
		Commentary:        req.Commentary,
	}
	_, err := s.approvals.Do(ctx, req.ClientRequestID, memoReq, sameApprovalAnswerMemoRequest, func(ctx context.Context) (struct{}, error) {
		if err := s.requireControllerLease(ctx, req.SessionID, req.ControllerLeaseID); err != nil {
			return struct{}{}, err
		}
		if req.ErrorMessage != "" {
			return struct{}{}, s.prompts.SubmitPromptResponse(req.SessionID, askquestion.Response{RequestID: req.ApprovalID}, errors.New(req.ErrorMessage))
		}
		return struct{}{}, s.prompts.SubmitPromptResponse(req.SessionID, askquestion.Response{
			RequestID: req.ApprovalID,
			Approval: &askquestion.ApprovalPayload{
				Decision:   askquestion.ApprovalDecision(req.Decision),
				Commentary: req.Commentary,
			},
		}, nil)
	})
	return err
}

func sameAskAnswerMemoRequest(a askAnswerMemoRequest, b askAnswerMemoRequest) bool {
	return a.SessionID == b.SessionID &&
		a.ControllerLeaseID == b.ControllerLeaseID &&
		a.AskID == b.AskID &&
		a.ErrorMessage == b.ErrorMessage &&
		a.Answer == b.Answer &&
		a.SelectedOptionNumber == b.SelectedOptionNumber &&
		a.FreeformAnswer == b.FreeformAnswer
}

func sameApprovalAnswerMemoRequest(a approvalAnswerMemoRequest, b approvalAnswerMemoRequest) bool {
	return a.SessionID == b.SessionID &&
		a.ControllerLeaseID == b.ControllerLeaseID &&
		a.ApprovalID == b.ApprovalID &&
		a.ErrorMessage == b.ErrorMessage &&
		a.Decision == b.Decision &&
		a.Commentary == b.Commentary
}

var _ serverapi.PromptControlService = (*Service)(nil)

package promptcontrol

import (
	"context"
	"errors"

	askquestion "builder/server/tools/askquestion"
	"builder/shared/serverapi"
)

type PendingPromptResponder interface {
	SubmitPromptResponse(sessionID string, resp askquestion.Response, err error) error
}

type ControllerLeaseVerifier interface {
	RequireControllerLease(ctx context.Context, sessionID string, leaseID string) error
}

type Service struct {
	prompts PendingPromptResponder
	control ControllerLeaseVerifier
}

func NewService(prompts PendingPromptResponder) *Service {
	return &Service{prompts: prompts}
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
	if err := s.requireControllerLease(ctx, req.SessionID, req.ControllerLeaseID); err != nil {
		return err
	}
	if req.ErrorMessage != "" {
		return s.prompts.SubmitPromptResponse(req.SessionID, askquestion.Response{RequestID: req.AskID}, errors.New(req.ErrorMessage))
	}
	return s.prompts.SubmitPromptResponse(req.SessionID, askquestion.Response{
		RequestID:            req.AskID,
		Answer:               req.Answer,
		SelectedOptionNumber: req.SelectedOptionNumber,
		FreeformAnswer:       req.FreeformAnswer,
	}, nil)
}

func (s *Service) AnswerApproval(ctx context.Context, req serverapi.ApprovalAnswerRequest) error {
	if err := req.Validate(); err != nil {
		return err
	}
	if s == nil || s.prompts == nil {
		return errors.New("prompt responder is required")
	}
	if err := s.requireControllerLease(ctx, req.SessionID, req.ControllerLeaseID); err != nil {
		return err
	}
	if req.ErrorMessage != "" {
		return s.prompts.SubmitPromptResponse(req.SessionID, askquestion.Response{RequestID: req.ApprovalID}, errors.New(req.ErrorMessage))
	}
	return s.prompts.SubmitPromptResponse(req.SessionID, askquestion.Response{
		RequestID: req.ApprovalID,
		Approval: &askquestion.ApprovalPayload{
			Decision:   askquestion.ApprovalDecision(req.Decision),
			Commentary: req.Commentary,
		},
	}, nil)
}

var _ serverapi.PromptControlService = (*Service)(nil)

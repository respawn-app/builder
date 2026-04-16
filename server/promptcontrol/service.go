package promptcontrol

import (
	"context"
	"errors"

	"builder/server/idempotency"
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
	prompts     PendingPromptResponder
	control     ControllerLeaseVerifier
	coordinator *idempotency.Coordinator
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

func (s *Service) WithIdempotencyCoordinator(coordinator *idempotency.Coordinator) *Service {
	if s == nil {
		return nil
	}
	s.coordinator = coordinator
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
	run := func(ctx context.Context) (struct{}, error) {
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
	}
	if s.coordinator == nil {
		_, err := run(ctx)
		return err
	}
	fingerprint, err := idempotency.FingerprintPayload(struct {
		SessionID            string `json:"session_id"`
		AskID                string `json:"ask_id"`
		ErrorMessage         string `json:"error_message,omitempty"`
		Answer               string `json:"answer,omitempty"`
		SelectedOptionNumber int    `json:"selected_option_number,omitempty"`
		FreeformAnswer       string `json:"freeform_answer,omitempty"`
	}{
		SessionID:            req.SessionID,
		AskID:                req.AskID,
		ErrorMessage:         req.ErrorMessage,
		Answer:               req.Answer,
		SelectedOptionNumber: req.SelectedOptionNumber,
		FreeformAnswer:       req.FreeformAnswer,
	})
	if err != nil {
		return err
	}
	_, err = idempotency.Execute(ctx, s.coordinator, idempotency.Request{
		Method:             "prompt.answer_ask",
		ResourceID:         req.AskID,
		ClientRequestID:    req.ClientRequestID,
		PayloadFingerprint: fingerprint,
	}, idempotency.JSONCodec[struct{}]{}, run)
	return err
}

func (s *Service) AnswerApproval(ctx context.Context, req serverapi.ApprovalAnswerRequest) error {
	if err := req.Validate(); err != nil {
		return err
	}
	if s == nil || s.prompts == nil {
		return errors.New("prompt responder is required")
	}
	run := func(ctx context.Context) (struct{}, error) {
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
	}
	if s.coordinator == nil {
		_, err := run(ctx)
		return err
	}
	fingerprint, err := idempotency.FingerprintPayload(struct {
		SessionID    string                    `json:"session_id"`
		ApprovalID   string                    `json:"approval_id"`
		ErrorMessage string                    `json:"error_message,omitempty"`
		Decision     clientui.ApprovalDecision `json:"decision"`
		Commentary   string                    `json:"commentary,omitempty"`
	}{
		SessionID:    req.SessionID,
		ApprovalID:   req.ApprovalID,
		ErrorMessage: req.ErrorMessage,
		Decision:     req.Decision,
		Commentary:   req.Commentary,
	})
	if err != nil {
		return err
	}
	_, err = idempotency.Execute(ctx, s.coordinator, idempotency.Request{
		Method:             "prompt.answer_approval",
		ResourceID:         req.ApprovalID,
		ClientRequestID:    req.ClientRequestID,
		PayloadFingerprint: fingerprint,
	}, idempotency.JSONCodec[struct{}]{}, run)
	return err
}

var _ serverapi.PromptControlService = (*Service)(nil)

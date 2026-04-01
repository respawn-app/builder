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

type Service struct {
	prompts PendingPromptResponder
}

func NewService(prompts PendingPromptResponder) *Service {
	return &Service{prompts: prompts}
}

func (s *Service) AnswerAsk(_ context.Context, req serverapi.AskAnswerRequest) error {
	if err := req.Validate(); err != nil {
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

func (s *Service) AnswerApproval(_ context.Context, req serverapi.ApprovalAnswerRequest) error {
	if err := req.Validate(); err != nil {
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

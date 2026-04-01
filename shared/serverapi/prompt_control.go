package serverapi

import (
	"context"
	"errors"
	"strings"

	"builder/shared/clientui"
)

type AskAnswerRequest struct {
	ClientRequestID      string `json:"client_request_id"`
	SessionID            string `json:"session_id"`
	AskID                string `json:"ask_id"`
	ErrorMessage         string `json:"error_message,omitempty"`
	Answer               string `json:"answer,omitempty"`
	SelectedOptionNumber int    `json:"selected_option_number,omitempty"`
	FreeformAnswer       string `json:"freeform_answer,omitempty"`
}

type ApprovalAnswerRequest struct {
	ClientRequestID string                    `json:"client_request_id"`
	SessionID       string                    `json:"session_id"`
	ApprovalID      string                    `json:"approval_id"`
	ErrorMessage    string                    `json:"error_message,omitempty"`
	Decision        clientui.ApprovalDecision `json:"decision"`
	Commentary      string                    `json:"commentary,omitempty"`
}

type PromptControlService interface {
	AnswerAsk(ctx context.Context, req AskAnswerRequest) error
	AnswerApproval(ctx context.Context, req ApprovalAnswerRequest) error
}

func (r AskAnswerRequest) Validate() error {
	if strings.TrimSpace(r.ClientRequestID) == "" {
		return errors.New("client_request_id is required")
	}
	if strings.TrimSpace(r.SessionID) == "" {
		return errors.New("session_id is required")
	}
	if strings.TrimSpace(r.AskID) == "" {
		return errors.New("ask_id is required")
	}
	return nil
}

func (r ApprovalAnswerRequest) Validate() error {
	if strings.TrimSpace(r.ClientRequestID) == "" {
		return errors.New("client_request_id is required")
	}
	if strings.TrimSpace(r.SessionID) == "" {
		return errors.New("session_id is required")
	}
	if strings.TrimSpace(r.ApprovalID) == "" {
		return errors.New("approval_id is required")
	}
	return nil
}

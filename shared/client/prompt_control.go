package client

import (
	"context"
	"errors"

	"builder/shared/serverapi"
)

type PromptControlClient interface {
	AnswerAsk(ctx context.Context, req serverapi.AskAnswerRequest) error
	AnswerApproval(ctx context.Context, req serverapi.ApprovalAnswerRequest) error
}

type loopbackPromptControlClient struct {
	service serverapi.PromptControlService
}

func NewLoopbackPromptControlClient(service serverapi.PromptControlService) PromptControlClient {
	return &loopbackPromptControlClient{service: service}
}

func (c *loopbackPromptControlClient) AnswerAsk(ctx context.Context, req serverapi.AskAnswerRequest) error {
	if c == nil || c.service == nil {
		return errors.New("prompt control service is required")
	}
	return c.service.AnswerAsk(ctx, req)
}

func (c *loopbackPromptControlClient) AnswerApproval(ctx context.Context, req serverapi.ApprovalAnswerRequest) error {
	if c == nil || c.service == nil {
		return errors.New("prompt control service is required")
	}
	return c.service.AnswerApproval(ctx, req)
}

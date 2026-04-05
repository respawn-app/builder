package client

import (
	"context"
	"errors"

	"builder/shared/serverapi"
)

type RunPromptClient interface {
	RunPrompt(ctx context.Context, req serverapi.RunPromptRequest, progress serverapi.RunPromptProgressSink) (serverapi.RunPromptResponse, error)
}

type loopbackRunPromptClient struct {
	service serverapi.RunPromptService
}

func NewLoopbackRunPromptClient(service serverapi.RunPromptService) RunPromptClient {
	return &loopbackRunPromptClient{service: service}
}

func (c *loopbackRunPromptClient) RunPrompt(ctx context.Context, req serverapi.RunPromptRequest, progress serverapi.RunPromptProgressSink) (serverapi.RunPromptResponse, error) {
	if c == nil || c.service == nil {
		return serverapi.RunPromptResponse{}, errors.New("run prompt service is required")
	}
	return c.service.RunPrompt(ctx, req, progress)
}

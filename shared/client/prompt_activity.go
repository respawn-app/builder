package client

import (
	"context"
	"errors"

	"builder/shared/serverapi"
	"builder/shared/servicecontract"
)

type PromptActivityClient interface {
	SubscribePromptActivity(ctx context.Context, req serverapi.PromptActivitySubscribeRequest) (serverapi.PromptActivitySubscription, error)
}

type loopbackPromptActivityClient struct {
	service servicecontract.PromptActivityService
}

func NewLoopbackPromptActivityClient(service servicecontract.PromptActivityService) PromptActivityClient {
	if service == nil {
		return nil
	}
	return &loopbackPromptActivityClient{service: service}
}

func (c *loopbackPromptActivityClient) SubscribePromptActivity(ctx context.Context, req serverapi.PromptActivitySubscribeRequest) (serverapi.PromptActivitySubscription, error) {
	if c == nil || c.service == nil {
		return nil, errors.New("prompt activity service is required")
	}
	return c.service.SubscribePromptActivity(ctx, req)
}

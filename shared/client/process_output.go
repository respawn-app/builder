package client

import (
	"context"
	"errors"

	"builder/shared/serverapi"
)

type ProcessOutputClient interface {
	SubscribeProcessOutput(ctx context.Context, req serverapi.ProcessOutputSubscribeRequest) (serverapi.ProcessOutputSubscription, error)
}

type loopbackProcessOutputClient struct {
	service serverapi.ProcessOutputService
}

func NewLoopbackProcessOutputClient(service serverapi.ProcessOutputService) ProcessOutputClient {
	return &loopbackProcessOutputClient{service: service}
}

func (c *loopbackProcessOutputClient) SubscribeProcessOutput(ctx context.Context, req serverapi.ProcessOutputSubscribeRequest) (serverapi.ProcessOutputSubscription, error) {
	if c == nil || c.service == nil {
		return nil, errors.New("process output service is required")
	}
	return c.service.SubscribeProcessOutput(ctx, req)
}

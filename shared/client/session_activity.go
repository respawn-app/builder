package client

import (
	"context"
	"errors"

	"builder/shared/serverapi"
)

type SessionActivityClient interface {
	SubscribeSessionActivity(ctx context.Context, req serverapi.SessionActivitySubscribeRequest) (serverapi.SessionActivitySubscription, error)
}

type loopbackSessionActivityClient struct {
	service serverapi.SessionActivityService
}

func NewLoopbackSessionActivityClient(service serverapi.SessionActivityService) SessionActivityClient {
	return &loopbackSessionActivityClient{service: service}
}

func (c *loopbackSessionActivityClient) SubscribeSessionActivity(ctx context.Context, req serverapi.SessionActivitySubscribeRequest) (serverapi.SessionActivitySubscription, error) {
	if c == nil || c.service == nil {
		return nil, errors.New("session activity service is required")
	}
	return c.service.SubscribeSessionActivity(ctx, req)
}

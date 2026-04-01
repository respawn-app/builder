package client

import (
	"context"
	"errors"

	"builder/shared/serverapi"
)

type SessionRuntimeClient interface {
	ActivateSessionRuntime(ctx context.Context, req serverapi.SessionRuntimeActivateRequest) error
	ReleaseSessionRuntime(ctx context.Context, req serverapi.SessionRuntimeReleaseRequest) error
}

type loopbackSessionRuntimeClient struct {
	service serverapi.SessionRuntimeService
}

func NewLoopbackSessionRuntimeClient(service serverapi.SessionRuntimeService) SessionRuntimeClient {
	return &loopbackSessionRuntimeClient{service: service}
}

func (c *loopbackSessionRuntimeClient) ActivateSessionRuntime(ctx context.Context, req serverapi.SessionRuntimeActivateRequest) error {
	if c == nil || c.service == nil {
		return errors.New("session runtime service is required")
	}
	return c.service.ActivateSessionRuntime(ctx, req)
}

func (c *loopbackSessionRuntimeClient) ReleaseSessionRuntime(ctx context.Context, req serverapi.SessionRuntimeReleaseRequest) error {
	if c == nil || c.service == nil {
		return errors.New("session runtime service is required")
	}
	return c.service.ReleaseSessionRuntime(ctx, req)
}

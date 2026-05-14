package client

import (
	"context"
	"errors"

	"builder/shared/serverapi"
	"builder/shared/servicecontract"
)

type SessionRuntimeClient interface {
	ActivateSessionRuntime(ctx context.Context, req serverapi.SessionRuntimeActivateRequest) (serverapi.SessionRuntimeActivateResponse, error)
	ReleaseSessionRuntime(ctx context.Context, req serverapi.SessionRuntimeReleaseRequest) (serverapi.SessionRuntimeReleaseResponse, error)
}

type loopbackSessionRuntimeClient struct {
	service servicecontract.SessionRuntimeService
}

func NewLoopbackSessionRuntimeClient(service servicecontract.SessionRuntimeService) SessionRuntimeClient {
	return &loopbackSessionRuntimeClient{service: service}
}

func (c *loopbackSessionRuntimeClient) ActivateSessionRuntime(ctx context.Context, req serverapi.SessionRuntimeActivateRequest) (serverapi.SessionRuntimeActivateResponse, error) {
	if c == nil || c.service == nil {
		return serverapi.SessionRuntimeActivateResponse{}, errors.New("session runtime service is required")
	}
	return c.service.ActivateSessionRuntime(ctx, req)
}

func (c *loopbackSessionRuntimeClient) ReleaseSessionRuntime(ctx context.Context, req serverapi.SessionRuntimeReleaseRequest) (serverapi.SessionRuntimeReleaseResponse, error) {
	if c == nil || c.service == nil {
		return serverapi.SessionRuntimeReleaseResponse{}, errors.New("session runtime service is required")
	}
	return c.service.ReleaseSessionRuntime(ctx, req)
}

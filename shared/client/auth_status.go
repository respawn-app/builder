package client

import (
	"context"
	"errors"

	"builder/shared/serverapi"
	"builder/shared/servicecontract"
)

type AuthStatusClient interface {
	GetAuthStatus(ctx context.Context, req serverapi.AuthStatusRequest) (serverapi.AuthStatusResponse, error)
}

type loopbackAuthStatusClient struct {
	service servicecontract.AuthStatusService
}

func NewLoopbackAuthStatusClient(service servicecontract.AuthStatusService) AuthStatusClient {
	return &loopbackAuthStatusClient{service: service}
}

func (c *loopbackAuthStatusClient) GetAuthStatus(ctx context.Context, req serverapi.AuthStatusRequest) (serverapi.AuthStatusResponse, error) {
	if c == nil || c.service == nil {
		return serverapi.AuthStatusResponse{}, errors.New("auth status service is required")
	}
	return c.service.GetAuthStatus(ctx, req)
}

package client

import (
	"context"
	"errors"

	"builder/shared/serverapi"
)

type AuthBootstrapClient interface {
	GetAuthBootstrapStatus(ctx context.Context, req serverapi.AuthGetBootstrapStatusRequest) (serverapi.AuthGetBootstrapStatusResponse, error)
	CompleteAuthBootstrap(ctx context.Context, req serverapi.AuthCompleteBootstrapRequest) (serverapi.AuthCompleteBootstrapResponse, error)
}

type loopbackAuthBootstrapClient struct {
	service serverapi.AuthBootstrapService
}

func NewLoopbackAuthBootstrapClient(service serverapi.AuthBootstrapService) AuthBootstrapClient {
	return &loopbackAuthBootstrapClient{service: service}
}

func (c *loopbackAuthBootstrapClient) GetAuthBootstrapStatus(ctx context.Context, req serverapi.AuthGetBootstrapStatusRequest) (serverapi.AuthGetBootstrapStatusResponse, error) {
	if c == nil || c.service == nil {
		return serverapi.AuthGetBootstrapStatusResponse{}, errors.New("auth bootstrap service is required")
	}
	return c.service.GetBootstrapStatus(ctx, req)
}

func (c *loopbackAuthBootstrapClient) CompleteAuthBootstrap(ctx context.Context, req serverapi.AuthCompleteBootstrapRequest) (serverapi.AuthCompleteBootstrapResponse, error) {
	if c == nil || c.service == nil {
		return serverapi.AuthCompleteBootstrapResponse{}, errors.New("auth bootstrap service is required")
	}
	return c.service.CompleteBootstrap(ctx, req)
}

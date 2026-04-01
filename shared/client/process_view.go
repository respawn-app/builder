package client

import (
	"context"
	"errors"

	"builder/shared/serverapi"
)

type ProcessViewClient interface {
	ListProcesses(ctx context.Context, req serverapi.ProcessListRequest) (serverapi.ProcessListResponse, error)
	GetProcess(ctx context.Context, req serverapi.ProcessGetRequest) (serverapi.ProcessGetResponse, error)
}

type loopbackProcessViewClient struct {
	service serverapi.ProcessViewService
}

func NewLoopbackProcessViewClient(service serverapi.ProcessViewService) ProcessViewClient {
	return &loopbackProcessViewClient{service: service}
}

func (c *loopbackProcessViewClient) ListProcesses(ctx context.Context, req serverapi.ProcessListRequest) (serverapi.ProcessListResponse, error) {
	if c == nil || c.service == nil {
		return serverapi.ProcessListResponse{}, errors.New("process view service is required")
	}
	return c.service.ListProcesses(ctx, req)
}

func (c *loopbackProcessViewClient) GetProcess(ctx context.Context, req serverapi.ProcessGetRequest) (serverapi.ProcessGetResponse, error) {
	if c == nil || c.service == nil {
		return serverapi.ProcessGetResponse{}, errors.New("process view service is required")
	}
	return c.service.GetProcess(ctx, req)
}

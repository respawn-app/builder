package client

import (
	"context"
	"errors"

	"builder/shared/serverapi"
)

type ProcessControlClient interface {
	KillProcess(ctx context.Context, req serverapi.ProcessKillRequest) (serverapi.ProcessKillResponse, error)
	GetInlineOutput(ctx context.Context, req serverapi.ProcessInlineOutputRequest) (serverapi.ProcessInlineOutputResponse, error)
}

type loopbackProcessControlClient struct {
	service serverapi.ProcessControlService
}

func NewLoopbackProcessControlClient(service serverapi.ProcessControlService) ProcessControlClient {
	return &loopbackProcessControlClient{service: service}
}

func (c *loopbackProcessControlClient) KillProcess(ctx context.Context, req serverapi.ProcessKillRequest) (serverapi.ProcessKillResponse, error) {
	if c == nil || c.service == nil {
		return serverapi.ProcessKillResponse{}, errors.New("process control service is required")
	}
	return c.service.KillProcess(ctx, req)
}

func (c *loopbackProcessControlClient) GetInlineOutput(ctx context.Context, req serverapi.ProcessInlineOutputRequest) (serverapi.ProcessInlineOutputResponse, error) {
	if c == nil || c.service == nil {
		return serverapi.ProcessInlineOutputResponse{}, errors.New("process control service is required")
	}
	return c.service.GetInlineOutput(ctx, req)
}

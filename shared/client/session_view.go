package client

import (
	"context"
	"errors"

	"builder/shared/serverapi"
)

type SessionViewClient interface {
	GetSessionMainView(ctx context.Context, req serverapi.SessionMainViewRequest) (serverapi.SessionMainViewResponse, error)
	GetRun(ctx context.Context, req serverapi.RunGetRequest) (serverapi.RunGetResponse, error)
}

type loopbackSessionViewClient struct {
	service serverapi.SessionViewService
}

func NewLoopbackSessionViewClient(service serverapi.SessionViewService) SessionViewClient {
	return &loopbackSessionViewClient{service: service}
}

func (c *loopbackSessionViewClient) GetSessionMainView(ctx context.Context, req serverapi.SessionMainViewRequest) (serverapi.SessionMainViewResponse, error) {
	if c == nil || c.service == nil {
		return serverapi.SessionMainViewResponse{}, errors.New("session view service is required")
	}
	return c.service.GetSessionMainView(ctx, req)
}

func (c *loopbackSessionViewClient) GetRun(ctx context.Context, req serverapi.RunGetRequest) (serverapi.RunGetResponse, error) {
	if c == nil || c.service == nil {
		return serverapi.RunGetResponse{}, errors.New("session view service is required")
	}
	return c.service.GetRun(ctx, req)
}

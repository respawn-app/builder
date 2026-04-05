package client

import (
	"context"
	"errors"

	"builder/shared/serverapi"
)

type SessionLaunchClient interface {
	PlanSession(ctx context.Context, req serverapi.SessionPlanRequest) (serverapi.SessionPlanResponse, error)
}

type loopbackSessionLaunchClient struct {
	service serverapi.SessionLaunchService
}

func NewLoopbackSessionLaunchClient(service serverapi.SessionLaunchService) SessionLaunchClient {
	return &loopbackSessionLaunchClient{service: service}
}

func (c *loopbackSessionLaunchClient) PlanSession(ctx context.Context, req serverapi.SessionPlanRequest) (serverapi.SessionPlanResponse, error) {
	if c == nil || c.service == nil {
		return serverapi.SessionPlanResponse{}, errors.New("session launch service is required")
	}
	return c.service.PlanSession(ctx, req)
}

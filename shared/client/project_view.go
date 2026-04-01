package client

import (
	"context"
	"errors"

	"builder/shared/serverapi"
)

type ProjectViewClient interface {
	ListProjects(ctx context.Context, req serverapi.ProjectListRequest) (serverapi.ProjectListResponse, error)
	GetProjectOverview(ctx context.Context, req serverapi.ProjectGetOverviewRequest) (serverapi.ProjectGetOverviewResponse, error)
	ListSessionsByProject(ctx context.Context, req serverapi.SessionListByProjectRequest) (serverapi.SessionListByProjectResponse, error)
}

type loopbackProjectViewClient struct {
	service serverapi.ProjectViewService
}

func NewLoopbackProjectViewClient(service serverapi.ProjectViewService) ProjectViewClient {
	return &loopbackProjectViewClient{service: service}
}

func (c *loopbackProjectViewClient) ListProjects(ctx context.Context, req serverapi.ProjectListRequest) (serverapi.ProjectListResponse, error) {
	if c == nil || c.service == nil {
		return serverapi.ProjectListResponse{}, errors.New("project view service is required")
	}
	return c.service.ListProjects(ctx, req)
}

func (c *loopbackProjectViewClient) GetProjectOverview(ctx context.Context, req serverapi.ProjectGetOverviewRequest) (serverapi.ProjectGetOverviewResponse, error) {
	if c == nil || c.service == nil {
		return serverapi.ProjectGetOverviewResponse{}, errors.New("project view service is required")
	}
	return c.service.GetProjectOverview(ctx, req)
}

func (c *loopbackProjectViewClient) ListSessionsByProject(ctx context.Context, req serverapi.SessionListByProjectRequest) (serverapi.SessionListByProjectResponse, error) {
	if c == nil || c.service == nil {
		return serverapi.SessionListByProjectResponse{}, errors.New("project view service is required")
	}
	return c.service.ListSessionsByProject(ctx, req)
}

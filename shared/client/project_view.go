package client

import (
	"context"
	"errors"

	"builder/shared/serverapi"
	"builder/shared/servicecontract"
)

type ProjectViewClient interface {
	ListProjects(ctx context.Context, req serverapi.ProjectListRequest) (serverapi.ProjectListResponse, error)
	ListProjectHome(ctx context.Context, req serverapi.ProjectHomeListRequest) (serverapi.ProjectHomeListResponse, error)
	ResolveProjectPath(ctx context.Context, req serverapi.ProjectResolvePathRequest) (serverapi.ProjectResolvePathResponse, error)
	PlanWorkspaceBinding(ctx context.Context, req serverapi.ProjectBindingPlanRequest) (serverapi.ProjectBindingPlanResponse, error)
	CreateProject(ctx context.Context, req serverapi.ProjectCreateRequest) (serverapi.ProjectCreateResponse, error)
	GetProjectEdit(ctx context.Context, req serverapi.ProjectEditGetRequest) (serverapi.ProjectEditGetResponse, error)
	UpdateProject(ctx context.Context, req serverapi.ProjectUpdateRequest) (serverapi.ProjectUpdateResponse, error)
	SetDefaultWorkspace(ctx context.Context, req serverapi.ProjectDefaultWorkspaceSetRequest) (serverapi.ProjectDefaultWorkspaceSetResponse, error)
	ListProjectWorkspaces(ctx context.Context, req serverapi.ProjectWorkspaceListRequest) (serverapi.ProjectWorkspaceListResponse, error)
	UnlinkWorkspaceFromProject(ctx context.Context, req serverapi.ProjectWorkspaceUnlinkRequest) (serverapi.ProjectWorkspaceUnlinkResponse, error)
	AttachWorkspaceToProject(ctx context.Context, req serverapi.ProjectAttachWorkspaceRequest) (serverapi.ProjectAttachWorkspaceResponse, error)
	RebindWorkspace(ctx context.Context, req serverapi.ProjectRebindWorkspaceRequest) (serverapi.ProjectRebindWorkspaceResponse, error)
	GetProjectOverview(ctx context.Context, req serverapi.ProjectGetOverviewRequest) (serverapi.ProjectGetOverviewResponse, error)
	ListSessionsByProject(ctx context.Context, req serverapi.SessionListByProjectRequest) (serverapi.SessionListByProjectResponse, error)
}

type loopbackProjectViewClient struct {
	service servicecontract.ProjectViewService
}

func NewLoopbackProjectViewClient(service servicecontract.ProjectViewService) ProjectViewClient {
	return &loopbackProjectViewClient{service: service}
}

func (c *loopbackProjectViewClient) ListProjects(ctx context.Context, req serverapi.ProjectListRequest) (serverapi.ProjectListResponse, error) {
	if c == nil || c.service == nil {
		return serverapi.ProjectListResponse{}, errors.New("project view service is required")
	}
	return c.service.ListProjects(ctx, req)
}

func (c *loopbackProjectViewClient) ListProjectHome(ctx context.Context, req serverapi.ProjectHomeListRequest) (serverapi.ProjectHomeListResponse, error) {
	if c == nil || c.service == nil {
		return serverapi.ProjectHomeListResponse{}, errors.New("project view service is required")
	}
	return c.service.ListProjectHome(ctx, req)
}

func (c *loopbackProjectViewClient) ResolveProjectPath(ctx context.Context, req serverapi.ProjectResolvePathRequest) (serverapi.ProjectResolvePathResponse, error) {
	if c == nil || c.service == nil {
		return serverapi.ProjectResolvePathResponse{}, errors.New("project view service is required")
	}
	return c.service.ResolveProjectPath(ctx, req)
}

func (c *loopbackProjectViewClient) PlanWorkspaceBinding(ctx context.Context, req serverapi.ProjectBindingPlanRequest) (serverapi.ProjectBindingPlanResponse, error) {
	if c == nil || c.service == nil {
		return serverapi.ProjectBindingPlanResponse{}, errors.New("project view service is required")
	}
	return c.service.PlanWorkspaceBinding(ctx, req)
}

func (c *loopbackProjectViewClient) CreateProject(ctx context.Context, req serverapi.ProjectCreateRequest) (serverapi.ProjectCreateResponse, error) {
	if c == nil || c.service == nil {
		return serverapi.ProjectCreateResponse{}, errors.New("project view service is required")
	}
	return c.service.CreateProject(ctx, req)
}

func (c *loopbackProjectViewClient) GetProjectEdit(ctx context.Context, req serverapi.ProjectEditGetRequest) (serverapi.ProjectEditGetResponse, error) {
	if c == nil || c.service == nil {
		return serverapi.ProjectEditGetResponse{}, errors.New("project view service is required")
	}
	return c.service.GetProjectEdit(ctx, req)
}

func (c *loopbackProjectViewClient) UpdateProject(ctx context.Context, req serverapi.ProjectUpdateRequest) (serverapi.ProjectUpdateResponse, error) {
	if c == nil || c.service == nil {
		return serverapi.ProjectUpdateResponse{}, errors.New("project view service is required")
	}
	return c.service.UpdateProject(ctx, req)
}

func (c *loopbackProjectViewClient) SetDefaultWorkspace(ctx context.Context, req serverapi.ProjectDefaultWorkspaceSetRequest) (serverapi.ProjectDefaultWorkspaceSetResponse, error) {
	if c == nil || c.service == nil {
		return serverapi.ProjectDefaultWorkspaceSetResponse{}, errors.New("project view service is required")
	}
	return c.service.SetDefaultWorkspace(ctx, req)
}

func (c *loopbackProjectViewClient) ListProjectWorkspaces(ctx context.Context, req serverapi.ProjectWorkspaceListRequest) (serverapi.ProjectWorkspaceListResponse, error) {
	if c == nil || c.service == nil {
		return serverapi.ProjectWorkspaceListResponse{}, errors.New("project view service is required")
	}
	return c.service.ListProjectWorkspaces(ctx, req)
}

func (c *loopbackProjectViewClient) UnlinkWorkspaceFromProject(ctx context.Context, req serverapi.ProjectWorkspaceUnlinkRequest) (serverapi.ProjectWorkspaceUnlinkResponse, error) {
	if c == nil || c.service == nil {
		return serverapi.ProjectWorkspaceUnlinkResponse{}, errors.New("project view service is required")
	}
	return c.service.UnlinkWorkspaceFromProject(ctx, req)
}

func (c *loopbackProjectViewClient) AttachWorkspaceToProject(ctx context.Context, req serverapi.ProjectAttachWorkspaceRequest) (serverapi.ProjectAttachWorkspaceResponse, error) {
	if c == nil || c.service == nil {
		return serverapi.ProjectAttachWorkspaceResponse{}, errors.New("project view service is required")
	}
	return c.service.AttachWorkspaceToProject(ctx, req)
}

func (c *loopbackProjectViewClient) RebindWorkspace(ctx context.Context, req serverapi.ProjectRebindWorkspaceRequest) (serverapi.ProjectRebindWorkspaceResponse, error) {
	if c == nil || c.service == nil {
		return serverapi.ProjectRebindWorkspaceResponse{}, errors.New("project view service is required")
	}
	return c.service.RebindWorkspace(ctx, req)
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

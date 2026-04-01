package serverapi

import (
	"context"
	"errors"
	"strings"

	"builder/shared/clientui"
)

type ProjectListRequest struct{}

type ProjectListResponse struct {
	Projects []clientui.ProjectSummary
}

type ProjectGetOverviewRequest struct {
	ProjectID string
}

type ProjectGetOverviewResponse struct {
	Overview clientui.ProjectOverview
}

type SessionListByProjectRequest struct {
	ProjectID string
}

type SessionListByProjectResponse struct {
	Sessions []clientui.SessionSummary
}

type ProjectViewService interface {
	ListProjects(ctx context.Context, req ProjectListRequest) (ProjectListResponse, error)
	GetProjectOverview(ctx context.Context, req ProjectGetOverviewRequest) (ProjectGetOverviewResponse, error)
	ListSessionsByProject(ctx context.Context, req SessionListByProjectRequest) (SessionListByProjectResponse, error)
}

func (r ProjectGetOverviewRequest) Validate() error {
	if strings.TrimSpace(r.ProjectID) == "" {
		return errors.New("project_id is required")
	}
	return nil
}

func (r SessionListByProjectRequest) Validate() error {
	if strings.TrimSpace(r.ProjectID) == "" {
		return errors.New("project_id is required")
	}
	return nil
}

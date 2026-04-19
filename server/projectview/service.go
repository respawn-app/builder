package projectview

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"builder/server/metadata"
	"builder/server/session"
	"builder/shared/clientui"
	"builder/shared/serverapi"
)

type Service struct {
	metadata     *metadata.Store
	projectID    string
	displayName  string
	rootPath     string
	containerDir string
	syncOnce     sync.Once
	syncErr      error
}

func NewService(projectID, rootPath, containerDir string) (*Service, error) {
	trimmedProjectID := strings.TrimSpace(projectID)
	trimmedRootPath := strings.TrimSpace(rootPath)
	trimmedContainerDir := strings.TrimSpace(containerDir)
	if trimmedProjectID == "" {
		return nil, errors.New("project id is required")
	}
	if trimmedRootPath == "" {
		return nil, errors.New("project root is required")
	}
	if trimmedContainerDir == "" {
		return nil, errors.New("project container dir is required")
	}
	return &Service{
		projectID:    trimmedProjectID,
		displayName:  filepath.Base(filepath.Clean(trimmedRootPath)),
		rootPath:     trimmedRootPath,
		containerDir: trimmedContainerDir,
	}, nil
}

func NewMetadataService(metadataStore *metadata.Store, projectID string, containerDir string) (*Service, error) {
	if metadataStore == nil {
		return nil, errors.New("metadata store is required")
	}
	return &Service{metadata: metadataStore, projectID: strings.TrimSpace(projectID), containerDir: strings.TrimSpace(containerDir)}, nil
}

func (s *Service) ProjectID() string {
	if s == nil {
		return ""
	}
	return s.projectID
}

func (s *Service) ListProjects(ctx context.Context, _ serverapi.ProjectListRequest) (serverapi.ProjectListResponse, error) {
	if s == nil {
		return serverapi.ProjectListResponse{}, errors.New("project service is required")
	}
	if s.metadata != nil {
		if err := s.syncMetadata(ctx); err != nil {
			return serverapi.ProjectListResponse{}, err
		}
		projects, err := s.metadata.ListProjects(ctx)
		if err != nil {
			return serverapi.ProjectListResponse{}, err
		}
		if trimmedProjectID := strings.TrimSpace(s.projectID); trimmedProjectID != "" {
			filtered := make([]clientui.ProjectSummary, 0, 1)
			for _, project := range projects {
				if strings.TrimSpace(project.ProjectID) == trimmedProjectID {
					filtered = append(filtered, project)
					break
				}
			}
			return serverapi.ProjectListResponse{Projects: filtered}, nil
		}
		return serverapi.ProjectListResponse{Projects: projects}, nil
	}
	project, err := s.projectSummary()
	if err != nil {
		return serverapi.ProjectListResponse{}, err
	}
	return serverapi.ProjectListResponse{Projects: []clientui.ProjectSummary{project}}, nil
}

func (s *Service) ResolveProjectPath(ctx context.Context, req serverapi.ProjectResolvePathRequest) (serverapi.ProjectResolvePathResponse, error) {
	if err := req.Validate(); err != nil {
		return serverapi.ProjectResolvePathResponse{}, err
	}
	if s == nil {
		return serverapi.ProjectResolvePathResponse{}, errors.New("project service is required")
	}
	if s.metadata == nil {
		return serverapi.ProjectResolvePathResponse{}, errors.New("project path resolution requires metadata service")
	}
	canonicalRoot, binding, err := s.metadata.ResolveWorkspacePath(ctx, req.Path)
	if err != nil {
		return serverapi.ProjectResolvePathResponse{}, err
	}
	resp := serverapi.ProjectResolvePathResponse{CanonicalRoot: canonicalRoot}
	resp.PathAvailability = clientui.ProjectAvailability(availabilityForProjectPath(canonicalRoot))
	if binding != nil {
		mapped := projectBindingFromMetadata(*binding)
		resp.Binding = &mapped
	}
	return resp, nil
}

func (s *Service) CreateProject(ctx context.Context, req serverapi.ProjectCreateRequest) (serverapi.ProjectCreateResponse, error) {
	if err := req.Validate(); err != nil {
		return serverapi.ProjectCreateResponse{}, err
	}
	if s == nil {
		return serverapi.ProjectCreateResponse{}, errors.New("project service is required")
	}
	if s.metadata == nil {
		return serverapi.ProjectCreateResponse{}, errors.New("project creation requires metadata service")
	}
	binding, err := s.metadata.CreateProjectForWorkspace(ctx, req.WorkspaceRoot, req.DisplayName)
	if err != nil {
		return serverapi.ProjectCreateResponse{}, err
	}
	return serverapi.ProjectCreateResponse{Binding: projectBindingFromMetadata(binding)}, nil
}

func (s *Service) AttachWorkspaceToProject(ctx context.Context, req serverapi.ProjectAttachWorkspaceRequest) (serverapi.ProjectAttachWorkspaceResponse, error) {
	if err := req.Validate(); err != nil {
		return serverapi.ProjectAttachWorkspaceResponse{}, err
	}
	if s == nil {
		return serverapi.ProjectAttachWorkspaceResponse{}, errors.New("project service is required")
	}
	if s.metadata == nil {
		return serverapi.ProjectAttachWorkspaceResponse{}, errors.New("workspace attachment requires metadata service")
	}
	if err := s.requireProjectID(req.ProjectID); err != nil {
		return serverapi.ProjectAttachWorkspaceResponse{}, err
	}
	binding, err := s.metadata.AttachWorkspaceToProject(ctx, req.ProjectID, req.WorkspaceRoot)
	if err != nil {
		return serverapi.ProjectAttachWorkspaceResponse{}, err
	}
	return serverapi.ProjectAttachWorkspaceResponse{Binding: projectBindingFromMetadata(binding)}, nil
}

func (s *Service) RebindWorkspace(ctx context.Context, req serverapi.ProjectRebindWorkspaceRequest) (serverapi.ProjectRebindWorkspaceResponse, error) {
	if err := req.Validate(); err != nil {
		return serverapi.ProjectRebindWorkspaceResponse{}, err
	}
	if s == nil {
		return serverapi.ProjectRebindWorkspaceResponse{}, errors.New("project service is required")
	}
	if s.metadata == nil {
		return serverapi.ProjectRebindWorkspaceResponse{}, errors.New("workspace rebind requires metadata service")
	}
	binding, err := s.metadata.RebindWorkspace(ctx, req.OldWorkspaceRoot, req.NewWorkspaceRoot)
	if err != nil {
		return serverapi.ProjectRebindWorkspaceResponse{}, err
	}
	return serverapi.ProjectRebindWorkspaceResponse{Binding: projectBindingFromMetadata(binding)}, nil
}

func (s *Service) GetProjectOverview(ctx context.Context, req serverapi.ProjectGetOverviewRequest) (serverapi.ProjectGetOverviewResponse, error) {
	if err := req.Validate(); err != nil {
		return serverapi.ProjectGetOverviewResponse{}, err
	}
	if s.metadata != nil {
		if err := s.syncMetadata(ctx); err != nil {
			return serverapi.ProjectGetOverviewResponse{}, err
		}
		if err := s.requireProjectID(req.ProjectID); err != nil {
			return serverapi.ProjectGetOverviewResponse{}, err
		}
		overview, err := s.metadata.GetProjectOverview(ctx, req.ProjectID)
		if err != nil {
			return serverapi.ProjectGetOverviewResponse{}, err
		}
		return serverapi.ProjectGetOverviewResponse{Overview: overview}, nil
	}
	if err := s.requireProjectID(req.ProjectID); err != nil {
		return serverapi.ProjectGetOverviewResponse{}, err
	}
	project, err := s.projectSummary()
	if err != nil {
		return serverapi.ProjectGetOverviewResponse{}, err
	}
	sessionsResp, err := s.ListSessionsByProject(ctx, serverapi.SessionListByProjectRequest{ProjectID: req.ProjectID})
	if err != nil {
		return serverapi.ProjectGetOverviewResponse{}, err
	}
	project.SessionCount = len(sessionsResp.Sessions)
	project.UpdatedAt = latestUpdatedAt(sessionsResp.Sessions)
	return serverapi.ProjectGetOverviewResponse{Overview: clientui.ProjectOverview{Project: project, Workspaces: []clientui.ProjectWorkspaceSummary{{
		WorkspaceID:  "workspace-legacy",
		DisplayName:  filepath.Base(project.RootPath),
		RootPath:     project.RootPath,
		Availability: project.Availability,
		IsPrimary:    true,
		SessionCount: project.SessionCount,
		UpdatedAt:    project.UpdatedAt,
	}}, Sessions: sessionsResp.Sessions}}, nil
}

func (s *Service) ListSessionsByProject(ctx context.Context, req serverapi.SessionListByProjectRequest) (serverapi.SessionListByProjectResponse, error) {
	if err := req.Validate(); err != nil {
		return serverapi.SessionListByProjectResponse{}, err
	}
	if s.metadata != nil {
		if err := s.syncMetadata(ctx); err != nil {
			return serverapi.SessionListByProjectResponse{}, err
		}
		if err := s.requireProjectID(req.ProjectID); err != nil {
			return serverapi.SessionListByProjectResponse{}, err
		}
		sessions, err := s.metadata.ListSessionsByProject(ctx, req.ProjectID)
		if err != nil {
			return serverapi.SessionListByProjectResponse{}, err
		}
		return serverapi.SessionListByProjectResponse{Sessions: sessions}, nil
	}
	if err := s.requireProjectID(req.ProjectID); err != nil {
		return serverapi.SessionListByProjectResponse{}, err
	}
	summaries, err := session.ListSessions(s.containerDir)
	if err != nil {
		return serverapi.SessionListByProjectResponse{}, err
	}
	out := make([]clientui.SessionSummary, 0, len(summaries))
	for _, summary := range summaries {
		out = append(out, clientui.SessionSummary{
			SessionID:          summary.SessionID,
			Name:               summary.Name,
			FirstPromptPreview: summary.FirstPromptPreview,
			UpdatedAt:          summary.UpdatedAt,
		})
	}
	return serverapi.SessionListByProjectResponse{Sessions: out}, nil
}

func (s *Service) requireProjectID(projectID string) error {
	if s == nil {
		return errors.New("project service is required")
	}
	if trimmedProjectID := strings.TrimSpace(s.projectID); trimmedProjectID != "" && strings.TrimSpace(projectID) != trimmedProjectID {
		return fmt.Errorf("project %q not available", strings.TrimSpace(projectID))
	}
	return nil
}

func (s *Service) projectSummary() (clientui.ProjectSummary, error) {
	if s == nil {
		return clientui.ProjectSummary{}, errors.New("project service is required")
	}
	availability := clientui.ProjectAvailabilityAvailable
	if _, err := os.Stat(s.rootPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			availability = clientui.ProjectAvailabilityMissing
		} else {
			availability = clientui.ProjectAvailabilityInaccessible
		}
	}
	sessions, err := session.ListSessions(s.containerDir)
	if err != nil {
		return clientui.ProjectSummary{}, err
	}
	return clientui.ProjectSummary{
		ProjectID:    s.projectID,
		DisplayName:  s.displayName,
		RootPath:     s.rootPath,
		Availability: availability,
		SessionCount: len(sessions),
		UpdatedAt:    latestSessionUpdatedAt(sessions),
	}, nil
}

func latestSessionUpdatedAt(summaries []session.Summary) (latest time.Time) {
	for _, summary := range summaries {
		if summary.UpdatedAt.After(latest) {
			latest = summary.UpdatedAt
		}
	}
	return latest
}

func latestUpdatedAt(summaries []clientui.SessionSummary) (latest time.Time) {
	for _, summary := range summaries {
		if summary.UpdatedAt.After(latest) {
			latest = summary.UpdatedAt
		}
	}
	return latest
}

func availabilityForProjectPath(path string) string {
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return string(clientui.ProjectAvailabilityMissing)
		}
		return string(clientui.ProjectAvailabilityInaccessible)
	}
	return string(clientui.ProjectAvailabilityAvailable)
}

func projectBindingFromMetadata(binding metadata.Binding) serverapi.ProjectBinding {
	return serverapi.ProjectBinding{
		ProjectID:       binding.ProjectID,
		ProjectName:     binding.ProjectName,
		WorkspaceID:     binding.WorkspaceID,
		CanonicalRoot:   binding.CanonicalRoot,
		WorkspaceName:   binding.WorkspaceName,
		WorkspaceStatus: binding.WorkspaceStatus,
	}
}

func (s *Service) syncMetadata(ctx context.Context) error {
	if s == nil || s.metadata == nil || strings.TrimSpace(s.containerDir) == "" {
		return nil
	}
	s.syncOnce.Do(func() {
		s.syncErr = s.metadata.SyncLegacyContainer(context.WithoutCancel(ctx), s.containerDir)
	})
	return s.syncErr
}

var _ serverapi.ProjectViewService = (*Service)(nil)

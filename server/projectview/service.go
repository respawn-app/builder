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
	trimmedProjectID := strings.TrimSpace(projectID)
	if metadataStore == nil {
		return nil, errors.New("metadata store is required")
	}
	if trimmedProjectID == "" {
		return nil, errors.New("project id is required")
	}
	return &Service{metadata: metadataStore, projectID: trimmedProjectID, containerDir: strings.TrimSpace(containerDir)}, nil
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
		overview, err := s.metadata.GetProjectOverview(ctx, s.projectID)
		if err != nil {
			return serverapi.ProjectListResponse{}, err
		}
		return serverapi.ProjectListResponse{Projects: []clientui.ProjectSummary{overview.Project}}, nil
	}
	project, err := s.projectSummary()
	if err != nil {
		return serverapi.ProjectListResponse{}, err
	}
	return serverapi.ProjectListResponse{Projects: []clientui.ProjectSummary{project}}, nil
}

func (s *Service) GetProjectOverview(ctx context.Context, req serverapi.ProjectGetOverviewRequest) (serverapi.ProjectGetOverviewResponse, error) {
	if err := req.Validate(); err != nil {
		return serverapi.ProjectGetOverviewResponse{}, err
	}
	if err := s.requireProjectID(req.ProjectID); err != nil {
		return serverapi.ProjectGetOverviewResponse{}, err
	}
	if s.metadata != nil {
		if err := s.syncMetadata(ctx); err != nil {
			return serverapi.ProjectGetOverviewResponse{}, err
		}
		overview, err := s.metadata.GetProjectOverview(ctx, req.ProjectID)
		if err != nil {
			return serverapi.ProjectGetOverviewResponse{}, err
		}
		return serverapi.ProjectGetOverviewResponse{Overview: overview}, nil
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
	return serverapi.ProjectGetOverviewResponse{Overview: clientui.ProjectOverview{Project: project, Sessions: sessionsResp.Sessions}}, nil
}

func (s *Service) ListSessionsByProject(ctx context.Context, req serverapi.SessionListByProjectRequest) (serverapi.SessionListByProjectResponse, error) {
	if err := req.Validate(); err != nil {
		return serverapi.SessionListByProjectResponse{}, err
	}
	if err := s.requireProjectID(req.ProjectID); err != nil {
		return serverapi.SessionListByProjectResponse{}, err
	}
	if s.metadata != nil {
		if err := s.syncMetadata(ctx); err != nil {
			return serverapi.SessionListByProjectResponse{}, err
		}
		sessions, err := s.metadata.ListSessionsByProject(ctx, req.ProjectID)
		if err != nil {
			return serverapi.SessionListByProjectResponse{}, err
		}
		return serverapi.SessionListByProjectResponse{Sessions: sessions}, nil
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
	if strings.TrimSpace(projectID) != s.projectID {
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

func (s *Service) syncMetadata(ctx context.Context) error {
	if s == nil || s.metadata == nil || strings.TrimSpace(s.containerDir) == "" {
		return nil
	}
	s.syncOnce.Do(func() {
		s.syncErr = s.metadata.SyncLegacyContainer(ctx, s.containerDir)
	})
	return s.syncErr
}

var _ serverapi.ProjectViewService = (*Service)(nil)

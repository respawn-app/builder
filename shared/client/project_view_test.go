package client

import (
	"context"
	"testing"
	"time"

	"builder/shared/clientui"
	"builder/shared/serverapi"
)

type stubProjectViewService struct {
	listResp     serverapi.ProjectListResponse
	overviewResp serverapi.ProjectGetOverviewResponse
	sessionsResp serverapi.SessionListByProjectResponse
	err          error
}

func (s *stubProjectViewService) ListProjects(context.Context, serverapi.ProjectListRequest) (serverapi.ProjectListResponse, error) {
	return s.listResp, s.err
}

func (s *stubProjectViewService) GetProjectOverview(context.Context, serverapi.ProjectGetOverviewRequest) (serverapi.ProjectGetOverviewResponse, error) {
	return s.overviewResp, s.err
}

func (s *stubProjectViewService) ListSessionsByProject(context.Context, serverapi.SessionListByProjectRequest) (serverapi.SessionListByProjectResponse, error) {
	return s.sessionsResp, s.err
}

func TestLoopbackProjectViewClientDelegatesToService(t *testing.T) {
	now := time.Now().UTC()
	svc := &stubProjectViewService{
		listResp: serverapi.ProjectListResponse{Projects: []clientui.ProjectSummary{{ProjectID: "project-1"}}},
		overviewResp: serverapi.ProjectGetOverviewResponse{Overview: clientui.ProjectOverview{
			Project:  clientui.ProjectSummary{ProjectID: "project-1"},
			Sessions: []clientui.SessionSummary{{SessionID: "session-1", UpdatedAt: now}},
		}},
		sessionsResp: serverapi.SessionListByProjectResponse{Sessions: []clientui.SessionSummary{{SessionID: "session-1", UpdatedAt: now}}},
	}
	client := NewLoopbackProjectViewClient(svc)

	listResp, err := client.ListProjects(context.Background(), serverapi.ProjectListRequest{})
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(listResp.Projects) != 1 || listResp.Projects[0].ProjectID != "project-1" {
		t.Fatalf("unexpected project list: %+v", listResp)
	}
	overviewResp, err := client.GetProjectOverview(context.Background(), serverapi.ProjectGetOverviewRequest{ProjectID: "project-1"})
	if err != nil {
		t.Fatalf("GetProjectOverview: %v", err)
	}
	if overviewResp.Overview.Project.ProjectID != "project-1" {
		t.Fatalf("unexpected overview response: %+v", overviewResp)
	}
	sessionsResp, err := client.ListSessionsByProject(context.Background(), serverapi.SessionListByProjectRequest{ProjectID: "project-1"})
	if err != nil {
		t.Fatalf("ListSessionsByProject: %v", err)
	}
	if len(sessionsResp.Sessions) != 1 || sessionsResp.Sessions[0].SessionID != "session-1" {
		t.Fatalf("unexpected sessions response: %+v", sessionsResp)
	}
}

func TestLoopbackProjectViewClientRequiresService(t *testing.T) {
	client := NewLoopbackProjectViewClient(nil)
	if _, err := client.ListProjects(context.Background(), serverapi.ProjectListRequest{}); err == nil {
		t.Fatal("expected ListProjects to fail without service")
	}
	if _, err := client.GetProjectOverview(context.Background(), serverapi.ProjectGetOverviewRequest{ProjectID: "project-1"}); err == nil {
		t.Fatal("expected GetProjectOverview to fail without service")
	}
	if _, err := client.ListSessionsByProject(context.Background(), serverapi.SessionListByProjectRequest{ProjectID: "project-1"}); err == nil {
		t.Fatal("expected ListSessionsByProject to fail without service")
	}
}

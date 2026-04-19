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
	resolveResp  serverapi.ProjectResolvePathResponse
	createResp   serverapi.ProjectCreateResponse
	attachResp   serverapi.ProjectAttachWorkspaceResponse
	rebindResp   serverapi.ProjectRebindWorkspaceResponse
	overviewResp serverapi.ProjectGetOverviewResponse
	sessionsResp serverapi.SessionListByProjectResponse
	err          error
}

func (s *stubProjectViewService) ListProjects(context.Context, serverapi.ProjectListRequest) (serverapi.ProjectListResponse, error) {
	return s.listResp, s.err
}

func (s *stubProjectViewService) ResolveProjectPath(context.Context, serverapi.ProjectResolvePathRequest) (serverapi.ProjectResolvePathResponse, error) {
	return s.resolveResp, s.err
}

func (s *stubProjectViewService) CreateProject(context.Context, serverapi.ProjectCreateRequest) (serverapi.ProjectCreateResponse, error) {
	return s.createResp, s.err
}

func (s *stubProjectViewService) AttachWorkspaceToProject(context.Context, serverapi.ProjectAttachWorkspaceRequest) (serverapi.ProjectAttachWorkspaceResponse, error) {
	return s.attachResp, s.err
}

func (s *stubProjectViewService) RebindWorkspace(context.Context, serverapi.ProjectRebindWorkspaceRequest) (serverapi.ProjectRebindWorkspaceResponse, error) {
	return s.rebindResp, s.err
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
		listResp:    serverapi.ProjectListResponse{Projects: []clientui.ProjectSummary{{ProjectID: "project-1"}}},
		resolveResp: serverapi.ProjectResolvePathResponse{CanonicalRoot: "/tmp/workspace-a", Binding: &serverapi.ProjectBinding{ProjectID: "project-1"}},
		createResp:  serverapi.ProjectCreateResponse{Binding: serverapi.ProjectBinding{ProjectID: "project-1"}},
		attachResp:  serverapi.ProjectAttachWorkspaceResponse{Binding: serverapi.ProjectBinding{ProjectID: "project-1"}},
		rebindResp:  serverapi.ProjectRebindWorkspaceResponse{Binding: serverapi.ProjectBinding{ProjectID: "project-1", WorkspaceID: "workspace-1"}},
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
	resolveResp, err := client.ResolveProjectPath(context.Background(), serverapi.ProjectResolvePathRequest{Path: "/tmp/workspace-a"})
	if err != nil {
		t.Fatalf("ResolveProjectPath: %v", err)
	}
	if resolveResp.Binding == nil || resolveResp.Binding.ProjectID != "project-1" {
		t.Fatalf("unexpected resolve response: %+v", resolveResp)
	}
	createResp, err := client.CreateProject(context.Background(), serverapi.ProjectCreateRequest{DisplayName: "workspace-a", WorkspaceRoot: "/tmp/workspace-a"})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if createResp.Binding.ProjectID != "project-1" {
		t.Fatalf("unexpected create response: %+v", createResp)
	}
	attachResp, err := client.AttachWorkspaceToProject(context.Background(), serverapi.ProjectAttachWorkspaceRequest{ProjectID: "project-1", WorkspaceRoot: "/tmp/workspace-b"})
	if err != nil {
		t.Fatalf("AttachWorkspaceToProject: %v", err)
	}
	if attachResp.Binding.ProjectID != "project-1" {
		t.Fatalf("unexpected attach response: %+v", attachResp)
	}
	rebindResp, err := client.RebindWorkspace(context.Background(), serverapi.ProjectRebindWorkspaceRequest{OldWorkspaceRoot: "/tmp/workspace-a", NewWorkspaceRoot: "/tmp/workspace-b"})
	if err != nil {
		t.Fatalf("RebindWorkspace: %v", err)
	}
	if rebindResp.Binding.WorkspaceID != "workspace-1" {
		t.Fatalf("unexpected rebind response: %+v", rebindResp)
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
	if _, err := client.ResolveProjectPath(context.Background(), serverapi.ProjectResolvePathRequest{Path: "/tmp/workspace-a"}); err == nil {
		t.Fatal("expected ResolveProjectPath to fail without service")
	}
	if _, err := client.CreateProject(context.Background(), serverapi.ProjectCreateRequest{DisplayName: "workspace-a", WorkspaceRoot: "/tmp/workspace-a"}); err == nil {
		t.Fatal("expected CreateProject to fail without service")
	}
	if _, err := client.AttachWorkspaceToProject(context.Background(), serverapi.ProjectAttachWorkspaceRequest{ProjectID: "project-1", WorkspaceRoot: "/tmp/workspace-b"}); err == nil {
		t.Fatal("expected AttachWorkspaceToProject to fail without service")
	}
	if _, err := client.RebindWorkspace(context.Background(), serverapi.ProjectRebindWorkspaceRequest{OldWorkspaceRoot: "/tmp/workspace-a", NewWorkspaceRoot: "/tmp/workspace-b"}); err == nil {
		t.Fatal("expected RebindWorkspace to fail without service")
	}
	if _, err := client.GetProjectOverview(context.Background(), serverapi.ProjectGetOverviewRequest{ProjectID: "project-1"}); err == nil {
		t.Fatal("expected GetProjectOverview to fail without service")
	}
	if _, err := client.ListSessionsByProject(context.Background(), serverapi.SessionListByProjectRequest{ProjectID: "project-1"}); err == nil {
		t.Fatal("expected ListSessionsByProject to fail without service")
	}
}

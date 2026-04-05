package projectview

import (
	"context"
	"path/filepath"
	"testing"

	"builder/server/session"
	"builder/shared/clientui"
	"builder/shared/serverapi"
)

func TestServiceListsSingleProjectAndSessions(t *testing.T) {
	root := t.TempDir()
	containerDir := filepath.Join(root, "sessions", "workspace-a")
	first, err := session.Create(containerDir, "workspace-a", "/tmp/workspace-a")
	if err != nil {
		t.Fatalf("create first session: %v", err)
	}
	if err := first.SetName("first"); err != nil {
		t.Fatalf("persist first session meta: %v", err)
	}
	second, err := session.Create(containerDir, "workspace-a", "/tmp/workspace-a")
	if err != nil {
		t.Fatalf("create second session: %v", err)
	}
	if err := second.SetName("second"); err != nil {
		t.Fatalf("persist second session meta: %v", err)
	}

	svc, err := NewService("project-1", "/tmp/workspace-a", containerDir)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	projects, err := svc.ListProjects(context.Background(), serverapi.ProjectListRequest{})
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(projects.Projects) != 1 {
		t.Fatalf("expected one project, got %+v", projects)
	}
	if projects.Projects[0].ProjectID != "project-1" {
		t.Fatalf("unexpected project summary: %+v", projects.Projects[0])
	}
	if projects.Projects[0].Availability != clientui.ProjectAvailabilityMissing {
		t.Fatalf("expected missing workspace availability for test path, got %+v", projects.Projects[0])
	}

	sessions, err := svc.ListSessionsByProject(context.Background(), serverapi.SessionListByProjectRequest{ProjectID: "project-1"})
	if err != nil {
		t.Fatalf("ListSessionsByProject: %v", err)
	}
	if len(sessions.Sessions) != 2 {
		t.Fatalf("expected two sessions, got %+v", sessions)
	}
	if sessions.Sessions[0].SessionID != second.Meta().SessionID {
		t.Fatalf("expected most recent session first, got %+v", sessions.Sessions)
	}

	overview, err := svc.GetProjectOverview(context.Background(), serverapi.ProjectGetOverviewRequest{ProjectID: "project-1"})
	if err != nil {
		t.Fatalf("GetProjectOverview: %v", err)
	}
	if overview.Overview.Project.SessionCount != 2 {
		t.Fatalf("unexpected overview session count: %+v", overview.Overview)
	}
	if len(overview.Overview.Sessions) != 2 {
		t.Fatalf("unexpected overview sessions: %+v", overview.Overview)
	}
}

func TestServiceRejectsUnknownProjectID(t *testing.T) {
	svc, err := NewService("project-1", "/tmp/workspace-a", filepath.Join(t.TempDir(), "sessions", "workspace-a"))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if _, err := svc.GetProjectOverview(context.Background(), serverapi.ProjectGetOverviewRequest{ProjectID: "project-2"}); err == nil {
		t.Fatal("expected GetProjectOverview to reject unknown project")
	}
	if _, err := svc.ListSessionsByProject(context.Background(), serverapi.SessionListByProjectRequest{ProjectID: "project-2"}); err == nil {
		t.Fatal("expected ListSessionsByProject to reject unknown project")
	}
}

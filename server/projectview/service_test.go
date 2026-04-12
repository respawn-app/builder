package projectview

import (
	"context"
	"path/filepath"
	"testing"

	"builder/server/metadata"
	"builder/server/session"
	"builder/shared/clientui"
	"builder/shared/config"
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

func TestMetadataServiceSupportsWildcardAndScopedProjectListing(t *testing.T) {
	home := t.TempDir()
	workspaceA := t.TempDir()
	workspaceB := t.TempDir()
	t.Setenv("HOME", home)

	cfgA, err := config.Load(workspaceA, config.LoadOptions{})
	if err != nil {
		t.Fatalf("config.Load workspace A: %v", err)
	}
	store, err := metadata.Open(cfgA.PersistenceRoot)
	if err != nil {
		t.Fatalf("metadata.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	bindingA, err := store.RegisterWorkspaceBinding(context.Background(), cfgA.WorkspaceRoot)
	if err != nil {
		t.Fatalf("RegisterWorkspaceBinding A: %v", err)
	}

	cfgB, err := config.Load(workspaceB, config.LoadOptions{})
	if err != nil {
		t.Fatalf("config.Load workspace B: %v", err)
	}
	bindingB, err := store.RegisterWorkspaceBinding(context.Background(), cfgB.WorkspaceRoot)
	if err != nil {
		t.Fatalf("RegisterWorkspaceBinding B: %v", err)
	}

	wildcard, err := NewMetadataService(store, "", "")
	if err != nil {
		t.Fatalf("NewMetadataService wildcard: %v", err)
	}
	projects, err := wildcard.ListProjects(context.Background(), serverapi.ProjectListRequest{})
	if err != nil {
		t.Fatalf("ListProjects wildcard: %v", err)
	}
	if len(projects.Projects) != 2 {
		t.Fatalf("expected wildcard metadata service to list both projects, got %+v", projects.Projects)
	}

	scoped, err := NewMetadataService(store, bindingA.ProjectID, "")
	if err != nil {
		t.Fatalf("NewMetadataService scoped: %v", err)
	}
	projects, err = scoped.ListProjects(context.Background(), serverapi.ProjectListRequest{})
	if err != nil {
		t.Fatalf("ListProjects scoped: %v", err)
	}
	if len(projects.Projects) != 1 || projects.Projects[0].ProjectID != bindingA.ProjectID {
		t.Fatalf("expected scoped metadata service to list only project A, got %+v", projects.Projects)
	}
	if _, err := scoped.GetProjectOverview(context.Background(), serverapi.ProjectGetOverviewRequest{ProjectID: bindingB.ProjectID}); err == nil {
		t.Fatal("expected scoped metadata service to reject other project overview")
	}
}

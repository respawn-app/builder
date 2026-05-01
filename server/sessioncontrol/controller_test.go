package sessioncontrol

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"builder/server/auth"
	"builder/server/launch"
	"builder/server/lifecycle"
	"builder/server/session"
	"builder/shared/client"
	"builder/shared/clientui"
	"builder/shared/config"
	"builder/shared/serverapi"
)

func TestControllerPlanSessionForwardsPickerTheme(t *testing.T) {
	root := t.TempDir()
	containerDir := filepath.Join(root, "sessions", "workspace-a")
	first, err := session.Create(containerDir, "workspace-a", "/tmp/workspace-a")
	if err != nil {
		t.Fatalf("create first session: %v", err)
	}
	if err := first.SetName("first"); err != nil {
		t.Fatalf("persist first session meta: %v", err)
	}
	store, err := session.Create(containerDir, "workspace-a", "/tmp/workspace-a")
	if err != nil {
		t.Fatalf("create second session: %v", err)
	}
	if err := store.SetName("second"); err != nil {
		t.Fatalf("persist second session meta: %v", err)
	}

	var gotTheme string
	pickedCalled := false
	controller := Controller{
		Config: config.App{
			WorkspaceRoot:   "/tmp/workspace-a",
			PersistenceRoot: root,
			Settings: config.Settings{
				Theme: "dark",
			},
		},
		ContainerDir: containerDir,
		ProjectID:    "project-1",
		ProjectViews: client.NewLoopbackProjectViewClient(&stubControllerProjectViewService{overview: serverapi.ProjectGetOverviewResponse{Overview: clientui.ProjectOverview{
			Project: clientui.ProjectSummary{ProjectID: "project-1", DisplayName: "workspace-a", RootPath: "/tmp/workspace-a"},
			Sessions: []clientui.SessionSummary{
				{SessionID: first.Meta().SessionID, Name: "first", UpdatedAt: first.Meta().UpdatedAt},
				{SessionID: store.Meta().SessionID, Name: "second", UpdatedAt: store.Meta().UpdatedAt},
			},
		}}}),
		PickSession: func(summaries []session.Summary, theme string) (launch.SessionSelection, error) {
			pickedCalled = true
			gotTheme = theme
			if len(summaries) != 2 {
				t.Fatalf("expected two summaries, got %d", len(summaries))
			}
			for _, picked := range summaries {
				if picked.SessionID == store.Meta().SessionID {
					selected := picked
					return launch.SessionSelection{Session: &selected}, nil
				}
			}
			t.Fatalf("expected picker summaries to include %q", store.Meta().SessionID)
			return launch.SessionSelection{}, nil
		},
	}

	plan, err := controller.PlanSession(context.Background(), launch.SessionRequest{Mode: launch.ModeInteractive})
	if err != nil {
		t.Fatalf("plan session: %v", err)
	}
	if !pickedCalled {
		t.Fatal("expected picker to be called")
	}
	if gotTheme != "dark" {
		t.Fatalf("theme = %q, want dark", gotTheme)
	}
	if plan.Store.Meta().SessionID != store.Meta().SessionID {
		t.Fatalf("planned session = %q, want %q", plan.Store.Meta().SessionID, store.Meta().SessionID)
	}
}

type stubControllerProjectViewService struct {
	overview serverapi.ProjectGetOverviewResponse
}

func (s *stubControllerProjectViewService) ListProjects(_ context.Context, _ serverapi.ProjectListRequest) (serverapi.ProjectListResponse, error) {
	return serverapi.ProjectListResponse{}, nil
}

func (s *stubControllerProjectViewService) ResolveProjectPath(_ context.Context, _ serverapi.ProjectResolvePathRequest) (serverapi.ProjectResolvePathResponse, error) {
	return serverapi.ProjectResolvePathResponse{}, errors.New("ResolveProjectPath should not be called in controller tests")
}

func (s *stubControllerProjectViewService) CreateProject(_ context.Context, _ serverapi.ProjectCreateRequest) (serverapi.ProjectCreateResponse, error) {
	return serverapi.ProjectCreateResponse{}, errors.New("CreateProject should not be called in controller tests")
}

func (s *stubControllerProjectViewService) AttachWorkspaceToProject(_ context.Context, _ serverapi.ProjectAttachWorkspaceRequest) (serverapi.ProjectAttachWorkspaceResponse, error) {
	return serverapi.ProjectAttachWorkspaceResponse{}, errors.New("AttachWorkspaceToProject should not be called in controller tests")
}

func (s *stubControllerProjectViewService) RebindWorkspace(_ context.Context, _ serverapi.ProjectRebindWorkspaceRequest) (serverapi.ProjectRebindWorkspaceResponse, error) {
	return serverapi.ProjectRebindWorkspaceResponse{}, errors.New("RebindWorkspace should not be called in controller tests")
}

func (s *stubControllerProjectViewService) GetProjectOverview(_ context.Context, _ serverapi.ProjectGetOverviewRequest) (serverapi.ProjectGetOverviewResponse, error) {
	return s.overview, nil
}

func (s *stubControllerProjectViewService) ListSessionsByProject(_ context.Context, _ serverapi.SessionListByProjectRequest) (serverapi.SessionListByProjectResponse, error) {
	return serverapi.SessionListByProjectResponse{}, errors.New("ListSessionsByProject should not be called when project overview is available")
}

func TestControllerResolveTransitionLogoutReauthsThroughCallback(t *testing.T) {
	mgr := auth.NewManager(auth.NewMemoryStore(auth.State{
		Scope: auth.ScopeGlobal,
		Method: auth.Method{
			Type:   auth.MethodAPIKey,
			APIKey: &auth.APIKeyMethod{Key: "sk-before"},
		},
	}), nil, time.Now)
	store, err := session.Create(t.TempDir(), "workspace-x", "/tmp/work")
	if err != nil {
		t.Fatalf("create session store: %v", err)
	}
	reauthCalls := 0
	controller := Controller{
		AuthManager: mgr,
		Reauth: func(ctx context.Context) error {
			reauthCalls++
			_, err := mgr.SwitchMethod(ctx, auth.Method{
				Type:   auth.MethodAPIKey,
				APIKey: &auth.APIKeyMethod{Key: "sk-after"},
			}, true)
			return err
		},
	}

	resolved, err := controller.ResolveTransition(context.Background(), store, lifecycle.Transition{Action: lifecycle.ActionLogout})
	if err != nil {
		t.Fatalf("resolve transition: %v", err)
	}
	if reauthCalls != 1 {
		t.Fatalf("reauth calls = %d, want 1", reauthCalls)
	}
	if !resolved.ShouldContinue || !resolved.RequiresReauth {
		t.Fatalf("unexpected resolved transition: %+v", resolved)
	}
	state, err := mgr.Load(context.Background())
	if err != nil {
		t.Fatalf("load auth state: %v", err)
	}
	if state.Method.APIKey == nil || state.Method.APIKey.Key != "sk-after" {
		t.Fatalf("expected reauthed api key, got %+v", state.Method.APIKey)
	}
}

func TestControllerResolveTransitionLogoutRequiresReauthHandler(t *testing.T) {
	mgr := auth.NewManager(auth.NewMemoryStore(auth.State{
		Scope: auth.ScopeGlobal,
		Method: auth.Method{
			Type:   auth.MethodAPIKey,
			APIKey: &auth.APIKeyMethod{Key: "sk-before"},
		},
	}), nil, time.Now)

	_, err := Controller{AuthManager: mgr}.ResolveTransition(context.Background(), nil, lifecycle.Transition{Action: lifecycle.ActionLogout})
	if err == nil || err.Error() != "reauth handler is required" {
		t.Fatalf("expected missing reauth handler error, got %v", err)
	}
}

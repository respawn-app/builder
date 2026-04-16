package sessionlaunch

import (
	"context"
	"testing"

	"builder/server/launch"
	"builder/server/registry"
	"builder/shared/config"
	"builder/shared/serverapi"
)

func TestServicePlanSessionRegistersStoreAndReturnsPlan(t *testing.T) {
	persistenceRoot := t.TempDir()
	containerDir := t.TempDir()
	stores := registry.NewSessionStoreRegistry()
	service := NewService(launch.Planner{
		Config: config.App{
			WorkspaceRoot:   "/tmp/workspace-a",
			PersistenceRoot: persistenceRoot,
			Settings:        config.Settings{Model: "gpt-5", OpenAIBaseURL: "http://config.local/v1"},
		},
		ContainerDir: containerDir,
	}, stores)

	resp, err := service.PlanSession(context.Background(), serverapi.SessionPlanRequest{
		ClientRequestID: "req-1",
		Mode:            serverapi.SessionLaunchModeInteractive,
		ForceNewSession: true,
		ParentSessionID: "parent-1",
	})
	if err != nil {
		t.Fatalf("PlanSession: %v", err)
	}
	if resp.Plan.SessionID == "" {
		t.Fatal("expected session id")
	}
	if resp.Plan.WorkspaceRoot != "/tmp/workspace-a" {
		t.Fatalf("workspace root = %q, want /tmp/workspace-a", resp.Plan.WorkspaceRoot)
	}
	if resp.Plan.ActiveSettings.OpenAIBaseURL != "http://config.local/v1" {
		t.Fatalf("active OpenAI base URL = %q, want http://config.local/v1", resp.Plan.ActiveSettings.OpenAIBaseURL)
	}
	store, err := stores.ResolveStore(context.Background(), resp.Plan.SessionID)
	if err != nil {
		t.Fatalf("ResolveStore: %v", err)
	}
	if store == nil {
		t.Fatal("expected planned session in registry")
	}
	if store.Meta().ParentSessionID != "parent-1" {
		t.Fatalf("parent session id = %q, want parent-1", store.Meta().ParentSessionID)
	}
}

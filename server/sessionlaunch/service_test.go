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

func TestDeduplicatingServiceReturnsSameSessionForDuplicateNewSession(t *testing.T) {
	dedupeRegistry.entries = map[string]*dedupeEntry{}
	stores := registry.NewSessionStoreRegistry()
	persistenceRoot := t.TempDir()
	containerDir := t.TempDir()
	inner := NewService(launch.Planner{
		Config:       config.App{WorkspaceRoot: "/tmp/workspace-a", PersistenceRoot: persistenceRoot},
		ContainerDir: containerDir,
	}, stores)
	service := NewDeduplicatingService(ScopeID(config.App{WorkspaceRoot: "/tmp/workspace-a", PersistenceRoot: persistenceRoot}, containerDir), inner)
	req := serverapi.SessionPlanRequest{
		ClientRequestID: "dup-1",
		Mode:            serverapi.SessionLaunchModeInteractive,
		ForceNewSession: true,
	}

	first, err := service.PlanSession(context.Background(), req)
	if err != nil {
		t.Fatalf("first PlanSession: %v", err)
	}
	second, err := service.PlanSession(context.Background(), req)
	if err != nil {
		t.Fatalf("second PlanSession: %v", err)
	}
	if first.Plan.SessionID != second.Plan.SessionID {
		t.Fatalf("duplicate session ids differ: first=%q second=%q", first.Plan.SessionID, second.Plan.SessionID)
	}
}

func TestDeduplicatingServiceRejectsReusedRequestIDWithDifferentPayload(t *testing.T) {
	dedupeRegistry.entries = map[string]*dedupeEntry{}
	stores := registry.NewSessionStoreRegistry()
	persistenceRoot := t.TempDir()
	containerDir := t.TempDir()
	inner := NewService(launch.Planner{
		Config:       config.App{WorkspaceRoot: "/tmp/workspace-a", PersistenceRoot: persistenceRoot},
		ContainerDir: containerDir,
	}, stores)
	service := NewDeduplicatingService(ScopeID(config.App{WorkspaceRoot: "/tmp/workspace-a", PersistenceRoot: persistenceRoot}, containerDir), inner)

	_, err := service.PlanSession(context.Background(), serverapi.SessionPlanRequest{
		ClientRequestID: "dup-2",
		Mode:            serverapi.SessionLaunchModeInteractive,
		ForceNewSession: true,
		ParentSessionID: "parent-1",
	})
	if err != nil {
		t.Fatalf("first PlanSession: %v", err)
	}

	_, err = service.PlanSession(context.Background(), serverapi.SessionPlanRequest{
		ClientRequestID: "dup-2",
		Mode:            serverapi.SessionLaunchModeInteractive,
		ForceNewSession: true,
		ParentSessionID: "parent-2",
	})
	if err == nil || err.Error() != "client_request_id \"dup-2\" reused with different payload" {
		t.Fatalf("expected reused request id error, got %v", err)
	}
}

package launch

import (
	"path/filepath"
	"testing"

	"builder/server/session"
)

func TestResolveBootstrapPlanUsesSessionWorkspaceAndPersistedBaseURL(t *testing.T) {
	persistenceRoot := t.TempDir()
	containerDir := filepath.Join(persistenceRoot, "sessions", "workspace-a")
	store, err := session.Create(containerDir, "workspace-a", "/tmp/original-workspace")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := store.SetContinuationContext(session.ContinuationContext{OpenAIBaseURL: "http://persisted.local/v1"}); err != nil {
		t.Fatalf("set continuation context: %v", err)
	}

	plan, err := ResolveBootstrapPlan(persistenceRoot, BootstrapRequest{
		WorkspaceRoot: "/tmp/current-dir",
		SessionID:     store.Meta().SessionID,
	})
	if err != nil {
		t.Fatalf("resolve bootstrap plan: %v", err)
	}
	if plan.WorkspaceRoot != "/tmp/original-workspace" {
		t.Fatalf("workspace root = %q, want /tmp/original-workspace", plan.WorkspaceRoot)
	}
	if !plan.UseOpenAIBaseURL {
		t.Fatal("expected persisted OpenAI base URL to be reused")
	}
	if plan.OpenAIBaseURL != "http://persisted.local/v1" {
		t.Fatalf("OpenAI base URL = %q, want http://persisted.local/v1", plan.OpenAIBaseURL)
	}
}

func TestResolveBootstrapPlanRespectsExplicitOverrides(t *testing.T) {
	persistenceRoot := t.TempDir()
	containerDir := filepath.Join(persistenceRoot, "sessions", "workspace-a")
	store, err := session.Create(containerDir, "workspace-a", "/tmp/original-workspace")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := store.SetContinuationContext(session.ContinuationContext{OpenAIBaseURL: "http://persisted.local/v1"}); err != nil {
		t.Fatalf("set continuation context: %v", err)
	}

	plan, err := ResolveBootstrapPlan(persistenceRoot, BootstrapRequest{
		WorkspaceRoot:         "/tmp/override-workspace",
		WorkspaceRootExplicit: true,
		SessionID:             store.Meta().SessionID,
		OpenAIBaseURL:         "http://override.local/v1",
		OpenAIBaseURLExplicit: true,
	})
	if err != nil {
		t.Fatalf("resolve bootstrap plan: %v", err)
	}
	if plan.WorkspaceRoot != "/tmp/override-workspace" {
		t.Fatalf("workspace root = %q, want /tmp/override-workspace", plan.WorkspaceRoot)
	}
	if !plan.UseOpenAIBaseURL {
		t.Fatal("expected explicit OpenAI base URL override to be applied")
	}
	if plan.OpenAIBaseURL != "http://override.local/v1" {
		t.Fatalf("OpenAI base URL = %q, want http://override.local/v1", plan.OpenAIBaseURL)
	}
}

func TestResolveBootstrapPlanStillUsesGlobalSessionLookupByID(t *testing.T) {
	persistenceRoot := t.TempDir()
	containerB := filepath.Join(persistenceRoot, "sessions", "workspace-b")
	store, err := session.Create(containerB, "workspace-b", "/tmp/workspace-b")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := store.SetContinuationContext(session.ContinuationContext{OpenAIBaseURL: "http://workspace-b.local/v1"}); err != nil {
		t.Fatalf("set continuation context: %v", err)
	}

	plan, err := ResolveBootstrapPlan(persistenceRoot, BootstrapRequest{SessionID: store.Meta().SessionID})
	if err != nil {
		t.Fatalf("resolve bootstrap plan: %v", err)
	}
	if plan.WorkspaceRoot != "/tmp/workspace-b" {
		t.Fatalf("workspace root = %q, want /tmp/workspace-b", plan.WorkspaceRoot)
	}
	if plan.OpenAIBaseURL != "http://workspace-b.local/v1" || !plan.UseOpenAIBaseURL {
		t.Fatalf("bootstrap plan = %+v, want workspace-b continuation", plan)
	}
}

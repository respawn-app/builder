package app

import (
	"path/filepath"
	"testing"

	"builder/internal/session"
)

func TestResolveContinuationLoadParamsUsesSessionWorkspaceAndPersistedBaseURL(t *testing.T) {
	persistenceRoot := t.TempDir()
	containerDir := filepath.Join(persistenceRoot, "workspace-a")
	store, err := session.Create(containerDir, "workspace-a", "/tmp/original-workspace")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := store.SetContinuationContext(session.ContinuationContext{OpenAIBaseURL: "http://persisted.local/v1"}); err != nil {
		t.Fatalf("set continuation context: %v", err)
	}

	workspaceRoot, openAIBaseURL, useOpenAIBaseURL, err := resolveContinuationLoadParams(persistenceRoot, Options{
		WorkspaceRoot: "/tmp/current-dir",
		SessionID:     store.Meta().SessionID,
	})
	if err != nil {
		t.Fatalf("resolve continuation load params: %v", err)
	}
	if workspaceRoot != "/tmp/original-workspace" {
		t.Fatalf("expected session workspace root, got %q", workspaceRoot)
	}
	if !useOpenAIBaseURL {
		t.Fatal("expected persisted OpenAI base URL to be reused")
	}
	if openAIBaseURL != "http://persisted.local/v1" {
		t.Fatalf("expected persisted OpenAI base URL, got %q", openAIBaseURL)
	}
}

func TestResolveContinuationLoadParamsRespectsExplicitOverrides(t *testing.T) {
	persistenceRoot := t.TempDir()
	containerDir := filepath.Join(persistenceRoot, "workspace-a")
	store, err := session.Create(containerDir, "workspace-a", "/tmp/original-workspace")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := store.SetContinuationContext(session.ContinuationContext{OpenAIBaseURL: "http://persisted.local/v1"}); err != nil {
		t.Fatalf("set continuation context: %v", err)
	}

	workspaceRoot, openAIBaseURL, useOpenAIBaseURL, err := resolveContinuationLoadParams(persistenceRoot, Options{
		WorkspaceRoot:         "/tmp/override-workspace",
		WorkspaceRootExplicit: true,
		SessionID:             store.Meta().SessionID,
		OpenAIBaseURL:         "http://override.local/v1",
		OpenAIBaseURLExplicit: true,
	})
	if err != nil {
		t.Fatalf("resolve continuation load params: %v", err)
	}
	if workspaceRoot != "/tmp/override-workspace" {
		t.Fatalf("expected explicit workspace override, got %q", workspaceRoot)
	}
	if !useOpenAIBaseURL {
		t.Fatal("expected explicit OpenAI base URL override to be applied")
	}
	if openAIBaseURL != "http://override.local/v1" {
		t.Fatalf("expected explicit OpenAI base URL override, got %q", openAIBaseURL)
	}
}

package core

import (
	"testing"
	"time"

	"builder/server/auth"
	serverbootstrap "builder/server/bootstrap"
	"builder/server/registry"
	askquestion "builder/server/tools/askquestion"
	"builder/server/workflow"
)

func TestNewWithContextComposesRequiredBundles(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)

	resolved, err := serverbootstrap.ResolveConfig(serverbootstrap.Request{WorkspaceRoot: workspace})
	if err != nil {
		t.Fatalf("ResolveConfig: %v", err)
	}
	authSupport, err := serverbootstrap.BuildAuthSupport(auth.NewMemoryStore(auth.EmptyState()), nil, nil)
	if err != nil {
		t.Fatalf("BuildAuthSupport: %v", err)
	}
	runtimeSupport, err := serverbootstrap.BuildRuntimeSupport(resolved.Config)
	if err != nil {
		t.Fatalf("BuildRuntimeSupport: %v", err)
	}

	appCore, err := NewWithContext(t.Context(), resolved.Config, authSupport, runtimeSupport)
	if err != nil {
		t.Fatalf("NewWithContext: %v", err)
	}
	t.Cleanup(func() { _ = appCore.Close() })

	if appCore.bundles == nil {
		t.Fatal("expected bundles")
	}
	if appCore.bundles.Auth == nil || appCore.bundles.Auth.authBootstrap == nil || appCore.bundles.Auth.authStatus == nil {
		t.Fatal("expected auth bundle clients")
	}
	if appCore.bundles.Persistence == nil || appCore.bundles.Persistence.rootLock == nil || appCore.bundles.Persistence.metadataStore == nil || appCore.bundles.Persistence.sessionStores == nil {
		t.Fatal("expected persistence bundle resources")
	}
	if appCore.bundles.Processes == nil || appCore.bundles.Processes.processControls == nil || appCore.bundles.Processes.processOutput == nil || appCore.bundles.Processes.processViews == nil {
		t.Fatal("expected process bundle clients")
	}
	if appCore.bundles.Projects == nil || appCore.bundles.Projects.projectViews == nil {
		t.Fatal("expected project bundle client")
	}
	if appCore.bundles.Prompts == nil || appCore.bundles.Prompts.askViews == nil || appCore.bundles.Prompts.approvalViews == nil || appCore.bundles.Prompts.promptControl == nil || appCore.bundles.Prompts.promptActivity == nil {
		t.Fatal("expected prompt bundle clients")
	}
	if appCore.bundles.Runtime == nil || appCore.bundles.Runtime.background == nil || appCore.bundles.Runtime.backgroundRouter == nil || appCore.bundles.Runtime.runtimeRegistry == nil || appCore.bundles.Runtime.runtimeControls == nil || appCore.bundles.Runtime.sessionRuntime == nil || appCore.bundles.Runtime.sessionActivity == nil {
		t.Fatal("expected runtime bundle services")
	}
	if appCore.bundles.Sessions == nil || appCore.bundles.Sessions.sessionLaunch == nil || appCore.bundles.Sessions.sessionViews == nil || appCore.bundles.Sessions.sessionLifecycle == nil || appCore.bundles.Sessions.runPrompt == nil {
		t.Fatal("expected session bundle clients")
	}
	if appCore.bundles.Updates == nil || appCore.bundles.Updates.updateStatus == nil {
		t.Fatal("expected update status bundle")
	}
	if appCore.bundles.Worktrees == nil || appCore.bundles.Worktrees.worktrees == nil {
		t.Fatal("expected worktree bundle client")
	}
	if appCore.bundles.Workflows == nil || appCore.bundles.Workflows.workflows == nil {
		t.Fatal("expected workflow bundle client")
	}
	if appCore.bundles.Workflows.scheduler == nil || !appCore.bundles.Workflows.scheduler.Started() {
		t.Fatal("expected started workflow scheduler")
	}
	scheduler := appCore.bundles.Workflows.scheduler
	if err := appCore.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !scheduler.Stopped() {
		t.Fatal("expected workflow scheduler to stop during core close")
	}
}

func TestRuntimePendingAskResolverUsesPendingPromptSource(t *testing.T) {
	resolver := runtimePendingAskResolver{prompts: fakePendingPromptSource{items: map[string][]registry.PendingPromptSnapshot{
		"session-1": {
			{Request: askquestion.Request{ID: "ask-1", Question: "Need input?"}, CreatedAt: time.Unix(1, 0)},
			{Request: askquestion.Request{ID: "approval-1", Question: "Approve?", Approval: true}, CreatedAt: time.Unix(2, 0)},
		},
	}}}

	ok, err := resolver.CanRehydrate(t.Context(), "session-1", workflow.RunID("run-1"), "ask-1")
	if err != nil {
		t.Fatalf("CanRehydrate ask: %v", err)
	}
	if !ok {
		t.Fatal("expected pending ordinary ask to rehydrate")
	}
	ok, err = resolver.CanRehydrate(t.Context(), "session-1", workflow.RunID("run-1"), "approval-1")
	if err != nil {
		t.Fatalf("CanRehydrate approval: %v", err)
	}
	if ok {
		t.Fatal("approval prompt must not satisfy workflow ask rehydration")
	}
	ok, err = resolver.CanRehydrate(t.Context(), "session-1", workflow.RunID("run-1"), "missing")
	if err != nil {
		t.Fatalf("CanRehydrate missing: %v", err)
	}
	if ok {
		t.Fatal("missing ask should not rehydrate")
	}
}

type fakePendingPromptSource struct {
	items map[string][]registry.PendingPromptSnapshot
}

func (f fakePendingPromptSource) ListPendingPrompts(sessionID string) []registry.PendingPromptSnapshot {
	return append([]registry.PendingPromptSnapshot(nil), f.items[sessionID]...)
}

package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"builder/internal/config"
	"builder/internal/session"
)

func TestSessionLaunchPlannerHeadlessCreatesNewSessionAndAppliesContinuationContext(t *testing.T) {
	root := t.TempDir()
	containerDir := filepath.Join(root, "sessions", "workspace-a")
	planner := newSessionLaunchPlanner(&appBootstrap{
		cfg: config.App{
			WorkspaceRoot:   "/tmp/workspace-a",
			PersistenceRoot: root,
			Settings: config.Settings{
				OpenAIBaseURL: "http://headless.local/v1",
			},
		},
		containerDir: containerDir,
	})

	plan, err := planner.PlanSession(sessionLaunchRequest{Mode: launchModeHeadless})
	if err != nil {
		t.Fatalf("plan session: %v", err)
	}
	meta := plan.Store.Meta()
	if meta.SessionID == "" {
		t.Fatal("expected session id")
	}
	if !strings.HasSuffix(meta.Name, " "+subagentSessionSuffix) {
		t.Fatalf("expected subagent session name, got %q", meta.Name)
	}
	if meta.Continuation == nil || meta.Continuation.OpenAIBaseURL != "http://headless.local/v1" {
		t.Fatalf("expected continuation base url applied, got %+v", meta.Continuation)
	}
	if plan.SessionName != meta.Name {
		t.Fatalf("expected plan session name %q, got %q", meta.Name, plan.SessionName)
	}
	if plan.WorkspaceRoot != "/tmp/workspace-a" {
		t.Fatalf("expected workspace root passthrough, got %q", plan.WorkspaceRoot)
	}
}

func TestSessionLaunchPlannerInteractiveUsesPickerSelection(t *testing.T) {
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
	planner := &launchPlanner{
		boot: &appBootstrap{
			cfg: config.App{
				WorkspaceRoot:   "/tmp/workspace-a",
				PersistenceRoot: root,
				Settings:        config.Settings{Theme: "dark", TUIAlternateScreen: config.TUIAlternateScreenAuto},
			},
			containerDir: containerDir,
		},
		pickSession: func(summaries []session.Summary, theme string, alternateScreenPolicy config.TUIAlternateScreenPolicy) (sessionPickerResult, error) {
			if len(summaries) != 2 {
				t.Fatalf("expected two summaries, got %d", len(summaries))
			}
			for _, summary := range summaries {
				if summary.SessionID == second.Meta().SessionID {
					picked := summary
					return sessionPickerResult{Session: &picked}, nil
				}
			}
			t.Fatalf("expected picker summaries to include %q", second.Meta().SessionID)
			return sessionPickerResult{}, nil
		},
	}

	plan, err := planner.PlanSession(sessionLaunchRequest{Mode: launchModeInteractive})
	if err != nil {
		t.Fatalf("plan session: %v", err)
	}
	if plan.Store.Meta().SessionID != second.Meta().SessionID {
		t.Fatalf("expected selected session %q, got %q", second.Meta().SessionID, plan.Store.Meta().SessionID)
	}
	if plan.Store.Meta().SessionID == first.Meta().SessionID {
		t.Fatalf("did not expect first session %q", first.Meta().SessionID)
	}
}

func TestSessionLaunchPlannerInteractiveUsesLegacyWorkspaceContainerMapping(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)

	cfg, err := config.Load(workspace, config.LoadOptions{})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	legacyContainer := "workspace-a-legacy"
	indexPath := filepath.Join(cfg.PersistenceRoot, "workspaces.json")
	if err := os.MkdirAll(filepath.Dir(indexPath), 0o755); err != nil {
		t.Fatalf("mkdir workspace index dir: %v", err)
	}
	indexData, err := json.Marshal(map[string]any{"entries": map[string]string{cfg.WorkspaceRoot: legacyContainer}})
	if err != nil {
		t.Fatalf("marshal workspace index: %v", err)
	}
	if err := os.WriteFile(indexPath, indexData, 0o644); err != nil {
		t.Fatalf("write workspace index: %v", err)
	}

	legacyContainerDir, err := filepath.Abs(filepath.Join(cfg.PersistenceRoot, "sessions", legacyContainer))
	if err != nil {
		t.Fatalf("abs legacy container dir: %v", err)
	}
	legacySession, err := session.Create(legacyContainerDir, legacyContainer, cfg.WorkspaceRoot)
	if err != nil {
		t.Fatalf("create legacy session: %v", err)
	}
	if err := legacySession.SetName("legacy session"); err != nil {
		t.Fatalf("persist legacy session meta: %v", err)
	}

	containerName, containerDir, err := config.ResolveWorkspaceContainer(cfg)
	if err != nil {
		t.Fatalf("resolve workspace container: %v", err)
	}
	if containerName != legacyContainer {
		t.Fatalf("expected legacy container %q, got %q", legacyContainer, containerName)
	}

	planner := &launchPlanner{
		boot: &appBootstrap{
			cfg: config.App{
				WorkspaceRoot:   cfg.WorkspaceRoot,
				PersistenceRoot: cfg.PersistenceRoot,
				Settings:        config.Settings{Theme: "dark", TUIAlternateScreen: config.TUIAlternateScreenAuto},
			},
			containerDir: containerDir,
		},
		pickSession: func(summaries []session.Summary, theme string, alternateScreenPolicy config.TUIAlternateScreenPolicy) (sessionPickerResult, error) {
			if len(summaries) != 1 {
				t.Fatalf("expected one legacy summary, got %d", len(summaries))
			}
			if summaries[0].SessionID != legacySession.Meta().SessionID {
				t.Fatalf("expected legacy session %q, got %q", legacySession.Meta().SessionID, summaries[0].SessionID)
			}
			picked := summaries[0]
			return sessionPickerResult{Session: &picked}, nil
		},
	}

	plan, err := planner.PlanSession(sessionLaunchRequest{Mode: launchModeInteractive})
	if err != nil {
		t.Fatalf("plan session: %v", err)
	}
	if plan.Store.Meta().SessionID != legacySession.Meta().SessionID {
		t.Fatalf("expected legacy session %q, got %q", legacySession.Meta().SessionID, plan.Store.Meta().SessionID)
	}
}

func TestSessionLaunchPlannerSelectedSessionIDBypassesPicker(t *testing.T) {
	root := t.TempDir()
	containerDir := filepath.Join(root, "sessions")
	store, err := session.Create(containerDir, "workspace-a", "/tmp/workspace-a")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := store.SetName("selected"); err != nil {
		t.Fatalf("persist selected session meta: %v", err)
	}
	if err := store.SetContinuationContext(session.ContinuationContext{OpenAIBaseURL: "http://session.local/v1"}); err != nil {
		t.Fatalf("persist continuation context: %v", err)
	}
	planner := &launchPlanner{
		boot: &appBootstrap{
			cfg: config.App{
				WorkspaceRoot:   "/tmp/workspace-a",
				PersistenceRoot: root,
				Settings:        config.Settings{Theme: "dark", TUIAlternateScreen: config.TUIAlternateScreenAuto, OpenAIBaseURL: "http://config.local/v1"},
			},
			containerDir: containerDir,
		},
		pickSession: func([]session.Summary, string, config.TUIAlternateScreenPolicy) (sessionPickerResult, error) {
			t.Fatal("did not expect picker for explicit session id")
			return sessionPickerResult{}, nil
		},
	}

	plan, err := planner.PlanSession(sessionLaunchRequest{Mode: launchModeInteractive, SelectedSessionID: store.Meta().SessionID})
	if err != nil {
		t.Fatalf("plan session: %v", err)
	}
	if plan.Store.Meta().SessionID != store.Meta().SessionID {
		t.Fatalf("expected explicit session %q, got %q", store.Meta().SessionID, plan.Store.Meta().SessionID)
	}
	if plan.ActiveSettings.OpenAIBaseURL != "http://session.local/v1" {
		t.Fatalf("expected session continuation base url, got %q", plan.ActiveSettings.OpenAIBaseURL)
	}
	if got := plan.Store.Meta().Continuation; got == nil || got.OpenAIBaseURL != "http://session.local/v1" {
		t.Fatalf("expected continuation base url preserved, got %+v", got)
	}
}

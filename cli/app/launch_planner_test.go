package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"builder/server/metadata"
	"builder/server/session"
	"builder/server/storagemigration"
	"builder/shared/client"
	"builder/shared/clientui"
	"builder/shared/config"
	"builder/shared/serverapi"
	"builder/shared/toolspec"
)

type plannerOwnershipServer struct {
	*testEmbeddedServer
	owns bool
}

type stubSessionViewClient struct {
	getSessionMainView func(context.Context, serverapi.SessionMainViewRequest) (serverapi.SessionMainViewResponse, error)
}

func (s stubSessionViewClient) GetSessionMainView(ctx context.Context, req serverapi.SessionMainViewRequest) (serverapi.SessionMainViewResponse, error) {
	if s.getSessionMainView == nil {
		return serverapi.SessionMainViewResponse{}, errors.New("session view stub is required")
	}
	return s.getSessionMainView(ctx, req)
}

func (stubSessionViewClient) GetSessionTranscriptPage(context.Context, serverapi.SessionTranscriptPageRequest) (serverapi.SessionTranscriptPageResponse, error) {
	return serverapi.SessionTranscriptPageResponse{}, errors.New("unexpected GetSessionTranscriptPage call")
}

func (stubSessionViewClient) GetRun(context.Context, serverapi.RunGetRequest) (serverapi.RunGetResponse, error) {
	return serverapi.RunGetResponse{}, errors.New("unexpected GetRun call")
}

func (s *plannerOwnershipServer) OwnsServer() bool {
	return s != nil && s.owns
}

func TestRuntimeLaunchPlanCurrentControllerLeaseIDFallsBackToRawID(t *testing.T) {
	plan := &runtimeLaunchPlan{ControllerLeaseID: " lease-raw ", controllerLease: newControllerLeaseManager("")}
	if got := plan.CurrentControllerLeaseID(); got != "lease-raw" {
		t.Fatalf("CurrentControllerLeaseID = %q, want lease-raw", got)
	}
}

func TestSessionLaunchPlannerHeadlessCreatesNewSessionAndAppliesContinuationContext(t *testing.T) {
	root := t.TempDir()
	workspaceRoot := "/tmp/workspace-a"
	binding := mustRegisterAppBinding(t, root, workspaceRoot)
	containerDir := config.ProjectSessionsRoot(config.App{PersistenceRoot: root}, binding.ProjectID)
	planner := newSessionLaunchPlanner(&testEmbeddedServer{
		cfg: config.App{
			WorkspaceRoot:   workspaceRoot,
			PersistenceRoot: root,
			Settings: config.Settings{
				OpenAIBaseURL: "http://headless.local/v1",
			},
		},
		containerDir: containerDir,
	})

	plan, err := planner.PlanSession(context.Background(), sessionLaunchRequest{Mode: launchModeHeadless})
	if err != nil {
		t.Fatalf("plan session: %v", err)
	}
	opened := openAuthoritativeAppSession(t, root, plan.SessionID)
	meta := opened.Meta()
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
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	registerAppWorkspace(t, workspace)

	cfg, err := config.Load(workspace, config.LoadOptions{})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	binding := mustRegisterAppBinding(t, cfg.PersistenceRoot, cfg.WorkspaceRoot)
	containerDir := config.ProjectSessionsRoot(cfg, binding.ProjectID)

	first := createAuthoritativeAppSession(t, cfg.PersistenceRoot, cfg.WorkspaceRoot)
	if err := first.SetName("first"); err != nil {
		t.Fatalf("persist first session meta: %v", err)
	}
	second := createAuthoritativeAppSession(t, cfg.PersistenceRoot, cfg.WorkspaceRoot)
	if err := second.SetName("second"); err != nil {
		t.Fatalf("persist second session meta: %v", err)
	}
	planner := &launchPlanner{
		server: &testEmbeddedServer{
			cfg: config.App{
				WorkspaceRoot:   cfg.WorkspaceRoot,
				PersistenceRoot: cfg.PersistenceRoot,
				Settings:        config.Settings{Theme: "dark", TUIAlternateScreen: config.TUIAlternateScreenAuto},
			},
			containerDir: containerDir,
			sessionViewClient: stubSessionViewClient{getSessionMainView: func(context.Context, serverapi.SessionMainViewRequest) (serverapi.SessionMainViewResponse, error) {
				return serverapi.SessionMainViewResponse{MainView: clientui.RuntimeMainView{Session: clientui.RuntimeSessionView{ExecutionTarget: clientui.SessionExecutionTarget{WorkspaceRoot: cfg.WorkspaceRoot}}}}, nil
			}},
		},
		pickSession: func(summaries []clientui.SessionSummary, theme string, alternateScreenPolicy config.TUIAlternateScreenPolicy) (sessionPickerResult, error) {
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

	plan, err := planner.PlanSession(context.Background(), sessionLaunchRequest{Mode: launchModeInteractive})
	if err != nil {
		t.Fatalf("plan session: %v", err)
	}
	if plan.SessionID != second.Meta().SessionID {
		t.Fatalf("expected selected session %q, got %q", second.Meta().SessionID, plan.SessionID)
	}
	if plan.SessionID == first.Meta().SessionID {
		t.Fatalf("did not expect first session %q", first.Meta().SessionID)
	}
	if !plan.SelectedViaPicker {
		t.Fatal("expected picker-selected session to be marked as selected via picker")
	}
	if !plan.HasOtherSessionsKnown {
		t.Fatal("expected other-session availability to be known")
	}
	if !plan.HasOtherSessions {
		t.Fatal("expected selected session to report other sessions available")
	}
	if comparableWorkspaceChangeRoot(plan.SelectedSessionWorkspaceRoot) != comparableWorkspaceChangeRoot(cfg.WorkspaceRoot) {
		t.Fatalf("expected selected session workspace root %q, got %q", comparableWorkspaceChangeRoot(cfg.WorkspaceRoot), comparableWorkspaceChangeRoot(plan.SelectedSessionWorkspaceRoot))
	}
}

func TestSessionLaunchPlannerMarksNoOtherSessionsForDirectSingleSessionResume(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	registerAppWorkspace(t, workspace)

	cfg, err := config.Load(workspace, config.LoadOptions{})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	binding := mustRegisterAppBinding(t, cfg.PersistenceRoot, cfg.WorkspaceRoot)
	containerDir := config.ProjectSessionsRoot(cfg, binding.ProjectID)
	single := createAuthoritativeAppSession(t, cfg.PersistenceRoot, cfg.WorkspaceRoot)
	planner := newSessionLaunchPlanner(&testEmbeddedServer{
		cfg: config.App{
			WorkspaceRoot:   cfg.WorkspaceRoot,
			PersistenceRoot: cfg.PersistenceRoot,
			Settings:        config.Settings{Theme: "dark", TUIAlternateScreen: config.TUIAlternateScreenAuto},
		},
		containerDir: containerDir,
	})

	plan, err := planner.PlanSession(context.Background(), sessionLaunchRequest{Mode: launchModeInteractive, SelectedSessionID: single.Meta().SessionID})
	if err != nil {
		t.Fatalf("plan session: %v", err)
	}
	if !plan.HasOtherSessionsKnown {
		t.Fatal("expected other-session availability to be known")
	}
	if plan.HasOtherSessions {
		t.Fatal("did not expect other sessions for single-session project")
	}
}

func TestSessionLaunchPlannerPickerSelectionMissingMetadataMarksRecoveryInsteadOfFailing(t *testing.T) {
	root := t.TempDir()
	workspaceRoot := "/tmp/workspace-a"
	binding := mustRegisterAppBinding(t, root, workspaceRoot)
	planner := &launchPlanner{
		server: &testEmbeddedServer{
			cfg: config.App{
				WorkspaceRoot:   workspaceRoot,
				PersistenceRoot: root,
				Settings:        config.Settings{Theme: "dark", TUIAlternateScreen: config.TUIAlternateScreenAuto},
			},
			projectID: binding.ProjectID,
			projectViewClient: client.NewLoopbackProjectViewClient(projectBindingFlowStubProjectViewService{
				projectOverviewResp: serverapi.ProjectGetOverviewResponse{Overview: clientui.ProjectOverview{Sessions: []clientui.SessionSummary{{SessionID: "missing-session", UpdatedAt: time.Now().UTC()}}}},
			}),
			sessionViewClient: stubSessionViewClient{getSessionMainView: func(context.Context, serverapi.SessionMainViewRequest) (serverapi.SessionMainViewResponse, error) {
				return serverapi.SessionMainViewResponse{}, errors.New("missing selected session")
			}},
			sessionLaunch: stubSessionLaunchClient{planSession: func(context.Context, serverapi.SessionPlanRequest) (serverapi.SessionPlanResponse, error) {
				return serverapi.SessionPlanResponse{Plan: serverapi.SessionPlan{SessionID: "missing-session", WorkspaceRoot: workspaceRoot, ActiveSettings: config.Settings{Theme: "dark", TUIAlternateScreen: config.TUIAlternateScreenAuto}}}, nil
			}},
		},
		pickSession: func(summaries []clientui.SessionSummary, theme string, alternateScreenPolicy config.TUIAlternateScreenPolicy) (sessionPickerResult, error) {
			picked := summaries[0]
			return sessionPickerResult{Session: &picked}, nil
		},
	}

	plan, err := planner.PlanSession(context.Background(), sessionLaunchRequest{Mode: launchModeInteractive})
	if err != nil {
		t.Fatalf("plan session: %v", err)
	}
	if !plan.SelectedViaPicker {
		t.Fatal("expected picker-selected session to be marked as selected via picker")
	}
	if !plan.SelectedSessionWorkspaceLookupFailed {
		t.Fatal("expected missing selected-session metadata to mark picker recovery")
	}
	if plan.SelectedSessionWorkspaceRoot != "" {
		t.Fatalf("expected empty selected session workspace root after lookup failure, got %q", plan.SelectedSessionWorkspaceRoot)
	}
}

func TestSessionLaunchPlannerInteractiveUsesMigratedLegacySession(t *testing.T) {
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

	if err := storagemigration.EnsureProjectV1(context.Background(), cfg.PersistenceRoot, func() time.Time {
		return time.Date(2026, 4, 11, 12, 0, 0, 0, time.UTC)
	}); err != nil {
		t.Fatalf("EnsureProjectV1: %v", err)
	}
	binding, err := metadata.ResolveBinding(context.Background(), cfg.PersistenceRoot, cfg.WorkspaceRoot)
	if err != nil {
		t.Fatalf("ResolveBinding: %v", err)
	}
	containerDir := config.ProjectSessionsRoot(cfg, binding.ProjectID)

	planner := &launchPlanner{
		server: &testEmbeddedServer{
			cfg: config.App{
				WorkspaceRoot:   cfg.WorkspaceRoot,
				PersistenceRoot: cfg.PersistenceRoot,
				Settings:        config.Settings{Theme: "dark", TUIAlternateScreen: config.TUIAlternateScreenAuto},
			},
			containerDir: containerDir,
		},
		pickSession: func(summaries []clientui.SessionSummary, theme string, alternateScreenPolicy config.TUIAlternateScreenPolicy) (sessionPickerResult, error) {
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

	plan, err := planner.PlanSession(context.Background(), sessionLaunchRequest{Mode: launchModeInteractive})
	if err != nil {
		t.Fatalf("plan session: %v", err)
	}
	if plan.SessionID != legacySession.Meta().SessionID {
		t.Fatalf("expected legacy session %q, got %q", legacySession.Meta().SessionID, plan.SessionID)
	}
}

func TestSessionLaunchPlannerPropagatesServerOwnershipToStatusConfig(t *testing.T) {
	for _, tt := range []struct {
		name string
		owns bool
	}{
		{name: "owned", owns: true},
		{name: "attached", owns: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			containerDir := filepath.Join(root, "sessions", "workspace-a")
			planner := newSessionLaunchPlanner(&plannerOwnershipServer{
				testEmbeddedServer: &testEmbeddedServer{
					cfg: config.App{
						WorkspaceRoot:   "/tmp/workspace-a",
						PersistenceRoot: root,
						Settings:        config.Settings{Theme: "dark", TUIAlternateScreen: config.TUIAlternateScreenAuto},
					},
					containerDir: containerDir,
				},
				owns: tt.owns,
			})

			plan, err := planner.PlanSession(context.Background(), sessionLaunchRequest{Mode: launchModeHeadless})
			if err != nil {
				t.Fatalf("plan session: %v", err)
			}
			if plan.StatusConfig.OwnsServer != tt.owns {
				t.Fatalf("status config owns server = %t, want %t", plan.StatusConfig.OwnsServer, tt.owns)
			}
		})
	}
}

func TestSessionLaunchPlannerSelectedSessionIDBypassesPicker(t *testing.T) {
	root := t.TempDir()
	workspaceRoot := "/tmp/workspace-a"
	binding := mustRegisterAppBinding(t, root, workspaceRoot)
	containerDir := config.ProjectSessionsRoot(config.App{PersistenceRoot: root}, binding.ProjectID)
	store := createAuthoritativeAppSession(t, root, workspaceRoot)
	if err := store.SetName("selected"); err != nil {
		t.Fatalf("persist selected session meta: %v", err)
	}
	if err := store.SetContinuationContext(session.ContinuationContext{OpenAIBaseURL: "http://session.local/v1"}); err != nil {
		t.Fatalf("persist continuation context: %v", err)
	}
	planner := &launchPlanner{
		server: &testEmbeddedServer{
			cfg: config.App{
				WorkspaceRoot:   "/tmp/workspace-a",
				PersistenceRoot: root,
				Settings:        config.Settings{Theme: "dark", TUIAlternateScreen: config.TUIAlternateScreenAuto, OpenAIBaseURL: "http://config.local/v1"},
			},
			containerDir: containerDir,
			sessionViewClient: stubSessionViewClient{getSessionMainView: func(context.Context, serverapi.SessionMainViewRequest) (serverapi.SessionMainViewResponse, error) {
				t.Fatal("did not expect session view lookup for explicit session id")
				return serverapi.SessionMainViewResponse{}, nil
			}},
		},
		pickSession: func([]clientui.SessionSummary, string, config.TUIAlternateScreenPolicy) (sessionPickerResult, error) {
			t.Fatal("did not expect picker for explicit session id")
			return sessionPickerResult{}, nil
		},
	}

	plan, err := planner.PlanSession(context.Background(), sessionLaunchRequest{Mode: launchModeInteractive, SelectedSessionID: store.Meta().SessionID})
	if err != nil {
		t.Fatalf("plan session: %v", err)
	}
	if plan.SessionID != store.Meta().SessionID {
		t.Fatalf("expected explicit session %q, got %q", store.Meta().SessionID, plan.SessionID)
	}
	if plan.SelectedViaPicker {
		t.Fatal("did not expect explicit session selection to be marked as picker-selected")
	}
	if plan.ActiveSettings.OpenAIBaseURL != "http://session.local/v1" {
		t.Fatalf("expected session continuation base url, got %q", plan.ActiveSettings.OpenAIBaseURL)
	}
	reopened := openAuthoritativeAppSession(t, root, plan.SessionID)
	if got := reopened.Meta().Continuation; got == nil || got.OpenAIBaseURL != "http://session.local/v1" {
		t.Fatalf("expected continuation base url preserved, got %+v", got)
	}
}

func TestApplyCLIOverridesToSessionPlanIgnoresNonCLISources(t *testing.T) {
	plan := sessionLaunchPlan{
		ActiveSettings: config.Settings{
			Model:         "server-model",
			ThinkingLevel: "low",
			EnabledTools:  map[toolspec.ID]bool{toolspec.ToolExecCommand: true},
			Timeouts:      config.Timeouts{ModelRequestSeconds: 20},
		},
		EnabledTools:        []toolspec.ID{toolspec.ToolExecCommand},
		ConfiguredModelName: "server-model",
		Source:              config.SourceReport{Sources: map[string]string{"model": "file"}},
		StatusConfig:        uiStatusConfig{},
	}
	cfg := config.App{Settings: config.Settings{
		Model:         "local-model",
		ThinkingLevel: "high",
		EnabledTools:  map[toolspec.ID]bool{toolspec.ToolPatch: true},
		Timeouts:      config.Timeouts{ModelRequestSeconds: 99},
	}, Source: config.SourceReport{Sources: map[string]string{
		"model":                          "env",
		"thinking_level":                 "env",
		"tools.shell":                    "env",
		"tools.patch":                    "env",
		"timeouts.model_request_seconds": "env",
	}}}

	updated := applyCLIOverridesToSessionPlan(plan, cfg)
	if updated.ActiveSettings.Model != "server-model" || updated.ConfiguredModelName != "server-model" {
		t.Fatalf("expected server model preserved, got %+v", updated.ActiveSettings)
	}
	if updated.ActiveSettings.ThinkingLevel != "low" {
		t.Fatalf("expected server thinking level preserved, got %q", updated.ActiveSettings.ThinkingLevel)
	}
	if len(updated.EnabledTools) != 1 || updated.EnabledTools[0] != toolspec.ToolExecCommand {
		t.Fatalf("expected server tools preserved, got %+v", updated.EnabledTools)
	}
	if updated.ActiveSettings.Timeouts.ModelRequestSeconds != 20 {
		t.Fatalf("expected server timeout preserved, got %+v", updated.ActiveSettings.Timeouts)
	}
	if updated.Source.Sources["model"] != "file" {
		t.Fatalf("expected source metadata preserved, got %+v", updated.Source.Sources)
	}
}

func TestApplyCLIOverridesToSessionPlanRespectsLockedModelContract(t *testing.T) {
	plan := sessionLaunchPlan{
		ActiveSettings: config.Settings{
			Model:         "locked-model",
			ThinkingLevel: "medium",
			EnabledTools:  map[toolspec.ID]bool{toolspec.ToolExecCommand: true},
		},
		EnabledTools:        []toolspec.ID{toolspec.ToolExecCommand},
		ConfiguredModelName: "locked-model",
		ModelContractLocked: true,
		StatusConfig:        uiStatusConfig{},
	}
	cfg := config.App{Settings: config.Settings{
		Model:        "cli-model",
		EnabledTools: map[toolspec.ID]bool{toolspec.ToolPatch: true},
	}, Source: config.SourceReport{Sources: map[string]string{
		"model":       "cli",
		"tools.shell": "cli",
		"tools.patch": "cli",
	}}}

	updated := applyCLIOverridesToSessionPlan(plan, cfg)
	if updated.ActiveSettings.Model != "locked-model" || updated.ConfiguredModelName != "locked-model" {
		t.Fatalf("expected locked model preserved, got %+v", updated.ActiveSettings)
	}
	if len(updated.EnabledTools) != 1 || updated.EnabledTools[0] != toolspec.ToolExecCommand {
		t.Fatalf("expected locked tools preserved, got %+v", updated.EnabledTools)
	}
}

func TestApplyCLIOverridesToSessionPlanAppliesCLIToolOverrideWithoutModelOverride(t *testing.T) {
	plan := sessionLaunchPlan{
		ActiveSettings: config.Settings{
			Model:        "gpt-5.4",
			EnabledTools: map[toolspec.ID]bool{toolspec.ToolExecCommand: true},
		},
		EnabledTools:        []toolspec.ID{toolspec.ToolExecCommand},
		ConfiguredModelName: "gpt-5.4",
		Source:              config.SourceReport{Sources: map[string]string{"model": "file", "tools.shell": "default", "tools.patch": "default"}},
		StatusConfig:        uiStatusConfig{},
	}
	cfg := config.App{Settings: config.Settings{
		Model:        "gpt-5.4",
		EnabledTools: map[toolspec.ID]bool{toolspec.ToolExecCommand: false, toolspec.ToolPatch: true},
	}, Source: config.SourceReport{Sources: map[string]string{
		"model":       "file",
		"tools.shell": "cli",
		"tools.patch": "cli",
	}}}

	updated := applyCLIOverridesToSessionPlan(plan, cfg)
	if updated.ActiveSettings.EnabledTools[toolspec.ToolExecCommand] {
		t.Fatalf("expected shell disabled by cli override, got %+v", updated.ActiveSettings.EnabledTools)
	}
	if !updated.ActiveSettings.EnabledTools[toolspec.ToolPatch] {
		t.Fatalf("expected patch enabled by cli override, got %+v", updated.ActiveSettings.EnabledTools)
	}
	if len(updated.EnabledTools) != 1 || updated.EnabledTools[0] != toolspec.ToolPatch {
		t.Fatalf("expected patch-only enabled tools, got %+v", updated.EnabledTools)
	}
	if updated.Source.Sources["tools.shell"] != "cli" || updated.Source.Sources["tools.patch"] != "cli" {
		t.Fatalf("expected cli tool sources preserved, got %+v", updated.Source.Sources)
	}
}

func TestApplyCLIOverridesToSessionPlanRecomputesEnabledToolsForCLIModelOverride(t *testing.T) {
	plan := sessionLaunchPlan{
		ActiveSettings: config.Settings{
			Model:        "gpt-5.4",
			EnabledTools: map[toolspec.ID]bool{toolspec.ToolExecCommand: true},
		},
		EnabledTools:        []toolspec.ID{toolspec.ToolExecCommand},
		ConfiguredModelName: "gpt-5.4",
		Source:              config.SourceReport{Sources: map[string]string{"model": "file", "tools.shell": "default"}},
		StatusConfig:        uiStatusConfig{},
	}
	cfg := config.App{Settings: config.Settings{
		Model:        "gpt-5.3-codex",
		EnabledTools: map[toolspec.ID]bool{toolspec.ToolExecCommand: true},
	}, Source: config.SourceReport{Sources: map[string]string{
		"model":       "cli",
		"tools.shell": "default",
	}}}

	updated := applyCLIOverridesToSessionPlan(plan, cfg)
	if updated.ActiveSettings.Model != "gpt-5.3-codex" {
		t.Fatalf("expected cli model override, got %q", updated.ActiveSettings.Model)
	}
	if len(updated.EnabledTools) != 1 || updated.EnabledTools[0] != toolspec.ToolExecCommand {
		t.Fatalf("expected recomputed tools for overridden model, got %+v", updated.EnabledTools)
	}
}

func TestApplyCLIOverridesToSessionPlanKeepsExplicitCLIToolsWhenModelAlsoOverrides(t *testing.T) {
	plan := sessionLaunchPlan{
		ActiveSettings: config.Settings{
			Model:        "gpt-5.4",
			EnabledTools: map[toolspec.ID]bool{toolspec.ToolExecCommand: true},
		},
		EnabledTools:        []toolspec.ID{toolspec.ToolExecCommand},
		ConfiguredModelName: "gpt-5.4",
		Source:              config.SourceReport{Sources: map[string]string{"model": "file", "tools.shell": "default"}},
		StatusConfig:        uiStatusConfig{},
	}
	cfg := config.App{Settings: config.Settings{
		Model:        "gpt-5.3-codex",
		EnabledTools: map[toolspec.ID]bool{toolspec.ToolExecCommand: true},
	}, Source: config.SourceReport{Sources: map[string]string{
		"model":       "cli",
		"tools.shell": "cli",
	}}}

	updated := applyCLIOverridesToSessionPlan(plan, cfg)
	if updated.ActiveSettings.Model != "gpt-5.3-codex" {
		t.Fatalf("expected cli model override, got %q", updated.ActiveSettings.Model)
	}
	if len(updated.EnabledTools) != 1 || updated.EnabledTools[0] != toolspec.ToolExecCommand {
		t.Fatalf("expected explicit cli tools to suppress model defaults, got %+v", updated.EnabledTools)
	}
}

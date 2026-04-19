package app

import (
	"builder/cli/tui"
	"builder/server/llm"
	"builder/server/metadata"
	"builder/server/runtime"
	"builder/server/session"
	"builder/server/tools"
	shelltool "builder/server/tools/shell"
	"builder/shared/client"
	"builder/shared/clientui"
	"builder/shared/config"
	"builder/shared/serverapi"
	"builder/shared/toolspec"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func TestRunSessionLifecycleMissingWorkspacePrepareRuntimeSuggestsRebind(t *testing.T) {
	missingWorkspace := filepath.Join(t.TempDir(), "workspace-removed")
	containerDir := t.TempDir()
	newWorkspace := t.TempDir()
	t.Chdir(newWorkspace)
	server := &testEmbeddedServer{
		cfg: config.App{
			WorkspaceRoot:   missingWorkspace,
			PersistenceRoot: t.TempDir(),
			Settings:        config.Settings{Theme: "dark", TUIAlternateScreen: config.TUIAlternateScreenAuto},
		},
		containerDir: containerDir,
		projectID:    "project-1",
		projectViewClient: client.NewLoopbackProjectViewClient(projectBindingFlowStubProjectViewService{
			resolveResp: serverapi.ProjectResolvePathResponse{
				CanonicalRoot: missingWorkspace,
				Binding: &serverapi.ProjectBinding{
					ProjectID:       "project-1",
					WorkspaceID:     "workspace-1",
					CanonicalRoot:   missingWorkspace,
					WorkspaceStatus: string(clientui.ProjectAvailabilityAvailable),
				},
			},
		}),
		prepareRuntime: func(_ context.Context, plan sessionLaunchPlan, _ io.Writer, _ string) (*runtimeLaunchPlan, error) {
			_, _, _, err := buildToolRegistry(
				plan.WorkspaceRoot,
				plan.SessionID,
				[]toolspec.ID{toolspec.ToolPatch},
				5*time.Second,
				15*time.Second,
				16_000,
				false,
				true,
				nil,
				nil,
			)
			return nil, err
		},
	}

	err := runSessionLifecycle(context.Background(), server, nil, "")
	if err == nil {
		t.Fatal("expected startup error for missing workspace")
	}
	summaries, listErr := session.ListSessions(containerDir)
	if listErr != nil {
		t.Fatalf("ListSessions: %v", listErr)
	}
	if len(summaries) != 1 {
		t.Fatalf("session count = %d, want 1", len(summaries))
	}
	want := `workspace root ` + strconv.Quote(missingWorkspace) + ` is missing; run ` + "`builder rebind " + strconv.Quote(summaries[0].SessionID) + " " + strconv.Quote(newWorkspace) + "`"
	if got := err.Error(); got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
}

func TestMaybeHandlePickedSessionWorkspaceChangeSkipsPromptWhenWorkspaceUnchanged(t *testing.T) {
	originalPrompt := runWorkspaceChangePromptFlow
	defer func() { runWorkspaceChangePromptFlow = originalPrompt }()
	promptCalls := 0
	runWorkspaceChangePromptFlow = func(string, string, string, config.TUIAlternateScreenPolicy) (workspaceChangePromptResult, error) {
		promptCalls++
		return workspaceChangePromptResult{Rebind: true}, nil
	}

	action, err := maybeHandlePickedSessionWorkspaceChange(context.Background(), &testEmbeddedServer{cfg: config.App{WorkspaceRoot: "/tmp/workspace", Settings: config.Settings{Theme: "dark", TUIAlternateScreen: config.TUIAlternateScreenNever}}}, sessionLaunchPlan{
		SessionID:                    "session-1",
		SelectedViaPicker:            true,
		SelectedSessionWorkspaceRoot: "/tmp/workspace",
	})
	if err != nil {
		t.Fatalf("maybeHandlePickedSessionWorkspaceChange: %v", err)
	}
	if action != sessionWorkspaceChangeProceed {
		t.Fatalf("action = %v, want proceed", action)
	}
	if promptCalls != 0 {
		t.Fatalf("prompt calls = %d, want 0", promptCalls)
	}
}

func TestMaybeHandlePickedSessionWorkspaceChangeCanonicalizesAliases(t *testing.T) {
	realRoot := t.TempDir()
	aliasRoot := filepath.Join(t.TempDir(), "workspace-link")
	if err := os.Symlink(realRoot, aliasRoot); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	originalPrompt := runWorkspaceChangePromptFlow
	defer func() { runWorkspaceChangePromptFlow = originalPrompt }()
	promptCalls := 0
	runWorkspaceChangePromptFlow = func(string, string, string, config.TUIAlternateScreenPolicy) (workspaceChangePromptResult, error) {
		promptCalls++
		return workspaceChangePromptResult{Rebind: true}, nil
	}

	action, err := maybeHandlePickedSessionWorkspaceChange(context.Background(), &testEmbeddedServer{cfg: config.App{WorkspaceRoot: aliasRoot, Settings: config.Settings{Theme: "dark", TUIAlternateScreen: config.TUIAlternateScreenNever}}}, sessionLaunchPlan{
		SessionID:                    "session-1",
		SelectedViaPicker:            true,
		SelectedSessionWorkspaceRoot: realRoot,
	})
	if err != nil {
		t.Fatalf("maybeHandlePickedSessionWorkspaceChange: %v", err)
	}
	if action != sessionWorkspaceChangeProceed {
		t.Fatalf("action = %v, want proceed", action)
	}
	if promptCalls != 0 {
		t.Fatalf("prompt calls = %d, want 0", promptCalls)
	}
}

func TestMaybeHandlePickedSessionWorkspaceChangeLookupFailureReturnsPicker(t *testing.T) {
	originalPrompt := runWorkspaceChangePromptFlow
	defer func() { runWorkspaceChangePromptFlow = originalPrompt }()
	promptCalls := 0
	runWorkspaceChangePromptFlow = func(string, string, string, config.TUIAlternateScreenPolicy) (workspaceChangePromptResult, error) {
		promptCalls++
		return workspaceChangePromptResult{Rebind: true}, nil
	}

	action, err := maybeHandlePickedSessionWorkspaceChange(context.Background(), &testEmbeddedServer{cfg: config.App{WorkspaceRoot: "/tmp/workspace", Settings: config.Settings{Theme: "dark", TUIAlternateScreen: config.TUIAlternateScreenNever}}}, sessionLaunchPlan{
		SessionID:                            "session-1",
		SelectedViaPicker:                    true,
		SelectedSessionWorkspaceLookupFailed: true,
	})
	if err != nil {
		t.Fatalf("maybeHandlePickedSessionWorkspaceChange: %v", err)
	}
	if action != sessionWorkspaceChangePickAgain {
		t.Fatalf("action = %v, want pick again", action)
	}
	if promptCalls != 0 {
		t.Fatalf("prompt calls = %d, want 0", promptCalls)
	}
}

func TestRunSessionLifecyclePickerWorkspaceChangeYesRetargetsSessionAndReplans(t *testing.T) {
	home := t.TempDir()
	currentWorkspace := t.TempDir()
	previousWorkspace := t.TempDir()
	t.Setenv("HOME", home)

	cfg, err := config.Load(currentWorkspace, config.LoadOptions{})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	binding := mustRegisterAppBinding(t, cfg.PersistenceRoot, cfg.WorkspaceRoot)
	store := createAttachedAuthoritativeAppSession(t, cfg.PersistenceRoot, binding.ProjectID, previousWorkspace)
	projectViews := sessionLifecycleProjectViewClient(binding, cfg.WorkspaceRoot, []clientui.SessionSummary{{SessionID: store.Meta().SessionID, UpdatedAt: time.Now().UTC()}})

	originalPicker := runSessionPickerFlow
	originalPrompt := runWorkspaceChangePromptFlow
	defer func() {
		runSessionPickerFlow = originalPicker
		runWorkspaceChangePromptFlow = originalPrompt
	}()

	pickerCalls := 0
	runSessionPickerFlow = func(summaries []clientui.SessionSummary, theme string, policy config.TUIAlternateScreenPolicy) (sessionPickerResult, error) {
		pickerCalls++
		for _, summary := range summaries {
			if summary.SessionID == store.Meta().SessionID {
				picked := summary
				return sessionPickerResult{Session: &picked}, nil
			}
		}
		t.Fatalf("picker summaries missing session %q", store.Meta().SessionID)
		return sessionPickerResult{}, nil
	}
	promptCalls := 0
	runWorkspaceChangePromptFlow = func(selectedRoot string, currentRoot string, theme string, policy config.TUIAlternateScreenPolicy) (workspaceChangePromptResult, error) {
		promptCalls++
		if comparableWorkspaceChangeRoot(selectedRoot) != mustCanonicalPath(t, previousWorkspace) {
			t.Fatalf("selected root = %q, want %q", selectedRoot, mustCanonicalPath(t, previousWorkspace))
		}
		if currentRoot != cfg.WorkspaceRoot {
			t.Fatalf("current root = %q, want %q", currentRoot, cfg.WorkspaceRoot)
		}
		return workspaceChangePromptResult{Rebind: true}, nil
	}

	launchCalls := 0
	stopErr := errors.New("stop after prepare")
	prepareCalls := 0
	server := &testEmbeddedServer{
		cfg: config.App{
			WorkspaceRoot:   cfg.WorkspaceRoot,
			PersistenceRoot: cfg.PersistenceRoot,
			Settings:        config.Settings{Theme: "dark", TUIAlternateScreen: config.TUIAlternateScreenNever},
		},
		projectID:         binding.ProjectID,
		projectViewClient: projectViews,
		sessionViewClient: stubSessionViewClient{getSessionMainView: func(_ context.Context, req serverapi.SessionMainViewRequest) (serverapi.SessionMainViewResponse, error) {
			if req.SessionID != store.Meta().SessionID {
				return serverapi.SessionMainViewResponse{}, errors.New("unexpected session id")
			}
			return serverapi.SessionMainViewResponse{MainView: clientui.RuntimeMainView{Session: clientui.RuntimeSessionView{ExecutionTarget: clientui.SessionExecutionTarget{WorkspaceRoot: previousWorkspace}}}}, nil
		}},
		sessionLaunch: stubSessionLaunchClient{planSession: func(_ context.Context, req serverapi.SessionPlanRequest) (serverapi.SessionPlanResponse, error) {
			launchCalls++
			if req.SelectedSessionID != store.Meta().SessionID {
				t.Fatalf("selected session id = %q, want %q", req.SelectedSessionID, store.Meta().SessionID)
			}
			return serverapi.SessionPlanResponse{Plan: serverapi.SessionPlan{
				SessionID:      store.Meta().SessionID,
				WorkspaceRoot:  cfg.WorkspaceRoot,
				ActiveSettings: config.Settings{Theme: "dark", TUIAlternateScreen: config.TUIAlternateScreenNever},
			}}, nil
		}},
		prepareRuntime: func(_ context.Context, plan sessionLaunchPlan, _ io.Writer, _ string) (*runtimeLaunchPlan, error) {
			prepareCalls++
			if plan.SessionID != store.Meta().SessionID {
				t.Fatalf("prepared session = %q, want %q", plan.SessionID, store.Meta().SessionID)
			}
			if plan.WorkspaceRoot != cfg.WorkspaceRoot {
				t.Fatalf("prepared workspace = %q, want %q", plan.WorkspaceRoot, cfg.WorkspaceRoot)
			}
			if plan.SelectedViaPicker {
				t.Fatal("did not expect replanned explicit session to remain picker-selected")
			}
			return nil, stopErr
		},
	}

	err = runSessionLifecycle(context.Background(), server, nil, "")
	if !errors.Is(err, stopErr) {
		t.Fatalf("runSessionLifecycle error = %v, want %v", err, stopErr)
	}
	if pickerCalls != 1 {
		t.Fatalf("picker calls = %d, want 1", pickerCalls)
	}
	if promptCalls != 1 {
		t.Fatalf("prompt calls = %d, want 1", promptCalls)
	}
	if launchCalls != 2 {
		t.Fatalf("launch calls = %d, want 2", launchCalls)
	}
	if prepareCalls != 1 {
		t.Fatalf("prepare calls = %d, want 1", prepareCalls)
	}
	reopened := openAuthoritativeAppSession(t, cfg.PersistenceRoot, store.Meta().SessionID)
	if comparableWorkspaceChangeRoot(reopened.Meta().WorkspaceRoot) != mustCanonicalPath(t, cfg.WorkspaceRoot) {
		t.Fatalf("session workspace = %q, want %q", reopened.Meta().WorkspaceRoot, mustCanonicalPath(t, cfg.WorkspaceRoot))
	}
}

func TestRunSessionLifecyclePickerWorkspaceChangeNoReturnsToPicker(t *testing.T) {
	home := t.TempDir()
	currentWorkspace := t.TempDir()
	previousWorkspace := t.TempDir()
	t.Setenv("HOME", home)

	cfg, err := config.Load(currentWorkspace, config.LoadOptions{})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	binding := mustRegisterAppBinding(t, cfg.PersistenceRoot, cfg.WorkspaceRoot)
	store := createAttachedAuthoritativeAppSession(t, cfg.PersistenceRoot, binding.ProjectID, previousWorkspace)
	projectViews := sessionLifecycleProjectViewClient(binding, cfg.WorkspaceRoot, []clientui.SessionSummary{{SessionID: store.Meta().SessionID, UpdatedAt: time.Now().UTC()}})

	originalPicker := runSessionPickerFlow
	originalPrompt := runWorkspaceChangePromptFlow
	defer func() {
		runSessionPickerFlow = originalPicker
		runWorkspaceChangePromptFlow = originalPrompt
	}()

	pickerCalls := 0
	runSessionPickerFlow = func(summaries []clientui.SessionSummary, theme string, policy config.TUIAlternateScreenPolicy) (sessionPickerResult, error) {
		pickerCalls++
		if pickerCalls == 1 {
			for _, summary := range summaries {
				if summary.SessionID == store.Meta().SessionID {
					picked := summary
					return sessionPickerResult{Session: &picked}, nil
				}
			}
			t.Fatalf("picker summaries missing session %q", store.Meta().SessionID)
		}
		return sessionPickerResult{Canceled: true}, nil
	}
	promptCalls := 0
	runWorkspaceChangePromptFlow = func(string, string, string, config.TUIAlternateScreenPolicy) (workspaceChangePromptResult, error) {
		promptCalls++
		return workspaceChangePromptResult{}, nil
	}
	launchCalls := 0

	server := &testEmbeddedServer{
		cfg: config.App{
			WorkspaceRoot:   cfg.WorkspaceRoot,
			PersistenceRoot: cfg.PersistenceRoot,
			Settings:        config.Settings{Theme: "dark", TUIAlternateScreen: config.TUIAlternateScreenNever},
		},
		projectID:         binding.ProjectID,
		projectViewClient: projectViews,
		sessionViewClient: stubSessionViewClient{getSessionMainView: func(_ context.Context, req serverapi.SessionMainViewRequest) (serverapi.SessionMainViewResponse, error) {
			if req.SessionID != store.Meta().SessionID {
				return serverapi.SessionMainViewResponse{}, errors.New("unexpected session id")
			}
			return serverapi.SessionMainViewResponse{MainView: clientui.RuntimeMainView{Session: clientui.RuntimeSessionView{ExecutionTarget: clientui.SessionExecutionTarget{WorkspaceRoot: previousWorkspace}}}}, nil
		}},
		sessionLaunch: stubSessionLaunchClient{planSession: func(_ context.Context, req serverapi.SessionPlanRequest) (serverapi.SessionPlanResponse, error) {
			launchCalls++
			if req.SelectedSessionID != store.Meta().SessionID {
				t.Fatalf("selected session id = %q, want %q", req.SelectedSessionID, store.Meta().SessionID)
			}
			return serverapi.SessionPlanResponse{Plan: serverapi.SessionPlan{
				SessionID:      store.Meta().SessionID,
				WorkspaceRoot:  cfg.WorkspaceRoot,
				ActiveSettings: config.Settings{Theme: "dark", TUIAlternateScreen: config.TUIAlternateScreenNever},
			}}, nil
		}},
	}

	err = runSessionLifecycle(context.Background(), server, nil, "")
	if err == nil || err.Error() != "startup canceled by user" {
		t.Fatalf("runSessionLifecycle error = %v, want startup canceled by user", err)
	}
	if pickerCalls != 2 {
		t.Fatalf("picker calls = %d, want 2", pickerCalls)
	}
	if promptCalls != 1 {
		t.Fatalf("prompt calls = %d, want 1", promptCalls)
	}
	if launchCalls != 1 {
		t.Fatalf("launch calls = %d, want 1", launchCalls)
	}
}

func TestRunSessionLifecycleStalePickedSessionReturnsToPickerAndOpensAnother(t *testing.T) {
	home := t.TempDir()
	currentWorkspace := t.TempDir()
	t.Setenv("HOME", home)

	cfg, err := config.Load(currentWorkspace, config.LoadOptions{})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	binding := mustRegisterAppBinding(t, cfg.PersistenceRoot, cfg.WorkspaceRoot)
	validStore := createAuthoritativeAppSession(t, cfg.PersistenceRoot, cfg.WorkspaceRoot)
	staleSessionID := "missing-session"
	projectViews := sessionLifecycleProjectViewClient(binding, cfg.WorkspaceRoot, []clientui.SessionSummary{{SessionID: staleSessionID, UpdatedAt: time.Now().UTC()}, {SessionID: validStore.Meta().SessionID, UpdatedAt: time.Now().UTC().Add(-time.Minute)}})

	originalPicker := runSessionPickerFlow
	originalPrompt := runWorkspaceChangePromptFlow
	defer func() {
		runSessionPickerFlow = originalPicker
		runWorkspaceChangePromptFlow = originalPrompt
	}()

	pickerCalls := 0
	runSessionPickerFlow = func(summaries []clientui.SessionSummary, theme string, policy config.TUIAlternateScreenPolicy) (sessionPickerResult, error) {
		pickerCalls++
		for _, summary := range summaries {
			if pickerCalls == 1 && summary.SessionID == staleSessionID {
				picked := summary
				return sessionPickerResult{Session: &picked}, nil
			}
			if pickerCalls == 2 && summary.SessionID == validStore.Meta().SessionID {
				picked := summary
				return sessionPickerResult{Session: &picked}, nil
			}
		}
		t.Fatalf("unexpected picker call %d with summaries %+v", pickerCalls, summaries)
		return sessionPickerResult{}, nil
	}
	promptCalls := 0
	runWorkspaceChangePromptFlow = func(string, string, string, config.TUIAlternateScreenPolicy) (workspaceChangePromptResult, error) {
		promptCalls++
		return workspaceChangePromptResult{Rebind: true}, nil
	}

	launchCalls := 0
	stopErr := errors.New("stop after prepare recovered")
	prepareCalls := 0
	server := &testEmbeddedServer{
		cfg: config.App{
			WorkspaceRoot:   cfg.WorkspaceRoot,
			PersistenceRoot: cfg.PersistenceRoot,
			Settings:        config.Settings{Theme: "dark", TUIAlternateScreen: config.TUIAlternateScreenNever},
		},
		projectID:         binding.ProjectID,
		projectViewClient: projectViews,
		sessionViewClient: stubSessionViewClient{getSessionMainView: func(_ context.Context, req serverapi.SessionMainViewRequest) (serverapi.SessionMainViewResponse, error) {
			switch req.SessionID {
			case staleSessionID:
				return serverapi.SessionMainViewResponse{}, session.ErrSessionNotFound
			case validStore.Meta().SessionID:
				return serverapi.SessionMainViewResponse{MainView: clientui.RuntimeMainView{Session: clientui.RuntimeSessionView{ExecutionTarget: clientui.SessionExecutionTarget{WorkspaceRoot: cfg.WorkspaceRoot}}}}, nil
			default:
				return serverapi.SessionMainViewResponse{}, errors.New("unexpected session id")
			}
		}},
		sessionLaunch: stubSessionLaunchClient{planSession: func(_ context.Context, req serverapi.SessionPlanRequest) (serverapi.SessionPlanResponse, error) {
			launchCalls++
			return serverapi.SessionPlanResponse{Plan: serverapi.SessionPlan{
				SessionID:      req.SelectedSessionID,
				WorkspaceRoot:  cfg.WorkspaceRoot,
				ActiveSettings: config.Settings{Theme: "dark", TUIAlternateScreen: config.TUIAlternateScreenNever},
			}}, nil
		}},
		prepareRuntime: func(_ context.Context, plan sessionLaunchPlan, _ io.Writer, _ string) (*runtimeLaunchPlan, error) {
			prepareCalls++
			if plan.SessionID != validStore.Meta().SessionID {
				t.Fatalf("prepared session = %q, want %q", plan.SessionID, validStore.Meta().SessionID)
			}
			return nil, stopErr
		},
	}

	err = runSessionLifecycle(context.Background(), server, nil, "")
	if !errors.Is(err, stopErr) {
		t.Fatalf("runSessionLifecycle error = %v, want %v", err, stopErr)
	}
	if pickerCalls != 2 {
		t.Fatalf("picker calls = %d, want 2", pickerCalls)
	}
	if promptCalls != 0 {
		t.Fatalf("prompt calls = %d, want 0", promptCalls)
	}
	if launchCalls != 2 {
		t.Fatalf("launch calls = %d, want 2", launchCalls)
	}
	if prepareCalls != 1 {
		t.Fatalf("prepare calls = %d, want 1", prepareCalls)
	}
}

func TestRunSessionLifecycleExplicitSessionIDBypassesWorkspaceChangePrompt(t *testing.T) {
	home := t.TempDir()
	currentWorkspace := t.TempDir()
	previousWorkspace := t.TempDir()
	t.Setenv("HOME", home)

	cfg, err := config.Load(currentWorkspace, config.LoadOptions{})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	binding := mustRegisterAppBinding(t, cfg.PersistenceRoot, cfg.WorkspaceRoot)
	store := createAttachedAuthoritativeAppSession(t, cfg.PersistenceRoot, binding.ProjectID, previousWorkspace)
	projectViews := sessionLifecycleProjectViewClient(binding, cfg.WorkspaceRoot, nil)

	originalPrompt := runWorkspaceChangePromptFlow
	defer func() { runWorkspaceChangePromptFlow = originalPrompt }()
	promptCalls := 0
	runWorkspaceChangePromptFlow = func(string, string, string, config.TUIAlternateScreenPolicy) (workspaceChangePromptResult, error) {
		promptCalls++
		return workspaceChangePromptResult{Rebind: true}, nil
	}

	launchCalls := 0
	stopErr := errors.New("stop after prepare explicit")
	server := &testEmbeddedServer{
		cfg: config.App{
			WorkspaceRoot:   cfg.WorkspaceRoot,
			PersistenceRoot: cfg.PersistenceRoot,
			Settings:        config.Settings{Theme: "dark", TUIAlternateScreen: config.TUIAlternateScreenNever},
		},
		projectID:         binding.ProjectID,
		projectViewClient: projectViews,
		sessionLaunch: stubSessionLaunchClient{planSession: func(_ context.Context, req serverapi.SessionPlanRequest) (serverapi.SessionPlanResponse, error) {
			launchCalls++
			if req.SelectedSessionID != store.Meta().SessionID {
				t.Fatalf("selected session id = %q, want %q", req.SelectedSessionID, store.Meta().SessionID)
			}
			return serverapi.SessionPlanResponse{Plan: serverapi.SessionPlan{
				SessionID:      store.Meta().SessionID,
				WorkspaceRoot:  cfg.WorkspaceRoot,
				ActiveSettings: config.Settings{Theme: "dark", TUIAlternateScreen: config.TUIAlternateScreenNever},
			}}, nil
		}},
		prepareRuntime: func(_ context.Context, plan sessionLaunchPlan, _ io.Writer, _ string) (*runtimeLaunchPlan, error) {
			if plan.WorkspaceRoot != cfg.WorkspaceRoot {
				t.Fatalf("prepared workspace = %q, want %q", plan.WorkspaceRoot, cfg.WorkspaceRoot)
			}
			if plan.SelectedViaPicker {
				t.Fatal("did not expect explicit session id to be marked picker-selected")
			}
			return nil, stopErr
		},
	}

	err = runSessionLifecycle(context.Background(), server, nil, store.Meta().SessionID)
	if !errors.Is(err, stopErr) {
		t.Fatalf("runSessionLifecycle error = %v, want %v", err, stopErr)
	}
	if promptCalls != 0 {
		t.Fatalf("prompt calls = %d, want 0", promptCalls)
	}
	if launchCalls != 1 {
		t.Fatalf("launch calls = %d, want 1", launchCalls)
	}
}

type stubSessionLaunchClient struct {
	planSession func(context.Context, serverapi.SessionPlanRequest) (serverapi.SessionPlanResponse, error)
}

func (s stubSessionLaunchClient) PlanSession(ctx context.Context, req serverapi.SessionPlanRequest) (serverapi.SessionPlanResponse, error) {
	if s.planSession == nil {
		return serverapi.SessionPlanResponse{}, errors.New("session launch stub is required")
	}
	return s.planSession(ctx, req)
}

func sessionLifecycleProjectViewClient(binding metadata.Binding, workspaceRoot string, sessions []clientui.SessionSummary) client.ProjectViewClient {
	return client.NewLoopbackProjectViewClient(projectBindingFlowStubProjectViewService{
		resolveResp: serverapi.ProjectResolvePathResponse{
			CanonicalRoot: workspaceRoot,
			Binding: &serverapi.ProjectBinding{
				ProjectID:       binding.ProjectID,
				WorkspaceID:     binding.WorkspaceID,
				CanonicalRoot:   workspaceRoot,
				WorkspaceStatus: string(clientui.ProjectAvailabilityAvailable),
			},
		},
		projectOverviewResp: serverapi.ProjectGetOverviewResponse{Overview: clientui.ProjectOverview{Sessions: sessions}},
	})
}

func createAttachedAuthoritativeAppSession(t *testing.T, persistenceRoot string, projectID string, workspaceRoot string) *session.Store {
	t.Helper()
	metadataStore, err := metadata.Open(persistenceRoot)
	if err != nil {
		t.Fatalf("metadata.Open: %v", err)
	}
	if _, err := metadataStore.AttachWorkspaceToProject(context.Background(), projectID, workspaceRoot); err != nil {
		_ = metadataStore.Close()
		t.Fatalf("AttachWorkspaceToProject: %v", err)
	}
	store, err := session.Create(
		config.ProjectSessionsRoot(config.App{PersistenceRoot: persistenceRoot}, projectID),
		filepath.Base(filepath.Clean(workspaceRoot)),
		workspaceRoot,
		metadataStore.AuthoritativeSessionStoreOptions()...,
	)
	if err != nil {
		_ = metadataStore.Close()
		t.Fatalf("session.Create: %v", err)
	}
	if err := store.EnsureDurable(); err != nil {
		_ = metadataStore.Close()
		t.Fatalf("EnsureDurable: %v", err)
	}
	t.Cleanup(func() { _ = metadataStore.Close() })
	return store
}

func mustCanonicalPath(t *testing.T, path string) string {
	t.Helper()
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q): %v", path, err)
	}
	return comparableWorkspaceChangeRoot(canonical)
}

func TestResolveSessionActionResumeReopensPicker(t *testing.T) {
	resolved, err := resolveSessionAction(
		context.Background(),
		&testEmbeddedServer{},
		nil,
		"",
		"",
		UITransition{Action: UIActionResume},
	)
	if err != nil {
		t.Fatalf("resolve session action: %v", err)
	}
	if !resolved.ShouldContinue {
		t.Fatal("expected lifecycle to continue for resume action")
	}
	if resolved.NextSessionID != "" {
		t.Fatalf("expected empty session id to force picker, got %q", resolved.NextSessionID)
	}
	if resolved.ForceNewSession {
		t.Fatal("did not expect force-new for resume action")
	}
	if resolved.ParentSessionID != "" {
		t.Fatalf("expected no parent session id on resume, got %q", resolved.ParentSessionID)
	}
	if resolved.InitialPrompt != "" || resolved.InitialInput != "" {
		t.Fatalf("expected no initial payload on resume, got prompt=%q input=%q", resolved.InitialPrompt, resolved.InitialInput)
	}
}

func TestResolveSessionActionNewSessionUsesForceNewFlow(t *testing.T) {
	resolved, err := resolveSessionAction(
		context.Background(),
		&testEmbeddedServer{},
		nil,
		"",
		"",
		UITransition{Action: UIActionNewSession, InitialPrompt: "hello", ParentSessionID: "parent-1"},
	)
	if err != nil {
		t.Fatalf("resolve session action: %v", err)
	}
	if !resolved.ShouldContinue {
		t.Fatal("expected lifecycle to continue for new session action")
	}
	if !resolved.ForceNewSession {
		t.Fatal("expected force-new session flow")
	}
	if resolved.NextSessionID != "" {
		t.Fatalf("expected empty session id for force-new flow, got %q", resolved.NextSessionID)
	}
	if resolved.ParentSessionID != "parent-1" {
		t.Fatalf("expected parent session id passthrough, got %q", resolved.ParentSessionID)
	}
	if resolved.InitialPrompt != "hello" || resolved.InitialInput != "" {
		t.Fatalf("expected initial prompt passthrough, got prompt=%q input=%q", resolved.InitialPrompt, resolved.InitialInput)
	}
}

func TestNewSessionTransitionKeepsBackgroundProcessesAlive(t *testing.T) {
	manager := newFastBackgroundTestManager(t)

	workdir := t.TempDir()
	res, err := manager.Start(context.Background(), shelltool.ExecRequest{
		Command:        []string{"sh", "-c", "printf 'transition-job\n'; sleep 1"},
		DisplayCommand: "transition-job",
		Workdir:        workdir,
		YieldTime:      fastBackgroundTestYield,
	})
	if err != nil {
		t.Fatalf("start background process: %v", err)
	}
	if !res.Backgrounded {
		t.Fatal("expected process to move to background")
	}

	root := t.TempDir()
	resolved, err := resolveSessionAction(
		context.Background(),
		&testEmbeddedServer{background: manager},
		nil,
		"",
		"",
		UITransition{Action: UIActionNewSession, InitialPrompt: "hello", ParentSessionID: "parent-1"},
	)
	if err != nil {
		t.Fatalf("resolve session action: %v", err)
	}
	if !resolved.ShouldContinue || !resolved.ForceNewSession {
		t.Fatalf("expected new-session transition, shouldContinue=%t forceNew=%t", resolved.ShouldContinue, resolved.ForceNewSession)
	}
	if resolved.NextSessionID != "" || resolved.InitialPrompt != "hello" || resolved.InitialInput != "" {
		t.Fatalf("unexpected transition payload nextSessionID=%q initialPrompt=%q initialInput=%q", resolved.NextSessionID, resolved.InitialPrompt, resolved.InitialInput)
	}

	wiring := &runtimeWiring{background: manager}
	if err := wiring.Close(); err != nil {
		t.Fatalf("close wiring: %v", err)
	}

	testServer := &testEmbeddedServer{
		cfg: config.App{
			WorkspaceRoot:   workdir,
			PersistenceRoot: root,
			Settings:        config.Settings{Theme: "dark", TUIAlternateScreen: config.TUIAlternateScreenAuto},
		},
		containerDir: root,
	}
	planner := &launchPlanner{server: testServer}
	storePlan, err := planner.PlanSession(context.Background(), sessionLaunchRequest{
		Mode:              launchModeInteractive,
		SelectedSessionID: resolved.NextSessionID,
		ForceNewSession:   resolved.ForceNewSession,
		ParentSessionID:   resolved.ParentSessionID,
	})
	if err != nil {
		t.Fatalf("open or create next session: %v", err)
	}
	store, err := testServer.sessionStoreRegistry().ResolveStore(context.Background(), storePlan.SessionID)
	if err != nil {
		t.Fatalf("resolve planned session from registry: %v", err)
	}
	if store == nil {
		t.Fatal("expected planned session store in registry")
	}
	if store.Meta().ParentSessionID != "parent-1" {
		t.Fatalf("expected parent session id preserved across new session transition, got %q", store.Meta().ParentSessionID)
	}
	entries := manager.List()
	if len(entries) != 1 {
		t.Fatalf("expected background process to survive session transition, got %d entries", len(entries))
	}
	if entries[0].ID != res.SessionID {
		t.Fatalf("expected surviving background process %s, got %s", res.SessionID, entries[0].ID)
	}
}

func TestResolveSessionActionForkRollbackTeleportsToForkWithPrompt(t *testing.T) {
	root := t.TempDir()
	store, err := session.Create(root, "workspace-x", "/tmp/work")
	if err != nil {
		t.Fatalf("create session store: %v", err)
	}
	if _, err := store.AppendEvent("s1", "message", llm.Message{Role: llm.RoleUser, Content: "u1"}); err != nil {
		t.Fatalf("append user message: %v", err)
	}
	if _, err := store.AppendEvent("s1", "message", llm.Message{Role: llm.RoleAssistant, Content: "a1"}); err != nil {
		t.Fatalf("append assistant message: %v", err)
	}

	resolved, err := resolveSessionAction(
		context.Background(),
		&testEmbeddedServer{cfg: config.App{PersistenceRoot: root}, containerDir: root},
		nil,
		store.Meta().SessionID,
		"lease-test-controller",
		UITransition{Action: UIActionForkRollback, InitialPrompt: "edited user message", ForkUserMessageIndex: 1},
	)
	if err != nil {
		t.Fatalf("resolve session action: %v", err)
	}
	if !resolved.ShouldContinue {
		t.Fatal("expected lifecycle to continue for fork rollback action")
	}
	if resolved.ForceNewSession {
		t.Fatal("did not expect force-new for fork rollback action")
	}
	if resolved.ParentSessionID != "" {
		t.Fatalf("expected no deferred parent for pre-created fork session, got %q", resolved.ParentSessionID)
	}
	if resolved.NextSessionID == "" {
		t.Fatal("expected target fork session id")
	}
	if resolved.NextSessionID == store.Meta().SessionID {
		t.Fatalf("expected fork session id to differ from parent, got %q", resolved.NextSessionID)
	}
	if resolved.InitialPrompt != "edited user message" || resolved.InitialInput != "" {
		t.Fatalf("expected initial prompt passthrough, got prompt=%q input=%q", resolved.InitialPrompt, resolved.InitialInput)
	}
}

func TestForkRollbackLifecycleDoesNotPersistEditedPromptAsSourceDraft(t *testing.T) {
	root := t.TempDir()
	store, err := session.Create(root, "workspace-x", "/tmp/work")
	if err != nil {
		t.Fatalf("create source store: %v", err)
	}
	if _, err := store.AppendEvent("s1", "message", llm.Message{Role: llm.RoleUser, Content: "u1"}); err != nil {
		t.Fatalf("append user message: %v", err)
	}
	if _, err := store.AppendEvent("s1", "message", llm.Message{Role: llm.RoleAssistant, Content: "a1"}); err != nil {
		t.Fatalf("append assistant message: %v", err)
	}

	m := newProjectedStaticUIModel()
	testSetRollbackEditing(m, 0, 0)
	m.input = "edited user message"
	server := &testEmbeddedServer{cfg: config.App{PersistenceRoot: root}, containerDir: root}

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := next.(*uiModel)
	if cmd == nil {
		t.Fatal("expected quit cmd for rollback fork")
	}
	if err := persistSessionDraftToServer(context.Background(), server, store.Meta().SessionID, "lease-test-controller", updated); err != nil {
		t.Fatalf("persist source draft: %v", err)
	}
	reopenedSource, err := session.Open(store.Dir())
	if err != nil {
		t.Fatalf("reopen source store: %v", err)
	}
	if reopenedSource.Meta().InputDraft != "" {
		t.Fatalf("expected no persisted source draft after fork handoff, got %q", reopenedSource.Meta().InputDraft)
	}

	resolved, err := resolveSessionAction(context.Background(), server, nil, reopenedSource.Meta().SessionID, "lease-test-controller", updated.Transition())
	if err != nil {
		t.Fatalf("resolve session action: %v", err)
	}
	if resolved.InitialPrompt != "edited user message" {
		t.Fatalf("expected fork prompt passthrough, got %q", resolved.InitialPrompt)
	}
	if resolved.InitialInput != "" {
		t.Fatalf("expected no fork input draft payload, got %q", resolved.InitialInput)
	}
}

func TestResolveSessionActionOpenSessionUsesTargetID(t *testing.T) {
	resolved, err := resolveSessionAction(
		context.Background(),
		&testEmbeddedServer{},
		nil,
		"",
		"",
		UITransition{Action: UIActionOpenSession, TargetSessionID: "session-42", InitialInput: "draft reply"},
	)
	if err != nil {
		t.Fatalf("resolve session action: %v", err)
	}
	if !resolved.ShouldContinue {
		t.Fatal("expected lifecycle to continue for open session action")
	}
	if resolved.NextSessionID != "session-42" {
		t.Fatalf("expected target session id passthrough, got %q", resolved.NextSessionID)
	}
	if resolved.InitialPrompt != "" {
		t.Fatalf("expected no initial prompt, got %q", resolved.InitialPrompt)
	}
	if resolved.InitialInput != "draft reply" {
		t.Fatalf("expected initial input passthrough, got %q", resolved.InitialInput)
	}
	if resolved.ParentSessionID != "" {
		t.Fatalf("expected no parent session id, got %q", resolved.ParentSessionID)
	}
	if resolved.ForceNewSession {
		t.Fatal("did not expect force-new session")
	}
}

func TestBackTeleportLifecycleSeedsParentDraftWithoutAutoSubmit(t *testing.T) {
	tests := []struct {
		name                string
		childMessages       []llm.Message
		childOngoing        string
		childActivity       uiActivity
		existingParentDraft string
		want                string
	}{
		{name: "copy idle child final assistant reply", childMessages: []llm.Message{{Role: llm.RoleAssistant, Content: "test", Phase: llm.MessagePhaseFinal}}, childActivity: uiActivityIdle, want: "test"},
		{name: "copy latest child final assistant reply past reminder entry", childMessages: []llm.Message{{Role: llm.RoleAssistant, Content: "test", Phase: llm.MessagePhaseFinal}, {Role: llm.RoleDeveloper, MessageType: llm.MessageTypeCompactionSoonReminder, Content: "heads up"}}, childActivity: uiActivityIdle, want: "test"},
		{name: "copy latest child final assistant reply past trailing error feedback", childMessages: []llm.Message{{Role: llm.RoleAssistant, Content: "test", Phase: llm.MessagePhaseFinal}, {Role: llm.RoleDeveloper, MessageType: llm.MessageTypeErrorFeedback, Content: "phase mismatch"}}, childActivity: uiActivityIdle, want: "test"},
		{name: "ignore interrupted child streaming reply", childOngoing: "review findings", childActivity: uiActivityInterrupted, want: ""},
		{name: "preserve existing parent draft", childMessages: []llm.Message{{Role: llm.RoleAssistant, Content: "test", Phase: llm.MessagePhaseFinal}}, childActivity: uiActivityIdle, existingParentDraft: "keep existing", want: "keep existing"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			parentStore, err := session.Create(root, "workspace-x", "/tmp/work")
			if err != nil {
				t.Fatalf("create parent store: %v", err)
			}
			if err := parentStore.SetInputDraft(tt.existingParentDraft); err != nil {
				t.Fatalf("set parent draft: %v", err)
			}

			childStore, err := session.Create(root, "workspace-x", "/tmp/work")
			if err != nil {
				t.Fatalf("create child store: %v", err)
			}
			if err := childStore.SetParentSessionID(parentStore.Meta().SessionID); err != nil {
				t.Fatalf("set child parent id: %v", err)
			}

			for idx, message := range tt.childMessages {
				if _, err := childStore.AppendEvent("step-1", "message", message); err != nil {
					t.Fatalf("append child transcript message %d: %v", idx, err)
				}
			}
			childEngine, err := runtime.New(childStore, statusLineFakeClient{}, tools.NewRegistry(), runtime.Config{Model: "gpt-5"})
			if err != nil {
				t.Fatalf("new child engine after transcript seed: %v", err)
			}
			childModel := newProjectedEngineUIModel(childEngine)
			childModel.activity = tt.childActivity
			if tt.childOngoing != "" {
				childModel.forwardToView(tui.SetConversationMsg{Entries: childModel.transcriptEntries, Ongoing: tt.childOngoing})
			}
			childModel.input = "/back"
			server := &testEmbeddedServer{cfg: config.App{PersistenceRoot: root}, containerDir: root}

			next, cmd := childModel.Update(tea.KeyMsg{Type: tea.KeyEnter})
			updatedChild := next.(*uiModel)
			if cmd == nil {
				t.Fatal("expected quit cmd for /back")
			}
			if err := persistSessionDraftToServer(context.Background(), server, childStore.Meta().SessionID, "lease-test-controller", updatedChild); err != nil {
				t.Fatalf("persist child draft: %v", err)
			}

			resolved, err := resolveSessionAction(context.Background(), server, nil, childStore.Meta().SessionID, "lease-test-controller", updatedChild.Transition())
			if err != nil {
				t.Fatalf("resolve session action: %v", err)
			}
			if !resolved.ShouldContinue {
				t.Fatal("expected lifecycle to continue")
			}
			if resolved.NextSessionID != parentStore.Meta().SessionID {
				t.Fatalf("expected parent session target, got %q", resolved.NextSessionID)
			}

			reopenedParent, err := session.Open(parentStore.Dir())
			if err != nil {
				t.Fatalf("reopen parent store: %v", err)
			}
			parentEngine, err := runtime.New(reopenedParent, statusLineFakeClient{}, tools.NewRegistry(), runtime.Config{Model: "gpt-5"})
			if err != nil {
				t.Fatalf("new parent engine: %v", err)
			}
			parentModel := newProjectedEngineUIModel(
				parentEngine,
				WithUIInitialInput(sessionLaunchInitialInputFromServer(context.Background(), server, reopenedParent.Meta().SessionID, resolved.InitialInput)),
			)

			if parentModel.input != tt.want {
				t.Fatalf("expected parent draft %q, got %q", tt.want, parentModel.input)
			}
			if parentModel.busy {
				t.Fatal("did not expect parent draft to auto-submit")
			}

			next, _ = parentModel.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
			editable := next.(*uiModel)
			if editable.input != tt.want+"x" {
				t.Fatalf("expected editable parent draft, got %q", editable.input)
			}
		})
	}
}

func TestForkRollbackNativeStartupReplayUsesForkedHistory(t *testing.T) {
	root := t.TempDir()
	store, err := session.Create(root, "workspace-x", "/tmp/work")
	if err != nil {
		t.Fatalf("create session store: %v", err)
	}
	if _, err := store.AppendEvent("s1", "message", llm.Message{Role: llm.RoleUser, Content: "u1"}); err != nil {
		t.Fatalf("append u1: %v", err)
	}
	if _, err := store.AppendEvent("s1", "message", llm.Message{Role: llm.RoleAssistant, Content: "a1"}); err != nil {
		t.Fatalf("append a1: %v", err)
	}
	if _, err := store.AppendEvent("s2", "message", llm.Message{Role: llm.RoleUser, Content: "u2"}); err != nil {
		t.Fatalf("append u2: %v", err)
	}
	if _, err := store.AppendEvent("s2", "message", llm.Message{Role: llm.RoleAssistant, Content: "a2"}); err != nil {
		t.Fatalf("append a2: %v", err)
	}

	resolved, err := resolveSessionAction(
		context.Background(),
		&testEmbeddedServer{cfg: config.App{PersistenceRoot: root}, containerDir: root},
		nil,
		store.Meta().SessionID,
		"lease-test-controller",
		UITransition{Action: UIActionForkRollback, InitialPrompt: "edited user message", ForkUserMessageIndex: 2},
	)
	if err != nil {
		t.Fatalf("resolve session action: %v", err)
	}
	if !resolved.ShouldContinue {
		t.Fatal("expected lifecycle to continue for fork rollback action")
	}

	forkedStore, err := session.Open(filepath.Join(root, resolved.NextSessionID))
	if err != nil {
		t.Fatalf("open fork session store: %v", err)
	}
	eng, err := runtime.New(forkedStore, statusLineFakeClient{}, tools.NewRegistry(), runtime.Config{Model: "gpt-5"})
	if err != nil {
		t.Fatalf("new runtime for fork: %v", err)
	}

	m := newProjectedEngineUIModel(eng)
	next, cmd := m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	updated := next.(*uiModel)
	if cmd == nil {
		t.Fatal("expected native startup replay command for fork session")
	}
	flushMsg, ok := cmd().(nativeHistoryFlushMsg)
	if !ok {
		t.Fatalf("expected nativeHistoryFlushMsg, got %T", cmd())
	}
	plain := stripANSIAndTrimRight(flushMsg.Text)
	if !strings.Contains(plain, "u1") || !strings.Contains(plain, "a1") {
		t.Fatalf("expected startup replay to include fork base history, got %q", plain)
	}
	if strings.Contains(plain, "u2") || strings.Contains(plain, "a2") {
		t.Fatalf("expected startup replay to exclude trimmed history after fork point, got %q", plain)
	}
	if len(updated.transcriptEntries) != 2 {
		t.Fatalf("expected forked transcript to include only two committed entries, got %d", len(updated.transcriptEntries))
	}
}

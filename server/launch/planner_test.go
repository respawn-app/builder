package launch

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"builder/server/session"
	"builder/shared/client"
	"builder/shared/clientui"
	"builder/shared/config"
	"builder/shared/serverapi"
)

func TestPlannerHeadlessCreatesNewSessionAndAppliesContinuationContext(t *testing.T) {
	root := t.TempDir()
	containerDir := filepath.Join(root, "sessions", "workspace-a")
	planner := Planner{
		Config: config.App{
			WorkspaceRoot:   "/tmp/workspace-a",
			PersistenceRoot: root,
			Settings: config.Settings{
				OpenAIBaseURL: "http://headless.local/v1",
			},
		},
		ContainerDir: containerDir,
	}

	plan, err := planner.PlanSession(SessionRequest{Mode: ModeHeadless})
	if err != nil {
		t.Fatalf("plan session: %v", err)
	}
	meta := plan.Store.Meta()
	if meta.SessionID == "" {
		t.Fatal("expected session id")
	}
	if !strings.HasSuffix(meta.Name, " "+SubagentSessionSuffix) {
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

func TestPlannerInteractiveUsesPickerSelection(t *testing.T) {
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
	planner := Planner{
		Config: config.App{
			WorkspaceRoot:   "/tmp/workspace-a",
			PersistenceRoot: root,
			Settings:        config.Settings{},
		},
		ContainerDir: containerDir,
		PickSession: func(summaries []session.Summary) (SessionSelection, error) {
			if len(summaries) != 2 {
				t.Fatalf("expected two summaries, got %d", len(summaries))
			}
			for _, summary := range summaries {
				if summary.SessionID == second.Meta().SessionID {
					picked := summary
					return SessionSelection{Session: &picked}, nil
				}
			}
			t.Fatalf("expected picker summaries to include %q", second.Meta().SessionID)
			return SessionSelection{}, nil
		},
	}

	plan, err := planner.PlanSession(SessionRequest{Mode: ModeInteractive})
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

func TestPlannerInteractiveUsesProjectViewSessionsAndReopensBySessionID(t *testing.T) {
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
	projectViews := &stubLaunchProjectViewService{overview: serverapi.ProjectGetOverviewResponse{Overview: clientui.ProjectOverview{
		Project: clientui.ProjectSummary{ProjectID: "project-1", DisplayName: "workspace-a", RootPath: "/tmp/workspace-a"},
		Sessions: []clientui.SessionSummary{
			{SessionID: first.Meta().SessionID, Name: "first", UpdatedAt: first.Meta().UpdatedAt},
			{SessionID: second.Meta().SessionID, Name: "second", UpdatedAt: second.Meta().UpdatedAt},
		},
	}}}
	planner := Planner{
		Config: config.App{
			WorkspaceRoot:   "/tmp/workspace-a",
			PersistenceRoot: root,
			Settings:        config.Settings{},
		},
		ContainerDir: containerDir,
		ProjectID:    "project-1",
		ProjectViews: client.NewLoopbackProjectViewClient(projectViews),
		PickSession: func(summaries []session.Summary) (SessionSelection, error) {
			if len(summaries) != 2 {
				t.Fatalf("expected two summaries, got %d", len(summaries))
			}
			picked := summaries[1]
			return SessionSelection{Session: &picked}, nil
		},
	}

	plan, err := planner.PlanSession(SessionRequest{Mode: ModeInteractive})
	if err != nil {
		t.Fatalf("plan session: %v", err)
	}
	if plan.Store.Meta().SessionID != second.Meta().SessionID {
		t.Fatalf("expected selected session %q, got %q", second.Meta().SessionID, plan.Store.Meta().SessionID)
	}
	if projectViews.overviewCalls != 1 {
		t.Fatalf("expected project overview to be used once, got %d", projectViews.overviewCalls)
	}
}

func TestPlannerInteractivePickerReopensSelectedSessionWithinActiveContainer(t *testing.T) {
	root := t.TempDir()
	containerA := filepath.Join(root, "sessions", "workspace-a")
	containerB := filepath.Join(root, "sessions", "workspace-b")
	selected, err := session.Create(containerA, "workspace-a", "/tmp/workspace-a")
	if err != nil {
		t.Fatalf("create selected session: %v", err)
	}
	if err := selected.SetName("selected"); err != nil {
		t.Fatalf("persist selected session meta: %v", err)
	}
	otherDir := filepath.Join(containerB, selected.Meta().SessionID)
	if err := os.MkdirAll(otherDir, 0o755); err != nil {
		t.Fatalf("mkdir duplicate session dir: %v", err)
	}
	duplicateMeta := selected.Meta()
	duplicateMeta.WorkspaceContainer = "workspace-b"
	duplicateMeta.WorkspaceRoot = "/tmp/workspace-b"
	duplicateMeta.Name = "duplicate"
	duplicateData, err := json.Marshal(duplicateMeta)
	if err != nil {
		t.Fatalf("marshal duplicate session meta: %v", err)
	}
	if err := os.WriteFile(filepath.Join(otherDir, "session.json"), duplicateData, 0o644); err != nil {
		t.Fatalf("write duplicate session meta: %v", err)
	}
	if err := os.WriteFile(filepath.Join(otherDir, "events.jsonl"), nil, 0o644); err != nil {
		t.Fatalf("write duplicate session events: %v", err)
	}
	planner := Planner{
		Config:       config.App{WorkspaceRoot: "/tmp/workspace-a", PersistenceRoot: root, Settings: config.Settings{}},
		ContainerDir: containerA,
		PickSession: func(summaries []session.Summary) (SessionSelection, error) {
			picked := summaries[0]
			return SessionSelection{Session: &picked}, nil
		},
	}

	plan, err := planner.PlanSession(SessionRequest{Mode: ModeInteractive})
	if err != nil {
		t.Fatalf("plan session: %v", err)
	}
	openedDir, err := filepath.EvalSymlinks(plan.Store.Dir())
	if err != nil {
		t.Fatalf("EvalSymlinks opened dir: %v", err)
	}
	selectedDir, err := filepath.EvalSymlinks(selected.Dir())
	if err != nil {
		t.Fatalf("EvalSymlinks selected dir: %v", err)
	}
	if openedDir != selectedDir {
		t.Fatalf("opened session dir = %q, want %q", openedDir, selectedDir)
	}
	if plan.Store.Meta().WorkspaceContainer != "workspace-a" {
		t.Fatalf("opened workspace container = %q, want workspace-a", plan.Store.Meta().WorkspaceContainer)
	}
}

func TestPlannerSelectedSessionIDUsesActiveContainerScope(t *testing.T) {
	root := t.TempDir()
	containerA := filepath.Join(root, "sessions", "workspace-a")
	containerB := filepath.Join(root, "sessions", "workspace-b")
	selected, err := session.Create(containerA, "workspace-a", "/tmp/workspace-a")
	if err != nil {
		t.Fatalf("create selected session: %v", err)
	}
	if err := selected.SetName("selected"); err != nil {
		t.Fatalf("persist selected session meta: %v", err)
	}
	duplicateDir := filepath.Join(containerB, selected.Meta().SessionID)
	if err := os.MkdirAll(duplicateDir, 0o755); err != nil {
		t.Fatalf("mkdir duplicate session dir: %v", err)
	}
	duplicateMeta := selected.Meta()
	duplicateMeta.WorkspaceContainer = "workspace-b"
	duplicateMeta.WorkspaceRoot = "/tmp/workspace-b"
	duplicateData, err := json.Marshal(duplicateMeta)
	if err != nil {
		t.Fatalf("marshal duplicate session meta: %v", err)
	}
	if err := os.WriteFile(filepath.Join(duplicateDir, "session.json"), duplicateData, 0o644); err != nil {
		t.Fatalf("write duplicate session meta: %v", err)
	}
	if err := os.WriteFile(filepath.Join(duplicateDir, "events.jsonl"), nil, 0o644); err != nil {
		t.Fatalf("write duplicate session events: %v", err)
	}
	planner := Planner{
		Config:       config.App{WorkspaceRoot: "/tmp/workspace-a", PersistenceRoot: root, Settings: config.Settings{}},
		ContainerDir: containerA,
	}

	plan, err := planner.PlanSession(SessionRequest{Mode: ModeInteractive, SelectedSessionID: selected.Meta().SessionID})
	if err != nil {
		t.Fatalf("plan session: %v", err)
	}
	openedDir, err := filepath.EvalSymlinks(plan.Store.Dir())
	if err != nil {
		t.Fatalf("EvalSymlinks opened dir: %v", err)
	}
	selectedDir, err := filepath.EvalSymlinks(selected.Dir())
	if err != nil {
		t.Fatalf("EvalSymlinks selected dir: %v", err)
	}
	if openedDir != selectedDir {
		t.Fatalf("opened session dir = %q, want %q", openedDir, selectedDir)
	}
}

func TestPlannerSelectedSessionIDRejectsSymlinkOutsideActiveContainer(t *testing.T) {
	root := t.TempDir()
	containerA := filepath.Join(root, "sessions", "workspace-a")
	containerB := filepath.Join(root, "sessions", "workspace-b")
	if err := os.MkdirAll(containerA, 0o755); err != nil {
		t.Fatalf("mkdir container A: %v", err)
	}
	escaped, err := session.Create(containerB, "workspace-b", "/tmp/workspace-b")
	if err != nil {
		t.Fatalf("create escaped session: %v", err)
	}
	if err := os.Symlink(escaped.Dir(), filepath.Join(containerA, "escaped-link")); err != nil {
		t.Fatalf("symlink escaped session: %v", err)
	}
	planner := Planner{
		Config:       config.App{WorkspaceRoot: "/tmp/workspace-a", PersistenceRoot: root, Settings: config.Settings{}},
		ContainerDir: containerA,
	}

	if _, err := planner.PlanSession(SessionRequest{Mode: ModeInteractive, SelectedSessionID: "escaped-link"}); err == nil {
		t.Fatal("expected planner to reject symlinked selected session outside active container")
	}
}

type stubLaunchProjectViewService struct {
	overview      serverapi.ProjectGetOverviewResponse
	overviewCalls int
}

func (s *stubLaunchProjectViewService) ListProjects(_ context.Context, _ serverapi.ProjectListRequest) (serverapi.ProjectListResponse, error) {
	return serverapi.ProjectListResponse{}, nil
}

func (s *stubLaunchProjectViewService) GetProjectOverview(_ context.Context, _ serverapi.ProjectGetOverviewRequest) (serverapi.ProjectGetOverviewResponse, error) {
	s.overviewCalls++
	return s.overview, nil
}

func (s *stubLaunchProjectViewService) ListSessionsByProject(_ context.Context, _ serverapi.SessionListByProjectRequest) (serverapi.SessionListByProjectResponse, error) {
	return serverapi.SessionListByProjectResponse{}, errors.New("ListSessionsByProject should not be called when project overview is available")
}

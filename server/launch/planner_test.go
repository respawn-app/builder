package launch

import (
	"path/filepath"
	"strings"
	"testing"

	"builder/server/session"
	"builder/shared/config"
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

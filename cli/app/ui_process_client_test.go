package app

import (
	"context"
	"errors"
	"testing"
	"time"

	shelltool "builder/server/tools/shell"
	"builder/shared/client"
	"builder/shared/clientui"
	"builder/shared/serverapi"
)

type fixedUIProcessClient struct {
	entries []clientui.BackgroundProcess
}

type stubProcessViewService struct {
	listResp serverapi.ProcessListResponse
	err      error
}

func (s *stubProcessViewService) ListProcesses(context.Context, serverapi.ProcessListRequest) (serverapi.ProcessListResponse, error) {
	if s.err != nil {
		return serverapi.ProcessListResponse{}, s.err
	}
	return s.listResp, nil
}

func (s *stubProcessViewService) GetProcess(context.Context, serverapi.ProcessGetRequest) (serverapi.ProcessGetResponse, error) {
	if s.err != nil {
		return serverapi.ProcessGetResponse{}, s.err
	}
	return serverapi.ProcessGetResponse{}, nil
}

func (c fixedUIProcessClient) ListProcesses() []clientui.BackgroundProcess {
	out := make([]clientui.BackgroundProcess, len(c.entries))
	copy(out, c.entries)
	return out
}

func TestUIProcessClientProjectsManagerSnapshots(t *testing.T) {
	manager, err := shelltool.NewManager(shelltool.WithMinimumExecToBgTime(250 * time.Millisecond))
	if err != nil {
		t.Fatalf("new background manager: %v", err)
	}
	t.Cleanup(func() { _ = manager.Close() })

	workdir := t.TempDir()
	res, err := manager.Start(context.Background(), shelltool.ExecRequest{
		Command:        []string{"sh", "-c", "printf 'done\n'; sleep 0.4; exit 7"},
		DisplayCommand: "project-test",
		Workdir:        workdir,
		YieldTime:      250 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("start background process: %v", err)
	}
	if !res.Backgrounded {
		t.Fatal("expected background process")
	}

	client := newUIProcessClient(manager)
	waitForTestCondition(t, 2*time.Second, "background process to finish", func() bool {
		for _, entry := range client.ListProcesses() {
			if entry.ID == res.SessionID {
				return !entry.Running && entry.ExitCode != nil
			}
		}
		return false
	})

	var projectedExitCode *int
	found := false
	for _, entry := range client.ListProcesses() {
		if entry.ID != res.SessionID {
			continue
		}
		found = true
		if entry.Command != "project-test" {
			t.Fatalf("command = %q, want project-test", entry.Command)
		}
		if entry.Workdir != workdir {
			t.Fatalf("workdir = %q, want %q", entry.Workdir, workdir)
		}
		if entry.LogPath == "" {
			t.Fatal("expected projected log path")
		}
		if entry.ExitCode == nil || *entry.ExitCode != 7 {
			t.Fatalf("exit code = %+v, want 7", entry.ExitCode)
		}
		projectedExitCode = entry.ExitCode
		break
	}
	if !found {
		t.Fatalf("expected projected process entry for %s", res.SessionID)
	}

	*projectedExitCode = 0
	for _, entry := range client.ListProcesses() {
		if entry.ID == res.SessionID {
			if entry.ExitCode == nil || *entry.ExitCode != 7 {
				t.Fatalf("expected projected exit code clone to remain 7, got %+v", entry.ExitCode)
			}
			return
		}
	}
	t.Fatalf("expected projected process entry for %s on second read", res.SessionID)
}

func TestExplicitUIProcessClientWinsOverBackgroundManagerOptionOrder(t *testing.T) {
	manager, err := shelltool.NewManager(shelltool.WithMinimumExecToBgTime(250 * time.Millisecond))
	if err != nil {
		t.Fatalf("new background manager: %v", err)
	}
	t.Cleanup(func() { _ = manager.Close() })

	explicit := fixedUIProcessClient{entries: []clientui.BackgroundProcess{{ID: "explicit-process"}}}

	first := newProjectedStaticUIModel(
		WithUIBackgroundManager(manager),
		WithUIProcessClient(explicit),
	)
	if got := first.listProcesses(); len(got) != 1 || got[0].ID != "explicit-process" {
		t.Fatalf("expected explicit process client to win when applied last, got %+v", got)
	}

	second := newProjectedStaticUIModel(
		WithUIProcessClient(explicit),
		WithUIBackgroundManager(manager),
	)
	if got := second.listProcesses(); len(got) != 1 || got[0].ID != "explicit-process" {
		t.Fatalf("expected explicit process client to win when applied first, got %+v", got)
	}
}

func TestUIProcessClientUsesLoopbackReadsWhenAvailable(t *testing.T) {
	reads := client.NewLoopbackProcessViewClient(&stubProcessViewService{
		listResp: serverapi.ProcessListResponse{Processes: []clientui.BackgroundProcess{{ID: "proc-1", OwnerRunID: "run-1", OwnerStepID: "step-1"}}},
	})
	processClient := newUIProcessClientWithReads(nil, reads)
	got := processClient.ListProcesses()
	if len(got) != 1 || got[0].ID != "proc-1" || got[0].OwnerRunID != "run-1" || got[0].OwnerStepID != "step-1" {
		t.Fatalf("unexpected loopback process payload: %+v", got)
	}
}

func TestUIProcessClientFallsBackToManagerOnReadError(t *testing.T) {
	manager, err := shelltool.NewManager(shelltool.WithMinimumExecToBgTime(250 * time.Millisecond))
	if err != nil {
		t.Fatalf("new background manager: %v", err)
	}
	t.Cleanup(func() { _ = manager.Close() })

	workdir := t.TempDir()
	res, err := manager.Start(context.Background(), shelltool.ExecRequest{
		Command:        []string{"sh", "-c", "printf 'done\n'; sleep 0.4; exit 0"},
		DisplayCommand: "fallback-process",
		OwnerSessionID: "session-1",
		OwnerRunID:     "run-1",
		OwnerStepID:    "step-1",
		Workdir:        workdir,
		YieldTime:      250 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("start background process: %v", err)
	}
	if !res.Backgrounded {
		t.Fatal("expected background process")
	}

	processClient := newUIProcessClientWithReads(manager, client.NewLoopbackProcessViewClient(&stubProcessViewService{err: errors.New("boom")}))
	got := processClient.ListProcesses()
	if len(got) == 0 {
		t.Fatal("expected fallback process list")
	}
	if got[0].OwnerRunID != "run-1" || got[0].OwnerStepID != "step-1" {
		t.Fatalf("unexpected fallback ownership: %+v", got[0])
	}
}

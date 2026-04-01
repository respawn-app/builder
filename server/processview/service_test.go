package processview

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"builder/server/tools"
	shelltool "builder/server/tools/shell"
	"builder/shared/serverapi"
)

func TestServiceListProcessesIncludesRunOwnership(t *testing.T) {
	manager, err := shelltool.NewManager(shelltool.WithMinimumExecToBgTime(250 * time.Millisecond))
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	t.Cleanup(func() { _ = manager.Close() })

	workspace := t.TempDir()
	tool := shelltool.NewExecCommandTool(workspace, 16_000, manager, "session-1")
	input, err := json.Marshal(map[string]any{
		"cmd":           "printf 'working\n'; sleep 1",
		"yield_time_ms": 250,
	})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}
	result, err := tool.Call(context.Background(), tools.Call{
		ID:     "call-1",
		Name:   tools.ToolExecCommand,
		Input:  input,
		RunID:  "run-1",
		StepID: "step-1",
	})
	if err != nil {
		t.Fatalf("tool call: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected successful tool result, got %+v", result)
	}

	svc := NewService(manager)
	resp, err := svc.ListProcesses(context.Background(), serverapi.ProcessListRequest{OwnerSessionID: "session-1", OwnerRunID: "run-1"})
	if err != nil {
		t.Fatalf("ListProcesses: %v", err)
	}
	if len(resp.Processes) != 1 {
		t.Fatalf("expected one process, got %+v", resp.Processes)
	}
	process := resp.Processes[0]
	if process.OwnerSessionID != "session-1" || process.OwnerRunID != "run-1" || process.OwnerStepID != "step-1" {
		t.Fatalf("unexpected ownership: %+v", process)
	}
	if !process.Backgrounded || !process.Running {
		t.Fatalf("expected backgrounded running process, got %+v", process)
	}

	got, err := svc.GetProcess(context.Background(), serverapi.ProcessGetRequest{ProcessID: process.ID})
	if err != nil {
		t.Fatalf("GetProcess: %v", err)
	}
	if got.Process == nil || got.Process.OwnerRunID != "run-1" || got.Process.OwnerStepID != "step-1" {
		t.Fatalf("unexpected process payload: %+v", got.Process)
	}
}

func TestServiceListProcessesFiltersByOwnerRunID(t *testing.T) {
	manager, err := shelltool.NewManager(shelltool.WithMinimumExecToBgTime(250 * time.Millisecond))
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	t.Cleanup(func() { _ = manager.Close() })

	workspace := t.TempDir()
	tool := shelltool.NewExecCommandTool(workspace, 16_000, manager, "session-1")
	for _, runID := range []string{"run-a", "run-b"} {
		input, marshalErr := json.Marshal(map[string]any{
			"cmd":           "sleep 1",
			"yield_time_ms": 250,
		})
		if marshalErr != nil {
			t.Fatalf("marshal input: %v", marshalErr)
		}
		if _, err := tool.Call(context.Background(), tools.Call{ID: runID, Name: tools.ToolExecCommand, Input: input, RunID: runID, StepID: runID + "-step"}); err != nil {
			t.Fatalf("tool call for %s: %v", runID, err)
		}
	}

	waitForProcessCount(t, manager, 2)

	svc := NewService(manager)
	resp, err := svc.ListProcesses(context.Background(), serverapi.ProcessListRequest{OwnerRunID: "run-b"})
	if err != nil {
		t.Fatalf("ListProcesses: %v", err)
	}
	if len(resp.Processes) != 1 || resp.Processes[0].OwnerRunID != "run-b" {
		t.Fatalf("unexpected filtered processes: %+v", resp.Processes)
	}
}

func waitForProcessCount(t *testing.T, manager *shelltool.Manager, count int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(manager.List()) >= count {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d processes", count)
}

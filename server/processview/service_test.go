package processview

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"builder/server/idempotency"
	"builder/server/tools"
	shelltool "builder/server/tools/shell"
	"builder/shared/serverapi"
	"builder/shared/toolspec"
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
		Name:   toolspec.ToolExecCommand,
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
	waitForProcessSnapshot(t, 2*time.Second, func() (shelltool.Snapshot, bool) {
		entries := manager.List()
		if len(entries) != 1 {
			return shelltool.Snapshot{}, false
		}
		process := entries[0]
		if !process.OutputAvailable || process.OutputRetainedFromBytes != 0 || process.OutputRetainedToBytes <= 0 {
			return shelltool.Snapshot{}, false
		}
		return process, true
	})
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
	if !process.OutputAvailable || process.OutputRetainedFromBytes != 0 || process.OutputRetainedToBytes <= 0 {
		t.Fatalf("expected retained output metadata, got %+v", process)
	}

	got, err := svc.GetProcess(context.Background(), serverapi.ProcessGetRequest{ProcessID: process.ID})
	if err != nil {
		t.Fatalf("GetProcess: %v", err)
	}
	if got.Process == nil || got.Process.OwnerRunID != "run-1" || got.Process.OwnerStepID != "step-1" {
		t.Fatalf("unexpected process payload: %+v", got.Process)
	}
	if !got.Process.OutputAvailable || got.Process.OutputRetainedFromBytes != 0 || got.Process.OutputRetainedToBytes < process.OutputRetainedToBytes {
		t.Fatalf("expected retained output metadata from get, got %+v", got.Process)
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
		if _, err := tool.Call(context.Background(), tools.Call{ID: runID, Name: toolspec.ToolExecCommand, Input: input, RunID: runID, StepID: runID + "-step"}); err != nil {
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

func TestServiceGetInlineOutputReturnsManagerPreview(t *testing.T) {
	manager, err := shelltool.NewManager(shelltool.WithMinimumExecToBgTime(250 * time.Millisecond))
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	t.Cleanup(func() { _ = manager.Close() })

	workspace := t.TempDir()
	tool := shelltool.NewExecCommandTool(workspace, 16_000, manager, "session-1")
	input, err := json.Marshal(map[string]any{
		"cmd":           "printf 'inline-preview\n'; sleep 1",
		"yield_time_ms": 250,
	})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}
	result, err := tool.Call(context.Background(), tools.Call{ID: "call-inline", Name: toolspec.ToolExecCommand, Input: input, RunID: "run-1", StepID: "step-1"})
	if err != nil {
		t.Fatalf("tool call: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected successful tool result, got %+v", result)
	}

	svc := NewService(manager)
	waitForInlineOutput(t, 2*time.Second, func() (serverapi.ProcessInlineOutputResponse, error) {
		return svc.GetInlineOutput(context.Background(), serverapi.ProcessInlineOutputRequest{ProcessID: "1000", MaxChars: 12_000})
	}, func(resp serverapi.ProcessInlineOutputResponse) bool {
		return resp.LogPath != "" && strings.Contains(resp.Output, "inline-preview")
	})
	resp, err := svc.GetInlineOutput(context.Background(), serverapi.ProcessInlineOutputRequest{ProcessID: "1000", MaxChars: 12_000})
	if err != nil {
		t.Fatalf("GetInlineOutput: %v", err)
	}
	if resp.LogPath == "" || !strings.Contains(resp.Output, "inline-preview") {
		t.Fatalf("unexpected inline output response: %+v", resp)
	}
}

func TestServiceKillProcessSignalsManagerEntry(t *testing.T) {
	manager, err := shelltool.NewManager(shelltool.WithMinimumExecToBgTime(250 * time.Millisecond))
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	t.Cleanup(func() { _ = manager.Close() })

	workspace := t.TempDir()
	tool := shelltool.NewExecCommandTool(workspace, 16_000, manager, "session-1")
	input, err := json.Marshal(map[string]any{
		"cmd":           "sleep 30",
		"yield_time_ms": 250,
	})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}
	result, err := tool.Call(context.Background(), tools.Call{ID: "call-kill", Name: toolspec.ToolExecCommand, Input: input, RunID: "run-1", StepID: "step-1"})
	if err != nil {
		t.Fatalf("tool call: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected successful tool result, got %+v", result)
	}

	svc := NewService(manager)
	if _, err := svc.KillProcess(context.Background(), serverapi.ProcessKillRequest{ClientRequestID: "req-kill-1", ProcessID: "1000"}); err != nil {
		t.Fatalf("KillProcess: %v", err)
	}
	waitForProcessKilled(t, manager, "1000")
}

func TestServiceKillProcessRequiresClientRequestID(t *testing.T) {
	manager, err := shelltool.NewManager(shelltool.WithMinimumExecToBgTime(250 * time.Millisecond))
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	t.Cleanup(func() { _ = manager.Close() })

	svc := NewService(manager)
	if _, err := svc.KillProcess(context.Background(), serverapi.ProcessKillRequest{ProcessID: "1000"}); err == nil {
		t.Fatal("expected KillProcess to require client_request_id")
	}
}

func TestServiceKillProcessDeduplicatesSuccessfulRetryByClientRequestID(t *testing.T) {
	processes := &stubKillProcessSource{}
	svc := NewService(processes).WithIdempotencyCoordinator(idempotency.NewCoordinator(nil, idempotency.DefaultRetention))
	req := serverapi.ProcessKillRequest{ClientRequestID: "req-kill-1", ProcessID: "proc-1"}
	if _, err := svc.KillProcess(context.Background(), req); err != nil {
		t.Fatalf("first KillProcess: %v", err)
	}
	processes.killErr = errors.New("unknown session_id proc-1")
	if _, err := svc.KillProcess(context.Background(), req); err != nil {
		t.Fatalf("second KillProcess retry: %v", err)
	}
	if processes.killCalls != 1 {
		t.Fatalf("kill call count = %d, want 1", processes.killCalls)
	}
}

func TestServiceKillProcessScopesClientRequestIDByProcess(t *testing.T) {
	processes := &stubKillProcessSource{}
	svc := NewService(processes).WithIdempotencyCoordinator(idempotency.NewCoordinator(nil, idempotency.DefaultRetention))
	if _, err := svc.KillProcess(context.Background(), serverapi.ProcessKillRequest{ClientRequestID: "req-kill-1", ProcessID: "proc-1"}); err != nil {
		t.Fatalf("first KillProcess: %v", err)
	}
	if _, err := svc.KillProcess(context.Background(), serverapi.ProcessKillRequest{ClientRequestID: "req-kill-1", ProcessID: "proc-2"}); err != nil {
		t.Fatalf("second KillProcess: %v", err)
	}
	if processes.killCalls != 2 {
		t.Fatalf("kill call count = %d, want 2", processes.killCalls)
	}
}

func TestServiceKillProcessReturnsContextCanceledWhileWaitingForDuplicateInFlight(t *testing.T) {
	processes := &blockingKillProcessSource{started: make(chan struct{}), release: make(chan struct{})}
	svc := NewService(processes).WithIdempotencyCoordinator(idempotency.NewCoordinator(nil, idempotency.DefaultRetention))
	done := make(chan error, 1)
	go func() {
		_, err := svc.KillProcess(context.Background(), serverapi.ProcessKillRequest{ClientRequestID: "req-kill-1", ProcessID: "proc-1"})
		done <- err
	}()
	<-processes.started
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := svc.KillProcess(ctx, serverapi.ProcessKillRequest{ClientRequestID: "req-kill-1", ProcessID: "proc-1"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context canceled while waiting for duplicate in-flight request, got %v", err)
	}
	close(processes.release)
	if err := <-done; err != nil {
		t.Fatalf("first KillProcess: %v", err)
	}
}

type stubKillProcessSource struct {
	killCalls int
	killErr   error
}

func (s *stubKillProcessSource) List() []shelltool.Snapshot { return nil }

func (s *stubKillProcessSource) Snapshot(string) (shelltool.Snapshot, error) {
	return shelltool.Snapshot{}, nil
}

func (s *stubKillProcessSource) Kill(string) error {
	s.killCalls++
	return s.killErr
}

func (s *stubKillProcessSource) InlineOutput(string, int) (string, string, error) {
	return "", "", nil
}

type blockingKillProcessSource struct {
	started chan struct{}
	release chan struct{}
}

func (s *blockingKillProcessSource) List() []shelltool.Snapshot { return nil }

func (s *blockingKillProcessSource) Snapshot(string) (shelltool.Snapshot, error) {
	return shelltool.Snapshot{}, nil
}

func (s *blockingKillProcessSource) Kill(string) error {
	close(s.started)
	<-s.release
	return nil
}

func (s *blockingKillProcessSource) InlineOutput(string, int) (string, string, error) {
	return "", "", nil
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

func waitForProcessKilled(t *testing.T, manager *shelltool.Manager, id string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, entry := range manager.List() {
			if entry.ID == id && (entry.KillRequested || !entry.Running) {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for process %s to be kill-requested", id)
}

func waitForProcessSnapshot(t *testing.T, timeout time.Duration, check func() (shelltool.Snapshot, bool)) shelltool.Snapshot {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if snapshot, ok := check(); ok {
			return snapshot
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting for process snapshot condition")
	return shelltool.Snapshot{}
}

func waitForInlineOutput(t *testing.T, timeout time.Duration, call func() (serverapi.ProcessInlineOutputResponse, error), match func(serverapi.ProcessInlineOutputResponse) bool) serverapi.ProcessInlineOutputResponse {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := call()
		if err == nil && match(resp) {
			return resp
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting for inline output")
	return serverapi.ProcessInlineOutputResponse{}
}

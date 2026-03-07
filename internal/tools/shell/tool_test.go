package shell

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"builder/internal/tools"
)

func decodeStringToolOutput(t *testing.T, result tools.Result) string {
	t.Helper()
	var out string
	if err := json.Unmarshal(result.Output, &out); err != nil {
		t.Fatalf("decode string output: %v", err)
	}
	return out
}

func waitForManagerCount(t *testing.T, manager *Manager, want int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if manager.Count() == want {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("manager count = %d, want %d", manager.Count(), want)
}

func envSliceToMap(t *testing.T, in []string) map[string]string {
	t.Helper()
	out := make(map[string]string, len(in))
	for _, entry := range in {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || key == "" {
			t.Fatalf("invalid env entry: %q", entry)
		}
		if _, exists := out[key]; exists {
			t.Fatalf("duplicate env key: %s", key)
		}
		out[key] = value
	}
	return out
}

func TestShellRunsAndMergesOutput(t *testing.T) {
	tool := New(".", 10_000)
	input, _ := json.Marshal(map[string]any{"command": "echo out && echo err 1>&2"})

	result, err := tool.Call(context.Background(), tools.Call{ID: "1", Name: tools.ToolShell, Input: input})
	if err != nil {
		t.Fatalf("call error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %s", string(result.Output))
	}

	var payload struct {
		ExitCode int    `json:"exit_code"`
		Output   string `json:"output"`
	}
	if err := json.Unmarshal(result.Output, &payload); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	if payload.ExitCode != 0 {
		t.Fatalf("exit code = %d, want 0", payload.ExitCode)
	}
	if !strings.Contains(payload.Output, "out") || !strings.Contains(payload.Output, "err") {
		t.Fatalf("merged output missing stdout/stderr: %q", payload.Output)
	}
}

func TestShellAcceptsCmdAlias(t *testing.T) {
	tool := New(".", 10_000)
	input, _ := json.Marshal(map[string]any{"cmd": "echo from-cmd"})

	result, err := tool.Call(context.Background(), tools.Call{ID: "cmd-alias", Name: tools.ToolShell, Input: input})
	if err != nil {
		t.Fatalf("call error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %s", string(result.Output))
	}

	var payload struct {
		ExitCode int    `json:"exit_code"`
		Output   string `json:"output"`
	}
	if err := json.Unmarshal(result.Output, &payload); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	if payload.ExitCode != 0 {
		t.Fatalf("exit code = %d, want 0", payload.ExitCode)
	}
	if !strings.Contains(payload.Output, "from-cmd") {
		t.Fatalf("expected cmd alias output, got %q", payload.Output)
	}
}

func TestShellOutputJSONDoesNotEscapeOperators(t *testing.T) {
	tool := New(".", 10_000)
	input, _ := json.Marshal(map[string]any{"command": "printf 'a => b < c & d\\n'"})

	result, err := tool.Call(context.Background(), tools.Call{ID: "operators", Name: tools.ToolShell, Input: input})
	if err != nil {
		t.Fatalf("call error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %s", string(result.Output))
	}

	raw := string(result.Output)
	if strings.Contains(raw, `\u003e`) || strings.Contains(raw, `\u003c`) || strings.Contains(raw, `\u0026`) {
		t.Fatalf("expected unescaped operators in JSON payload, got %q", raw)
	}
	if !strings.Contains(raw, "=>") || !strings.Contains(raw, "<") || !strings.Contains(raw, "&") {
		t.Fatalf("expected decoded operators in JSON payload, got %q", raw)
	}
}

func TestShellTimeout(t *testing.T) {
	tool := New(".", 10_000)
	input, _ := json.Marshal(map[string]any{"command": "sleep 2", "timeout_seconds": 1})

	result, err := tool.Call(context.Background(), tools.Call{ID: "2", Name: tools.ToolShell, Input: input})
	if err != nil {
		t.Fatalf("call error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected launch error: %s", string(result.Output))
	}

	var payload struct {
		ExitCode int `json:"exit_code"`
	}
	if err := json.Unmarshal(result.Output, &payload); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	if payload.ExitCode != 124 {
		t.Fatalf("exit code = %d, want 124 timeout", payload.ExitCode)
	}
}

func TestEnrichEnvOverridesNonInteractiveDefaults(t *testing.T) {
	env := envSliceToMap(t, enrichEnv([]string{
		"TERM=xterm-256color",
		"GIT_EDITOR=vim",
		"PAGER=less",
		"NO_COLOR=0",
		"KEEP=1",
	}))

	if env["TERM"] != "dumb" {
		t.Fatalf("TERM = %q, want dumb", env["TERM"])
	}
	if env["GIT_EDITOR"] != ":" {
		t.Fatalf("GIT_EDITOR = %q, want :", env["GIT_EDITOR"])
	}
	if env["PAGER"] != "cat" {
		t.Fatalf("PAGER = %q, want cat", env["PAGER"])
	}
	if env["NO_COLOR"] != "1" {
		t.Fatalf("NO_COLOR = %q, want 1", env["NO_COLOR"])
	}
	if env["GIT_TERMINAL_PROMPT"] != "0" {
		t.Fatalf("GIT_TERMINAL_PROMPT = %q, want 0", env["GIT_TERMINAL_PROMPT"])
	}
	if env["KEEP"] != "1" {
		t.Fatalf("KEEP = %q, want 1", env["KEEP"])
	}
}

func TestSanitizeOutputStripsANSIAndControlSequences(t *testing.T) {
	in := "\x1b[31mred\x1b[0m\r\nline2\a\b\tok\rline3"
	out := sanitizeOutput(in)

	if strings.Contains(out, "\x1b[") {
		t.Fatalf("output still contains ANSI escape: %q", out)
	}
	if strings.ContainsAny(out, "\a\b\r") {
		t.Fatalf("output still contains control chars: %q", out)
	}
	if !strings.Contains(out, "red\nline2\tok\nline3") {
		t.Fatalf("sanitized output mismatch: %q", out)
	}
}

func TestTruncateBannerUsesByteWording(t *testing.T) {
	in := strings.Repeat("a", headTailSize+headTailSize+10)
	out, truncated, removed := truncate(in, 100)
	if !truncated {
		t.Fatal("expected truncation")
	}
	if removed <= 0 {
		t.Fatalf("expected positive removed bytes, got %d", removed)
	}
	if !strings.Contains(out, "omitted ") || !strings.Contains(out, " bytes.") {
		t.Fatalf("expected byte-based truncation banner, output = %q", out)
	}
}

func TestExecCommandMovesToBackgroundAndPollsToCompletion(t *testing.T) {
	workspace := t.TempDir()
	manager, err := NewManager()
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	execTool := NewExecCommandTool(workspace, 16_000, manager)
	pollTool := NewWriteStdinTool(16_000, manager)

	execInput, _ := json.Marshal(map[string]any{
		"cmd":           "sleep 1; echo done",
		"shell":         "/bin/sh",
		"login":         false,
		"yield_time_ms": 250,
	})
	result, err := execTool.Call(context.Background(), tools.Call{ID: "bg-1", Name: tools.ToolExecCommand, Input: execInput})
	if err != nil {
		t.Fatalf("exec_command call error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected exec_command error: %s", string(result.Output))
	}
	text := decodeStringToolOutput(t, result)
	if !strings.Contains(text, "Process moved to background.") {
		t.Fatalf("expected background message, got %q", text)
	}
	if !strings.Contains(text, "session ID 1000") {
		t.Fatalf("expected numeric session id, got %q", text)
	}
	if manager.Count() != 1 {
		t.Fatalf("manager count = %d, want 1", manager.Count())
	}

	pollInput, _ := json.Marshal(map[string]any{
		"session_id":    1000,
		"yield_time_ms": 2_000,
	})
	pollResult, err := pollTool.Call(context.Background(), tools.Call{ID: "bg-2", Name: tools.ToolWriteStdin, Input: pollInput})
	if err != nil {
		t.Fatalf("write_stdin call error: %v", err)
	}
	if pollResult.IsError {
		t.Fatalf("unexpected write_stdin error: %s", string(pollResult.Output))
	}
	pollText := decodeStringToolOutput(t, pollResult)
	if !strings.Contains(pollText, "Process exited with code 0") {
		t.Fatalf("expected exit code in poll output, got %q", pollText)
	}
	if !strings.Contains(pollText, "done") {
		t.Fatalf("expected command output in poll output, got %q", pollText)
	}
	waitForManagerCount(t, manager, 0, 3*time.Second)
}

func TestWriteStdinSendsInputToInteractiveProcess(t *testing.T) {
	workspace := t.TempDir()
	manager, err := NewManager()
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	execTool := NewExecCommandTool(workspace, 16_000, manager)
	stdinTool := NewWriteStdinTool(16_000, manager)

	execInput, _ := json.Marshal(map[string]any{
		"cmd":           "read line; echo $line",
		"shell":         "/bin/sh",
		"login":         false,
		"tty":           true,
		"yield_time_ms": 250,
	})
	result, err := execTool.Call(context.Background(), tools.Call{ID: "tty-1", Name: tools.ToolExecCommand, Input: execInput})
	if err != nil {
		t.Fatalf("exec_command call error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected exec_command error: %s", string(result.Output))
	}
	text := decodeStringToolOutput(t, result)
	if !strings.Contains(text, "Process moved to background.") {
		t.Fatalf("expected background message, got %q", text)
	}

	stdinInput, _ := json.Marshal(map[string]any{
		"session_id":    1000,
		"chars":         "hello builder\n",
		"yield_time_ms": 2_000,
	})
	stdinResult, err := stdinTool.Call(context.Background(), tools.Call{ID: "tty-2", Name: tools.ToolWriteStdin, Input: stdinInput})
	if err != nil {
		t.Fatalf("write_stdin call error: %v", err)
	}
	if stdinResult.IsError {
		t.Fatalf("unexpected write_stdin error: %s", string(stdinResult.Output))
	}
	stdinText := decodeStringToolOutput(t, stdinResult)
	if !strings.Contains(stdinText, "Process exited with code 0") {
		t.Fatalf("expected exit code in stdin output, got %q", stdinText)
	}
	if !strings.Contains(stdinText, "hello builder") {
		t.Fatalf("expected echoed stdin in output, got %q", stdinText)
	}
	waitForManagerCount(t, manager, 0, 3*time.Second)
}

func TestExecCommandClosesStdinForNonInteractiveProcess(t *testing.T) {
	workspace := t.TempDir()
	manager, err := NewManager()
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	events := make(chan Event, 1)
	manager.SetEventHandler(func(evt Event) {
		select {
		case events <- evt:
		default:
		}
	})
	execTool := NewExecCommandTool(workspace, 16_000, manager)

	execInput, _ := json.Marshal(map[string]any{
		"cmd":           "if read line; then echo line:$line; else echo eof; fi",
		"shell":         "/bin/sh",
		"login":         false,
		"yield_time_ms": 1_500,
	})
	result, err := execTool.Call(context.Background(), tools.Call{ID: "eof-1", Name: tools.ToolExecCommand, Input: execInput})
	if err != nil {
		t.Fatalf("exec_command call error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected exec_command error: %s", string(result.Output))
	}
	text := decodeStringToolOutput(t, result)
	if strings.Contains(text, "Process moved to background.") {
		t.Fatalf("expected immediate completion with closed stdin, got %q", text)
	}
	if !strings.Contains(text, "Process exited with code 0") {
		t.Fatalf("expected exit code in output, got %q", text)
	}
	if !strings.Contains(text, "eof") {
		t.Fatalf("expected EOF branch output, got %q", text)
	}
	waitForManagerCount(t, manager, 0, 3*time.Second)
	select {
	case evt := <-events:
		t.Fatalf("did not expect foreground exec_command event, got %+v", evt)
	default:
	}
}

func TestManagerCloseKillsRunningProcesses(t *testing.T) {
	manager, err := NewManager()
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	events := make(chan Event, 1)
	manager.SetEventHandler(func(evt Event) {
		if evt.Type == EventKilled {
			select {
			case events <- evt:
			default:
			}
		}
	})

	result, err := manager.Start(context.Background(), ExecRequest{
		Command:        []string{"/bin/sh", "-c", "trap '' TERM INT; sleep 30"},
		DisplayCommand: "trap '' TERM INT; sleep 30",
		Workdir:        t.TempDir(),
		YieldTime:      250 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("start background process: %v", err)
	}
	if !result.MovedToBackground || !result.Running {
		t.Fatalf("expected background process, got %+v", result)
	}
	if manager.Count() != 1 {
		t.Fatalf("manager count = %d, want 1", manager.Count())
	}

	start := time.Now()
	if err := manager.Close(); err != nil {
		t.Fatalf("close manager: %v", err)
	}
	if elapsed := time.Since(start); elapsed > closeWaitTimeout+time.Second {
		t.Fatalf("close took too long: %v", elapsed)
	}

	select {
	case evt := <-events:
		if evt.Snapshot.ID != result.SessionID {
			t.Fatalf("killed event id = %s, want %s", evt.Snapshot.ID, result.SessionID)
		}
		if evt.Snapshot.State != "killed" {
			t.Fatalf("killed event state = %s, want killed", evt.Snapshot.State)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for killed event")
	}
	waitForManagerCount(t, manager, 0, 3*time.Second)
}

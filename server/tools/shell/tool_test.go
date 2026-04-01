package shell

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"builder/server/tools"
)

func decodeStringToolOutput(t *testing.T, result tools.Result) string {
	t.Helper()
	var out string
	if err := json.Unmarshal(result.Output, &out); err == nil {
		return out
	}
	var wrapped struct {
		Output string `json:"output"`
	}
	if err := json.Unmarshal(result.Output, &wrapped); err != nil {
		t.Fatalf("decode string output: %v", err)
	}
	return wrapped.Output
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

func newBackgroundTestManager(t *testing.T) *Manager {
	t.Helper()
	manager, err := NewManager(WithMinimumExecToBgTime(250 * time.Millisecond))
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	return manager
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

func TestManagerSubscribeOutputStreamsTailAndEndsAtEOF(t *testing.T) {
	manager := newBackgroundTestManager(t)
	workspace := t.TempDir()

	result, err := manager.Start(context.Background(), ExecRequest{
		Command:        []string{"sh", "-c", "printf 'hello\\n'; sleep 0.4; printf 'world\\n'"},
		DisplayCommand: "tail-test",
		Workdir:        workspace,
		YieldTime:      250 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if !result.Backgrounded {
		t.Fatalf("expected backgrounded process, got %+v", result)
	}

	sub, err := manager.SubscribeOutput(context.Background(), result.SessionID, 0)
	if err != nil {
		t.Fatalf("SubscribeOutput: %v", err)
	}
	defer func() { _ = sub.Close() }()

	first, err := sub.Next(context.Background())
	if err != nil {
		t.Fatalf("first Next: %v", err)
	}
	if first.ProcessID != result.SessionID || !strings.Contains(first.Text, "hello") {
		t.Fatalf("unexpected first chunk: %+v", first)
	}

	second, err := sub.Next(context.Background())
	if err != nil {
		t.Fatalf("second Next: %v", err)
	}
	if second.OffsetBytes <= first.OffsetBytes || !strings.Contains(second.Text, "world") {
		t.Fatalf("unexpected second chunk: %+v", second)
	}

	if _, err := sub.Next(context.Background()); err != io.EOF {
		t.Fatalf("expected EOF after process exit, got %v", err)
	}

	tailSub, err := manager.SubscribeOutput(context.Background(), result.SessionID, second.OffsetBytes+int64(len([]byte("world\n"))))
	if err != nil {
		t.Fatalf("SubscribeOutput from tail: %v", err)
	}
	defer func() { _ = tailSub.Close() }()
	if _, err := tailSub.Next(context.Background()); err != io.EOF {
		t.Fatalf("expected EOF for tail subscription at end, got %v", err)
	}
}

func TestTruncateBackgroundOutputBannerReferencesLogFile(t *testing.T) {
	in := strings.Repeat("a", headTailSize+headTailSize+10)
	out, truncated, removed := truncateBackgroundOutput(in, 100)
	if !truncated {
		t.Fatal("expected truncation")
	}
	if removed <= 0 {
		t.Fatalf("expected positive removed bytes, got %d", removed)
	}
	if !strings.Contains(out, "Omitted ") || !strings.Contains(out, "read log file for details") {
		t.Fatalf("expected background truncation banner to point to the log file, output = %q", out)
	}
	if strings.Contains(out, "Consider using more targeted commands") {
		t.Fatalf("did not expect foreground truncation guidance in background output, got %q", out)
	}
}

func TestTruncateDoesNotDuplicateWholeOutputWhenShorterThanHeadTailWindow(t *testing.T) {
	in := strings.Repeat("x", 543)
	out, truncated, removed := truncate(in, 80)
	if !truncated {
		t.Fatal("expected truncation")
	}
	if removed <= 0 {
		t.Fatalf("expected positive removed bytes, got %d", removed)
	}
	if strings.Contains(out, "omitted -") {
		t.Fatalf("did not expect negative omitted bytes, got %q", out)
	}
	if strings.Count(out, in) > 0 {
		t.Fatalf("did not expect full input duplicated in output, got %q", out)
	}
	headLen, tailLen := truncationSegmentLengths(len(in), 80)
	wantMax := headLen + tailLen + truncationBannerLen(removed)
	if got := len(out); got > wantMax {
		t.Fatalf("expected bounded truncated output <= %d bytes, got %d", wantMax, got)
	}
	if len(out) >= len(in) {
		t.Fatalf("expected truncated output smaller than input, got out=%d in=%d", len(out), len(in))
	}
}

func TestExecCommandMovesToBackgroundAndPollsToCompletion(t *testing.T) {
	workspace := t.TempDir()
	manager := newBackgroundTestManager(t)
	execTool := NewExecCommandTool(workspace, 16_000, manager, "")
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
	if strings.Contains(text, "Wall time:") {
		t.Fatalf("did not expect wall time for still-running background shell, got %q", text)
	}
	if strings.Contains(text, "Log file:") {
		t.Fatalf("did not expect log file for still-running background shell, got %q", text)
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
	if !strings.Contains(pollText, "Wall time:") {
		t.Fatalf("expected wall time once backgrounded shell completed, got %q", pollText)
	}
	if !strings.Contains(pollText, "Log file:") {
		t.Fatalf("expected log file once backgrounded shell completed, got %q", pollText)
	}
	if !strings.Contains(pollText, "done") {
		t.Fatalf("expected command output in poll output, got %q", pollText)
	}
	waitForManagerCount(t, manager, 0, 3*time.Second)
}

func TestExecCommandClampsShortYieldTimeSilently(t *testing.T) {
	workspace := t.TempDir()
	manager, err := NewManager(WithMinimumExecToBgTime(1500 * time.Millisecond))
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	execTool := NewExecCommandTool(workspace, 16_000, manager, "")

	execInput, _ := json.Marshal(map[string]any{
		"cmd":           "sleep 1; echo done",
		"shell":         "/bin/sh",
		"login":         false,
		"yield_time_ms": 250,
	})
	result, err := execTool.Call(context.Background(), tools.Call{ID: "clamp-1", Name: tools.ToolExecCommand, Input: execInput})
	if err != nil {
		t.Fatalf("exec_command call error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected exec_command error: %s", string(result.Output))
	}
	text := decodeStringToolOutput(t, result)
	if strings.Contains(text, "Warning: yield_time_ms below the minimum exec-to-background time") {
		t.Fatalf("did not expect clamp warning, got %q", text)
	}
	if strings.Contains(text, "Process moved to background.") {
		t.Fatalf("expected command to stay foreground after clamp, got %q", text)
	}
	if !strings.Contains(text, "Process exited with code 0") {
		t.Fatalf("expected exit code in output, got %q", text)
	}
	if !strings.Contains(text, "done") {
		t.Fatalf("expected command output, got %q", text)
	}
	if manager.Count() != 0 {
		t.Fatalf("manager count = %d, want 0", manager.Count())
	}
}

func TestNormalizeExecYieldTimeDoesNotCapConfiguredMinimum(t *testing.T) {
	manager, err := NewManager(WithMinimumExecToBgTime(45 * time.Second))
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	t.Cleanup(func() { _ = manager.Close() })

	yieldTime := manager.normalizeExecYieldTime(250 * time.Millisecond)
	if yieldTime != 45*time.Second {
		t.Fatalf("yield time = %s, want %s", yieldTime, 45*time.Second)
	}

	yieldTime = manager.normalizeExecYieldTime(50 * time.Second)
	if yieldTime != 50*time.Second {
		t.Fatalf("yield time = %s, want %s", yieldTime, 50*time.Second)
	}

	yieldTime = manager.normalizeExecYieldTime(0)
	if yieldTime != 45*time.Second {
		t.Fatalf("yield time = %s, want %s for zero input", yieldTime, 45*time.Second)
	}
}

func TestNormalizeWriteYieldTimeDoesNotCapLongPolls(t *testing.T) {
	yieldTime := normalizeWriteYieldTime(5*time.Minute, defaultWriteYieldTime)
	if yieldTime != 5*time.Minute {
		t.Fatalf("yield time = %s, want %s", yieldTime, 5*time.Minute)
	}

	yieldTime = normalizeWriteYieldTime(100*time.Millisecond, defaultWriteYieldTime)
	if yieldTime != minWriteYieldTime {
		t.Fatalf("yield time = %s, want %s for short input", yieldTime, minWriteYieldTime)
	}

	yieldTime = normalizeWriteYieldTime(0, defaultWriteYieldTime)
	if yieldTime != defaultWriteYieldTime {
		t.Fatalf("yield time = %s, want %s for zero input", yieldTime, defaultWriteYieldTime)
	}
}

func TestWriteStdinPollHonorsRequestedDuration(t *testing.T) {
	workspace := t.TempDir()
	manager := newBackgroundTestManager(t)
	execTool := NewExecCommandTool(workspace, 16_000, manager, "")
	pollTool := NewWriteStdinTool(16_000, manager)

	execInput, _ := json.Marshal(map[string]any{
		"cmd":           "sleep 3",
		"shell":         "/bin/sh",
		"login":         false,
		"yield_time_ms": 250,
	})
	result, err := execTool.Call(context.Background(), tools.Call{ID: "poll-duration-exec", Name: tools.ToolExecCommand, Input: execInput})
	if err != nil {
		t.Fatalf("exec_command call error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected exec_command error: %s", string(result.Output))
	}

	pollInput, _ := json.Marshal(map[string]any{
		"session_id":        1000,
		"yield_time_ms":     1200,
		"max_output_tokens": 32,
	})
	start := time.Now()
	pollResult, err := pollTool.Call(context.Background(), tools.Call{ID: "poll-duration-poll", Name: tools.ToolWriteStdin, Input: pollInput})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("write_stdin call error: %v", err)
	}
	if pollResult.IsError {
		t.Fatalf("unexpected write_stdin error: %s", string(pollResult.Output))
	}
	if elapsed < time.Second {
		t.Fatalf("poll returned too early: %s", elapsed)
	}
	if elapsed > 2500*time.Millisecond {
		t.Fatalf("poll took too long: %s", elapsed)
	}

	var payload writeStdinOutput
	if err := json.Unmarshal(pollResult.Output, &payload); err != nil {
		t.Fatalf("decode write_stdin output: %v", err)
	}
	if !payload.BackgroundRunning {
		t.Fatalf("expected session to still be running after requested poll window, got %+v", payload)
	}
	if !payload.Backgrounded {
		t.Fatalf("expected session to remain backgrounded, got %+v", payload)
	}
	waitForManagerCount(t, manager, 0, 4*time.Second)
}

func TestExecCommandForegroundTruncationUsesForegroundBanner(t *testing.T) {
	workspace := t.TempDir()
	manager := newBackgroundTestManager(t)
	execTool := NewExecCommandTool(workspace, 16_000, manager, "")

	execInput, _ := json.Marshal(map[string]any{
		"cmd":               "i=0; while [ $i -lt 400 ]; do printf x; i=$((i+1)); done",
		"shell":             "/bin/sh",
		"login":             false,
		"yield_time_ms":     2_000,
		"max_output_tokens": 10,
	})
	result, err := execTool.Call(context.Background(), tools.Call{ID: "fg-trunc-1", Name: tools.ToolExecCommand, Input: execInput})
	if err != nil {
		t.Fatalf("exec_command call error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected exec_command error: %s", string(result.Output))
	}
	text := decodeStringToolOutput(t, result)
	if !strings.Contains(text, "Output is very large, omitted ") {
		t.Fatalf("expected foreground truncation banner, got %q", text)
	}
	if strings.Contains(text, "read log file for details") {
		t.Fatalf("did not expect background truncation guidance in foreground output, got %q", text)
	}
	if strings.Contains(text, "Log file:") {
		t.Fatalf("did not expect log file in foreground output, got %q", text)
	}
	if strings.Contains(text, "Process moved to background.") {
		t.Fatalf("expected immediate completion, got %q", text)
	}
	if manager.Count() != 0 {
		t.Fatalf("manager count = %d, want 0", manager.Count())
	}
}

func TestExecCommandUsesBackgroundTruncationBannerWhenPreviewIsCut(t *testing.T) {
	workspace := t.TempDir()
	manager := newBackgroundTestManager(t)
	execTool := NewExecCommandTool(workspace, 16_000, manager, "")
	pollTool := NewWriteStdinTool(16_000, manager)

	execInput, _ := json.Marshal(map[string]any{
		"cmd":               "i=0; while [ $i -lt 400 ]; do printf x; i=$((i+1)); done; sleep 1",
		"shell":             "/bin/sh",
		"login":             false,
		"yield_time_ms":     250,
		"max_output_tokens": 10,
	})
	result, err := execTool.Call(context.Background(), tools.Call{ID: "bg-trunc-1", Name: tools.ToolExecCommand, Input: execInput})
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
	if !strings.Contains(text, "Omitted ") {
		t.Fatalf("expected background truncation banner, got %q", text)
	}
	if !strings.Contains(text, "read log file for details") {
		t.Fatalf("expected background truncation banner to reference the log file, got %q", text)
	}
	if strings.Contains(text, "Consider using more targeted commands") {
		t.Fatalf("did not expect foreground truncation guidance in background output, got %q", text)
	}
	if strings.Contains(text, "Log file:") {
		t.Fatalf("did not expect log file for still-running background shell, got %q", text)
	}

	pollInput, _ := json.Marshal(map[string]any{
		"session_id":    1000,
		"yield_time_ms": 2_000,
	})
	pollResult, err := pollTool.Call(context.Background(), tools.Call{ID: "bg-trunc-2", Name: tools.ToolWriteStdin, Input: pollInput})
	if err != nil {
		t.Fatalf("write_stdin call error: %v", err)
	}
	if pollResult.IsError {
		t.Fatalf("unexpected write_stdin error: %s", string(pollResult.Output))
	}
	waitForManagerCount(t, manager, 0, 3*time.Second)
}

func TestWriteStdinSendsInputToInteractiveProcess(t *testing.T) {
	workspace := t.TempDir()
	manager := newBackgroundTestManager(t)
	execTool := NewExecCommandTool(workspace, 16_000, manager, "")
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
	if strings.Contains(text, "Wall time:") {
		t.Fatalf("did not expect wall time for still-running interactive shell, got %q", text)
	}
	if strings.Contains(text, "Log file:") {
		t.Fatalf("did not expect log file for still-running interactive shell, got %q", text)
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
	if !strings.Contains(stdinText, "Wall time:") {
		t.Fatalf("expected wall time once interactive background shell completed, got %q", stdinText)
	}
	if !strings.Contains(stdinText, "Log file:") {
		t.Fatalf("expected log file once interactive background shell completed, got %q", stdinText)
	}
	if !strings.Contains(stdinText, "hello builder") {
		t.Fatalf("expected echoed stdin in output, got %q", stdinText)
	}
	waitForManagerCount(t, manager, 0, 3*time.Second)
}

func TestWriteStdinUsesBackgroundTruncationBannerOnCompletion(t *testing.T) {
	workspace := t.TempDir()
	manager := newBackgroundTestManager(t)
	execTool := NewExecCommandTool(workspace, 16_000, manager, "")
	stdinTool := NewWriteStdinTool(16_000, manager)

	execInput, _ := json.Marshal(map[string]any{
		"cmd":           "read line; printf '%s' \"$line\"",
		"shell":         "/bin/sh",
		"login":         false,
		"tty":           true,
		"yield_time_ms": 250,
	})
	result, err := execTool.Call(context.Background(), tools.Call{ID: "tty-trunc-1", Name: tools.ToolExecCommand, Input: execInput})
	if err != nil {
		t.Fatalf("exec_command call error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected exec_command error: %s", string(result.Output))
	}

	stdinInput, _ := json.Marshal(map[string]any{
		"session_id":        1000,
		"chars":             strings.Repeat("x", 400) + "\n",
		"yield_time_ms":     2_000,
		"max_output_tokens": 10,
	})
	stdinResult, err := stdinTool.Call(context.Background(), tools.Call{ID: "tty-trunc-2", Name: tools.ToolWriteStdin, Input: stdinInput})
	if err != nil {
		t.Fatalf("write_stdin call error: %v", err)
	}
	if stdinResult.IsError {
		t.Fatalf("unexpected write_stdin error: %s", string(stdinResult.Output))
	}
	stdinText := decodeStringToolOutput(t, stdinResult)
	if !strings.Contains(stdinText, "Omitted ") {
		t.Fatalf("expected background truncation banner, got %q", stdinText)
	}
	if !strings.Contains(stdinText, "read log file for details") {
		t.Fatalf("expected background truncation banner to reference the log file, got %q", stdinText)
	}
	if strings.Contains(stdinText, "Consider using more targeted commands") {
		t.Fatalf("did not expect foreground truncation guidance in background output, got %q", stdinText)
	}
	if !strings.Contains(stdinText, "Log file:") {
		t.Fatalf("expected completed background shell response to include log file, got %q", stdinText)
	}
	waitForManagerCount(t, manager, 0, 3*time.Second)
}

func TestWriteStdinCompletionSuppressesBackgroundNoticeEvent(t *testing.T) {
	workspace := t.TempDir()
	manager := newBackgroundTestManager(t)
	execTool := NewExecCommandTool(workspace, 16_000, manager, "")
	pollTool := NewWriteStdinTool(16_000, manager)
	events := make(chan Event, 2)
	manager.SetEventHandler(func(evt Event) {
		if evt.Type == EventCompleted || evt.Type == EventKilled {
			select {
			case events <- evt:
			default:
			}
		}
	})

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

	select {
	case evt := <-events:
		if !evt.NoticeSuppressed {
			t.Fatalf("expected completion event notice to be suppressed after write_stdin harvest, got %+v", evt)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for completion event")
	}
	waitForManagerCount(t, manager, 0, 3*time.Second)
}

func TestExecCommandClosesStdinForNonInteractiveProcess(t *testing.T) {
	workspace := t.TempDir()
	manager := newBackgroundTestManager(t)
	events := make(chan Event, 1)
	manager.SetEventHandler(func(evt Event) {
		select {
		case events <- evt:
		default:
		}
	})
	execTool := NewExecCommandTool(workspace, 16_000, manager, "")

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
	if strings.Contains(text, "Wall time:") {
		t.Fatalf("did not expect wall time for foreground shell, got %q", text)
	}
	if strings.Contains(text, "Log file:") {
		t.Fatalf("did not expect log file for foreground shell, got %q", text)
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
	manager, err := NewManager(WithMinimumExecToBgTime(250 * time.Millisecond))
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

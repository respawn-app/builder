package shell

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"builder/server/tools"
	"builder/server/tools/shell/postprocess"
	"builder/shared/config"
	"builder/shared/toolspec"
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

func assertBackgroundTransitionMessage(t *testing.T, text, sessionID string) {
	t.Helper()
	want := "Process moved to background with ID " + sessionID + "."
	if !strings.Contains(text, want) {
		t.Fatalf("expected compact background transition message %q, got %q", want, text)
	}
	if strings.Contains(text, "Process running with session ID "+sessionID) {
		t.Fatalf("did not expect legacy session-id line after background transition, got %q", text)
	}
}

func assertBackgroundTransitionMessageWithOutput(t *testing.T, text, sessionID string) {
	t.Helper()
	want := "Process moved to background with ID " + sessionID + ". Output:"
	if !strings.Contains(text, want) {
		t.Fatalf("expected inline background transition output header %q, got %q", want, text)
	}
	if strings.Contains(text, "Process moved to background with ID "+sessionID+".\n") {
		t.Fatalf("did not expect output header split across lines, got %q", text)
	}
	if strings.Contains(text, "Process running with session ID "+sessionID) {
		t.Fatalf("did not expect legacy session-id line after background transition, got %q", text)
	}
}

func writeExecutableScript(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "hook.sh")
	if err := os.WriteFile(path, []byte(contents), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
	return path
}

func newBackgroundTestManager(t *testing.T) *Manager {
	t.Helper()
	manager, err := NewManager(WithMinimumExecToBgTime(250*time.Millisecond), WithCloseTimeouts(20*time.Millisecond, 200*time.Millisecond))
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

func TestEnrichEnvOverridesNonInteractiveDefaults(t *testing.T) {
	env := envSliceToMap(t, enrichEnv([]string{
		"TERM=xterm-256color",
		"AGENT=other",
		"GIT_EDITOR=vim",
		"PAGER=less",
		"NO_COLOR=0",
		"DOCKER_CLI_HINTS=true",
		"BUILDKIT_PROGRESS=auto",
		"COMPOSE_PROGRESS=auto",
		"COMPOSE_ANSI=always",
		"npm_config_progress=true",
		"YARN_ENABLE_PROGRESS_BARS=true",
		"KEEP=1",
	}))

	if env["TERM"] != "dumb" {
		t.Fatalf("TERM = %q, want dumb", env["TERM"])
	}
	if env["AGENT"] != "builder" {
		t.Fatalf("AGENT = %q, want builder", env["AGENT"])
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
	if env["DOCKER_CLI_HINTS"] != "false" {
		t.Fatalf("DOCKER_CLI_HINTS = %q, want false", env["DOCKER_CLI_HINTS"])
	}
	if env["BUILDKIT_PROGRESS"] != "plain" {
		t.Fatalf("BUILDKIT_PROGRESS = %q, want plain", env["BUILDKIT_PROGRESS"])
	}
	if env["COMPOSE_PROGRESS"] != "plain" {
		t.Fatalf("COMPOSE_PROGRESS = %q, want plain", env["COMPOSE_PROGRESS"])
	}
	if env["COMPOSE_ANSI"] != "never" {
		t.Fatalf("COMPOSE_ANSI = %q, want never", env["COMPOSE_ANSI"])
	}
	if env["npm_config_progress"] != "false" {
		t.Fatalf("npm_config_progress = %q, want false", env["npm_config_progress"])
	}
	if env["YARN_ENABLE_PROGRESS_BARS"] != "false" {
		t.Fatalf("YARN_ENABLE_PROGRESS_BARS = %q, want false", env["YARN_ENABLE_PROGRESS_BARS"])
	}
	if env["KEEP"] != "1" {
		t.Fatalf("KEEP = %q, want 1", env["KEEP"])
	}
}

func TestEnrichEnvAddsManagedRGConfigPathWhenAvailable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if _, _, err := config.EnsureManagedRGConfigFile(); err != nil {
		t.Fatalf("ensure managed rg config file: %v", err)
	}

	env := envSliceToMap(t, enrichEnv([]string{"KEEP=1"}))
	want := filepath.Join(home, ".builder", "rg.conf")
	if env["RIPGREP_CONFIG_PATH"] != want {
		t.Fatalf("RIPGREP_CONFIG_PATH = %q, want %q", env["RIPGREP_CONFIG_PATH"], want)
	}
}

func TestEnrichEnvKeepsUserRIPGREPConfigPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if _, _, err := config.EnsureManagedRGConfigFile(); err != nil {
		t.Fatalf("ensure managed rg config file: %v", err)
	}

	env := envSliceToMap(t, enrichEnv([]string{"RIPGREP_CONFIG_PATH=/tmp/user-rg.conf"}))
	if env["RIPGREP_CONFIG_PATH"] != "/tmp/user-rg.conf" {
		t.Fatalf("RIPGREP_CONFIG_PATH = %q, want /tmp/user-rg.conf", env["RIPGREP_CONFIG_PATH"])
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
		Command:        []string{"sh", "-c", "printf 'hello\\n'; sleep 0.3; printf 'world\\n'"},
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
	if second.OffsetBytes <= first.OffsetBytes || second.NextOffsetBytes <= second.OffsetBytes || !strings.Contains(second.Text, "world") {
		t.Fatalf("unexpected second chunk: %+v", second)
	}

	if _, err := sub.Next(context.Background()); err != io.EOF {
		t.Fatalf("expected EOF after process exit, got %v", err)
	}

	tailSub, err := manager.SubscribeOutput(context.Background(), result.SessionID, second.NextOffsetBytes)
	if err != nil {
		t.Fatalf("SubscribeOutput from tail: %v", err)
	}
	defer func() { _ = tailSub.Close() }()
	if _, err := tailSub.Next(context.Background()); err != io.EOF {
		t.Fatalf("expected EOF for tail subscription at end, got %v", err)
	}
}

func TestManagerSubscribeOutputRejectsInvalidOffset(t *testing.T) {
	manager := newBackgroundTestManager(t)
	if _, err := manager.SubscribeOutput(context.Background(), "proc-1", -1); err == nil {
		t.Fatal("expected invalid offset error")
	}
}

func TestManagerSubscribeOutputRejectsUnknownProcess(t *testing.T) {
	manager := newBackgroundTestManager(t)
	if _, err := manager.SubscribeOutput(context.Background(), "missing", 0); err == nil {
		t.Fatal("expected unknown process error")
	}
}

func TestManagerSubscribeOutputCloseUnblocksNext(t *testing.T) {
	manager := newBackgroundTestManager(t)
	workspace := t.TempDir()

	result, err := manager.Start(context.Background(), ExecRequest{
		Command:        []string{"sh", "-c", "sleep 1"},
		DisplayCommand: "tail-close-test",
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

	done := make(chan error, 1)
	go func() {
		_, err := sub.Next(context.Background())
		done <- err
	}()

	time.Sleep(20 * time.Millisecond)
	if err := sub.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case err := <-done:
		if err != io.EOF {
			t.Fatalf("expected EOF after Close, got %v", err)
		}
	case <-time.After(300 * time.Millisecond):
		t.Fatal("timed out waiting for Next to unblock after Close")
	}
	_ = manager.Kill(result.SessionID)
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
		"cmd":           "sleep 0.3; echo done",
		"shell":         "/bin/sh",
		"login":         false,
		"yield_time_ms": 250,
	})
	result, err := execTool.Call(context.Background(), tools.Call{ID: "bg-1", Name: toolspec.ToolExecCommand, Input: execInput})
	if err != nil {
		t.Fatalf("exec_command call error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected exec_command error: %s", string(result.Output))
	}
	text := decodeStringToolOutput(t, result)
	assertBackgroundTransitionMessage(t, text, "1000")
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
		"yield_time_ms": 800,
	})
	pollResult, err := pollTool.Call(context.Background(), tools.Call{ID: "bg-2", Name: toolspec.ToolWriteStdin, Input: pollInput})
	if err != nil {
		t.Fatalf("write_stdin call error: %v", err)
	}
	if pollResult.IsError {
		t.Fatalf("unexpected write_stdin error: %s", string(pollResult.Output))
	}
	pollText := decodeStringToolOutput(t, pollResult)
	if !strings.Contains(pollText, "Exit code 0, output:") {
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
	waitForManagerCount(t, manager, 0, time.Second)
}

func TestExecCommandExportsAgentEnv(t *testing.T) {
	workspace := t.TempDir()
	manager := newBackgroundTestManager(t)
	execTool := NewExecCommandTool(workspace, 16_000, manager, "")

	execInput, _ := json.Marshal(map[string]any{
		"cmd":           "printf '%s' \"$AGENT\"",
		"shell":         "/bin/sh",
		"login":         false,
		"yield_time_ms": 1_000,
	})
	result, err := execTool.Call(context.Background(), tools.Call{ID: "agent-env", Name: toolspec.ToolExecCommand, Input: execInput})
	if err != nil {
		t.Fatalf("exec_command call error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected exec_command error: %s", string(result.Output))
	}
	if got := decodeStringToolOutput(t, result); !strings.Contains(got, "builder") {
		t.Fatalf("expected AGENT=builder in shell output, got %q", got)
	}
}

func TestExecCommandBackgroundProcessExportsAgentEnv(t *testing.T) {
	workspace := t.TempDir()
	manager := newBackgroundTestManager(t)
	execTool := NewExecCommandTool(workspace, 16_000, manager, "")
	pollTool := NewWriteStdinTool(16_000, manager)

	execInput, _ := json.Marshal(map[string]any{
		"cmd":           "sleep 0.35; printf '%s' \"$AGENT\"",
		"shell":         "/bin/sh",
		"login":         false,
		"yield_time_ms": 250,
	})
	result, err := execTool.Call(context.Background(), tools.Call{ID: "agent-env-bg-start", Name: toolspec.ToolExecCommand, Input: execInput})
	if err != nil {
		t.Fatalf("exec_command call error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected exec_command error: %s", string(result.Output))
	}
	if got := decodeStringToolOutput(t, result); !strings.Contains(got, "Process moved to background with ID 1000.") {
		t.Fatalf("expected background transition, got %q", got)
	}

	pollInput, _ := json.Marshal(map[string]any{
		"session_id":    1000,
		"yield_time_ms": 800,
	})
	pollResult, err := pollTool.Call(context.Background(), tools.Call{ID: "agent-env-bg-poll", Name: toolspec.ToolWriteStdin, Input: pollInput})
	if err != nil {
		t.Fatalf("write_stdin call error: %v", err)
	}
	if pollResult.IsError {
		t.Fatalf("unexpected write_stdin error: %s", string(pollResult.Output))
	}
	if got := decodeStringToolOutput(t, pollResult); !strings.Contains(got, "builder") {
		t.Fatalf("expected AGENT=builder in background shell output, got %q", got)
	}
}

func TestExecCommandAppliesUserHookOutput(t *testing.T) {
	workspace := t.TempDir()
	hookPath := writeExecutableScript(t, "#!/bin/sh\nif [ \"$AGENT\" != builder ]; then printf '{\"processed\":true,\"replaced_output\":\"MISSING_AGENT\"}'; exit 0; fi\nprintf '{\"processed\":true,\"replaced_output\":\"HOOKED\"}\n'")
	manager, err := NewManager(
		WithMinimumExecToBgTime(250*time.Millisecond),
		WithCloseTimeouts(20*time.Millisecond, 200*time.Millisecond),
		WithPostprocessor(postprocess.NewRunner(postprocess.Settings{Mode: config.ShellPostprocessingModeUser, HookPath: hookPath})),
	)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	execTool := NewExecCommandTool(workspace, 16_000, manager, "")

	execInput, _ := json.Marshal(map[string]any{
		"cmd":           "printf raw",
		"shell":         "/bin/sh",
		"login":         false,
		"yield_time_ms": 5_000,
	})
	result, err := execTool.Call(context.Background(), tools.Call{ID: "hooked", Name: toolspec.ToolExecCommand, Input: execInput})
	if err != nil {
		t.Fatalf("exec_command call error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected exec_command error: %s", string(result.Output))
	}
	if got := decodeStringToolOutput(t, result); got != "HOOKED" {
		t.Fatalf("output = %q, want HOOKED", got)
	}
}

func TestWriteStdinWarnsAndRetriesWhenFullLogReadFails(t *testing.T) {
	workspace := t.TempDir()
	manager := newBackgroundTestManager(t)
	pollTool := NewWriteStdinTool(16_000, manager)

	result, err := manager.Start(context.Background(), ExecRequest{
		Command:        []string{"sh", "-c", "sleep 0.35; printf done"},
		DisplayCommand: "delayed-done",
		Workdir:        workspace,
		YieldTime:      250 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !result.Backgrounded {
		t.Fatalf("expected backgrounded result, got %+v", result)
	}
	logPath := result.OutputPath
	backupPath := logPath + ".bak"
	sessionID, err := strconv.Atoi(result.SessionID)
	if err != nil {
		t.Fatalf("parse session id: %v", err)
	}

	time.Sleep(500 * time.Millisecond)
	if err := os.Rename(logPath, backupPath); err != nil {
		t.Fatalf("rename log away: %v", err)
	}

	pollInput, _ := json.Marshal(map[string]any{
		"session_id":    sessionID,
		"yield_time_ms": 20,
	})
	first, err := pollTool.Call(context.Background(), tools.Call{ID: "log-missing-1", Name: toolspec.ToolWriteStdin, Input: pollInput})
	if err != nil {
		t.Fatalf("first write_stdin call error: %v", err)
	}
	if first.IsError {
		t.Fatalf("unexpected first write_stdin error: %s", string(first.Output))
	}
	firstText := decodeStringToolOutput(t, first)
	if !strings.Contains(firstText, "failed to read full output log") {
		t.Fatalf("expected full-log warning, got %q", firstText)
	}

	if err := os.Rename(backupPath, logPath); err != nil {
		t.Fatalf("restore log: %v", err)
	}
	second, err := pollTool.Call(context.Background(), tools.Call{ID: "log-missing-2", Name: toolspec.ToolWriteStdin, Input: pollInput})
	if err != nil {
		t.Fatalf("second write_stdin call error: %v", err)
	}
	if second.IsError {
		t.Fatalf("unexpected second write_stdin error: %s", string(second.Output))
	}
	secondText := decodeStringToolOutput(t, second)
	if strings.Contains(secondText, "failed to read full output log") {
		t.Fatalf("did not expect warning after log restored, got %q", secondText)
	}
	if !strings.Contains(secondText, "done") {
		t.Fatalf("expected restored full output, got %q", secondText)
	}
}

func TestExecCommandClampsShortYieldTimeSilently(t *testing.T) {
	workspace := t.TempDir()
	manager, err := NewManager(WithMinimumExecToBgTime(150 * time.Millisecond))
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	execTool := NewExecCommandTool(workspace, 16_000, manager, "")

	execInput, _ := json.Marshal(map[string]any{
		"cmd":           "sleep 0.1; echo done",
		"shell":         "/bin/sh",
		"login":         false,
		"yield_time_ms": 20,
	})
	result, err := execTool.Call(context.Background(), tools.Call{ID: "clamp-1", Name: toolspec.ToolExecCommand, Input: execInput})
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
	if !strings.Contains(text, "Exit code 0, output:") {
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
		"cmd":           "sleep 0.8",
		"shell":         "/bin/sh",
		"login":         false,
		"yield_time_ms": 250,
	})
	result, err := execTool.Call(context.Background(), tools.Call{ID: "poll-duration-exec", Name: toolspec.ToolExecCommand, Input: execInput})
	if err != nil {
		t.Fatalf("exec_command call error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected exec_command error: %s", string(result.Output))
	}

	pollInput, _ := json.Marshal(map[string]any{
		"session_id":        1000,
		"yield_time_ms":     300,
		"max_output_tokens": 32,
	})
	start := time.Now()
	pollResult, err := pollTool.Call(context.Background(), tools.Call{ID: "poll-duration-poll", Name: toolspec.ToolWriteStdin, Input: pollInput})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("write_stdin call error: %v", err)
	}
	if pollResult.IsError {
		t.Fatalf("unexpected write_stdin error: %s", string(pollResult.Output))
	}
	if elapsed < 250*time.Millisecond {
		t.Fatalf("poll returned too early: %s", elapsed)
	}
	if elapsed > time.Second {
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
	waitForManagerCount(t, manager, 0, 2*time.Second)
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
	result, err := execTool.Call(context.Background(), tools.Call{ID: "fg-trunc-1", Name: toolspec.ToolExecCommand, Input: execInput})
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
		"cmd":               "i=0; while [ $i -lt 400 ]; do printf x; i=$((i+1)); done; sleep 0.3",
		"shell":             "/bin/sh",
		"login":             false,
		"yield_time_ms":     250,
		"max_output_tokens": 10,
	})
	result, err := execTool.Call(context.Background(), tools.Call{ID: "bg-trunc-1", Name: toolspec.ToolExecCommand, Input: execInput})
	if err != nil {
		t.Fatalf("exec_command call error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected exec_command error: %s", string(result.Output))
	}
	text := decodeStringToolOutput(t, result)
	assertBackgroundTransitionMessageWithOutput(t, text, "1000")
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
		"yield_time_ms": 800,
	})
	pollResult, err := pollTool.Call(context.Background(), tools.Call{ID: "bg-trunc-2", Name: toolspec.ToolWriteStdin, Input: pollInput})
	if err != nil {
		t.Fatalf("write_stdin call error: %v", err)
	}
	if pollResult.IsError {
		t.Fatalf("unexpected write_stdin error: %s", string(pollResult.Output))
	}
	waitForManagerCount(t, manager, 0, time.Second)
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
	result, err := execTool.Call(context.Background(), tools.Call{ID: "tty-1", Name: toolspec.ToolExecCommand, Input: execInput})
	if err != nil {
		t.Fatalf("exec_command call error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected exec_command error: %s", string(result.Output))
	}
	text := decodeStringToolOutput(t, result)
	assertBackgroundTransitionMessage(t, text, "1000")
	if strings.Contains(text, "Wall time:") {
		t.Fatalf("did not expect wall time for still-running interactive shell, got %q", text)
	}
	if strings.Contains(text, "Log file:") {
		t.Fatalf("did not expect log file for still-running interactive shell, got %q", text)
	}

	stdinInput, _ := json.Marshal(map[string]any{
		"session_id":    1000,
		"chars":         "hello builder\n",
		"yield_time_ms": 800,
	})
	stdinResult, err := stdinTool.Call(context.Background(), tools.Call{ID: "tty-2", Name: toolspec.ToolWriteStdin, Input: stdinInput})
	if err != nil {
		t.Fatalf("write_stdin call error: %v", err)
	}
	if stdinResult.IsError {
		t.Fatalf("unexpected write_stdin error: %s", string(stdinResult.Output))
	}
	stdinText := decodeStringToolOutput(t, stdinResult)
	if !strings.Contains(stdinText, "Exit code 0, output:") {
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
	waitForManagerCount(t, manager, 0, time.Second)
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
	result, err := execTool.Call(context.Background(), tools.Call{ID: "tty-trunc-1", Name: toolspec.ToolExecCommand, Input: execInput})
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
	stdinResult, err := stdinTool.Call(context.Background(), tools.Call{ID: "tty-trunc-2", Name: toolspec.ToolWriteStdin, Input: stdinInput})
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
		"cmd":           "sleep 0.3; echo done",
		"shell":         "/bin/sh",
		"login":         false,
		"yield_time_ms": 250,
	})
	result, err := execTool.Call(context.Background(), tools.Call{ID: "bg-1", Name: toolspec.ToolExecCommand, Input: execInput})
	if err != nil {
		t.Fatalf("exec_command call error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected exec_command error: %s", string(result.Output))
	}

	pollInput, _ := json.Marshal(map[string]any{
		"session_id":    1000,
		"yield_time_ms": 800,
	})
	pollResult, err := pollTool.Call(context.Background(), tools.Call{ID: "bg-2", Name: toolspec.ToolWriteStdin, Input: pollInput})
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
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for completion event")
	}
	waitForManagerCount(t, manager, 0, time.Second)
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
	result, err := execTool.Call(context.Background(), tools.Call{ID: "eof-1", Name: toolspec.ToolExecCommand, Input: execInput})
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
	if !strings.Contains(text, "Exit code 0, output:") {
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
	manager, err := NewManager(WithMinimumExecToBgTime(250*time.Millisecond), WithCloseTimeouts(20*time.Millisecond, 200*time.Millisecond))
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
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
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
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for killed event")
	}
	waitForManagerCount(t, manager, 0, time.Second)
}

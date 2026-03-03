package shell

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"builder/internal/tools"
)

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

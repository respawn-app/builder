package shell

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"builder/internal/tools"
)

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

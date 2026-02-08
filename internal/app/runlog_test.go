package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"builder/internal/llm"
	"builder/internal/runtime"
	"builder/internal/tools"
)

func TestRunLoggerWritesStepsFile(t *testing.T) {
	dir := t.TempDir()
	logger, err := newRunLogger(dir)
	if err != nil {
		t.Fatalf("newRunLogger failed: %v", err)
	}
	logger.Logf("step.start user_chars=%d", 10)
	logger.Logf("step.error err=%q", "boom")
	if err := logger.Close(); err != nil {
		t.Fatalf("close logger: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, runLogFileName))
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, "step.start user_chars=10") {
		t.Fatalf("missing step start log: %q", text)
	}
	if !strings.Contains(text, `step.error err="boom"`) {
		t.Fatalf("missing step error log: %q", text)
	}
}

func TestFormatRuntimeEventIncludesToolMetadata(t *testing.T) {
	call := llm.ToolCall{ID: "call-1", Name: "shell"}
	line := formatRuntimeEvent(runtime.Event{
		Kind:     runtime.EventToolCallStarted,
		StepID:   "step-1",
		ToolCall: &call,
	})
	if !strings.Contains(line, "call_id=call-1") || !strings.Contains(line, "name=shell") {
		t.Fatalf("unexpected event line: %q", line)
	}

	res := tools.Result{CallID: "call-1", Name: tools.ToolShell, IsError: true}
	line = formatRuntimeEvent(runtime.Event{
		Kind:       runtime.EventToolCallCompleted,
		StepID:     "step-1",
		ToolResult: &res,
	})
	if !strings.Contains(line, "is_error=true") {
		t.Fatalf("unexpected completion line: %q", line)
	}

	line = formatRuntimeEvent(runtime.Event{
		Kind:   runtime.EventInFlightClearFailed,
		StepID: "step-2",
		Error:  "mark in-flight false: write failed",
	})
	if !strings.Contains(line, "kind=in_flight_clear_failed") || !strings.Contains(line, `err="mark in-flight false: write failed"`) {
		t.Fatalf("unexpected in-flight clear failure line: %q", line)
	}
}

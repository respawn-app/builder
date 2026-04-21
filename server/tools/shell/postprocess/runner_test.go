package postprocess

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"builder/shared/config"
	"builder/shared/toolspec"
)

func TestRunnerBuiltinGoTestSuccessCollapsesToPass(t *testing.T) {
	runner := NewRunner(Settings{Mode: config.ShellPostprocessingModeBuiltin})
	exitCode := 0
	result, err := runner.Apply(context.Background(), Request{
		ToolName:    toolspec.ToolExecCommand,
		CommandText: "go test ./...",
		ExitCode:    &exitCode,
		Output:      "raw go test output",
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !result.Processed {
		t.Fatal("expected builtin processor to handle successful go test")
	}
	if result.Output != "PASS" {
		t.Fatalf("output = %q, want PASS", result.Output)
	}
}

func TestRunnerRawBypassesBuiltinProcessing(t *testing.T) {
	runner := NewRunner(Settings{Mode: config.ShellPostprocessingModeBuiltin})
	exitCode := 0
	result, err := runner.Apply(context.Background(), Request{
		ToolName:    toolspec.ToolExecCommand,
		CommandText: "go test ./...",
		ExitCode:    &exitCode,
		Raw:         true,
		Output:      "raw go test output",
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if result.Processed {
		t.Fatal("expected raw request to bypass postprocessing")
	}
	if result.Output != "raw go test output" {
		t.Fatalf("output = %q, want raw output", result.Output)
	}
}

func TestRunnerUserHookReplacesOutput(t *testing.T) {
	hookPath := writeHookScript(t, "#!/bin/sh\nprintf '{\"processed\":true,\"replaced_output\":\"HOOKED\"}\n'")
	runner := NewRunner(Settings{Mode: config.ShellPostprocessingModeUser, HookPath: hookPath})
	result, err := runner.Apply(context.Background(), Request{
		ToolName:    toolspec.ToolExecCommand,
		CommandText: "printf hi",
		Output:      "hi",
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !result.Processed {
		t.Fatal("expected user hook to mark output processed")
	}
	if result.Output != "HOOKED" {
		t.Fatalf("output = %q, want HOOKED", result.Output)
	}
}

func TestRunnerAllModeFallsBackToBuiltinWhenHookFails(t *testing.T) {
	hookPath := writeHookScript(t, "#!/bin/sh\nprintf 'not-json\n'")
	runner := NewRunner(Settings{Mode: config.ShellPostprocessingModeAll, HookPath: hookPath})
	exitCode := 0
	result, err := runner.Apply(context.Background(), Request{
		ToolName:    toolspec.ToolExecCommand,
		CommandText: "go test ./...",
		ExitCode:    &exitCode,
		Output:      "raw go test output",
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if result.Output != "PASS" {
		t.Fatalf("output = %q, want PASS", result.Output)
	}
	if !result.Processed {
		t.Fatal("expected builtin fallback to remain processed")
	}
}

func TestRunnerUserModeBrokenHookFallsBackToOriginal(t *testing.T) {
	hookPath := writeHookScript(t, "#!/bin/sh\nprintf 'not-json\n'")
	runner := NewRunner(Settings{Mode: config.ShellPostprocessingModeUser, HookPath: hookPath})
	result, err := runner.Apply(context.Background(), Request{
		ToolName:    toolspec.ToolExecCommand,
		CommandText: "printf hi",
		Output:      "hi",
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if result.Processed {
		t.Fatal("expected broken user hook to fall back to original output")
	}
	if result.Output != "hi" {
		t.Fatalf("output = %q, want hi", result.Output)
	}
}

func writeHookScript(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "hook.sh")
	if err := os.WriteFile(path, []byte(contents), 0o755); err != nil {
		t.Fatalf("write hook: %v", err)
	}
	return path
}

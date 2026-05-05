package postprocess

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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
		Output:      "PASS\nok\texample.com/postprocess\t0.123s\n",
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

func TestRunnerBuiltinGoTestPreservesDetailedOutput(t *testing.T) {
	runner := NewRunner(Settings{Mode: config.ShellPostprocessingModeBuiltin})
	exitCode := 0
	tests := []struct {
		name        string
		commandText string
		parsedArgs  []string
		output      string
	}{
		{
			name:        "benchmark",
			commandText: "go test -bench=. ./...",
			parsedArgs:  []string{"go", "test", "-bench=.", "./..."},
			output:      "PASS\nBenchmarkFoo\t100\t123 ns/op\nok\texample.com/postprocess\t0.123s\n",
		},
		{
			name:        "coverage",
			commandText: "go test -cover ./...",
			parsedArgs:  []string{"go", "test", "-cover", "./..."},
			output:      "PASS\ncoverage: 81.2% of statements\nok\texample.com/postprocess\t0.123s\n",
		},
		{
			name:        "json",
			commandText: "go test -json ./...",
			parsedArgs:  []string{"go", "test", "-json", "./..."},
			output:      "{\"Time\":\"2026-04-23T00:00:00Z\",\"Action\":\"pass\",\"Package\":\"example.com/postprocess\"}\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := runner.Apply(context.Background(), Request{
				ToolName:    toolspec.ToolExecCommand,
				CommandText: tt.commandText,
				ParsedArgs:  tt.parsedArgs,
				CommandName: "go",
				ExitCode:    &exitCode,
				Output:      tt.output,
			})
			if err != nil {
				t.Fatalf("Apply: %v", err)
			}
			if result.Processed {
				t.Fatalf("expected %s output to bypass collapse", tt.name)
			}
			if result.Output != tt.output {
				t.Fatalf("output = %q, want original output", result.Output)
			}
		})
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

func TestRunnerUserHookInheritsOwnerSessionID(t *testing.T) {
	hookPath := writeHookScript(t, `#!/bin/sh
printf '{"processed":true,"replaced_output":"%s"}' "$BUILDER_SESSION_ID"
`)
	runner := NewRunner(Settings{Mode: config.ShellPostprocessingModeUser, HookPath: hookPath})
	result, err := runner.Apply(context.Background(), Request{
		ToolName:       toolspec.ToolExecCommand,
		CommandText:    "printf hi",
		OwnerSessionID: "session-hook-123",
		Output:         "hi",
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !result.Processed {
		t.Fatal("expected user hook to mark output processed")
	}
	if result.Output != "session-hook-123" {
		t.Fatalf("output = %q, want session-hook-123", result.Output)
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
		Output:      "PASS\nok\texample.com/postprocess\t0.123s\n",
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

func TestRunnerUserHookCancellationPropagates(t *testing.T) {
	hookPath := writeHookScript(t, "#!/bin/sh\nsleep 5\n")
	runner := NewRunner(Settings{Mode: config.ShellPostprocessingModeUser, HookPath: hookPath})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := runner.Apply(ctx, Request{
		ToolName:    toolspec.ToolExecCommand,
		CommandText: "printf hi",
		Output:      "hi",
	})
	if err == nil {
		t.Fatal("expected canceled context to propagate")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want errors.Is(..., context.Canceled)", err)
	}
}

func TestRunnerUserHookFailureWarningTruncatesStderr(t *testing.T) {
	hookPath := writeHookScript(t, "#!/bin/sh\ni=0\nwhile [ \"$i\" -lt 5000 ]; do\n  printf 'xxxxxxxxxx' 1>&2\n  i=$((i + 1))\ndone\nexit 1\n")
	runner := NewRunner(Settings{Mode: config.ShellPostprocessingModeUser, HookPath: hookPath})
	result, err := runner.Apply(context.Background(), Request{
		ToolName:    toolspec.ToolExecCommand,
		CommandText: "printf hi",
		Output:      "hi",
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !strings.Contains(result.Warning, "[hook output truncated]") {
		t.Fatalf("expected truncated stderr marker, got %q", result.Warning)
	}
	if len(result.Warning) > maxHookOutputBytes+512 {
		t.Fatalf("expected bounded warning length, got %d", len(result.Warning))
	}
}

func writeHookScript(t *testing.T, contents string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("hook tests require POSIX shell")
	}
	path := filepath.Join(t.TempDir(), "hook.sh")
	if err := os.WriteFile(path, []byte(contents), 0o755); err != nil {
		t.Fatalf("write hook: %v", err)
	}
	return path
}

func TestRunnerAllModeAccumulatesWarnings(t *testing.T) {
	missingHookPath := filepath.Join(t.TempDir(), "missing-hook")
	runner := NewRunner(Settings{Mode: config.ShellPostprocessingModeAll, HookPath: missingHookPath})
	runner.processors = []Processor{warningProcessor{}}
	result, err := runner.Apply(context.Background(), Request{
		ToolName:    toolspec.ToolExecCommand,
		CommandText: "printf hi",
		Output:      "hi",
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !strings.Contains(result.Warning, "builtin warning") || !strings.Contains(result.Warning, "command postprocess hook failed:") {
		t.Fatalf("warning = %q, want both warnings", result.Warning)
	}
}

type warningProcessor struct{}

func (warningProcessor) ID() string { return "test/warning" }

func (warningProcessor) Process(context.Context, Request) (Result, error) {
	return Result{Output: "hi", Warning: "builtin warning"}, nil
}

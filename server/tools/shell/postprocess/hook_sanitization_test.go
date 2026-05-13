package postprocess

import (
	"context"
	"testing"

	"builder/shared/config"
	"builder/shared/toolspec"
)

func TestRunnerUserHookReceivesSanitizedOutput(t *testing.T) {
	hookPath := writeHookScript(t, `#!/bin/sh
payload=$(cat)
case "$payload" in
  *'\u001b'*) printf '{"processed":true,"replaced_output":"RAW"}' ;;
  *) printf '{"processed":true,"replaced_output":"SANITIZED"}' ;;
esac
`)
	runner := NewRunner(Settings{Mode: config.ShellPostprocessingModeUser, HookPath: hookPath})
	result, err := runner.Apply(context.Background(), Request{
		ToolName:    toolspec.ToolExecCommand,
		CommandText: "printf color",
		Output:      "\x1b[31mcolor\x1b[0m",
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if result.Output != "SANITIZED" {
		t.Fatalf("output = %q, want SANITIZED", result.Output)
	}
}

package postprocess

import (
	"context"
	"strings"

	"builder/shared/toolspec"
)

type goTestSuccessProcessor struct{}

func (goTestSuccessProcessor) ID() string {
	return "builtin/go-test-pass"
}

func (goTestSuccessProcessor) Process(_ context.Context, req Request) (Result, error) {
	if req.ToolName != toolspec.ToolExecCommand {
		return Result{Output: req.Output}, nil
	}
	if req.ExitCode == nil || *req.ExitCode != 0 {
		return Result{Output: req.Output}, nil
	}
	if len(req.ParsedArgs) < 2 {
		return Result{Output: req.Output}, nil
	}
	if req.CommandName != "go" || strings.TrimSpace(req.ParsedArgs[1]) != "test" {
		return Result{Output: req.Output}, nil
	}
	return Result{Output: "PASS", Processed: true, ProcessorID: "builtin/go-test-pass"}, nil
}

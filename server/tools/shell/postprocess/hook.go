package postprocess

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"builder/shared/toolspec"
)

type hookRequest struct {
	ToolName        toolspec.ID `json:"tool_name"`
	Command         string      `json:"command"`
	ParsedArgs      []string    `json:"parsed_args,omitempty"`
	CommandName     string      `json:"command_name,omitempty"`
	Workdir         string      `json:"workdir,omitempty"`
	OriginalOutput  string      `json:"original_output"`
	CurrentOutput   string      `json:"current_output"`
	ExitCode        *int        `json:"exit_code,omitempty"`
	Backgrounded    bool        `json:"backgrounded,omitempty"`
	MaxDisplayChars int         `json:"max_display_chars,omitempty"`
}

type hookResponse struct {
	Processed      bool   `json:"processed"`
	ReplacedOutput string `json:"replaced_output,omitempty"`
}

func (r *Runner) applyHook(ctx context.Context, req Request, originalOutput string, currentOutput string) (Result, error) {
	hookPath, ok := resolveHookPath(r.hookPath)
	if !ok {
		return Result{Output: currentOutput, Warning: "command postprocess hook unavailable"}, nil
	}
	payload, err := json.Marshal(hookRequest{
		ToolName:        req.ToolName,
		Command:         req.CommandText,
		ParsedArgs:      append([]string(nil), req.ParsedArgs...),
		CommandName:     req.CommandName,
		Workdir:         req.Workdir,
		OriginalOutput:  originalOutput,
		CurrentOutput:   currentOutput,
		ExitCode:        cloneIntPtr(req.ExitCode),
		Backgrounded:    req.Backgrounded,
		MaxDisplayChars: req.MaxDisplayChars,
	})
	if err != nil {
		return Result{Output: currentOutput, Warning: "command postprocess hook request encode failed"}, nil
	}

	cmd := exec.CommandContext(ctx, hookPath)
	cmd.Stdin = bytes.NewReader(payload)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return Result{}, ctx.Err()
		}
		return Result{Output: currentOutput, Warning: hookFailureWarning(err, stderr.String())}, nil
	}

	var response hookResponse
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		return Result{Output: currentOutput, Warning: "command postprocess hook returned invalid JSON"}, nil
	}
	if !response.Processed {
		return Result{Output: currentOutput}, nil
	}
	return Result{Output: response.ReplacedOutput, Processed: true, ProcessorID: "user/hook"}, nil
}

func hookFailureWarning(err error, stderr string) string {
	trimmed := strings.TrimSpace(stderr)
	if trimmed == "" {
		return fmt.Sprintf("command postprocess hook failed: %v", err)
	}
	return fmt.Sprintf("command postprocess hook failed: %v: %s", err, trimmed)
}

func cloneIntPtr(in *int) *int {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}

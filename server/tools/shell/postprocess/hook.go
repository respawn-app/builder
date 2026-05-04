package postprocess

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"builder/server/tools/shell/shellenv"
	"builder/shared/toolspec"
)

const (
	hookTimeout        = 5 * time.Second
	maxHookOutputBytes = 32 * 1024
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

	timeoutCtx, cancel := context.WithTimeout(ctx, hookTimeout)
	defer cancel()

	cmd := exec.CommandContext(timeoutCtx, hookPath)
	cmd.Env = shellenv.EnrichForSession(os.Environ(), req.OwnerSessionID)
	cmd.Stdin = bytes.NewReader(payload)
	stdout := newLimitedBuffer(maxHookOutputBytes)
	stderr := newLimitedBuffer(maxHookOutputBytes)
	cmd.Stdout = stdout
	cmd.Stderr = stderr

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

type limitedBuffer struct {
	buffer    bytes.Buffer
	remaining int64
	truncated bool
}

func newLimitedBuffer(limit int64) *limitedBuffer {
	if limit <= 0 {
		limit = maxHookOutputBytes
	}
	return &limitedBuffer{remaining: limit}
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	written := len(p)
	if b.remaining <= 0 {
		b.truncated = true
		return written, nil
	}
	chunk := p
	if int64(len(chunk)) > b.remaining {
		chunk = chunk[:int(b.remaining)]
		b.truncated = true
	}
	_, _ = b.buffer.Write(chunk)
	b.remaining -= int64(len(chunk))
	return written, nil
}

func (b *limitedBuffer) Bytes() []byte {
	return b.buffer.Bytes()
}

func (b *limitedBuffer) String() string {
	text := b.buffer.String()
	if b.truncated {
		return text + "\n[hook output truncated]"
	}
	return text
}

var _ io.Writer = (*limitedBuffer)(nil)

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

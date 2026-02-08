package bash

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"time"

	"builder/internal/tools"
)

const (
	defaultTimeout = 5 * time.Minute
	maxTimeout     = time.Hour
	defaultLimit   = 10_000
	headTailSize   = 500
)

type input struct {
	Command        string `json:"command"`
	TimeoutSeconds *int   `json:"timeout_seconds,omitempty"`
	Workdir        string `json:"workdir,omitempty"`
}

type output struct {
	ExitCode        int    `json:"exit_code"`
	Output          string `json:"output"`
	Truncated       bool   `json:"truncated"`
	TruncationBytes int    `json:"truncation_bytes,omitempty"`
}

type Tool struct {
	workspaceRoot  string
	outputLimit    int
	defaultTimeout time.Duration
}

type Option func(*Tool)

func WithDefaultTimeout(timeout time.Duration) Option {
	return func(t *Tool) {
		if timeout > 0 {
			t.defaultTimeout = timeout
		}
	}
}

func New(workspaceRoot string, outputLimit int, opts ...Option) *Tool {
	if outputLimit <= 0 {
		outputLimit = defaultLimit
	}
	t := &Tool{workspaceRoot: workspaceRoot, outputLimit: outputLimit, defaultTimeout: defaultTimeout}
	for _, opt := range opts {
		if opt != nil {
			opt(t)
		}
	}
	return t
}

func (t *Tool) Name() tools.ID {
	return tools.ToolBash
}

func (t *Tool) Call(ctx context.Context, c tools.Call) (tools.Result, error) {
	var in input
	if err := json.Unmarshal(c.Input, &in); err != nil {
		return resultErr(c, fmt.Sprintf("invalid input: %v", err)), nil
	}
	if in.Command == "" {
		return resultErr(c, "command is required"), nil
	}

	timeout := t.defaultTimeout
	if in.TimeoutSeconds != nil {
		requested := time.Duration(*in.TimeoutSeconds) * time.Second
		if requested <= 0 {
			return resultErr(c, "timeout_seconds must be positive"), nil
		}
		if requested > maxTimeout {
			requested = maxTimeout
		}
		timeout = requested
	}

	workdir := t.workspaceRoot
	if in.Workdir != "" {
		if filepath.IsAbs(in.Workdir) {
			workdir = in.Workdir
		} else {
			workdir = filepath.Join(t.workspaceRoot, in.Workdir)
		}
	}

	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}

	cmd := exec.Command(shell, "-lc", in.Command)
	cmd.Dir = workdir
	cmd.Env = enrichEnv(os.Environ())

	var merged bytes.Buffer
	cmd.Stdout = &merged
	cmd.Stderr = &merged

	if err := cmd.Start(); err != nil {
		return resultErr(c, fmt.Sprintf("failed to launch command: %v", err)), nil
	}

	waitCh := make(chan error, 1)
	go func() {
		waitCh <- cmd.Wait()
	}()

	var err error
	var timedOut atomic.Bool
	select {
	case err = <-waitCh:
	case <-callCtx.Done():
		if callCtx.Err() == context.DeadlineExceeded {
			timedOut.Store(true)
		}
		if cmd.Process != nil {
			_ = cmd.Process.Signal(os.Interrupt)
		}
		select {
		case err = <-waitCh:
		case <-time.After(10 * time.Second):
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
			err = <-waitCh
		}
	}

	exitCode := 0
	if timedOut.Load() {
		exitCode = 124
	}
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			if timedOut.Load() {
				exitCode = 124
			} else {
				exitCode = ee.ExitCode()
				if exitCode == -1 {
					exitCode = 130
				}
			}
		} else if timedOut.Load() {
			exitCode = 124
		} else if errors.Is(callCtx.Err(), context.Canceled) {
			exitCode = 130
		} else {
			return resultErr(c, fmt.Sprintf("failed to launch command: %v", err)), nil
		}
	}

	raw := merged.String()
	display, truncated, removed := truncate(raw, t.outputLimit)

	body, marshalErr := json.Marshal(output{
		ExitCode:        exitCode,
		Output:          display,
		Truncated:       truncated,
		TruncationBytes: removed,
	})
	if marshalErr != nil {
		return tools.Result{}, marshalErr
	}
	return tools.Result{CallID: c.ID, Name: c.Name, Output: body}, nil
}

func resultErr(c tools.Call, msg string) tools.Result {
	body, _ := json.Marshal(map[string]any{"error": msg})
	return tools.Result{CallID: c.ID, Name: c.Name, Output: body, IsError: true}
}

func enrichEnv(base []string) []string {
	return append(base,
		"TERM=dumb",
		"CI=1",
		"NO_COLOR=1",
		"CLICOLOR=0",
	)
}

func truncate(s string, maxLen int) (string, bool, int) {
	if len(s) <= maxLen {
		return s, false, 0
	}
	head := s
	if len(head) > headTailSize {
		head = head[:headTailSize]
	}
	tail := s
	if len(tail) > headTailSize {
		tail = tail[len(tail)-headTailSize:]
	}
	removed := len(s) - len(head) - len(tail)
	return fmt.Sprintf("%s\n\n...[truncated %d bytes]...\n\n%s", head, removed, tail), true, removed
}

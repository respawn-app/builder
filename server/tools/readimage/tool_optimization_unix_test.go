//go:build darwin || linux

package readimage

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"builder/server/tools"
	"builder/shared/toolspec"
)

func TestCall_FIFOPathReturnsToolError(t *testing.T) {
	workspace := t.TempDir()
	fifoPath := filepath.Join(workspace, "pipe.png")
	if err := syscall.Mkfifo(fifoPath, 0o644); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}

	tool, err := New(workspace, true)
	if err != nil {
		t.Fatalf("new tool: %v", err)
	}

	result, err := tool.Call(context.Background(), tools.Call{
		ID:    "call-fifo",
		Name:  toolspec.ToolViewImage,
		Input: json.RawMessage(`{"path":"pipe.png"}`),
	})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected FIFO path to be rejected")
	}
	if got := toolError(t, result); !strings.Contains(got, "not a regular file") {
		t.Fatalf("expected regular file error, got %q", got)
	}
}

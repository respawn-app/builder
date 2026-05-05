package fsguard

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultUserDeniedIncludesRejectionInstruction(t *testing.T) {
	workspace := t.TempDir()
	real, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		t.Fatalf("resolve workspace: %v", err)
	}
	info, err := os.Stat(real)
	if err != nil {
		t.Fatalf("stat workspace: %v", err)
	}
	outside := filepath.Join(t.TempDir(), "outside.txt")
	guard := New(
		workspace,
		real,
		info,
		true,
		false,
		func(context.Context, Request) (Approval, error) {
			return Approval{Decision: DecisionDeny, Commentary: "no"}, nil
		},
		nil,
		nil,
		"ask user for a safe path",
		ErrorLabels{OutsidePath: "outside"},
		FailureFactory{},
		nil,
		nil,
	)

	_, err = guard.Allow(context.Background(), outside, outside, nil)
	if err == nil {
		t.Fatal("expected denial error")
	}
	if got := err.Error(); !strings.Contains(got, "no") || !strings.Contains(got, "ask user for a safe path") {
		t.Fatalf("denial error = %q, want commentary and instruction", got)
	}
}

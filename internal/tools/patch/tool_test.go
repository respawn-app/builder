package patch

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"builder/internal/tools"
)

func TestRejectDeleteBlockAtomically(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(target, []byte("keep\n"), 0o644); err != nil {
		t.Fatalf("write seed file: %v", err)
	}

	tool, err := New(dir, true)
	if err != nil {
		t.Fatalf("new patch tool: %v", err)
	}

	patchText := "*** Begin Patch\n*** Delete File: a.txt\n*** End Patch\n"
	input, _ := json.Marshal(map[string]any{"patch": patchText})
	result, err := tool.Call(context.Background(), tools.Call{ID: "1", Name: "patch", Input: input})
	if err != nil {
		t.Fatalf("patch call error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected tool error result")
	}

	data, readErr := os.ReadFile(target)
	if readErr != nil {
		t.Fatalf("read target: %v", readErr)
	}
	if string(data) != "keep\n" {
		t.Fatalf("file mutated on delete rejection: %q", string(data))
	}
}

func TestAddUpdateMove(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "one.txt")
	if err := os.WriteFile(src, []byte("line1\nline2\n"), 0o644); err != nil {
		t.Fatalf("seed source: %v", err)
	}

	tool, err := New(dir, true)
	if err != nil {
		t.Fatalf("new patch tool: %v", err)
	}

	patchText := "*** Begin Patch\n*** Add File: new.txt\n+hello\n*** Update File: one.txt\n*** Move to: moved.txt\n line1\n-line2\n+line2-updated\n*** End Patch\n"
	input, _ := json.Marshal(map[string]any{"patch": patchText})
	result, err := tool.Call(context.Background(), tools.Call{ID: "2", Name: "patch", Input: input})
	if err != nil {
		t.Fatalf("patch call error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got %s", string(result.Output))
	}

	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Fatalf("old path still exists")
	}
	moved, err := os.ReadFile(filepath.Join(dir, "moved.txt"))
	if err != nil {
		t.Fatalf("read moved file: %v", err)
	}
	if string(moved) != "line1\nline2-updated\n" {
		t.Fatalf("unexpected moved contents: %q", string(moved))
	}
	added, err := os.ReadFile(filepath.Join(dir, "new.txt"))
	if err != nil {
		t.Fatalf("read added file: %v", err)
	}
	if string(added) != "hello\n" {
		t.Fatalf("unexpected added contents: %q", string(added))
	}
}

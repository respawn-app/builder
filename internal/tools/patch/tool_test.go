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
	result, err := tool.Call(context.Background(), tools.Call{ID: "1", Name: tools.ToolPatch, Input: input})
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
	result, err := tool.Call(context.Background(), tools.Call{ID: "2", Name: tools.ToolPatch, Input: input})
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

func TestAddFileInNewDirectory(t *testing.T) {
	dir := t.TempDir()
	tool, err := New(dir, true)
	if err != nil {
		t.Fatalf("new patch tool: %v", err)
	}

	patchText := "*** Begin Patch\n*** Add File: nested/new/file.txt\n+hello\n*** End Patch\n"
	input, _ := json.Marshal(map[string]any{"patch": patchText})
	result, err := tool.Call(context.Background(), tools.Call{ID: "3", Name: tools.ToolPatch, Input: input})
	if err != nil {
		t.Fatalf("patch call error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got %s", string(result.Output))
	}

	data, err := os.ReadFile(filepath.Join(dir, "nested", "new", "file.txt"))
	if err != nil {
		t.Fatalf("read added file: %v", err)
	}
	if string(data) != "hello\n" {
		t.Fatalf("unexpected file content: %q", string(data))
	}
}

func TestUpdateAnchorsToHeaderInRepeatedBlocks(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "repeat.txt")
	seed := "alpha\nblock-start\nx\nblock-end\nmid\nblock-start\nx\nblock-end\nomega\n"
	if err := os.WriteFile(target, []byte(seed), 0o644); err != nil {
		t.Fatalf("write seed file: %v", err)
	}

	tool, err := New(dir, true)
	if err != nil {
		t.Fatalf("new patch tool: %v", err)
	}

	patchText := "*** Begin Patch\n*** Update File: repeat.txt\n@@ -6,3 +6,3 @@\n block-start\n-x\n+y\n block-end\n*** End Patch\n"
	input, _ := json.Marshal(map[string]any{"patch": patchText})
	result, err := tool.Call(context.Background(), tools.Call{ID: "4", Name: tools.ToolPatch, Input: input})
	if err != nil {
		t.Fatalf("patch call error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got %s", string(result.Output))
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read updated file: %v", err)
	}
	want := "alpha\nblock-start\nx\nblock-end\nmid\nblock-start\ny\nblock-end\nomega\n"
	if string(got) != want {
		t.Fatalf("unexpected updated content:\n%s", string(got))
	}
}

func TestUpdateAnchoredHeaderAllowsFuzz(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "fuzz.txt")
	seed := "line1\nb\nc\nd\nline5\n"
	if err := os.WriteFile(target, []byte(seed), 0o644); err != nil {
		t.Fatalf("write seed file: %v", err)
	}

	tool, err := New(dir, true)
	if err != nil {
		t.Fatalf("new patch tool: %v", err)
	}

	patchText := "*** Begin Patch\n*** Update File: fuzz.txt\n@@ -4,3 +4,3 @@\n b\n-c\n+C\n d\n*** End Patch\n"
	input, _ := json.Marshal(map[string]any{"patch": patchText})
	result, err := tool.Call(context.Background(), tools.Call{ID: "5", Name: tools.ToolPatch, Input: input})
	if err != nil {
		t.Fatalf("patch call error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got %s", string(result.Output))
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read updated file: %v", err)
	}
	want := "line1\nb\nC\nd\nline5\n"
	if string(got) != want {
		t.Fatalf("unexpected updated content:\n%s", string(got))
	}
}

func TestUpdateAnchoredHeaderFailsOutsideFuzz(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "far.txt")
	seed := "line1\nb\nc\nd\nline5\n"
	if err := os.WriteFile(target, []byte(seed), 0o644); err != nil {
		t.Fatalf("write seed file: %v", err)
	}

	tool, err := New(dir, true)
	if err != nil {
		t.Fatalf("new patch tool: %v", err)
	}

	patchText := "*** Begin Patch\n*** Update File: far.txt\n@@ -30,3 +30,3 @@\n b\n-c\n+C\n d\n*** End Patch\n"
	input, _ := json.Marshal(map[string]any{"patch": patchText})
	result, err := tool.Call(context.Background(), tools.Call{ID: "6", Name: tools.ToolPatch, Input: input})
	if err != nil {
		t.Fatalf("patch call error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected patch failure outside fuzz window")
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read file after failed patch: %v", err)
	}
	if string(got) != seed {
		t.Fatalf("file changed despite failed patch:\n%s", string(got))
	}
}

func TestCommitStagedFilesRollsBackCommittedTargetsOnLaterFailure(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "first.txt")
	if err := os.WriteFile(first, []byte("original-first\n"), 0o644); err != nil {
		t.Fatalf("seed first file: %v", err)
	}

	blockingDir := filepath.Join(dir, "z-blocking-dir")
	if err := os.Mkdir(blockingDir, 0o755); err != nil {
		t.Fatalf("seed blocking dir: %v", err)
	}

	if err := os.WriteFile(stagedPath(first), []byte("patched-first\n"), 0o644); err != nil {
		t.Fatalf("stage first file: %v", err)
	}
	if err := os.WriteFile(stagedPath(blockingDir), []byte("patched-second\n"), 0o644); err != nil {
		t.Fatalf("stage second file: %v", err)
	}

	states := []*patchFileState{
		{Exists: true, NewPath: first, Original: first},
		{Exists: true, NewPath: blockingDir, Original: blockingDir},
	}

	err := commitStagedFiles(states)
	if err == nil {
		t.Fatal("expected transactional commit failure")
	}

	gotFirst, err := os.ReadFile(first)
	if err != nil {
		t.Fatalf("read first file: %v", err)
	}
	if string(gotFirst) != "original-first\n" {
		t.Fatalf("first file not rolled back: %q", string(gotFirst))
	}

	info, err := os.Stat(blockingDir)
	if err != nil {
		t.Fatalf("stat blocking dir: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("blocking path changed type")
	}
}

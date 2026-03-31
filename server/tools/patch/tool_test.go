package patch

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode"

	"builder/server/tools"
)

func TestDeleteFile(t *testing.T) {
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
	if result.IsError {
		t.Fatalf("expected success, got %s", string(result.Output))
	}

	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("expected file deleted, stat err=%v", err)
	}
}

func TestDeleteParticipatesInAtomicPatchCommit(t *testing.T) {
	dir := t.TempDir()
	deleteTarget := filepath.Join(dir, "delete.txt")
	keepTarget := filepath.Join(dir, "keep.txt")
	if err := os.WriteFile(deleteTarget, []byte("delete me\n"), 0o644); err != nil {
		t.Fatalf("write delete target: %v", err)
	}
	if err := os.WriteFile(keepTarget, []byte("one\n"), 0o644); err != nil {
		t.Fatalf("write keep target: %v", err)
	}

	tool, err := New(dir, true)
	if err != nil {
		t.Fatalf("new patch tool: %v", err)
	}

	patchText := "*** Begin Patch\n*** Delete File: delete.txt\n*** Add File: added.txt\n+hello\n*** Update File: keep.txt\n-two\n+two\n*** End Patch\n"
	input, _ := json.Marshal(map[string]any{"patch": patchText})
	result, err := tool.Call(context.Background(), tools.Call{ID: "atomic-delete", Name: tools.ToolPatch, Input: input})
	if err != nil {
		t.Fatalf("patch call error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected tool error result")
	}

	deleted, err := os.ReadFile(deleteTarget)
	if err != nil {
		t.Fatalf("read delete target after failure: %v", err)
	}
	if string(deleted) != "delete me\n" {
		t.Fatalf("unexpected delete target contents after rollback: %q", string(deleted))
	}
	if _, err := os.Stat(filepath.Join(dir, "added.txt")); !os.IsNotExist(err) {
		t.Fatalf("expected added file absent after rollback, stat err=%v", err)
	}
	kept, err := os.ReadFile(keepTarget)
	if err != nil {
		t.Fatalf("read keep target after failure: %v", err)
	}
	if string(kept) != "one\n" {
		t.Fatalf("unexpected keep target contents after rollback: %q", string(kept))
	}
}

func TestDeleteAddUpdateCommitTogether(t *testing.T) {
	dir := t.TempDir()
	deleteTarget := filepath.Join(dir, "delete.txt")
	updateTarget := filepath.Join(dir, "update.txt")
	if err := os.WriteFile(deleteTarget, []byte("remove me\n"), 0o644); err != nil {
		t.Fatalf("write delete target: %v", err)
	}
	if err := os.WriteFile(updateTarget, []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatalf("write update target: %v", err)
	}

	tool, err := New(dir, true)
	if err != nil {
		t.Fatalf("new patch tool: %v", err)
	}

	patchText := "*** Begin Patch\n*** Delete File: delete.txt\n*** Add File: added.txt\n+hello\n*** Update File: update.txt\n one\n-two\n+two updated\n*** End Patch\n"
	input, _ := json.Marshal(map[string]any{"patch": patchText})
	result, err := tool.Call(context.Background(), tools.Call{ID: "mixed-success", Name: tools.ToolPatch, Input: input})
	if err != nil {
		t.Fatalf("patch call error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got %s", string(result.Output))
	}

	if _, err := os.Stat(deleteTarget); !os.IsNotExist(err) {
		t.Fatalf("expected delete target removed, stat err=%v", err)
	}
	added, err := os.ReadFile(filepath.Join(dir, "added.txt"))
	if err != nil {
		t.Fatalf("read added file: %v", err)
	}
	if string(added) != "hello\n" {
		t.Fatalf("unexpected added file contents: %q", string(added))
	}
	updated, err := os.ReadFile(updateTarget)
	if err != nil {
		t.Fatalf("read updated file: %v", err)
	}
	if string(updated) != "one\ntwo updated\n" {
		t.Fatalf("unexpected updated file contents: %q", string(updated))
	}
}

func TestDeleteThenMoveToSamePathCommitsReplacement(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	dest := filepath.Join(dir, "dest.txt")
	if err := os.WriteFile(src, []byte("line1\nline2\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	if err := os.WriteFile(dest, []byte("old\n"), 0o644); err != nil {
		t.Fatalf("write destination: %v", err)
	}

	tool, err := New(dir, true)
	if err != nil {
		t.Fatalf("new patch tool: %v", err)
	}

	patchText := "*** Begin Patch\n*** Delete File: dest.txt\n*** Update File: src.txt\n*** Move to: dest.txt\n line1\n-line2\n+line2 moved\n*** End Patch\n"
	input, _ := json.Marshal(map[string]any{"patch": patchText})
	result, err := tool.Call(context.Background(), tools.Call{ID: "replace-move", Name: tools.ToolPatch, Input: input})
	if err != nil {
		t.Fatalf("patch call error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got %s", string(result.Output))
	}

	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Fatalf("expected source removed after move, stat err=%v", err)
	}
	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read replacement destination: %v", err)
	}
	if string(data) != "line1\nline2 moved\n" {
		t.Fatalf("unexpected replacement destination contents: %q", string(data))
	}
}

func TestDeleteThenAddNestedFileReplacesFileWithDirectory(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "tools")
	if err := os.WriteFile(blocker, []byte("old blocker\n"), 0o644); err != nil {
		t.Fatalf("write blocker file: %v", err)
	}

	tool, err := New(dir, true)
	if err != nil {
		t.Fatalf("new patch tool: %v", err)
	}

	patchText := "*** Begin Patch\n*** Delete File: tools\n*** Add File: tools/main.go\n+package main\n+\n+func main() {}\n*** End Patch\n"
	input, _ := json.Marshal(map[string]any{"patch": patchText})
	result, err := tool.Call(context.Background(), tools.Call{ID: "replace-file-dir", Name: tools.ToolPatch, Input: input})
	if err != nil {
		t.Fatalf("patch call error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got %s", string(result.Output))
	}

	info, err := os.Stat(filepath.Join(dir, "tools"))
	if err != nil {
		t.Fatalf("stat tools directory: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("expected tools to become a directory, mode=%v", info.Mode())
	}
	data, err := os.ReadFile(filepath.Join(dir, "tools", "main.go"))
	if err != nil {
		t.Fatalf("read nested replacement file: %v", err)
	}
	if string(data) != "package main\n\nfunc main() {}\n" {
		t.Fatalf("unexpected nested replacement contents: %q", string(data))
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

	firstStage, err := createStagedFile(first, []byte("patched-first\n"))
	if err != nil {
		t.Fatalf("stage first file: %v", err)
	}
	defer func() { _ = os.Remove(firstStage) }()
	secondStage, err := createStagedFile(blockingDir, []byte("patched-second\n"))
	if err != nil {
		t.Fatalf("stage second file: %v", err)
	}
	defer func() { _ = os.Remove(secondStage) }()

	states := []*patchFileState{
		{Exists: true, NewPath: first, Original: first, StagedPath: firstStage},
		{Exists: true, NewPath: blockingDir, Original: blockingDir, StagedPath: secondStage},
	}

	err = commitStagedFiles(states, nil)
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

func TestOutsideWorkspaceEditAllowedWhenConfigured(t *testing.T) {
	workspace := t.TempDir()
	outsideRoot := outsideNonTempDir(t)
	target := filepath.Join(outsideRoot, "outside.txt")
	if err := os.WriteFile(target, []byte("start\n"), 0o644); err != nil {
		t.Fatalf("seed outside file: %v", err)
	}

	tool, err := New(workspace, true, WithAllowOutsideWorkspace(true))
	if err != nil {
		t.Fatalf("new patch tool: %v", err)
	}

	result := callPatch(t, tool, "allow-config", "*** Begin Patch\n*** Update File: "+target+"\n-start\n+done\n*** End Patch\n")
	if result.IsError {
		t.Fatalf("expected success, got %s", string(result.Output))
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read outside file: %v", err)
	}
	if string(got) != "done\n" {
		t.Fatalf("outside file not updated: %q", string(got))
	}
}

func TestOutsideWorkspaceTempDirAllowedWithoutApproval(t *testing.T) {
	workspace := t.TempDir()
	outsideRoot := t.TempDir()
	target := filepath.Join(outsideRoot, "outside.txt")
	if err := os.WriteFile(target, []byte("start\n"), 0o644); err != nil {
		t.Fatalf("seed outside file: %v", err)
	}

	tool, err := New(workspace, true)
	if err != nil {
		t.Fatalf("new patch tool: %v", err)
	}

	result := callPatch(t, tool, "allow-temp-default", "*** Begin Patch\n*** Update File: "+target+"\n-start\n+done\n*** End Patch\n")
	if result.IsError {
		t.Fatalf("expected success for temp outside path, got %s", string(result.Output))
	}
}

func TestOutsideWorkspaceTempDirBypassesApprover(t *testing.T) {
	workspace := t.TempDir()
	outsideRoot := t.TempDir()
	target := filepath.Join(outsideRoot, "outside.txt")
	if err := os.WriteFile(target, []byte("start\n"), 0o644); err != nil {
		t.Fatalf("seed outside file: %v", err)
	}

	approveCalls := 0
	tool, err := New(workspace, true, WithOutsideWorkspaceApprover(func(context.Context, OutsideWorkspaceRequest) (OutsideWorkspaceApproval, error) {
		approveCalls++
		return OutsideWorkspaceApproval{Decision: OutsideWorkspaceDecisionDeny}, nil
	}))
	if err != nil {
		t.Fatalf("new patch tool: %v", err)
	}

	result := callPatch(t, tool, "allow-temp-bypass", "*** Begin Patch\n*** Update File: "+target+"\n-start\n+done\n*** End Patch\n")
	if result.IsError {
		t.Fatalf("expected success for temp outside path, got %s", string(result.Output))
	}
	if approveCalls != 0 {
		t.Fatalf("expected temp exclusion to bypass approver, got %d calls", approveCalls)
	}
}

func TestCaseVariantAbsoluteInWorkspaceDoesNotTriggerOutsideApproval(t *testing.T) {
	workspace := t.TempDir()
	target := filepath.Join(workspace, "inside.txt")
	if err := os.WriteFile(target, []byte("start\n"), 0o644); err != nil {
		t.Fatalf("seed inside file: %v", err)
	}

	variantWorkspace, ok := findCaseVariantExistingAlias(workspace)
	if !ok {
		t.Skip("filesystem does not provide a case-variant alias for workspace path")
	}
	variantTarget := filepath.Join(variantWorkspace, "inside.txt")

	approveCalls := 0
	tool, err := New(workspace, true, WithOutsideWorkspaceApprover(func(context.Context, OutsideWorkspaceRequest) (OutsideWorkspaceApproval, error) {
		approveCalls++
		return OutsideWorkspaceApproval{Decision: OutsideWorkspaceDecisionDeny}, nil
	}))
	if err != nil {
		t.Fatalf("new patch tool: %v", err)
	}

	result := callPatch(t, tool, "case-variant-inside", "*** Begin Patch\n*** Update File: "+variantTarget+"\n-start\n+done\n*** End Patch\n")
	if result.IsError {
		t.Fatalf("expected success for case-variant absolute in-workspace target, got %s", string(result.Output))
	}
	if approveCalls != 0 {
		t.Fatalf("expected no outside-workspace approval prompts, got %d", approveCalls)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read inside file: %v", err)
	}
	if string(got) != "done\n" {
		t.Fatalf("inside file not updated: %q", string(got))
	}
}

func TestOutsideWorkspaceEditRejectionContainsSteeringMessage(t *testing.T) {
	workspace := t.TempDir()
	outsideRoot := outsideNonTempDir(t)
	target := filepath.Join(outsideRoot, "outside.txt")
	if err := os.WriteFile(target, []byte("start\n"), 0o644); err != nil {
		t.Fatalf("seed outside file: %v", err)
	}

	approveCalls := 0
	tool, err := New(workspace, true, WithOutsideWorkspaceApprover(func(context.Context, OutsideWorkspaceRequest) (OutsideWorkspaceApproval, error) {
		approveCalls++
		return OutsideWorkspaceApproval{Decision: OutsideWorkspaceDecisionDeny}, nil
	}))
	if err != nil {
		t.Fatalf("new patch tool: %v", err)
	}

	result := callPatch(t, tool, "deny-outside", "*** Begin Patch\n*** Update File: "+target+"\n-start\n+done\n*** End Patch\n")
	if !result.IsError {
		t.Fatalf("expected error result")
	}
	if approveCalls != 1 {
		t.Fatalf("expected one approval call, got %d", approveCalls)
	}
	errMessage := toolError(t, result)
	want := "patch target outside workspace rejected by user: " + target + ". User rejected the approval request for this tool call. Do not attempt to circumvent, hack around, or re-execute the same path. Treat this rejection as authoritative. If it's essential to the task, ask the user to make the edit manually at the end of the task."
	if errMessage != want {
		t.Fatalf("unexpected steering guidance in error, got %q want %q", errMessage, want)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read outside file: %v", err)
	}
	if string(got) != "start\n" {
		t.Fatalf("outside file changed despite rejection: %q", string(got))
	}
}

func TestOutsideWorkspaceAllowSessionSkipsFuturePrompts(t *testing.T) {
	workspace := t.TempDir()
	outsideRoot := outsideNonTempDir(t)
	first := filepath.Join(outsideRoot, "first.txt")
	second := filepath.Join(outsideRoot, "second.txt")
	if err := os.WriteFile(first, []byte("one\n"), 0o644); err != nil {
		t.Fatalf("seed first file: %v", err)
	}
	if err := os.WriteFile(second, []byte("two\n"), 0o644); err != nil {
		t.Fatalf("seed second file: %v", err)
	}

	approveCalls := 0
	tool, err := New(workspace, true, WithOutsideWorkspaceApprover(func(context.Context, OutsideWorkspaceRequest) (OutsideWorkspaceApproval, error) {
		approveCalls++
		return OutsideWorkspaceApproval{Decision: OutsideWorkspaceDecisionAllowSession}, nil
	}))
	if err != nil {
		t.Fatalf("new patch tool: %v", err)
	}

	result := callPatch(t, tool, "allow-session-1", "*** Begin Patch\n*** Update File: "+first+"\n-one\n+one-updated\n*** End Patch\n")
	if result.IsError {
		t.Fatalf("expected first patch success, got %s", string(result.Output))
	}
	result = callPatch(t, tool, "allow-session-2", "*** Begin Patch\n*** Update File: "+second+"\n-two\n+two-updated\n*** End Patch\n")
	if result.IsError {
		t.Fatalf("expected second patch success, got %s", string(result.Output))
	}
	if approveCalls != 1 {
		t.Fatalf("expected one approval call, got %d", approveCalls)
	}
}

func TestOutsideWorkspaceAllowOncePromptsEachCall(t *testing.T) {
	workspace := t.TempDir()
	outsideRoot := outsideNonTempDir(t)
	target := filepath.Join(outsideRoot, "outside.txt")
	if err := os.WriteFile(target, []byte("start\n"), 0o644); err != nil {
		t.Fatalf("seed outside file: %v", err)
	}

	approveCalls := 0
	tool, err := New(workspace, true, WithOutsideWorkspaceApprover(func(context.Context, OutsideWorkspaceRequest) (OutsideWorkspaceApproval, error) {
		approveCalls++
		return OutsideWorkspaceApproval{Decision: OutsideWorkspaceDecisionAllowOnce}, nil
	}))
	if err != nil {
		t.Fatalf("new patch tool: %v", err)
	}

	result := callPatch(t, tool, "allow-once-1", "*** Begin Patch\n*** Update File: "+target+"\n-start\n+mid\n*** End Patch\n")
	if result.IsError {
		t.Fatalf("expected first patch success, got %s", string(result.Output))
	}
	result = callPatch(t, tool, "allow-once-2", "*** Begin Patch\n*** Update File: "+target+"\n-mid\n+done\n*** End Patch\n")
	if result.IsError {
		t.Fatalf("expected second patch success, got %s", string(result.Output))
	}
	if approveCalls != 2 {
		t.Fatalf("expected two approval calls, got %d", approveCalls)
	}
}

func TestOutsideWorkspaceRejectionIncludesUserCommentary(t *testing.T) {
	workspace := t.TempDir()
	outsideRoot := outsideNonTempDir(t)
	target := filepath.Join(outsideRoot, "outside.txt")
	if err := os.WriteFile(target, []byte("start\n"), 0o644); err != nil {
		t.Fatalf("seed outside file: %v", err)
	}

	tool, err := New(workspace, true, WithOutsideWorkspaceApprover(func(context.Context, OutsideWorkspaceRequest) (OutsideWorkspaceApproval, error) {
		return OutsideWorkspaceApproval{Decision: OutsideWorkspaceDecisionDeny, Commentary: "not allowed by policy"}, nil
	}))
	if err != nil {
		t.Fatalf("new patch tool: %v", err)
	}

	result := callPatch(t, tool, "deny-commentary", "*** Begin Patch\n*** Update File: "+target+"\n-start\n+done\n*** End Patch\n")
	if !result.IsError {
		t.Fatalf("expected error result")
	}
	errMessage := toolError(t, result)
	want := `patch target outside workspace rejected by user: ` + target + `. User rejected the approval request for this tool call, and said: "not allowed by policy". Do not attempt to circumvent, hack around, or re-execute the same path. Treat this rejection as authoritative. If it's essential to the task, ask the user to make the edit manually at the end of the task.`
	if errMessage != want {
		t.Fatalf("unexpected rejection error, got %q want %q", errMessage, want)
	}
}

func TestOutsideWorkspaceApprovalFailureUsesPatchSpecificWording(t *testing.T) {
	workspace := t.TempDir()
	outsideRoot := outsideNonTempDir(t)
	target := filepath.Join(outsideRoot, "outside.txt")
	if err := os.WriteFile(target, []byte("start\n"), 0o644); err != nil {
		t.Fatalf("seed outside file: %v", err)
	}

	tool, err := New(workspace, true, WithOutsideWorkspaceApprover(func(context.Context, OutsideWorkspaceRequest) (OutsideWorkspaceApproval, error) {
		return OutsideWorkspaceApproval{}, errors.New("ask failed")
	}))
	if err != nil {
		t.Fatalf("new patch tool: %v", err)
	}

	result := callPatch(t, tool, "deny-approval-error", "*** Begin Patch\n*** Update File: "+target+"\n-start\n+done\n*** End Patch\n")
	if !result.IsError {
		t.Fatalf("expected error result")
	}
	errMessage := toolError(t, result)
	if !strings.Contains(errMessage, "outside-workspace edit approval failed") {
		t.Fatalf("expected patch approval failure wording, got %q", errMessage)
	}
	if strings.Contains(errMessage, "read approval failed") || strings.Contains(errMessage, "view_image path outside workspace") {
		t.Fatalf("unexpected non-patch wording, got %q", errMessage)
	}
}

func TestOutsideWorkspaceAddFileRequestsApprovalBeforeMissingPathChecks(t *testing.T) {
	workspace := t.TempDir()
	outsideRoot := outsideNonTempDir(t)
	target := filepath.Join(outsideRoot, "missing", "created.txt")

	approvalCalls := 0
	tool, err := New(workspace, true, WithOutsideWorkspaceApprover(func(context.Context, OutsideWorkspaceRequest) (OutsideWorkspaceApproval, error) {
		approvalCalls++
		return OutsideWorkspaceApproval{Decision: OutsideWorkspaceDecisionAllowOnce}, nil
	}))
	if err != nil {
		t.Fatalf("new patch tool: %v", err)
	}

	result := callPatch(t, tool, "outside-add-missing", "*** Begin Patch\n*** Add File: "+target+"\n+hello\n*** End Patch\n")
	if result.IsError {
		t.Fatalf("expected success, got %s", toolError(t, result))
	}
	if approvalCalls != 1 {
		t.Fatalf("expected one approval call, got %d", approvalCalls)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read created outside file: %v", err)
	}
	if string(got) != "hello\n" {
		t.Fatalf("unexpected outside file content: %q", string(got))
	}
	if err := os.RemoveAll(filepath.Dir(target)); err != nil {
		t.Fatalf("cleanup created outside tree: %v", err)
	}
}

func TestOutsideWorkspaceQueuesApprovalPerFileInSinglePatch(t *testing.T) {
	workspace := t.TempDir()
	outsideRoot := outsideNonTempDir(t)
	first := filepath.Join(outsideRoot, "first", "one.txt")
	second := filepath.Join(outsideRoot, "second", "two.txt")

	requests := make([]OutsideWorkspaceRequest, 0, 2)
	tool, err := New(workspace, true, WithOutsideWorkspaceApprover(func(_ context.Context, req OutsideWorkspaceRequest) (OutsideWorkspaceApproval, error) {
		requests = append(requests, req)
		return OutsideWorkspaceApproval{Decision: OutsideWorkspaceDecisionAllowOnce}, nil
	}))
	if err != nil {
		t.Fatalf("new patch tool: %v", err)
	}

	patchText := "*** Begin Patch\n*** Add File: " + first + "\n+one\n*** Add File: " + second + "\n+two\n*** End Patch\n"
	result := callPatch(t, tool, "outside-multi-add", patchText)
	if result.IsError {
		t.Fatalf("expected success, got %s", toolError(t, result))
	}
	if len(requests) != 2 {
		t.Fatalf("expected two approval requests, got %d", len(requests))
	}
	if requests[0].ResolvedPath != first {
		t.Fatalf("unexpected first approval path: %+v", requests[0])
	}
	if requests[1].ResolvedPath != second {
		t.Fatalf("unexpected second approval path: %+v", requests[1])
	}
	for _, tc := range []struct {
		path string
		want string
	}{
		{path: first, want: "one\n"},
		{path: second, want: "two\n"},
	} {
		got, err := os.ReadFile(tc.path)
		if err != nil {
			t.Fatalf("read outside file %s: %v", tc.path, err)
		}
		if string(got) != tc.want {
			t.Fatalf("unexpected content for %s: %q", tc.path, string(got))
		}
	}
	if err := os.RemoveAll(filepath.Dir(first)); err != nil {
		t.Fatalf("cleanup first outside tree: %v", err)
	}
	if err := os.RemoveAll(filepath.Dir(second)); err != nil {
		t.Fatalf("cleanup second outside tree: %v", err)
	}
}

func outsideNonTempDir(t *testing.T) string {
	t.Helper()
	bases := make([]string, 0, 2)
	if wd, err := os.Getwd(); err == nil {
		bases = append(bases, wd)
	}
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		bases = append(bases, home)
	}
	for _, base := range bases {
		dir, err := os.MkdirTemp(base, "builder-patch-outside-*")
		if err != nil {
			continue
		}
		abs, err := filepath.Abs(dir)
		if err != nil {
			_ = os.RemoveAll(dir)
			continue
		}
		if IsPathInTemporaryDir(abs) {
			_ = os.RemoveAll(dir)
			continue
		}
		t.Cleanup(func() {
			_ = os.RemoveAll(dir)
		})
		return abs
	}
	t.Skip("unable to create non-temporary outside directory for test")
	return ""
}

func TestTemporaryEditableRootsIncludeBasicTmpAliases(t *testing.T) {
	assertAlias := func(primary, alias string) {
		t.Helper()
		primaryInfo, err := os.Stat(primary)
		if err != nil {
			return
		}
		aliasInfo, err := os.Stat(alias)
		if err != nil {
			return
		}
		if !os.SameFile(primaryInfo, aliasInfo) {
			return
		}
		roots := tempEditableRoots()
		if !containsString(roots, filepath.Clean(primary)) {
			t.Fatalf("expected temp roots to include %q, got %v", primary, roots)
		}
		if !containsString(roots, filepath.Clean(alias)) {
			t.Fatalf("expected temp roots to include %q, got %v", alias, roots)
		}
	}

	assertAlias("/tmp", "/private/tmp")
	assertAlias("/var/tmp", "/private/var/tmp")
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func findCaseVariantExistingAlias(path string) (string, bool) {
	canonical := filepath.Clean(path)
	canonicalInfo, err := os.Stat(canonical)
	if err != nil {
		return "", false
	}
	if candidate, ok := caseAliasUsersSubstitution(canonical, canonicalInfo); ok {
		return candidate, true
	}

	parts := strings.Split(canonical, string(filepath.Separator))
	start := 0
	if filepath.IsAbs(canonical) && len(parts) > 0 && parts[0] == "" {
		start = 1
	}

	for idx := start; idx < len(parts); idx++ {
		variantPart := toggleFirstLetterCase(parts[idx])
		if variantPart == parts[idx] {
			continue
		}
		candidateParts := append([]string(nil), parts...)
		candidateParts[idx] = variantPart
		candidate := strings.Join(candidateParts, string(filepath.Separator))
		if candidate == canonical {
			continue
		}
		candidateInfo, statErr := os.Stat(candidate)
		if statErr != nil {
			continue
		}
		if os.SameFile(candidateInfo, canonicalInfo) {
			return candidate, true
		}
	}

	return "", false
}

func caseAliasUsersSubstitution(canonical string, canonicalInfo os.FileInfo) (string, bool) {
	if strings.HasPrefix(canonical, "/Users/") {
		candidate := "/users/" + strings.TrimPrefix(canonical, "/Users/")
		if info, err := os.Stat(candidate); err == nil && os.SameFile(info, canonicalInfo) {
			return candidate, true
		}
	}
	if strings.HasPrefix(canonical, "/users/") {
		candidate := "/Users/" + strings.TrimPrefix(canonical, "/users/")
		if info, err := os.Stat(candidate); err == nil && os.SameFile(info, canonicalInfo) {
			return candidate, true
		}
	}
	return "", false
}

func toggleFirstLetterCase(value string) string {
	runes := []rune(value)
	if len(runes) == 0 {
		return value
	}
	first := runes[0]
	upper := unicode.ToUpper(first)
	lower := unicode.ToLower(first)
	if first == upper && first == lower {
		return value
	}
	if first == upper {
		runes[0] = lower
		return string(runes)
	}
	runes[0] = upper
	return string(runes)
}

func callPatch(t *testing.T, tool *Tool, id, patchText string) tools.Result {
	t.Helper()
	input, _ := json.Marshal(map[string]any{"patch": patchText})
	result, err := tool.Call(context.Background(), tools.Call{ID: id, Name: tools.ToolPatch, Input: input})
	if err != nil {
		t.Fatalf("patch call error: %v", err)
	}
	return result
}

func toolError(t *testing.T, result tools.Result) string {
	t.Helper()
	payload := map[string]string{}
	if err := json.Unmarshal(result.Output, &payload); err != nil {
		t.Fatalf("decode tool error output: %v", err)
	}
	return payload["error"]
}

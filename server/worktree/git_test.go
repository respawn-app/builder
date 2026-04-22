package worktree

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

type stubGitCommandRunner struct {
	output  []byte
	err     error
	dir     string
	args    []string
	outputs map[string][]byte
}

func (s *stubGitCommandRunner) Output(_ context.Context, dir string, args ...string) ([]byte, error) {
	s.dir = dir
	s.args = append([]string(nil), args...)
	if s.outputs != nil {
		if output, ok := s.outputs[strings.Join(args, "\x00")]; ok {
			return append([]byte(nil), output...), s.err
		}
	}
	return append([]byte(nil), s.output...), s.err
}

func TestGitInspectorListParsesPorcelainTopology(t *testing.T) {
	workspaceRoot := filepath.Join(t.TempDir(), "workspace")
	linkedRoot := filepath.Join(t.TempDir(), "linked")
	prunableRoot := filepath.Join(t.TempDir(), "missing-linked")
	runner := &stubGitCommandRunner{output: []byte("worktree " + workspaceRoot + "\nHEAD aaa111\nbranch refs/heads/main\n\nworktree " + linkedRoot + "\nHEAD bbb222\nbranch refs/heads/feature/worktree\nlocked bootstrap running\n\nworktree " + prunableRoot + "\nHEAD ccc333\ndetached\nprunable gitdir file points to non-existent location\n")}
	inspector := NewGitInspector(runner)
	entries, err := inspector.List(context.Background(), workspaceRoot)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if got, want := runner.args, []string{"worktree", "list", "--porcelain"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("git args=%v want=%v", got, want)
	}
	if got, want := runner.dir, canonicalTestPath(t, workspaceRoot); got != want {
		t.Fatalf("git dir=%q want=%q", got, want)
	}
	if len(entries) != 3 {
		t.Fatalf("entries=%d want 3", len(entries))
	}
	mainEntry := entries[0]
	if !mainEntry.IsMain || mainEntry.BranchName != "main" || mainEntry.Root != canonicalTestPath(t, workspaceRoot) {
		t.Fatalf("unexpected main entry: %+v", mainEntry)
	}
	linkedEntry := entries[1]
	if linkedEntry.IsMain || linkedEntry.BranchRef != "refs/heads/feature/worktree" || linkedEntry.BranchName != "feature/worktree" || linkedEntry.LockedReason != "bootstrap running" {
		t.Fatalf("unexpected linked entry: %+v", linkedEntry)
	}
	prunableEntry := entries[2]
	if !prunableEntry.Detached || prunableEntry.BranchName != "" || prunableEntry.PrunableReason == "" || prunableEntry.Root != canonicalTestPath(t, prunableRoot) {
		t.Fatalf("unexpected prunable entry: %+v", prunableEntry)
	}
}

func TestParseGitWorktreeListPorcelainRejectsUnsupportedKeys(t *testing.T) {
	workspaceRoot := filepath.Join(t.TempDir(), "workspace")
	_, err := parseGitWorktreeListPorcelain("worktree "+workspaceRoot+"\nHEAD aaa111\nunsupported nope\n", workspaceRoot)
	if err == nil {
		t.Fatal("expected parse error")
	}
}

func TestGitInspectorAddCreatesBranchFromHeadByDefault(t *testing.T) {
	workspaceRoot := filepath.Join(t.TempDir(), "workspace")
	worktreeRoot := filepath.Join(t.TempDir(), "linked")
	runner := &stubGitCommandRunner{outputs: map[string][]byte{
		strings.Join([]string{"worktree", "add", "-b", "feature/new", canonicalTestPath(t, worktreeRoot), "HEAD"}, "\x00"): nil,
	}}
	inspector := NewGitInspector(runner)
	created, err := inspector.Add(context.Background(), workspaceRoot, worktreeRoot, CreateSpec{CreateBranch: true, BranchName: "feature/new"})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if !created {
		t.Fatal("expected created branch=true for new branch")
	}
	if got, want := runner.args, []string{"worktree", "add", "-b", "feature/new", canonicalTestPath(t, worktreeRoot), "HEAD"}; !equalStrings(got, want) {
		t.Fatalf("git args=%v want=%v", got, want)
	}
	if got, want := runner.dir, canonicalTestPath(t, workspaceRoot); got != want {
		t.Fatalf("git dir=%q want=%q", got, want)
	}
}

func TestGitInspectorAddUsesExistingRefWithoutCreatingBranch(t *testing.T) {
	workspaceRoot := filepath.Join(t.TempDir(), "workspace")
	worktreeRoot := filepath.Join(t.TempDir(), "linked")
	runner := &stubGitCommandRunner{outputs: map[string][]byte{
		strings.Join([]string{"worktree", "add", canonicalTestPath(t, worktreeRoot), "feature/existing"}, "\x00"): nil,
	}}
	inspector := NewGitInspector(runner)
	created, err := inspector.Add(context.Background(), workspaceRoot, worktreeRoot, CreateSpec{BaseRef: "feature/existing", CreateBranch: false})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if created {
		t.Fatal("expected created branch=false for existing ref")
	}
	if got, want := runner.args, []string{"worktree", "add", canonicalTestPath(t, worktreeRoot), "feature/existing"}; !equalStrings(got, want) {
		t.Fatalf("git args=%v want=%v", got, want)
	}
	if got, want := runner.dir, canonicalTestPath(t, workspaceRoot); got != want {
		t.Fatalf("git dir=%q want=%q", got, want)
	}
}

func canonicalTestPath(t *testing.T, path string) string {
	t.Helper()
	canonical, err := filepath.EvalSymlinks(path)
	if err == nil {
		return filepath.Clean(canonical)
	}
	abs, absErr := filepath.Abs(path)
	if absErr != nil {
		t.Fatalf("abs path %q: %v", path, absErr)
	}
	return filepath.Clean(abs)
}

func equalStrings(got []string, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

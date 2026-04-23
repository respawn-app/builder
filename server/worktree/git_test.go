package worktree

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

type stubGitCommandRunner struct {
	output    []byte
	err       error
	exitCode  int
	dir       string
	args      []string
	outputs   map[string][]byte
	errors    map[string]error
	exitCodes map[string]int
}

func (s *stubGitCommandRunner) Output(_ context.Context, dir string, args ...string) ([]byte, error) {
	output, exitCode, err := s.Run(context.Background(), dir, args...)
	if err != nil {
		return nil, formatGitRunError(exitCode, err, output, args...)
	}
	return output, nil
}

func (s *stubGitCommandRunner) Run(_ context.Context, dir string, args ...string) ([]byte, int, error) {
	s.dir = dir
	s.args = append([]string(nil), args...)
	key := strings.Join(args, "\x00")
	output := append([]byte(nil), s.output...)
	if s.outputs != nil {
		if specific, ok := s.outputs[key]; ok {
			output = append([]byte(nil), specific...)
		}
	}
	err := s.err
	if s.errors != nil {
		if specific, ok := s.errors[key]; ok {
			err = specific
		}
	}
	exitCode := s.exitCode
	if s.exitCodes != nil {
		if specific, ok := s.exitCodes[key]; ok {
			exitCode = specific
		}
	}
	if err != nil && exitCode == 0 {
		exitCode = 1
	}
	return output, exitCode, err
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

func TestGitInspectorResolveCreateTargetClassifiesExistingBranch(t *testing.T) {
	workspaceRoot := filepath.Join(t.TempDir(), "workspace")
	runner := &stubGitCommandRunner{outputs: map[string][]byte{
		strings.Join([]string{"for-each-ref", "--format=%(refname:short)", "--count=1", "refs/heads/main"}, "\x00"): []byte("main\n"),
	}}
	inspector := NewGitInspector(runner)
	resolution, err := inspector.ResolveCreateTarget(context.Background(), workspaceRoot, "main")
	if err != nil {
		t.Fatalf("ResolveCreateTarget: %v", err)
	}
	if resolution.Kind != CreateTargetResolutionKindExistingBranch || resolution.ResolvedRef != "main" {
		t.Fatalf("unexpected resolution: %+v", resolution)
	}
}

func TestGitInspectorResolveCreateTargetClassifiesDetachedRef(t *testing.T) {
	workspaceRoot := filepath.Join(t.TempDir(), "workspace")
	runner := &stubGitCommandRunner{outputs: map[string][]byte{
		strings.Join([]string{"rev-parse", "--verify", "--quiet", "HEAD^{object}"}, "\x00"): []byte("abc123\n"),
	}}
	inspector := NewGitInspector(runner)
	resolution, err := inspector.ResolveCreateTarget(context.Background(), workspaceRoot, "HEAD")
	if err != nil {
		t.Fatalf("ResolveCreateTarget: %v", err)
	}
	if resolution.Kind != CreateTargetResolutionKindDetachedRef || resolution.ResolvedRef != "abc123" {
		t.Fatalf("unexpected resolution: %+v", resolution)
	}
}

func TestGitInspectorResolveCreateTargetClassifiesNewBranch(t *testing.T) {
	workspaceRoot := filepath.Join(t.TempDir(), "workspace")
	runner := &stubGitCommandRunner{
		errors: map[string]error{
			strings.Join([]string{"rev-parse", "--verify", "--quiet", "feature/new^{object}"}, "\x00"): errors.New("exit status 1"),
		},
		exitCodes: map[string]int{
			strings.Join([]string{"rev-parse", "--verify", "--quiet", "feature/new^{object}"}, "\x00"): 1,
		},
	}
	inspector := NewGitInspector(runner)
	resolution, err := inspector.ResolveCreateTarget(context.Background(), workspaceRoot, "feature/new")
	if err != nil {
		t.Fatalf("ResolveCreateTarget: %v", err)
	}
	if resolution.Kind != CreateTargetResolutionKindNewBranch {
		t.Fatalf("unexpected resolution: %+v", resolution)
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

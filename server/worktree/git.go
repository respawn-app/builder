package worktree

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"builder/shared/config"
)

type GitWorktree struct {
	Root           string
	HeadOID        string
	BranchRef      string
	BranchName     string
	Detached       bool
	Bare           bool
	LockedReason   string
	PrunableReason string
	IsMain         bool
}

type CreateSpec struct {
	BaseRef      string
	CreateBranch bool
	BranchName   string
}

type gitCommandRunner interface {
	Output(ctx context.Context, dir string, args ...string) ([]byte, error)
}

type GitInspector struct {
	runner gitCommandRunner
}

func NewGitInspector(runner gitCommandRunner) *GitInspector {
	if runner == nil {
		runner = execGitCommandRunner{}
	}
	return &GitInspector{runner: runner}
}

func (i *GitInspector) List(ctx context.Context, workspaceRoot string) ([]GitWorktree, error) {
	if i == nil {
		return nil, fmt.Errorf("git inspector is required")
	}
	canonicalRoot, err := config.CanonicalWorkspaceRoot(workspaceRoot)
	if err != nil {
		return nil, err
	}
	output, err := i.runner.Output(ctx, canonicalRoot, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	return parseGitWorktreeListPorcelain(string(output), canonicalRoot)
}

func (i *GitInspector) BranchExists(ctx context.Context, workspaceRoot string, branchName string) (bool, error) {
	if i == nil {
		return false, fmt.Errorf("git inspector is required")
	}
	canonicalRoot, err := config.CanonicalWorkspaceRoot(workspaceRoot)
	if err != nil {
		return false, err
	}
	trimmedBranch := strings.TrimSpace(branchName)
	if trimmedBranch == "" {
		return false, fmt.Errorf("branch name is required")
	}
	output, err := i.runner.Output(ctx, canonicalRoot, "branch", "--list", "--format=%(refname:short)", trimmedBranch)
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(string(output)) != "", nil
}

func (i *GitInspector) Add(ctx context.Context, workspaceRoot string, worktreeRoot string, spec CreateSpec) (bool, error) {
	if i == nil {
		return false, fmt.Errorf("git inspector is required")
	}
	canonicalWorkspaceRoot, err := config.CanonicalWorkspaceRoot(workspaceRoot)
	if err != nil {
		return false, err
	}
	canonicalWorktreeRoot, err := config.CanonicalWorkspaceRoot(worktreeRoot)
	if err != nil {
		return false, err
	}
	normalized, err := normalizeCreateSpec(spec)
	if err != nil {
		return false, err
	}
	args := []string{"worktree", "add"}
	if normalized.CreateBranch {
		args = append(args, "-b", normalized.BranchName, canonicalWorktreeRoot)
		if normalized.BaseRef != "" {
			args = append(args, normalized.BaseRef)
		}
	} else {
		args = append(args, canonicalWorktreeRoot, normalized.BaseRef)
	}
	if _, err := i.runner.Output(ctx, canonicalWorkspaceRoot, args...); err != nil {
		return false, err
	}
	return normalized.CreateBranch, nil
}

func (i *GitInspector) Remove(ctx context.Context, workspaceRoot string, worktreeRoot string) error {
	if i == nil {
		return fmt.Errorf("git inspector is required")
	}
	canonicalWorkspaceRoot, err := config.CanonicalWorkspaceRoot(workspaceRoot)
	if err != nil {
		return err
	}
	canonicalWorktreeRoot, err := config.CanonicalWorkspaceRoot(worktreeRoot)
	if err != nil {
		return err
	}
	_, err = i.runner.Output(ctx, canonicalWorkspaceRoot, "worktree", "remove", canonicalWorktreeRoot)
	return err
}

func (i *GitInspector) Prune(ctx context.Context, workspaceRoot string) error {
	if i == nil {
		return fmt.Errorf("git inspector is required")
	}
	canonicalWorkspaceRoot, err := config.CanonicalWorkspaceRoot(workspaceRoot)
	if err != nil {
		return err
	}
	_, err = i.runner.Output(ctx, canonicalWorkspaceRoot, "worktree", "prune")
	return err
}

func (i *GitInspector) DeleteBranch(ctx context.Context, workspaceRoot string, branchName string) error {
	if i == nil {
		return fmt.Errorf("git inspector is required")
	}
	canonicalWorkspaceRoot, err := config.CanonicalWorkspaceRoot(workspaceRoot)
	if err != nil {
		return err
	}
	trimmedBranch := strings.TrimSpace(branchName)
	if trimmedBranch == "" {
		return fmt.Errorf("branch name is required")
	}
	_, err = i.runner.Output(ctx, canonicalWorkspaceRoot, "branch", "-d", trimmedBranch)
	return err
}

func defaultWorktreeRoot(baseDir string, workspaceID string, pathSeed string) (string, error) {
	trimmedBaseDir := strings.TrimSpace(baseDir)
	if trimmedBaseDir == "" {
		return "", fmt.Errorf("worktree base dir is required")
	}
	trimmedWorkspaceID := strings.TrimSpace(workspaceID)
	if trimmedWorkspaceID == "" {
		return "", fmt.Errorf("workspace id is required")
	}
	trimmedSeed := strings.TrimSpace(pathSeed)
	if trimmedSeed == "" {
		return "", fmt.Errorf("worktree path seed is required")
	}
	relativeBranchPath := filepath.Clean(filepath.FromSlash(trimmedSeed))
	if relativeBranchPath == "." || filepath.IsAbs(relativeBranchPath) || relativeBranchPath == ".." || strings.HasPrefix(relativeBranchPath, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("worktree path seed %q cannot be mapped to worktree path", trimmedSeed)
	}
	return config.CanonicalWorkspaceRoot(filepath.Join(trimmedBaseDir, trimmedWorkspaceID, relativeBranchPath))
}

func normalizeCreateSpec(spec CreateSpec) (CreateSpec, error) {
	baseRef := strings.TrimSpace(spec.BaseRef)
	branchName := strings.TrimSpace(spec.BranchName)
	if spec.CreateBranch {
		if branchName == "" {
			return CreateSpec{}, fmt.Errorf("branch name is required when create_branch=true")
		}
		if baseRef == "" {
			baseRef = "HEAD"
		}
		return CreateSpec{BaseRef: baseRef, CreateBranch: true, BranchName: branchName}, nil
	}
	if baseRef == "" {
		return CreateSpec{}, fmt.Errorf("base ref is required when create_branch=false")
	}
	if branchName != "" {
		return CreateSpec{}, fmt.Errorf("branch name must be empty when create_branch=false")
	}
	return CreateSpec{BaseRef: baseRef, CreateBranch: false}, nil
}

type execGitCommandRunner struct{}

func (execGitCommandRunner) Output(ctx context.Context, dir string, args ...string) ([]byte, error) {
	argv := append([]string(nil), args...)
	cmd := exec.CommandContext(ctx, "git", argv...)
	cmd.Dir = strings.TrimSpace(dir)
	output, err := cmd.CombinedOutput()
	if err != nil {
		trimmed := strings.TrimSpace(string(output))
		if trimmed == "" {
			return nil, fmt.Errorf("git %s: %w", strings.Join(argv, " "), err)
		}
		return nil, fmt.Errorf("git %s: %s", strings.Join(argv, " "), trimmed)
	}
	return output, nil
}

func parseGitWorktreeListPorcelain(body string, workspaceRoot string) ([]GitWorktree, error) {
	canonicalWorkspaceRoot, err := config.CanonicalWorkspaceRoot(workspaceRoot)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	entries := make([]GitWorktree, 0, 4)
	current := GitWorktree{}
	haveCurrent := false
	flush := func() error {
		if !haveCurrent {
			return nil
		}
		if strings.TrimSpace(current.Root) == "" {
			return fmt.Errorf("git worktree entry missing root")
		}
		canonicalRoot, err := config.CanonicalWorkspaceRoot(current.Root)
		if err != nil {
			return err
		}
		current.Root = canonicalRoot
		current.IsMain = canonicalRoot == canonicalWorkspaceRoot
		entries = append(entries, current)
		current = GitWorktree{}
		haveCurrent = false
		return nil
	}
	for _, rawLine := range lines {
		line := strings.TrimRight(rawLine, "\r")
		if strings.TrimSpace(line) == "" {
			if err := flush(); err != nil {
				return nil, err
			}
			continue
		}
		key, value, hasValue := strings.Cut(line, " ")
		if !hasValue {
			value = ""
		}
		value = strings.TrimSpace(value)
		switch strings.TrimSpace(key) {
		case "worktree":
			if err := flush(); err != nil {
				return nil, err
			}
			current = GitWorktree{Root: value}
			haveCurrent = true
		case "HEAD":
			if !haveCurrent {
				return nil, fmt.Errorf("git worktree HEAD entry without worktree root")
			}
			current.HeadOID = value
		case "branch":
			if !haveCurrent {
				return nil, fmt.Errorf("git worktree branch entry without worktree root")
			}
			current.BranchRef = value
			current.BranchName = shortBranchName(value)
		case "detached":
			if !haveCurrent {
				return nil, fmt.Errorf("git worktree detached entry without worktree root")
			}
			current.Detached = true
		case "bare":
			if !haveCurrent {
				return nil, fmt.Errorf("git worktree bare entry without worktree root")
			}
			current.Bare = true
		case "locked":
			if !haveCurrent {
				return nil, fmt.Errorf("git worktree locked entry without worktree root")
			}
			current.LockedReason = value
		case "prunable":
			if !haveCurrent {
				return nil, fmt.Errorf("git worktree prunable entry without worktree root")
			}
			current.PrunableReason = value
		default:
			return nil, fmt.Errorf("unsupported git worktree porcelain key %q", strings.TrimSpace(key))
		}
	}
	if err := flush(); err != nil {
		return nil, err
	}
	return entries, nil
}

func shortBranchName(ref string) string {
	trimmed := strings.TrimSpace(ref)
	if strings.HasPrefix(trimmed, "refs/heads/") {
		return strings.TrimPrefix(trimmed, "refs/heads/")
	}
	return trimmed
}

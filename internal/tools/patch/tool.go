package patch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"builder/internal/tools"
)

type input struct {
	Patch string `json:"patch"`
}

type patchFileState struct {
	Exists     bool
	Content    []string
	NewPath    string
	Original   string
	StagedPath string
}

type fileSnapshot struct {
	Exists bool
	Mode   os.FileMode
	Data   []byte
}

type committedWrite struct {
	Path   string
	Before fileSnapshot
}

type removedSource struct {
	Path   string
	Before fileSnapshot
}

type OutsideWorkspaceRequest struct {
	RequestedPath string
	ResolvedPath  string
	WorkspaceRoot string
}

type OutsideWorkspaceDecision int

const (
	OutsideWorkspaceDecisionDeny OutsideWorkspaceDecision = iota
	OutsideWorkspaceDecisionAllowOnce
	OutsideWorkspaceDecisionAllowSession
)

type OutsideWorkspaceApproval struct {
	Decision   OutsideWorkspaceDecision
	Commentary string
}

type OutsideWorkspaceApprover func(context.Context, OutsideWorkspaceRequest) (OutsideWorkspaceApproval, error)

type Option func(*Tool)

func WithAllowOutsideWorkspace(allow bool) Option {
	return func(t *Tool) {
		t.allowOutsideWorkspace = allow
	}
}

func WithOutsideWorkspaceApprover(approver OutsideWorkspaceApprover) Option {
	return func(t *Tool) {
		t.outsideWorkspaceApprover = approver
	}
}

type Tool struct {
	workspaceRoot                string
	workspaceRootReal            string
	workspaceRootInfo            os.FileInfo
	workspaceOnly                bool
	allowOutsideWorkspace        bool
	outsideWorkspaceApprover     OutsideWorkspaceApprover
	outsideWorkspaceSessionMu    sync.RWMutex
	outsideWorkspaceSessionAllow bool
}

const hunkMaxFuzz = 8

const outsideWorkspaceRejectionInstruction = "do not attempt to circumvent this restriction in any way. if it's essential to the task, ask the user to make the edit manually at the end of the task."

var unifiedHunkHeaderPattern = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@(?: .*)?$`)

var (
	temporaryEditableRootsOnce sync.Once
	temporaryEditableRoots     []string
)

type editHunk struct {
	header  hunkHeader
	changes []ChangeLine
}

type hunkHeader struct {
	hasPosition bool
	oldStart    int
	oldCount    int
	newStart    int
	newCount    int
}

func New(workspaceRoot string, workspaceOnly bool, opts ...Option) (*Tool, error) {
	abs, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace root: %w", err)
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace real path: %w", err)
	}
	rootInfo, err := os.Stat(real)
	if err != nil {
		return nil, fmt.Errorf("stat workspace root: %w", err)
	}
	t := &Tool{workspaceRoot: abs, workspaceRootReal: real, workspaceRootInfo: rootInfo, workspaceOnly: workspaceOnly}
	for _, opt := range opts {
		if opt != nil {
			opt(t)
		}
	}
	return t, nil
}

func (t *Tool) Name() tools.ID {
	return tools.ToolPatch
}

func (t *Tool) Call(ctx context.Context, c tools.Call) (tools.Result, error) {
	var in input
	if err := json.Unmarshal(c.Input, &in); err != nil {
		return tools.ErrorResult(c, fmt.Sprintf("invalid input: %v", err)), nil
	}
	if strings.TrimSpace(in.Patch) == "" {
		return tools.ErrorResult(c, "patch is required"), nil
	}

	doc, err := parse(in.Patch)
	if err != nil {
		return tools.ErrorResult(c, err.Error()), nil
	}
	if err := t.apply(ctx, doc); err != nil {
		return tools.ErrorResult(c, err.Error()), nil
	}

	body, _ := json.Marshal(map[string]any{
		"ok":         true,
		"operations": len(doc.Hunks),
	})
	return tools.Result{CallID: c.ID, Name: c.Name, Output: body}, nil
}

func (t *Tool) apply(ctx context.Context, doc Document) error {
	state := map[string]*patchFileState{}
	deleteTargets := map[string]struct{}{}
	approvedOutside := map[string]bool{}

	hasDeleteTarget := func(path string) bool {
		_, ok := deleteTargets[path]
		return ok
	}

	hasDeletedAncestor := func(path string) bool {
		for current := filepath.Dir(path); current != "" && current != path; current = filepath.Dir(current) {
			if hasDeleteTarget(current) {
				return true
			}
			next := filepath.Dir(current)
			if next == current {
				break
			}
		}
		return false
	}

	getState := func(path string) (*patchFileState, error) {
		resolved, err := t.resolvePath(ctx, path, false, approvedOutside)
		if err != nil {
			return nil, err
		}
		if s, ok := state[resolved]; ok {
			return s, nil
		}
		s := &patchFileState{NewPath: resolved, Original: resolved}
		data, err := os.ReadFile(resolved)
		if err == nil {
			s.Exists = true
			s.Content = splitLines(string(data))
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("read file %q: %w", path, err)
		}
		state[resolved] = s
		return s, nil
	}

	for _, h := range doc.Hunks {
		switch op := h.(type) {
		case AddFile:
			target, err := t.resolvePath(ctx, op.Path, false, approvedOutside)
			if err != nil {
				return err
			}
			if _, exists := state[target]; exists {
				return fmt.Errorf("add file target already referenced: %s", op.Path)
			}
			allowReplacement := hasDeleteTarget(target)
			allowBlockedAncestor := hasDeletedAncestor(target)
			if _, err := os.Stat(target); err == nil {
				if !allowReplacement {
					return fmt.Errorf("add file target already exists: %s", op.Path)
				}
			} else if !errors.Is(err, os.ErrNotExist) {
				if !allowReplacement && !allowBlockedAncestor {
					return fmt.Errorf("stat add target: %w", err)
				}
			}
			state[target] = &patchFileState{
				Exists:   true,
				Content:  append([]string(nil), op.Content...),
				NewPath:  target,
				Original: target,
			}
		case DeleteFile:
			target, err := t.resolvePath(ctx, op.Path, true, approvedOutside)
			if err != nil {
				return err
			}
			if _, exists := state[target]; exists {
				return fmt.Errorf("delete target already referenced: %s", op.Path)
			}
			snapshot, err := captureSnapshot(target)
			if err != nil {
				return fmt.Errorf("stat delete target %q: %w", op.Path, err)
			}
			if !snapshot.Exists {
				return fmt.Errorf("delete target does not exist: %s", op.Path)
			}
			deleteTargets[target] = struct{}{}
		case UpdateFile:
			resolved, err := t.resolvePath(ctx, op.Path, false, approvedOutside)
			if err != nil {
				return err
			}
			if hasDeleteTarget(resolved) {
				return fmt.Errorf("update target already marked for deletion: %s", op.Path)
			}
			s, err := getState(op.Path)
			if err != nil {
				return err
			}
			if !s.Exists {
				return fmt.Errorf("update target does not exist: %s", op.Path)
			}
			updated, err := applyEdit(s.Content, op.Changes)
			if err != nil {
				return fmt.Errorf("apply update %s: %w", op.Path, err)
			}
			s.Content = updated
			if op.MoveTo != "" {
				moveTarget, err := t.resolvePath(ctx, op.MoveTo, false, approvedOutside)
				if err != nil {
					return err
				}
				if moveTarget != s.Original {
					if _, ok := state[moveTarget]; ok {
						return fmt.Errorf("move target already referenced: %s", op.MoveTo)
					}
					allowReplacement := hasDeleteTarget(moveTarget)
					allowBlockedAncestor := hasDeletedAncestor(moveTarget)
					if _, err := os.Stat(moveTarget); err == nil {
						if !allowReplacement {
							return fmt.Errorf("move target already exists: %s", op.MoveTo)
						}
					} else if !errors.Is(err, os.ErrNotExist) {
						if !allowReplacement && !allowBlockedAncestor {
							return fmt.Errorf("stat move target: %w", err)
						}
					}
					delete(state, s.NewPath)
					s.NewPath = moveTarget
					state[moveTarget] = s
				}
			}
		default:
			return fmt.Errorf("unsupported patch hunk: %T", h)
		}
	}

	states := sortedCommitStates(state)
	for _, s := range states {
		text := strings.Join(s.Content, "\n")
		if len(s.Content) > 0 && !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		staged, err := createStagedFile(s.NewPath, []byte(text))
		if err != nil {
			return fmt.Errorf("stage write %s: %w", s.NewPath, err)
		}
		s.StagedPath = staged
	}
	defer cleanupStagedFiles(states)

	if err := commitStagedFiles(states, deleteTargets); err != nil {
		return err
	}

	return nil
}

func sortedCommitStates(state map[string]*patchFileState) []*patchFileState {
	out := make([]*patchFileState, 0, len(state))
	for _, s := range state {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].NewPath < out[j].NewPath
	})
	return out
}

func cleanupStagedFiles(states []*patchFileState) {
	for _, s := range states {
		if strings.TrimSpace(s.StagedPath) == "" {
			continue
		}
		_ = os.Remove(s.StagedPath)
	}
}

func commitStagedFiles(states []*patchFileState, deleteTargets map[string]struct{}) error {
	committed := make([]committedWrite, 0, len(states))
	removed := make([]removedSource, 0, len(states)*2)
	removedPaths := make(map[string]struct{}, len(deleteTargets)+len(states))
	rollback := func() error {
		var rollbackErr error
		for i := len(committed) - 1; i >= 0; i-- {
			entry := committed[i]
			if err := restoreSnapshot(entry.Path, entry.Before); err != nil {
				rollbackErr = errors.Join(rollbackErr, fmt.Errorf("restore target %s: %w", entry.Path, err))
			}
		}
		for i := len(removed) - 1; i >= 0; i-- {
			entry := removed[i]
			if err := restoreSnapshot(entry.Path, entry.Before); err != nil {
				rollbackErr = errors.Join(rollbackErr, fmt.Errorf("restore moved source %s: %w", entry.Path, err))
			}
		}
		return rollbackErr
	}

	removePath := func(path string, label string) error {
		if _, seen := removedPaths[path]; seen {
			return nil
		}
		before, err := captureSnapshot(path)
		if err != nil {
			return withRollback(fmt.Errorf("snapshot %s %s: %w", label, path, err), rollback())
		}
		removedPaths[path] = struct{}{}
		if !before.Exists {
			return nil
		}
		if err := os.Remove(path); err != nil {
			return withRollback(fmt.Errorf("remove %s %s: %w", label, path, err), rollback())
		}
		removed = append(removed, removedSource{Path: path, Before: before})
		return nil
	}

	deletePaths := make([]string, 0, len(deleteTargets))
	for path := range deleteTargets {
		deletePaths = append(deletePaths, path)
	}
	sort.Strings(deletePaths)
	for _, path := range deletePaths {
		if err := removePath(path, "delete target"); err != nil {
			return err
		}
	}

	for _, s := range states {
		if s.NewPath != s.Original {
			if err := removePath(s.Original, "moved source"); err != nil {
				return err
			}
		}
	}

	for _, s := range states {
		if err := os.MkdirAll(filepath.Dir(s.NewPath), 0o755); err != nil {
			return withRollback(fmt.Errorf("create parent dir for %s: %w", s.NewPath, err), rollback())
		}
		before, err := captureSnapshot(s.NewPath)
		if err != nil {
			return withRollback(fmt.Errorf("snapshot target %s: %w", s.NewPath, err), rollback())
		}
		if err := os.Rename(s.StagedPath, s.NewPath); err != nil {
			return withRollback(fmt.Errorf("commit write %s: %w", s.NewPath, err), rollback())
		}
		committed = append(committed, committedWrite{Path: s.NewPath, Before: before})
	}

	return nil
}

func createStagedFile(targetPath string, data []byte) (string, error) {
	stageDir, err := nearestExistingDirectory(filepath.Dir(targetPath))
	if err != nil {
		return "", err
	}
	pattern := ".builder-patch-*"
	if base := strings.TrimSpace(filepath.Base(targetPath)); base != "" && base != "." && base != string(filepath.Separator) {
		pattern = ".builder-patch-" + base + "-*"
	}
	file, err := os.CreateTemp(stageDir, pattern)
	if err != nil {
		return "", err
	}
	path := file.Name()
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return "", err
	}
	if err := file.Chmod(0o644); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return "", err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return path, nil
}

func nearestExistingDirectory(path string) (string, error) {
	current := filepath.Clean(path)
	for {
		info, err := os.Stat(current)
		if err == nil {
			if info.IsDir() {
				return current, nil
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		next := filepath.Dir(current)
		if next == current {
			return "", fmt.Errorf("no existing directory ancestor for %s", path)
		}
		current = next
	}
}

func captureSnapshot(path string) (fileSnapshot, error) {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return fileSnapshot{}, nil
	}
	if err != nil {
		return fileSnapshot{}, err
	}
	if !info.Mode().IsRegular() {
		return fileSnapshot{}, fmt.Errorf("not a regular file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fileSnapshot{}, err
	}
	return fileSnapshot{Exists: true, Mode: info.Mode().Perm(), Data: data}, nil
}

func restoreSnapshot(path string, snapshot fileSnapshot) error {
	if !snapshot.Exists {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".builder.rollback.tmp"
	if err := os.WriteFile(tmp, snapshot.Data, snapshot.Mode); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func withRollback(primary, rollbackErr error) error {
	if rollbackErr == nil {
		return primary
	}
	return errors.Join(primary, fmt.Errorf("rollback failed: %w", rollbackErr))
}

func (t *Tool) resolvePath(ctx context.Context, path string, mustExist bool, approvedOutside map[string]bool) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("empty path")
	}
	candidate := path
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(t.workspaceRoot, candidate)
	}
	candidate = filepath.Clean(candidate)

	real := candidate
	if mustExist {
		var err error
		real, err = filepath.EvalSymlinks(real)
		if err != nil {
			return "", fmt.Errorf("resolve path %q: %w", path, err)
		}
	} else {
		parent := filepath.Dir(candidate)
		if _, err := os.Stat(parent); err == nil {
			parentReal, evalErr := filepath.EvalSymlinks(parent)
			if evalErr != nil {
				return "", fmt.Errorf("resolve parent path for %q: %w", path, evalErr)
			}
			real = filepath.Join(parentReal, filepath.Base(candidate))
		} else if errors.Is(err, os.ErrNotExist) {
			anchor := parent
			for {
				if _, statErr := os.Stat(anchor); statErr == nil {
					break
				} else if !errors.Is(statErr, os.ErrNotExist) {
					return "", fmt.Errorf("stat path anchor for %q: %w", path, statErr)
				}
				next := filepath.Dir(anchor)
				if next == anchor {
					return "", fmt.Errorf("resolve parent path for %q: no existing ancestor", path)
				}
				anchor = next
			}
			anchorReal, evalErr := filepath.EvalSymlinks(anchor)
			if evalErr != nil {
				return "", fmt.Errorf("resolve existing ancestor for %q: %w", path, evalErr)
			}
			relTail, relErr := filepath.Rel(anchor, candidate)
			if relErr != nil {
				return "", fmt.Errorf("build target tail for %q: %w", path, relErr)
			}
			real = filepath.Clean(filepath.Join(anchorReal, relTail))
		} else {
			return "", fmt.Errorf("stat parent path for %q: %w", path, err)
		}
	}

	if t.workspaceOnly {
		insideWorkspace, containmentErr := t.isWithinWorkspace(real)
		if containmentErr != nil {
			return "", fmt.Errorf("workspace boundary check for %q: %w", path, containmentErr)
		}
		if !insideWorkspace {
			if isPathInTemporaryDir(real) {
				return real, nil
			}
			if t.allowOutsideWorkspace || t.outsideWorkspaceSessionAllowed() {
				return real, nil
			}
			if approvedOutside != nil && approvedOutside[real] {
				return real, nil
			}
			if t.outsideWorkspaceApprover == nil {
				return "", fmt.Errorf("patch target outside workspace: %s", path)
			}
			approval, approveErr := t.outsideWorkspaceApprover(ctx, OutsideWorkspaceRequest{
				RequestedPath: path,
				ResolvedPath:  real,
				WorkspaceRoot: t.workspaceRoot,
			})
			if approveErr != nil {
				return "", fmt.Errorf("outside-workspace edit approval failed for %s: %w", path, approveErr)
			}
			switch approval.Decision {
			case OutsideWorkspaceDecisionAllowOnce:
				if approvedOutside != nil {
					approvedOutside[real] = true
				}
				return real, nil
			case OutsideWorkspaceDecisionAllowSession:
				t.setOutsideWorkspaceSessionAllowed(true)
				if approvedOutside != nil {
					approvedOutside[real] = true
				}
				return real, nil
			default:
				errMessage := fmt.Sprintf("patch target outside workspace rejected by user: %s; %s", path, outsideWorkspaceRejectionInstruction)
				commentary := strings.TrimSpace(approval.Commentary)
				if commentary != "" {
					errMessage += fmt.Sprintf(" User commented about this: %s", strconv.Quote(commentary))
				}
				return "", errors.New(errMessage)
			}
		}
	}
	return real, nil
}

func (t *Tool) isWithinWorkspace(real string) (bool, error) {
	rel, relErr := filepath.Rel(t.workspaceRootReal, real)
	if relErr == nil {
		if rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
			return true, nil
		}
	}

	if t.workspaceRootInfo == nil {
		return false, errors.New("workspace root info unavailable")
	}

	current := real
	for {
		info, statErr := os.Stat(current)
		if statErr != nil {
			return false, fmt.Errorf("stat candidate path %q: %w", current, statErr)
		}
		if os.SameFile(info, t.workspaceRootInfo) {
			return true, nil
		}
		next := filepath.Dir(current)
		if next == current {
			break
		}
		current = next
	}

	return false, nil
}

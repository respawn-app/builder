package patch

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

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

const outsideWorkspaceRejectionInstruction = "do not attempt to circumvent this restriction in any way. if it's essential to the task, ask the user to make the edit manually at the end of the task."

var (
	temporaryEditableRootsOnce sync.Once
	temporaryEditableRoots     []string
)

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

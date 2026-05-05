package fsguard

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type ErrorLabels struct {
	OutsidePath          string
	ApprovalFailed       string
	RejectedByUserPrefix string
}

type FailureFactory struct {
	ApprovalFailed        func(Request, error) error
	UserDenied            func(Request, Approval, string) error
	NoPermission          func(string, string) error
	DefaultApprovalFailed func(string, string) error
	DefaultUserDenied     func(string, string) error
}

type Request struct {
	RequestedPath string
	ResolvedPath  string
	WorkspaceRoot string
}

type Decision int

const (
	DecisionDeny Decision = iota
	DecisionAllowOnce
	DecisionAllowSession
)

type Approval struct {
	Decision   Decision
	Commentary string
}

type Approver func(context.Context, Request) (Approval, error)

type Guard struct {
	workspaceRoot         string
	workspaceRootReal     string
	workspaceRootInfo     os.FileInfo
	workspaceOnly         bool
	allowOutsideWorkspace bool
	approver              Approver
	sessionAllowed        func() bool
	setSessionAllowed     func(bool)
	rejectionInstruction  string
	errorLabels           ErrorLabels
	failures              FailureFactory
	temporaryPathAllowed  func(string) bool
	onApproved            func(Request, string)
}

func New(workspaceRoot string, workspaceRootReal string, workspaceRootInfo os.FileInfo, workspaceOnly bool, allowOutsideWorkspace bool, approver Approver, sessionAllowed func() bool, setSessionAllowed func(bool), rejectionInstruction string, errorLabels ErrorLabels, failures FailureFactory, temporaryPathAllowed func(string) bool, onApproved func(Request, string)) Guard {
	return Guard{
		workspaceRoot:         workspaceRoot,
		workspaceRootReal:     workspaceRootReal,
		workspaceRootInfo:     workspaceRootInfo,
		workspaceOnly:         workspaceOnly,
		allowOutsideWorkspace: allowOutsideWorkspace,
		approver:              approver,
		sessionAllowed:        sessionAllowed,
		setSessionAllowed:     setSessionAllowed,
		rejectionInstruction:  rejectionInstruction,
		errorLabels:           errorLabels,
		failures:              failures,
		temporaryPathAllowed:  temporaryPathAllowed,
		onApproved:            onApproved,
	}
}

func (g Guard) Allow(ctx context.Context, requestedPath string, resolvedPath string, approvedOutside map[string]bool) (string, error) {
	if !g.workspaceOnly {
		return resolvedPath, nil
	}
	insideWorkspace, containmentErr := g.isWithinWorkspace(resolvedPath)
	if containmentErr != nil {
		return "", fmt.Errorf("workspace boundary check for %q: %w", requestedPath, containmentErr)
	}
	if insideWorkspace {
		return resolvedPath, nil
	}

	req := Request{
		RequestedPath: requestedPath,
		ResolvedPath:  resolvedPath,
		WorkspaceRoot: g.workspaceRoot,
	}
	if g.temporaryPathAllowed != nil && g.temporaryPathAllowed(resolvedPath) {
		g.logApproved(req, "temporary_allow")
		return resolvedPath, nil
	}
	if g.allowOutsideWorkspace {
		g.logApproved(req, "configured_allow")
		return resolvedPath, nil
	}
	if g.sessionAllowed != nil && g.sessionAllowed() {
		g.logApproved(req, "session_allow")
		return resolvedPath, nil
	}
	if approvedOutside != nil && approvedOutside[resolvedPath] {
		g.logApproved(req, "call_allow")
		return resolvedPath, nil
	}
	if g.approver == nil {
		return "", g.noPermission(requestedPath, g.errorLabels.OutsidePath)
	}
	approval, approveErr := g.approver(ctx, req)
	if approveErr != nil {
		if g.failures.ApprovalFailed != nil {
			return "", g.failures.ApprovalFailed(req, approveErr)
		}
		return "", g.approvalFailed(requestedPath, approveErr.Error())
	}
	switch approval.Decision {
	case DecisionAllowOnce:
		if approvedOutside != nil {
			approvedOutside[resolvedPath] = true
		}
		g.logApproved(req, "allow_once")
		return resolvedPath, nil
	case DecisionAllowSession:
		if g.setSessionAllowed != nil {
			g.setSessionAllowed(true)
		}
		if approvedOutside != nil {
			approvedOutside[resolvedPath] = true
		}
		g.logApproved(req, "allow_session")
		return resolvedPath, nil
	default:
		if g.failures.UserDenied != nil {
			return "", g.failures.UserDenied(req, approval, g.rejectionInstruction)
		}
		return "", g.userDenied(requestedPath, approval.Commentary)
	}
}

func (g Guard) isWithinWorkspace(real string) (bool, error) {
	rel, relErr := filepath.Rel(g.workspaceRootReal, real)
	if relErr == nil {
		if rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
			return true, nil
		}
		return false, nil
	}

	if g.workspaceRootInfo == nil {
		return false, errors.New("workspace root info unavailable")
	}

	current := real
	for {
		info, statErr := os.Stat(current)
		if statErr != nil {
			if !errors.Is(statErr, os.ErrNotExist) {
				return false, fmt.Errorf("stat candidate path %q: %w", current, statErr)
			}
			next := filepath.Dir(current)
			if next == current {
				return false, fmt.Errorf("stat candidate path %q: %w", real, statErr)
			}
			current = next
			continue
		}
		if os.SameFile(info, g.workspaceRootInfo) {
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

func (g Guard) noPermission(path, reason string) error {
	if g.failures.NoPermission != nil {
		return g.failures.NoPermission(path, reason)
	}
	return fmt.Errorf("no file edit permission for %s: %s", path, reason)
}

func (g Guard) approvalFailed(path, reason string) error {
	if g.failures.DefaultApprovalFailed != nil {
		return g.failures.DefaultApprovalFailed(path, reason)
	}
	return fmt.Errorf("file edit approval failed for %s: %s", path, reason)
}

func (g Guard) userDenied(path, commentary string) error {
	if g.failures.DefaultUserDenied != nil {
		return g.failures.DefaultUserDenied(path, commentary)
	}
	if strings.TrimSpace(commentary) == "" {
		return fmt.Errorf("user denied edit for %s", path)
	}
	return fmt.Errorf("user denied edit for %s: %s", path, commentary)
}

func (g Guard) logApproved(req Request, reason string) {
	if g.onApproved != nil {
		g.onApproved(req, reason)
	}
}

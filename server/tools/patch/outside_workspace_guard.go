package patch

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type OutsideWorkspaceErrorLabels struct {
	OutsidePath          string
	ApprovalFailed       string
	RejectedByUserPrefix string
}

type OutsideWorkspaceGuard struct {
	workspaceRoot         string
	workspaceRootReal     string
	workspaceRootInfo     os.FileInfo
	workspaceOnly         bool
	allowOutsideWorkspace bool
	approver              OutsideWorkspaceApprover
	sessionAllowed        func() bool
	setSessionAllowed     func(bool)
	rejectionInstruction  string
	errorLabels           OutsideWorkspaceErrorLabels
	temporaryPathAllowed  func(string) bool
	onApproved            func(OutsideWorkspaceRequest, string)
}

func NewOutsideWorkspaceGuard(workspaceRoot string, workspaceRootReal string, workspaceRootInfo os.FileInfo, workspaceOnly bool, allowOutsideWorkspace bool, approver OutsideWorkspaceApprover, sessionAllowed func() bool, setSessionAllowed func(bool), rejectionInstruction string, errorLabels OutsideWorkspaceErrorLabels, temporaryPathAllowed func(string) bool, onApproved func(OutsideWorkspaceRequest, string)) OutsideWorkspaceGuard {
	return OutsideWorkspaceGuard{
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
		temporaryPathAllowed:  temporaryPathAllowed,
		onApproved:            onApproved,
	}
}

func (g OutsideWorkspaceGuard) Allow(ctx context.Context, requestedPath string, resolvedPath string, approvedOutside map[string]bool) (string, error) {
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

	req := OutsideWorkspaceRequest{
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
		return "", noPermissionFailure(requestedPath, g.errorLabels.OutsidePath)
	}
	approval, approveErr := g.approver(ctx, req)
	if approveErr != nil {
		return "", approvalFailedFailure(requestedPath, approveErr.Error())
	}
	switch approval.Decision {
	case OutsideWorkspaceDecisionAllowOnce:
		if approvedOutside != nil {
			approvedOutside[resolvedPath] = true
		}
		g.logApproved(req, "allow_once")
		return resolvedPath, nil
	case OutsideWorkspaceDecisionAllowSession:
		if g.setSessionAllowed != nil {
			g.setSessionAllowed(true)
		}
		if approvedOutside != nil {
			approvedOutside[resolvedPath] = true
		}
		g.logApproved(req, "allow_session")
		return resolvedPath, nil
	default:
		return "", userDeniedFailure(requestedPath, approval.Commentary)
	}
}

func (g OutsideWorkspaceGuard) isWithinWorkspace(real string) (bool, error) {
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
			return false, fmt.Errorf("stat candidate path %q: %w", current, statErr)
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

func (g OutsideWorkspaceGuard) logApproved(req OutsideWorkspaceRequest, reason string) {
	if g.onApproved != nil {
		g.onApproved(req, reason)
	}
}

package app

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"builder/internal/tools/askquestion"
	patchtool "builder/internal/tools/patch"
)

const (
	outsideWorkspaceAllowOnceSuggestion             = "Allow once (recommended): permit this outside-workspace edit for this patch call."
	outsideWorkspaceAllowSessionSuggestion          = "Allow for this session: permit outside-workspace patch edits until builder exits."
	outsideWorkspaceDenySuggestion                  = "Deny: keep patch edits limited to the workspace root."
	outsideWorkspaceAllowWithCommentaryAnswerPrefix = "allow_once_with_commentary:"
	approvalKindPatchOutsideWorkspace               = "patch_outside_workspace"
)

type patchOutsideWorkspaceApprover struct {
	broker         *askquestion.Broker
	mu             sync.Mutex
	sessionAllowed bool
}

func newPatchOutsideWorkspaceApprover(broker *askquestion.Broker) *patchOutsideWorkspaceApprover {
	return &patchOutsideWorkspaceApprover{broker: broker}
}

func (a *patchOutsideWorkspaceApprover) Approve(ctx context.Context, req patchtool.OutsideWorkspaceRequest) (patchtool.OutsideWorkspaceApproval, error) {
	a.mu.Lock()
	if a.sessionAllowed {
		a.mu.Unlock()
		return patchtool.OutsideWorkspaceApproval{Decision: patchtool.OutsideWorkspaceDecisionAllowSession}, nil
	}
	a.mu.Unlock()

	resp, err := a.broker.Ask(ctx, askquestion.Request{
		Question:     fmt.Sprintf("Allow editing %s (outside workspace dir)?", req.ResolvedPath),
		Approval:     true,
		ApprovalKind: approvalKindPatchOutsideWorkspace,
		Suggestions: []string{
			outsideWorkspaceAllowOnceSuggestion,
			outsideWorkspaceAllowSessionSuggestion,
			outsideWorkspaceDenySuggestion,
		},
	})
	if err != nil {
		return patchtool.OutsideWorkspaceApproval{Decision: patchtool.OutsideWorkspaceDecisionDeny}, err
	}

	approval := parseOutsideWorkspaceApprovalAnswer(resp.Answer)
	if approval.Decision == patchtool.OutsideWorkspaceDecisionAllowSession {
		a.mu.Lock()
		a.sessionAllowed = true
		a.mu.Unlock()
	}
	return approval, nil
}

func parseOutsideWorkspaceApprovalAnswer(answer string) patchtool.OutsideWorkspaceApproval {
	trimmed := strings.TrimSpace(answer)
	normalized := strings.ToLower(trimmed)
	if strings.HasPrefix(trimmed, outsideWorkspaceAllowWithCommentaryAnswerPrefix) {
		commentary := strings.TrimSpace(strings.TrimPrefix(trimmed, outsideWorkspaceAllowWithCommentaryAnswerPrefix))
		return patchtool.OutsideWorkspaceApproval{Decision: patchtool.OutsideWorkspaceDecisionAllowOnce, Commentary: commentary}
	}
	switch normalized {
	case strings.ToLower(outsideWorkspaceAllowOnceSuggestion), "allow once", "once", "allow", "yes", "y":
		return patchtool.OutsideWorkspaceApproval{Decision: patchtool.OutsideWorkspaceDecisionAllowOnce}
	case strings.ToLower(outsideWorkspaceAllowSessionSuggestion), "allow for this session", "allow session", "session", "always allow":
		return patchtool.OutsideWorkspaceApproval{Decision: patchtool.OutsideWorkspaceDecisionAllowSession}
	case strings.ToLower(outsideWorkspaceDenySuggestion), "deny", "no", "n":
		return patchtool.OutsideWorkspaceApproval{Decision: patchtool.OutsideWorkspaceDecisionDeny}
	default:
		if strings.HasPrefix(normalized, "allow for this session") || strings.HasPrefix(normalized, "allow session") || strings.HasPrefix(normalized, "session allow") {
			return patchtool.OutsideWorkspaceApproval{Decision: patchtool.OutsideWorkspaceDecisionAllowSession}
		}
		if strings.HasPrefix(normalized, "allow once") || normalized == "allow" {
			return patchtool.OutsideWorkspaceApproval{Decision: patchtool.OutsideWorkspaceDecisionAllowOnce}
		}
		if trimmed == "" {
			return patchtool.OutsideWorkspaceApproval{Decision: patchtool.OutsideWorkspaceDecisionDeny}
		}
		return patchtool.OutsideWorkspaceApproval{Decision: patchtool.OutsideWorkspaceDecisionDeny, Commentary: trimmed}
	}
}

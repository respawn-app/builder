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
	outsideWorkspaceAllowOnceSuggestion     = "Allow once (recommended): permit this outside-workspace access for this tool call."
	outsideWorkspaceAllowSessionSuggestion  = "Allow for this session: permit outside-workspace access until builder exits."
	outsideWorkspaceDenySuggestion          = "Deny: keep access limited to the workspace root."
	approvalAllowWithCommentaryAnswerPrefix = "allow_with_commentary:"
)

type outsideWorkspaceApprover struct {
	broker         *askquestion.Broker
	actionVerb     string
	mu             sync.Mutex
	sessionAllowed bool
}

func newOutsideWorkspaceApprover(broker *askquestion.Broker, actionVerb string) *outsideWorkspaceApprover {
	verb := strings.TrimSpace(actionVerb)
	if verb == "" {
		verb = "accessing"
	}
	return &outsideWorkspaceApprover{broker: broker, actionVerb: verb}
}

func (a *outsideWorkspaceApprover) Approve(ctx context.Context, req patchtool.OutsideWorkspaceRequest) (patchtool.OutsideWorkspaceApproval, error) {
	a.mu.Lock()
	if a.sessionAllowed {
		a.mu.Unlock()
		return patchtool.OutsideWorkspaceApproval{Decision: patchtool.OutsideWorkspaceDecisionAllowSession}, nil
	}
	a.mu.Unlock()

	resp, err := a.broker.Ask(ctx, askquestion.Request{
		Question: fmt.Sprintf("Allow %s %s (outside workspace dir)?", a.actionVerb, req.ResolvedPath),
		Approval: true,
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
	if strings.HasPrefix(trimmed, approvalAllowWithCommentaryAnswerPrefix) {
		commentary := strings.TrimSpace(strings.TrimPrefix(trimmed, approvalAllowWithCommentaryAnswerPrefix))
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

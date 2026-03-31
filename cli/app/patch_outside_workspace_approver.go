package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"builder/server/tools/askquestion"
	patchtool "builder/server/tools/patch"
)

const (
	outsideWorkspaceAllowOnceSuggestion    = "Allow once"
	outsideWorkspaceAllowSessionSuggestion = "Allow for this session"
	outsideWorkspaceDenySuggestion         = "Deny"
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
		ApprovalOptions: []askquestion.ApprovalOption{
			{Decision: askquestion.ApprovalDecisionAllowOnce, Label: outsideWorkspaceAllowOnceSuggestion},
			{Decision: askquestion.ApprovalDecisionAllowSession, Label: outsideWorkspaceAllowSessionSuggestion},
			{Decision: askquestion.ApprovalDecisionDeny, Label: outsideWorkspaceDenySuggestion},
		},
	})
	if err != nil {
		return patchtool.OutsideWorkspaceApproval{Decision: patchtool.OutsideWorkspaceDecisionDeny}, err
	}

	approval, err := outsideWorkspaceApprovalFromResponse(resp)
	if err != nil {
		return patchtool.OutsideWorkspaceApproval{Decision: patchtool.OutsideWorkspaceDecisionDeny}, err
	}
	if approval.Decision == patchtool.OutsideWorkspaceDecisionAllowSession {
		a.mu.Lock()
		a.sessionAllowed = true
		a.mu.Unlock()
	}
	return approval, nil
}

func outsideWorkspaceApprovalFromResponse(resp askquestion.Response) (patchtool.OutsideWorkspaceApproval, error) {
	payload := resp.Approval
	if payload == nil {
		return patchtool.OutsideWorkspaceApproval{}, errors.New("missing approval payload")
	}
	approval := patchtool.OutsideWorkspaceApproval{Commentary: strings.TrimSpace(payload.Commentary)}
	switch payload.Decision {
	case askquestion.ApprovalDecisionAllowOnce:
		approval.Decision = patchtool.OutsideWorkspaceDecisionAllowOnce
	case askquestion.ApprovalDecisionAllowSession:
		approval.Decision = patchtool.OutsideWorkspaceDecisionAllowSession
	case askquestion.ApprovalDecisionDeny:
		approval.Decision = patchtool.OutsideWorkspaceDecisionDeny
	default:
		return patchtool.OutsideWorkspaceApproval{}, fmt.Errorf("unsupported approval decision %q", payload.Decision)
	}
	return approval, nil
}

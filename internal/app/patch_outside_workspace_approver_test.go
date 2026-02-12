package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"builder/internal/tools/askquestion"
	patchtool "builder/internal/tools/patch"
)

func TestParseOutsideWorkspaceApprovalAnswer(t *testing.T) {
	tests := []struct {
		name   string
		answer string
		want   patchtool.OutsideWorkspaceApproval
	}{
		{name: "allow once suggestion", answer: outsideWorkspaceAllowOnceSuggestion, want: patchtool.OutsideWorkspaceApproval{Decision: patchtool.OutsideWorkspaceDecisionAllowOnce}},
		{name: "allow session suggestion", answer: outsideWorkspaceAllowSessionSuggestion, want: patchtool.OutsideWorkspaceApproval{Decision: patchtool.OutsideWorkspaceDecisionAllowSession}},
		{name: "deny suggestion", answer: outsideWorkspaceDenySuggestion, want: patchtool.OutsideWorkspaceApproval{Decision: patchtool.OutsideWorkspaceDecisionDeny}},
		{name: "freeform allow session", answer: "allow for this session", want: patchtool.OutsideWorkspaceApproval{Decision: patchtool.OutsideWorkspaceDecisionAllowSession}},
		{name: "freeform allow once", answer: "allow once", want: patchtool.OutsideWorkspaceApproval{Decision: patchtool.OutsideWorkspaceDecisionAllowOnce}},
		{name: "freeform allow once with commentary prefix", answer: outsideWorkspaceAllowWithCommentaryAnswerPrefix + "approved, but keep it small", want: patchtool.OutsideWorkspaceApproval{Decision: patchtool.OutsideWorkspaceDecisionAllowOnce, Commentary: "approved, but keep it small"}},
		{name: "freeform deny commentary", answer: "no because this is protected", want: patchtool.OutsideWorkspaceApproval{Decision: patchtool.OutsideWorkspaceDecisionDeny, Commentary: "no because this is protected"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parseOutsideWorkspaceApprovalAnswer(tc.answer)
			if got != tc.want {
				t.Fatalf("decision mismatch: got %v want %v", got, tc.want)
			}
		})
	}
}

func TestPatchOutsideWorkspaceApproverCachesSessionDecision(t *testing.T) {
	broker := askquestion.NewBroker()
	askCalls := 0
	broker.SetAskHandler(func(req askquestion.Request) (string, error) {
		askCalls++
		if !req.Approval {
			t.Fatalf("expected approval=true for outside-workspace ask")
		}
		if req.ApprovalKind != approvalKindPatchOutsideWorkspace {
			t.Fatalf("unexpected approval kind: %q", req.ApprovalKind)
		}
		if strings.Contains(req.Question, "workspace:") || strings.Contains(req.Question, "requested path:") || strings.Contains(req.Question, "Patch requested an edit outside the workspace.") {
			t.Fatalf("approval prompt contains removed fields: %q", req.Question)
		}
		if !strings.Contains(req.Question, "Allow editing /tmp/x.txt (outside workspace dir)?") {
			t.Fatalf("unexpected approval question text: %q", req.Question)
		}
		return outsideWorkspaceAllowSessionSuggestion, nil
	})

	approver := newPatchOutsideWorkspaceApprover(broker)
	req := patchtool.OutsideWorkspaceRequest{RequestedPath: "../x.txt", ResolvedPath: "/tmp/x.txt", WorkspaceRoot: "/tmp/w"}

	first, err := approver.Approve(context.Background(), req)
	if err != nil {
		t.Fatalf("approve first call: %v", err)
	}
	if first.Decision != patchtool.OutsideWorkspaceDecisionAllowSession {
		t.Fatalf("unexpected first decision: %v", first)
	}
	second, err := approver.Approve(context.Background(), req)
	if err != nil {
		t.Fatalf("approve second call: %v", err)
	}
	if second.Decision != patchtool.OutsideWorkspaceDecisionAllowSession {
		t.Fatalf("unexpected second decision: %v", second)
	}
	if askCalls != 1 {
		t.Fatalf("expected one ask call, got %d", askCalls)
	}
}

func TestPatchOutsideWorkspaceApproverPropagatesAskError(t *testing.T) {
	broker := askquestion.NewBroker()
	broker.SetAskHandler(func(askquestion.Request) (string, error) {
		return "", errors.New("ask failed")
	})

	approver := newPatchOutsideWorkspaceApprover(broker)
	_, err := approver.Approve(context.Background(), patchtool.OutsideWorkspaceRequest{RequestedPath: "../x.txt", ResolvedPath: "/tmp/x.txt", WorkspaceRoot: "/tmp/w"})
	if err == nil {
		t.Fatal("expected ask error")
	}
}

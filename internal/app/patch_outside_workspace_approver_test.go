package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"builder/internal/tools/askquestion"
	patchtool "builder/internal/tools/patch"
)

func TestOutsideWorkspaceApprovalFromResponse(t *testing.T) {
	tests := []struct {
		name string
		resp askquestion.Response
		want patchtool.OutsideWorkspaceApproval
	}{
		{name: "allow once", resp: askquestion.Response{Approval: &askquestion.ApprovalPayload{Decision: askquestion.ApprovalDecisionAllowOnce}}, want: patchtool.OutsideWorkspaceApproval{Decision: patchtool.OutsideWorkspaceDecisionAllowOnce}},
		{name: "allow session", resp: askquestion.Response{Approval: &askquestion.ApprovalPayload{Decision: askquestion.ApprovalDecisionAllowSession}}, want: patchtool.OutsideWorkspaceApproval{Decision: patchtool.OutsideWorkspaceDecisionAllowSession}},
		{name: "deny", resp: askquestion.Response{Approval: &askquestion.ApprovalPayload{Decision: askquestion.ApprovalDecisionDeny}}, want: patchtool.OutsideWorkspaceApproval{Decision: patchtool.OutsideWorkspaceDecisionDeny}},
		{name: "allow once with commentary", resp: askquestion.Response{Approval: &askquestion.ApprovalPayload{Decision: askquestion.ApprovalDecisionAllowOnce, Commentary: "approved, but keep it small"}}, want: patchtool.OutsideWorkspaceApproval{Decision: patchtool.OutsideWorkspaceDecisionAllowOnce, Commentary: "approved, but keep it small"}},
		{name: "deny with commentary", resp: askquestion.Response{Approval: &askquestion.ApprovalPayload{Decision: askquestion.ApprovalDecisionDeny, Commentary: "no because this is protected"}}, want: patchtool.OutsideWorkspaceApproval{Decision: patchtool.OutsideWorkspaceDecisionDeny, Commentary: "no because this is protected"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := outsideWorkspaceApprovalFromResponse(tc.resp)
			if err != nil {
				t.Fatalf("parse approval response: %v", err)
			}
			if got != tc.want {
				t.Fatalf("decision mismatch: got %v want %v", got, tc.want)
			}
		})
	}
}

func TestOutsideWorkspaceApprovalFromResponseRejectsMissingOrInvalidPayload(t *testing.T) {
	tests := []struct {
		name string
		resp askquestion.Response
	}{
		{name: "missing payload", resp: askquestion.Response{}},
		{name: "invalid decision", resp: askquestion.Response{Approval: &askquestion.ApprovalPayload{Decision: "maybe"}}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := outsideWorkspaceApprovalFromResponse(tc.resp); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestPatchOutsideWorkspaceApproverCachesSessionDecision(t *testing.T) {
	broker := askquestion.NewBroker()
	askCalls := 0
	broker.SetAskHandler(func(req askquestion.Request) (askquestion.Response, error) {
		askCalls++
		if !req.Approval {
			t.Fatalf("expected approval=true for outside-workspace ask")
		}
		if len(req.Suggestions) != 0 {
			t.Fatalf("expected structured approval options instead of suggestions, got %+v", req.Suggestions)
		}
		if len(req.ApprovalOptions) != 3 {
			t.Fatalf("expected 3 approval options, got %+v", req.ApprovalOptions)
		}
		if strings.Contains(req.Question, "workspace:") || strings.Contains(req.Question, "requested path:") || strings.Contains(req.Question, "Patch requested an edit outside the workspace.") {
			t.Fatalf("approval prompt contains removed fields: %q", req.Question)
		}
		if !strings.Contains(req.Question, "Allow editing /tmp/x.txt (outside workspace dir)?") {
			t.Fatalf("unexpected approval question text: %q", req.Question)
		}
		return askquestion.Response{Approval: &askquestion.ApprovalPayload{Decision: askquestion.ApprovalDecisionAllowSession}}, nil
	})

	approver := newOutsideWorkspaceApprover(broker, "editing")
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
	broker.SetAskHandler(func(askquestion.Request) (askquestion.Response, error) {
		return askquestion.Response{}, errors.New("ask failed")
	})

	approver := newOutsideWorkspaceApprover(broker, "editing")
	_, err := approver.Approve(context.Background(), patchtool.OutsideWorkspaceRequest{RequestedPath: "../x.txt", ResolvedPath: "/tmp/x.txt", WorkspaceRoot: "/tmp/w"})
	if err == nil {
		t.Fatal("expected ask error")
	}
}

func TestOutsideWorkspaceApproverUsesReadPromptText(t *testing.T) {
	broker := askquestion.NewBroker()
	askCalls := 0
	broker.SetAskHandler(func(req askquestion.Request) (askquestion.Response, error) {
		askCalls++
		if !strings.Contains(req.Question, "Allow reading /tmp/x.pdf (outside workspace dir)?") {
			t.Fatalf("unexpected read approval question text: %q", req.Question)
		}
		return askquestion.Response{Approval: &askquestion.ApprovalPayload{Decision: askquestion.ApprovalDecisionAllowOnce}}, nil
	})

	approver := newOutsideWorkspaceApprover(broker, "reading")
	approval, err := approver.Approve(context.Background(), patchtool.OutsideWorkspaceRequest{RequestedPath: "../x.pdf", ResolvedPath: "/tmp/x.pdf", WorkspaceRoot: "/tmp/w"})
	if err != nil {
		t.Fatalf("approve read call: %v", err)
	}
	if approval.Decision != patchtool.OutsideWorkspaceDecisionAllowOnce {
		t.Fatalf("unexpected approval decision: %v", approval)
	}
	if askCalls != 1 {
		t.Fatalf("expected one ask call, got %d", askCalls)
	}
}

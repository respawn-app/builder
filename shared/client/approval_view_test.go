package client

import (
	"context"
	"testing"
	"time"

	"builder/shared/clientui"
	"builder/shared/serverapi"
)

type stubApprovalViewService struct {
	resp serverapi.ApprovalListPendingBySessionResponse
	err  error
}

func (s *stubApprovalViewService) ListPendingApprovalsBySession(context.Context, serverapi.ApprovalListPendingBySessionRequest) (serverapi.ApprovalListPendingBySessionResponse, error) {
	return s.resp, s.err
}

func TestLoopbackApprovalViewClientDelegatesToService(t *testing.T) {
	svc := &stubApprovalViewService{resp: serverapi.ApprovalListPendingBySessionResponse{Approvals: []clientui.PendingApproval{{ApprovalID: "approval-1", SessionID: "session-1", CreatedAt: time.Now().UTC()}}}}
	client := NewLoopbackApprovalViewClient(svc)

	resp, err := client.ListPendingApprovalsBySession(context.Background(), serverapi.ApprovalListPendingBySessionRequest{SessionID: "session-1"})
	if err != nil {
		t.Fatalf("ListPendingApprovalsBySession: %v", err)
	}
	if len(resp.Approvals) != 1 || resp.Approvals[0].ApprovalID != "approval-1" {
		t.Fatalf("unexpected approvals response: %+v", resp)
	}
}

func TestLoopbackApprovalViewClientRequiresService(t *testing.T) {
	client := NewLoopbackApprovalViewClient(nil)
	if _, err := client.ListPendingApprovalsBySession(context.Background(), serverapi.ApprovalListPendingBySessionRequest{SessionID: "session-1"}); err == nil {
		t.Fatal("expected ListPendingApprovalsBySession to fail without service")
	}
}

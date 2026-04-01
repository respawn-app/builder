package client

import (
	"context"
	"testing"
	"time"

	"builder/shared/clientui"
	"builder/shared/serverapi"
)

type stubAskViewService struct {
	resp serverapi.AskListPendingBySessionResponse
	err  error
}

func (s *stubAskViewService) ListPendingAsksBySession(context.Context, serverapi.AskListPendingBySessionRequest) (serverapi.AskListPendingBySessionResponse, error) {
	return s.resp, s.err
}

func TestLoopbackAskViewClientDelegatesToService(t *testing.T) {
	svc := &stubAskViewService{resp: serverapi.AskListPendingBySessionResponse{Asks: []clientui.PendingAsk{{AskID: "ask-1", SessionID: "session-1", CreatedAt: time.Now().UTC()}}}}
	client := NewLoopbackAskViewClient(svc)

	resp, err := client.ListPendingAsksBySession(context.Background(), serverapi.AskListPendingBySessionRequest{SessionID: "session-1"})
	if err != nil {
		t.Fatalf("ListPendingAsksBySession: %v", err)
	}
	if len(resp.Asks) != 1 || resp.Asks[0].AskID != "ask-1" {
		t.Fatalf("unexpected asks response: %+v", resp)
	}
}

func TestLoopbackAskViewClientRequiresService(t *testing.T) {
	client := NewLoopbackAskViewClient(nil)
	if _, err := client.ListPendingAsksBySession(context.Background(), serverapi.AskListPendingBySessionRequest{SessionID: "session-1"}); err == nil {
		t.Fatal("expected ListPendingAsksBySession to fail without service")
	}
}

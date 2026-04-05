package client

import (
	"context"
	"testing"

	"builder/shared/serverapi"
)

type stubProcessControlService struct {
	inlineResp serverapi.ProcessInlineOutputResponse
	killedReq  serverapi.ProcessKillRequest
	inlineReq  serverapi.ProcessInlineOutputRequest
}

func (s *stubProcessControlService) KillProcess(_ context.Context, req serverapi.ProcessKillRequest) (serverapi.ProcessKillResponse, error) {
	s.killedReq = req
	return serverapi.ProcessKillResponse{}, nil
}

func (s *stubProcessControlService) GetInlineOutput(_ context.Context, req serverapi.ProcessInlineOutputRequest) (serverapi.ProcessInlineOutputResponse, error) {
	s.inlineReq = req
	return s.inlineResp, nil
}

func TestLoopbackProcessControlClientDelegatesToService(t *testing.T) {
	svc := &stubProcessControlService{inlineResp: serverapi.ProcessInlineOutputResponse{Output: "hello", LogPath: "/tmp/proc.log"}}
	client := NewLoopbackProcessControlClient(svc)

	if _, err := client.KillProcess(context.Background(), serverapi.ProcessKillRequest{ClientRequestID: "req-1", ProcessID: "proc-1"}); err != nil {
		t.Fatalf("KillProcess: %v", err)
	}
	if svc.killedReq.ClientRequestID != "req-1" || svc.killedReq.ProcessID != "proc-1" {
		t.Fatalf("unexpected kill request: %+v", svc.killedReq)
	}

	resp, err := client.GetInlineOutput(context.Background(), serverapi.ProcessInlineOutputRequest{ProcessID: "proc-1", MaxChars: 123})
	if err != nil {
		t.Fatalf("GetInlineOutput: %v", err)
	}
	if svc.inlineReq.ProcessID != "proc-1" || svc.inlineReq.MaxChars != 123 {
		t.Fatalf("unexpected inline request: %+v", svc.inlineReq)
	}
	if resp.Output != "hello" || resp.LogPath != "/tmp/proc.log" {
		t.Fatalf("unexpected inline response: %+v", resp)
	}
}

func TestLoopbackProcessControlClientRequiresService(t *testing.T) {
	client := NewLoopbackProcessControlClient(nil)
	if _, err := client.KillProcess(context.Background(), serverapi.ProcessKillRequest{ClientRequestID: "req-1", ProcessID: "proc-1"}); err == nil {
		t.Fatal("expected KillProcess to fail without service")
	}
	if _, err := client.GetInlineOutput(context.Background(), serverapi.ProcessInlineOutputRequest{ProcessID: "proc-1"}); err == nil {
		t.Fatal("expected GetInlineOutput to fail without service")
	}
}

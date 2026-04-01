package client

import (
	"context"
	"testing"

	"builder/shared/clientui"
	"builder/shared/serverapi"
)

type stubProcessViewService struct {
	listResp serverapi.ProcessListResponse
	getResp  serverapi.ProcessGetResponse
	listReq  serverapi.ProcessListRequest
	getReq   serverapi.ProcessGetRequest
}

func (s *stubProcessViewService) ListProcesses(context.Context, serverapi.ProcessListRequest) (serverapi.ProcessListResponse, error) {
	return s.listResp, nil
}

func (s *stubProcessViewService) GetProcess(context.Context, serverapi.ProcessGetRequest) (serverapi.ProcessGetResponse, error) {
	return s.getResp, nil
}

func TestLoopbackProcessViewClientDelegatesToService(t *testing.T) {
	process := clientui.BackgroundProcess{ID: "proc-1", OwnerSessionID: "session-1", OwnerRunID: "run-1", OwnerStepID: "step-1"}
	svc := &stubProcessViewService{
		listResp: serverapi.ProcessListResponse{Processes: []clientui.BackgroundProcess{process}},
		getResp:  serverapi.ProcessGetResponse{Process: &process},
	}
	client := NewLoopbackProcessViewClient(svc)

	list, err := client.ListProcesses(context.Background(), serverapi.ProcessListRequest{OwnerSessionID: "session-1"})
	if err != nil {
		t.Fatalf("ListProcesses: %v", err)
	}
	if len(list.Processes) != 1 || list.Processes[0].ID != "proc-1" {
		t.Fatalf("unexpected list response: %+v", list.Processes)
	}

	got, err := client.GetProcess(context.Background(), serverapi.ProcessGetRequest{ProcessID: "proc-1"})
	if err != nil {
		t.Fatalf("GetProcess: %v", err)
	}
	if got.Process == nil || got.Process.OwnerRunID != "run-1" || got.Process.OwnerStepID != "step-1" {
		t.Fatalf("unexpected get response: %+v", got.Process)
	}
}

func TestLoopbackProcessViewClientRequiresService(t *testing.T) {
	client := NewLoopbackProcessViewClient(nil)
	if _, err := client.ListProcesses(context.Background(), serverapi.ProcessListRequest{}); err == nil {
		t.Fatal("expected ListProcesses to fail without service")
	}
	if _, err := client.GetProcess(context.Background(), serverapi.ProcessGetRequest{ProcessID: "proc-1"}); err == nil {
		t.Fatal("expected GetProcess to fail without service")
	}
}

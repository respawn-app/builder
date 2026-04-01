package client

import (
	"context"
	"testing"

	"builder/shared/clientui"
	"builder/shared/serverapi"
)

type stubSessionViewService struct {
	mainView serverapi.SessionMainViewResponse
	run      serverapi.RunGetResponse
	err      error
}

func (s *stubSessionViewService) GetSessionMainView(context.Context, serverapi.SessionMainViewRequest) (serverapi.SessionMainViewResponse, error) {
	return s.mainView, s.err
}

func (s *stubSessionViewService) GetRun(context.Context, serverapi.RunGetRequest) (serverapi.RunGetResponse, error) {
	return s.run, s.err
}

func TestLoopbackSessionViewClientDelegatesToService(t *testing.T) {
	svc := &stubSessionViewService{
		mainView: serverapi.SessionMainViewResponse{MainView: clientui.RuntimeMainView{Session: clientui.RuntimeSessionView{SessionID: "session-1"}}},
		run:      serverapi.RunGetResponse{Run: &clientui.RunView{RunID: "run-1"}},
	}
	client := NewLoopbackSessionViewClient(svc)

	mainView, err := client.GetSessionMainView(context.Background(), serverapi.SessionMainViewRequest{SessionID: "session-1"})
	if err != nil {
		t.Fatalf("GetSessionMainView: %v", err)
	}
	if mainView.MainView.Session.SessionID != "session-1" {
		t.Fatalf("unexpected main view response: %+v", mainView)
	}
	run, err := client.GetRun(context.Background(), serverapi.RunGetRequest{SessionID: "session-1", RunID: "run-1"})
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if run.Run == nil || run.Run.RunID != "run-1" {
		t.Fatalf("unexpected run response: %+v", run)
	}
}

func TestLoopbackSessionViewClientRequiresService(t *testing.T) {
	client := NewLoopbackSessionViewClient(nil)
	if _, err := client.GetSessionMainView(context.Background(), serverapi.SessionMainViewRequest{SessionID: "session-1"}); err == nil {
		t.Fatalf("expected service error, got %v", err)
	}
	if _, err := client.GetRun(context.Background(), serverapi.RunGetRequest{SessionID: "session-1", RunID: "run-1"}); err == nil {
		t.Fatalf("expected service error, got %v", err)
	}
}

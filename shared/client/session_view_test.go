package client

import (
	"context"
	"testing"

	"builder/shared/clientui"
	"builder/shared/serverapi"
)

type stubSessionViewService struct {
	mainView serverapi.SessionMainViewResponse
	page     serverapi.SessionTranscriptPageResponse
	run      serverapi.RunGetResponse
	err      error
}

func (s *stubSessionViewService) GetSessionMainView(context.Context, serverapi.SessionMainViewRequest) (serverapi.SessionMainViewResponse, error) {
	return s.mainView, s.err
}

func (s *stubSessionViewService) GetSessionTranscriptPage(context.Context, serverapi.SessionTranscriptPageRequest) (serverapi.SessionTranscriptPageResponse, error) {
	return s.page, s.err
}

func (s *stubSessionViewService) GetRun(context.Context, serverapi.RunGetRequest) (serverapi.RunGetResponse, error) {
	return s.run, s.err
}

func TestLoopbackSessionViewClientDelegatesToService(t *testing.T) {
	svc := &stubSessionViewService{
		mainView: serverapi.SessionMainViewResponse{MainView: clientui.RuntimeMainView{Session: clientui.RuntimeSessionView{SessionID: "session-1"}}},
		page:     serverapi.SessionTranscriptPageResponse{Transcript: clientui.TranscriptPage{SessionID: "session-1", Entries: []clientui.ChatEntry{{Role: "assistant", Text: "hello"}}}},
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
	page, err := client.GetSessionTranscriptPage(context.Background(), serverapi.SessionTranscriptPageRequest{SessionID: "session-1"})
	if err != nil {
		t.Fatalf("GetSessionTranscriptPage: %v", err)
	}
	if page.Transcript.SessionID != "session-1" || len(page.Transcript.Entries) != 1 {
		t.Fatalf("unexpected transcript response: %+v", page)
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
	if _, err := client.GetSessionTranscriptPage(context.Background(), serverapi.SessionTranscriptPageRequest{SessionID: "session-1"}); err == nil {
		t.Fatalf("expected service error, got %v", err)
	}
	if _, err := client.GetRun(context.Background(), serverapi.RunGetRequest{SessionID: "session-1", RunID: "run-1"}); err == nil {
		t.Fatalf("expected service error, got %v", err)
	}
}

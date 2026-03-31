package client

import (
	"context"
	"testing"

	"builder/shared/serverapi"
)

func TestLoopbackRunPromptClientDelegatesToService(t *testing.T) {
	svc := &stubRunPromptService{
		response: serverapi.RunPromptResponse{SessionID: "session-1", Result: "done"},
	}
	client := NewLoopbackRunPromptClient(svc)

	result, err := client.RunPrompt(context.Background(), serverapi.RunPromptRequest{ClientRequestID: "req-1", Prompt: "hello"}, nil)
	if err != nil {
		t.Fatalf("RunPrompt: %v", err)
	}
	if svc.request.Prompt != "hello" {
		t.Fatalf("service request prompt = %q, want hello", svc.request.Prompt)
	}
	if result != svc.response {
		t.Fatalf("result = %+v, want %+v", result, svc.response)
	}
}

func TestLoopbackRunPromptClientRequiresService(t *testing.T) {
	client := NewLoopbackRunPromptClient(nil)

	_, err := client.RunPrompt(context.Background(), serverapi.RunPromptRequest{ClientRequestID: "req-1", Prompt: "hello"}, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if err.Error() != "run prompt service is required" {
		t.Fatalf("unexpected error: %v", err)
	}
}

type stubRunPromptService struct {
	request  serverapi.RunPromptRequest
	response serverapi.RunPromptResponse
	err      error
}

func (s *stubRunPromptService) RunPrompt(_ context.Context, req serverapi.RunPromptRequest, _ serverapi.RunPromptProgressSink) (serverapi.RunPromptResponse, error) {
	s.request = req
	return s.response, s.err
}

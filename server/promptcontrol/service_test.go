package promptcontrol

import (
	"context"
	"errors"
	"testing"

	"builder/server/idempotency"
	askquestion "builder/server/tools/askquestion"
	"builder/shared/clientui"
	"builder/shared/serverapi"
)

type stubPromptResponder struct {
	calls     int
	sessionID string
	response  askquestion.Response
	err       error
	submitErr error
}

func (s *stubPromptResponder) SubmitPromptResponse(sessionID string, resp askquestion.Response, err error) error {
	s.calls++
	s.sessionID = sessionID
	s.response = resp
	s.err = err
	return s.submitErr
}

type stubLeaseVerifier struct {
	calls int
	err   error
}

func (s *stubLeaseVerifier) RequireControllerLease(context.Context, string, string) error {
	s.calls++
	return s.err
}

func TestServiceAnswerAskReplaysCommittedResultAcrossLaterLeaseChange(t *testing.T) {
	responder := &stubPromptResponder{}
	verifier := &stubLeaseVerifier{}
	service := NewService(responder).WithControllerLeaseVerifier(verifier).WithIdempotencyCoordinator(idempotency.NewCoordinator(nil, idempotency.DefaultRetention))
	first := serverapi.AskAnswerRequest{
		ClientRequestID:   "req-1",
		SessionID:         "session-1",
		ControllerLeaseID: "lease-1",
		AskID:             "ask-1",
		Answer:            "hello",
	}
	second := first
	second.ControllerLeaseID = "lease-2"

	if err := service.AnswerAsk(context.Background(), first); err != nil {
		t.Fatalf("AnswerAsk first: %v", err)
	}
	verifier.err = serverapi.ErrInvalidControllerLease
	if err := service.AnswerAsk(context.Background(), second); err != nil {
		t.Fatalf("AnswerAsk replay: %v", err)
	}
	if responder.calls != 1 {
		t.Fatalf("responder call count = %d, want 1", responder.calls)
	}
	if verifier.calls != 1 {
		t.Fatalf("lease verifier call count = %d, want 1", verifier.calls)
	}
	if responder.sessionID != "session-1" || responder.response.RequestID != "ask-1" || responder.response.Answer != "hello" {
		t.Fatalf("unexpected stored response: session=%q response=%+v", responder.sessionID, responder.response)
	}
}

func TestServiceAnswerAskRejectsPayloadMismatch(t *testing.T) {
	service := NewService(&stubPromptResponder{}).WithIdempotencyCoordinator(idempotency.NewCoordinator(nil, idempotency.DefaultRetention))
	first := serverapi.AskAnswerRequest{
		ClientRequestID:   "req-1",
		SessionID:         "session-1",
		ControllerLeaseID: "lease-1",
		AskID:             "ask-1",
		Answer:            "hello",
	}
	second := first
	second.Answer = "goodbye"

	if err := service.AnswerAsk(context.Background(), first); err != nil {
		t.Fatalf("AnswerAsk first: %v", err)
	}
	if err := service.AnswerAsk(context.Background(), second); err == nil || err.Error() != "client_request_id \"req-1\" reused with different payload" {
		t.Fatalf("expected payload reuse rejection, got %v", err)
	}
}

func TestServiceAnswerApprovalReplaysKnownPromptError(t *testing.T) {
	responder := &stubPromptResponder{submitErr: serverapi.ErrPromptAlreadyResolved}
	service := NewService(responder).WithIdempotencyCoordinator(idempotency.NewCoordinator(nil, idempotency.DefaultRetention))
	req := serverapi.ApprovalAnswerRequest{
		ClientRequestID:   "req-1",
		SessionID:         "session-1",
		ControllerLeaseID: "lease-1",
		ApprovalID:        "approval-1",
		Decision:          clientui.ApprovalDecisionAllowOnce,
		Commentary:        "looks good",
	}

	err := service.AnswerApproval(context.Background(), req)
	if !errors.Is(err, serverapi.ErrPromptAlreadyResolved) {
		t.Fatalf("AnswerApproval first error = %v, want ErrPromptAlreadyResolved", err)
	}
	err = service.AnswerApproval(context.Background(), req)
	if !errors.Is(err, serverapi.ErrPromptAlreadyResolved) {
		t.Fatalf("AnswerApproval replay error = %v, want ErrPromptAlreadyResolved", err)
	}
	if responder.calls != 1 {
		t.Fatalf("responder call count = %d, want 1", responder.calls)
	}
	if responder.response.Approval == nil || responder.response.Approval.Decision != askquestion.ApprovalDecision(clientui.ApprovalDecisionAllowOnce) {
		t.Fatalf("unexpected approval response: %+v", responder.response)
	}
}

func TestServiceAnswerApprovalReplaysCommittedResultAcrossLaterLeaseChange(t *testing.T) {
	responder := &stubPromptResponder{}
	verifier := &stubLeaseVerifier{}
	service := NewService(responder).WithControllerLeaseVerifier(verifier).WithIdempotencyCoordinator(idempotency.NewCoordinator(nil, idempotency.DefaultRetention))
	first := serverapi.ApprovalAnswerRequest{
		ClientRequestID:   "req-1",
		SessionID:         "session-1",
		ControllerLeaseID: "lease-1",
		ApprovalID:        "approval-1",
		Decision:          clientui.ApprovalDecisionAllowOnce,
		Commentary:        "looks good",
	}
	second := first
	second.ControllerLeaseID = "lease-2"

	if err := service.AnswerApproval(context.Background(), first); err != nil {
		t.Fatalf("AnswerApproval first: %v", err)
	}
	verifier.err = serverapi.ErrInvalidControllerLease
	if err := service.AnswerApproval(context.Background(), second); err != nil {
		t.Fatalf("AnswerApproval replay: %v", err)
	}
	if responder.calls != 1 {
		t.Fatalf("responder call count = %d, want 1", responder.calls)
	}
	if verifier.calls != 1 {
		t.Fatalf("lease verifier call count = %d, want 1", verifier.calls)
	}
	if responder.response.Approval == nil || responder.response.Approval.Decision != askquestion.ApprovalDecision(clientui.ApprovalDecisionAllowOnce) {
		t.Fatalf("unexpected approval response: %+v", responder.response)
	}
}

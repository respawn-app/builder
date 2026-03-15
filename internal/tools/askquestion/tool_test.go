package askquestion

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"builder/internal/tools"
)

func TestBrokerFIFOQueue(t *testing.T) {
	b := NewBroker()

	ctx := context.Background()
	type out struct {
		id   string
		resp Response
		err  error
	}
	ch := make(chan out, 2)

	go func() {
		resp, err := b.Ask(ctx, Request{ID: "q1", Question: "one?"})
		ch <- out{id: "q1", resp: resp, err: err}
	}()
	for i := 0; i < 100; i++ {
		if len(b.Pending()) == 1 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	go func() {
		resp, err := b.Ask(ctx, Request{ID: "q2", Question: "two?"})
		ch <- out{id: "q2", resp: resp, err: err}
	}()

	time.Sleep(10 * time.Millisecond)
	pending := b.Pending()
	if len(pending) != 2 {
		t.Fatalf("pending count = %d", len(pending))
	}
	if pending[0].ID != "q1" || pending[1].ID != "q2" {
		t.Fatalf("pending not fifo: %+v", pending)
	}

	if err := b.Submit("q1", Response{Answer: "a1"}); err != nil {
		t.Fatalf("submit q1: %v", err)
	}
	if err := b.Submit("q2", Response{Answer: "a2"}); err != nil {
		t.Fatalf("submit q2: %v", err)
	}

	got := map[string]string{}
	for i := 0; i < 2; i++ {
		item := <-ch
		if item.err != nil {
			t.Fatalf("ask result err: %v", item.err)
		}
		got[item.id] = item.resp.Answer
	}

	if got["q1"] != "a1" || got["q2"] != "a2" {
		t.Fatalf("unexpected answers: %+v", got)
	}
}

func TestSubmitApprovalResponse(t *testing.T) {
	b := NewBroker()
	ctx := context.Background()
	type out struct {
		resp Response
		err  error
	}
	done := make(chan out, 1)

	go func() {
		resp, err := b.Ask(ctx, Request{ID: "approval", Question: "approve?", Approval: true, ApprovalOptions: []ApprovalOption{{Decision: ApprovalDecisionAllowOnce, Label: "Allow once"}, {Decision: ApprovalDecisionAllowSession, Label: "Allow for this session"}, {Decision: ApprovalDecisionDeny, Label: "Deny"}}})
		done <- out{resp: resp, err: err}
	}()

	for i := 0; i < 100; i++ {
		if len(b.Pending()) == 1 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}

	approval := &ApprovalPayload{Decision: ApprovalDecisionAllowSession, Commentary: "trusted path"}
	if err := b.Submit("approval", Response{Approval: approval}); err != nil {
		t.Fatalf("submit approval: %v", err)
	}

	select {
	case result := <-done:
		if result.err != nil {
			t.Fatalf("ask approval: %v", result.err)
		}
		if result.resp.RequestID != "approval" {
			t.Fatalf("request id = %q, want approval", result.resp.RequestID)
		}
		if result.resp.Approval == nil || *result.resp.Approval != *approval {
			t.Fatalf("approval payload = %+v, want %+v", result.resp.Approval, approval)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for approval response")
	}
}

func TestApprovalAskRequiresApprovalOptions(t *testing.T) {
	b := NewBroker()
	_, err := b.Ask(context.Background(), Request{ID: "approval", Question: "approve?", Approval: true})
	if err == nil {
		t.Fatal("expected error")
	}
	if err.Error() != "approval questions require approval_options" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSubmitRejectsPlainStringResponseForApprovalAsk(t *testing.T) {
	b := NewBroker()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type out struct {
		resp Response
		err  error
	}
	done := make(chan out, 1)
	approvalReq := Request{
		ID:       "approval",
		Question: "approve?",
		Approval: true,
		ApprovalOptions: []ApprovalOption{
			{Decision: ApprovalDecisionAllowOnce, Label: "Allow once"},
			{Decision: ApprovalDecisionAllowSession, Label: "Allow for this session"},
			{Decision: ApprovalDecisionDeny, Label: "Deny"},
		},
	}

	go func() {
		resp, err := b.Ask(ctx, approvalReq)
		done <- out{resp: resp, err: err}
	}()

	for i := 0; i < 100; i++ {
		if len(b.Pending()) == 1 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}

	if err := b.Submit("approval", Response{Answer: "allow once"}); err == nil {
		t.Fatal("expected submit error for plain-string approval response")
	} else if err.Error() != "approval questions require approval responses" {
		t.Fatalf("unexpected submit error: %v", err)
	}

	valid := &ApprovalPayload{Decision: ApprovalDecisionAllowOnce}
	if err := b.Submit("approval", Response{Approval: valid}); err != nil {
		t.Fatalf("submit valid approval: %v", err)
	}

	select {
	case result := <-done:
		if result.err != nil {
			t.Fatalf("ask approval: %v", result.err)
		}
		if result.resp.Approval == nil || *result.resp.Approval != *valid {
			t.Fatalf("approval payload = %+v, want %+v", result.resp.Approval, valid)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for approval response")
	}
}

func TestAskHandlerRejectsPlainStringResponseForApprovalAsk(t *testing.T) {
	b := NewBroker()
	b.SetAskHandler(func(Request) (Response, error) {
		return Response{Answer: "allow once"}, nil
	})

	_, err := b.Ask(context.Background(), Request{
		ID:       "approval",
		Question: "approve?",
		Approval: true,
		ApprovalOptions: []ApprovalOption{
			{Decision: ApprovalDecisionAllowOnce, Label: "Allow once"},
			{Decision: ApprovalDecisionAllowSession, Label: "Allow for this session"},
			{Decision: ApprovalDecisionDeny, Label: "Deny"},
		},
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if err.Error() != "approval questions require approval responses" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCanceledAskIsRemovedFromPendingQueue(t *testing.T) {
	b := NewBroker()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() {
		_, err := b.Ask(ctx, Request{ID: "q-cancel", Question: "will cancel?"})
		done <- err
	}()

	for i := 0; i < 100; i++ {
		if len(b.Pending()) == 1 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context canceled error, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for canceled ask")
	}

	if pending := b.Pending(); len(pending) != 0 {
		t.Fatalf("pending queue should be empty after cancellation, got %+v", pending)
	}
}

func TestToolCallRejectsActionField(t *testing.T) {
	tl := NewTool(NewBroker())
	result, err := tl.Call(context.Background(), tools.Call{
		ID:    "call-1",
		Name:  tools.ToolAskQuestion,
		Input: json.RawMessage(`{"question":"pick one","action":{"id":"unsafe"}}`),
	})
	if err != nil {
		t.Fatalf("unexpected call error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected error result, got %+v", result)
	}
	var payload map[string]string
	if err := json.Unmarshal(result.Output, &payload); err != nil {
		t.Fatalf("decode error output: %v", err)
	}
	if payload["error"] != `invalid input: field "action" is not allowed` {
		t.Fatalf("expected action rejection message, got %q", payload["error"])
	}
}

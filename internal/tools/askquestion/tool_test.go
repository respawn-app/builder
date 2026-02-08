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
		id  string
		ans string
		err error
	}
	ch := make(chan out, 2)

	go func() {
		resp, err := b.Ask(ctx, Request{ID: "q1", Question: "one?"})
		ch <- out{id: "q1", ans: resp.Answer, err: err}
	}()
	for i := 0; i < 100; i++ {
		if len(b.Pending()) == 1 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	go func() {
		resp, err := b.Ask(ctx, Request{ID: "q2", Question: "two?"})
		ch <- out{id: "q2", ans: resp.Answer, err: err}
	}()

	time.Sleep(10 * time.Millisecond)
	pending := b.Pending()
	if len(pending) != 2 {
		t.Fatalf("pending count = %d", len(pending))
	}
	if pending[0].ID != "q1" || pending[1].ID != "q2" {
		t.Fatalf("pending not fifo: %+v", pending)
	}

	if err := b.Submit("q1", "a1"); err != nil {
		t.Fatalf("submit q1: %v", err)
	}
	if err := b.Submit("q2", "a2"); err != nil {
		t.Fatalf("submit q2: %v", err)
	}

	got := map[string]string{}
	for i := 0; i < 2; i++ {
		item := <-ch
		if item.err != nil {
			t.Fatalf("ask result err: %v", item.err)
		}
		got[item.id] = item.ans
	}

	if got["q1"] != "a1" || got["q2"] != "a2" {
		t.Fatalf("unexpected answers: %+v", got)
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

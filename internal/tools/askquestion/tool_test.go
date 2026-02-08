package askquestion

import (
	"context"
	"testing"
	"time"

	"builder/internal/actions"
)

func TestBrokerFIFOQueue(t *testing.T) {
	b := NewBroker(actions.NewRegistry())

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

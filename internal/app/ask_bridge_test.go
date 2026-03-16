package app

import (
	"context"
	"testing"

	"builder/internal/tools/askquestion"
)

func TestAskBridgeHandleUsesSynchronousBrokerPathWithoutQueuedEvent(t *testing.T) {
	bridge := newAskBridge()
	broker := askquestion.NewBroker()
	broker.SetAskHandler(bridge.Handle)

	done := make(chan struct{})
	go func() {
		defer close(done)
		evt := <-bridge.Events()
		evt.reply <- askReply{response: askquestion.Response{RequestID: evt.req.ID, Answer: "handled"}}
	}()

	resp, err := broker.Ask(context.Background(), askquestion.Request{ID: "q1", Question: "one?"})
	if err != nil {
		t.Fatalf("ask: %v", err)
	}
	if resp.Answer != "handled" {
		t.Fatalf("unexpected response: %+v", resp)
	}
	<-done
	if pending := broker.Pending(); len(pending) != 0 {
		t.Fatalf("expected no broker pending requests in synchronous bridge mode, got %+v", pending)
	}
}

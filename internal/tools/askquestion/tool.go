package askquestion

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"builder/internal/tools"
	"github.com/google/uuid"
)

type Request struct {
	ID          string   `json:"id"`
	Question    string   `json:"question"`
	Suggestions []string `json:"suggestions,omitempty"`
	Approval    bool     `json:"approval,omitempty"`
}

type Response struct {
	RequestID string `json:"request_id"`
	Answer    string `json:"answer"`
}

type Broker struct {
	mu    sync.Mutex
	queue []*pending
	onAsk func(Request) (string, error)
}

type pending struct {
	req Request
	ch  chan responseResult
}

type responseResult struct {
	answer string
	err    error
}

func NewBroker() *Broker {
	return &Broker{}
}

func (b *Broker) SetAskHandler(handler func(Request) (string, error)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.onAsk = handler
}

func (b *Broker) Ask(ctx context.Context, req Request) (Response, error) {
	if req.ID == "" {
		req.ID = uuid.NewString()
	}
	if req.Question == "" {
		return Response{}, errors.New("question is required")
	}

	p := &pending{req: req, ch: make(chan responseResult, 1)}
	b.mu.Lock()
	b.queue = append(b.queue, p)
	h := b.onAsk
	b.mu.Unlock()
	defer b.dequeue(req.ID)

	if h != nil {
		answer, err := h(req)
		if err != nil {
			return Response{}, err
		}
		p.ch <- responseResult{answer: answer}
	}

	select {
	case <-ctx.Done():
		return Response{}, ctx.Err()
	case rr := <-p.ch:
		if rr.err != nil {
			return Response{}, rr.err
		}
		return Response{RequestID: req.ID, Answer: rr.answer}, nil
	}
}

func (b *Broker) Submit(requestID, answer string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, p := range b.queue {
		if p.req.ID == requestID {
			p.ch <- responseResult{answer: answer}
			return nil
		}
	}
	return fmt.Errorf("request %s not found", requestID)
}

func (b *Broker) Pending() []Request {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]Request, 0, len(b.queue))
	for _, p := range b.queue {
		out = append(out, p.req)
	}
	return out
}

func (b *Broker) dequeue(requestID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]*pending, 0, len(b.queue))
	for _, p := range b.queue {
		if p.req.ID == requestID {
			continue
		}
		out = append(out, p)
	}
	b.queue = out
}

type input struct {
	Question    string   `json:"question"`
	Suggestions []string `json:"suggestions,omitempty"`
}

type Tool struct {
	broker *Broker
}

func NewTool(b *Broker) *Tool {
	return &Tool{broker: b}
}

func (t *Tool) Name() tools.ID {
	return tools.ToolAskQuestion
}

func (t *Tool) Call(ctx context.Context, c tools.Call) (tools.Result, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(c.Input, &raw); err != nil {
		return tools.ErrorResult(c, fmt.Sprintf("invalid input: %v", err)), nil
	}
	if _, ok := raw["action"]; ok {
		return tools.ErrorResult(c, "invalid input: field \"action\" is not allowed"), nil
	}

	var in input
	if err := json.Unmarshal(c.Input, &in); err != nil {
		return tools.ErrorResult(c, fmt.Sprintf("invalid input: %v", err)), nil
	}
	resp, err := t.broker.Ask(ctx, Request{
		ID:          c.ID,
		Question:    in.Question,
		Suggestions: in.Suggestions,
	})
	if err != nil {
		return tools.ErrorResult(c, err.Error()), nil
	}
	body, marshalErr := json.Marshal(resp)
	if marshalErr != nil {
		return tools.Result{}, marshalErr
	}
	return tools.Result{CallID: c.ID, Name: c.Name, Output: body}, nil
}

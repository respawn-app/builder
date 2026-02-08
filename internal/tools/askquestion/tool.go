package askquestion

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"builder/internal/actions"
	"builder/internal/tools"
	"github.com/google/uuid"
)

type ActionBinding struct {
	ID      string          `json:"id"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

type Request struct {
	ID          string         `json:"id"`
	Question    string         `json:"question"`
	Suggestions []string       `json:"suggestions,omitempty"`
	Action      *ActionBinding `json:"action,omitempty"`
}

type Response struct {
	RequestID string `json:"request_id"`
	Answer    string `json:"answer"`
}

type Broker struct {
	mu      sync.Mutex
	queue   []*pending
	onAsk   func(Request) (string, error)
	actions *actions.Registry
}

type pending struct {
	req Request
	ch  chan responseResult
}

type responseResult struct {
	answer string
	err    error
}

func NewBroker(reg *actions.Registry) *Broker {
	if reg == nil {
		reg = actions.NewRegistry()
	}
	return &Broker{actions: reg}
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
		if req.Action != nil {
			if err := b.actions.Execute(ctx, req.Action.ID, req.Action.Payload); err != nil {
				return Response{}, err
			}
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
	Question    string         `json:"question"`
	Suggestions []string       `json:"suggestions,omitempty"`
	Action      *ActionBinding `json:"action,omitempty"`
}

type Tool struct {
	broker *Broker
}

func NewTool(b *Broker) *Tool {
	return &Tool{broker: b}
}

func (t *Tool) Name() string {
	return "ask_question"
}

func (t *Tool) Definition() tools.Definition {
	schema := json.RawMessage(`{"type":"object","required":["question"],"properties":{"question":{"type":"string"},"suggestions":{"type":"array","items":{"type":"string"}},"action":{"type":"object","properties":{"id":{"type":"string"},"payload":{"type":"object"}}}}}`)
	return tools.Definition{
		Name:        t.Name(),
		Description: "Ask user a question and block until answered.",
		Schema:      schema,
	}
}

func (t *Tool) Call(ctx context.Context, c tools.Call) (tools.Result, error) {
	var in input
	if err := json.Unmarshal(c.Input, &in); err != nil {
		return resultErr(c, fmt.Sprintf("invalid input: %v", err)), nil
	}
	resp, err := t.broker.Ask(ctx, Request{
		ID:          c.ID,
		Question:    in.Question,
		Suggestions: in.Suggestions,
		Action:      in.Action,
	})
	if err != nil {
		return resultErr(c, err.Error()), nil
	}
	body, marshalErr := json.Marshal(resp)
	if marshalErr != nil {
		return tools.Result{}, marshalErr
	}
	return tools.Result{CallID: c.ID, Name: c.Name, Output: body}, nil
}

func resultErr(c tools.Call, msg string) tools.Result {
	body, _ := json.Marshal(map[string]any{"error": msg})
	return tools.Result{CallID: c.ID, Name: c.Name, Output: body, IsError: true}
}

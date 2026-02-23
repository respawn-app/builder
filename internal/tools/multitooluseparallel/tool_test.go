package multitooluseparallel

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"builder/internal/tools"
)

type fakeHandler struct {
	id    tools.ID
	delay time.Duration
}

func (f fakeHandler) Name() tools.ID { return f.id }

func (f fakeHandler) Call(_ context.Context, c tools.Call) (tools.Result, error) {
	if f.delay > 0 {
		time.Sleep(f.delay)
	}
	out, _ := json.Marshal(map[string]any{
		"tool": string(c.Name),
		"id":   c.ID,
	})
	return tools.Result{CallID: c.ID, Name: c.Name, Output: out}, nil
}

func TestCallExecutesSubtoolsInParallelAndPreservesDeclaredOrder(t *testing.T) {
	var reg *tools.Registry
	reg = tools.NewRegistry(
		fakeHandler{id: tools.ToolShell, delay: 40 * time.Millisecond},
		fakeHandler{id: tools.ToolPatch, delay: 1 * time.Millisecond},
		New(func() *tools.Registry { return reg }),
	)
	tool := New(func() *tools.Registry { return reg })

	input := json.RawMessage(`{
		"tool_uses": [
			{"recipient_name":"functions.shell","parameters":{"command":"pwd"}},
			{"recipient_name":"functions.patch","parameters":{"patch":"*** Begin Patch\n*** End Patch\n"}}
		]
	}`)
	result, err := tool.Call(context.Background(), tools.Call{
		ID:    "call_parallel_1",
		Name:  tools.ToolMultiToolUseParallel,
		Input: input,
	})
	if err != nil {
		t.Fatalf("parallel call error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error result: %s", string(result.Output))
	}

	var out struct {
		Results []struct {
			RecipientName string `json:"recipient_name"`
			CallID        string `json:"call_id"`
			Name          string `json:"name"`
			IsError       bool   `json:"is_error"`
		} `json:"results"`
	}
	if err := json.Unmarshal(result.Output, &out); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	if len(out.Results) != 2 {
		t.Fatalf("results len=%d want 2", len(out.Results))
	}
	if out.Results[0].RecipientName != "functions.shell" || out.Results[1].RecipientName != "functions.patch" {
		t.Fatalf("recipient order mismatch: %+v", out.Results)
	}
	if out.Results[0].CallID != "call_parallel_1.1" || out.Results[1].CallID != "call_parallel_1.2" {
		t.Fatalf("subcall ids mismatch: %+v", out.Results)
	}
}

func TestCallRejectsInvalidRecipientNamespace(t *testing.T) {
	var reg *tools.Registry
	reg = tools.NewRegistry(
		fakeHandler{id: tools.ToolShell},
		New(func() *tools.Registry { return reg }),
	)
	tool := New(func() *tools.Registry { return reg })

	input := json.RawMessage(`{"tool_uses":[{"recipient_name":"shell","parameters":{"command":"pwd"}}]}`)
	result, err := tool.Call(context.Background(), tools.Call{
		ID:    "call_parallel_2",
		Name:  tools.ToolMultiToolUseParallel,
		Input: input,
	})
	if err != nil {
		t.Fatalf("parallel call error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected tool error result, got: %s", string(result.Output))
	}
}

func TestCallRejectsRecursiveParallelInvocation(t *testing.T) {
	var reg *tools.Registry
	reg = tools.NewRegistry(New(func() *tools.Registry { return reg }))
	tool := New(func() *tools.Registry { return reg })

	input := json.RawMessage(`{"tool_uses":[{"recipient_name":"functions.multi_tool_use.parallel","parameters":{"tool_uses":[]}}]}`)
	result, err := tool.Call(context.Background(), tools.Call{
		ID:    "call_parallel_3",
		Name:  tools.ToolMultiToolUseParallel,
		Input: input,
	})
	if err != nil {
		t.Fatalf("parallel call error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected tool error result, got: %s", string(result.Output))
	}
}

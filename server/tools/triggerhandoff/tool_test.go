package triggerhandoff

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"builder/server/llm"
	"builder/server/tools"
)

type controllerStub struct {
	activeCall         llm.ToolCall
	stepID             string
	summarizerPrompt   string
	futureAgentMessage string
	summary            string
	futureAdded        bool
	err                error
}

func (s *controllerStub) TriggerHandoff(_ context.Context, stepID string, activeCall llm.ToolCall, summarizerPrompt string, futureAgentMessage string) (string, bool, error) {
	s.activeCall = activeCall
	s.stepID = stepID
	s.summarizerPrompt = summarizerPrompt
	s.futureAgentMessage = futureAgentMessage
	if s.err != nil {
		return "", false, s.err
	}
	return s.summary, s.futureAdded, nil
}

func TestToolCallPassesArgumentsToController(t *testing.T) {
	stub := &controllerStub{summary: "Handoff triggered.", futureAdded: true}
	tool := New(func() Controller { return stub })
	input := json.RawMessage(`{"summarizer_prompt":"keep API details","future_agent_message":"resume with tests"}`)

	result, err := tool.Call(context.Background(), tools.Call{ID: "call-1", Name: tools.ToolTriggerHandoff, Input: input, StepID: "step-1"})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success result, got %s", string(result.Output))
	}
	if stub.stepID != "step-1" || stub.summarizerPrompt != "keep API details" || stub.futureAgentMessage != "resume with tests" {
		t.Fatalf("unexpected controller args: %+v", stub)
	}
	if stub.activeCall.ID != "call-1" || stub.activeCall.Name != string(tools.ToolTriggerHandoff) {
		t.Fatalf("unexpected active call: %+v", stub.activeCall)
	}
	if string(stub.activeCall.Input) != string(input) {
		t.Fatalf("unexpected active call input: %s", string(stub.activeCall.Input))
	}
	var payload ResultPayload
	if err := json.Unmarshal(result.Output, &payload); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if payload.Summary != "Handoff triggered." || !payload.FutureAgentMessageAdded {
		t.Fatalf("unexpected output payload: %+v", payload)
	}
}

func TestToolCallTreatsArgsAsOptional(t *testing.T) {
	stub := &controllerStub{summary: "Handoff triggered."}
	tool := New(func() Controller { return stub })

	result, err := tool.Call(context.Background(), tools.Call{ID: "call-1", Name: tools.ToolTriggerHandoff, Input: json.RawMessage(`{}`), StepID: "step-2"})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success result, got %s", string(result.Output))
	}
	if stub.summarizerPrompt != "" || stub.futureAgentMessage != "" {
		t.Fatalf("expected optional args to remain blank, got %+v", stub)
	}
}

func TestToolCallReturnsControllerErrorsAsToolErrors(t *testing.T) {
	tool := New(func() Controller { return &controllerStub{err: errors.New("too early")} })

	result, err := tool.Call(context.Background(), tools.Call{ID: "call-1", Name: tools.ToolTriggerHandoff, Input: json.RawMessage(`{}`), StepID: "step-3"})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected tool error result, got %s", string(result.Output))
	}
	if string(result.Output) == "" {
		t.Fatal("expected non-empty error output")
	}
}

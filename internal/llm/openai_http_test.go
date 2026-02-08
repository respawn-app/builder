package llm

import (
	"context"
	"encoding/json"
	"testing"
)

type staticAuth struct{}

func (staticAuth) AuthorizationHeader(context.Context) (string, error) {
	return "Bearer token", nil
}

func TestBuildPayload_SerializesAssistantToolCalls(t *testing.T) {
	transport := NewHTTPTransport(staticAuth{})
	payload, err := transport.buildPayload(OpenAIRequest{
		Model:        "gpt-5",
		SystemPrompt: "sys",
		Messages: []Message{
			{
				Role:    RoleAssistant,
				Content: "",
				ToolCalls: []ToolCall{
					{ID: "call-1", Name: "bash", Input: json.RawMessage(`{"command":"pwd"}`)},
				},
			},
			{Role: RoleTool, ToolCallID: "call-1", Name: "bash", Content: "{}"},
		},
	})
	if err != nil {
		t.Fatalf("build payload: %v", err)
	}

	if len(payload.Messages) < 2 {
		t.Fatalf("expected at least 2 payload messages, got %d", len(payload.Messages))
	}
	assistant := payload.Messages[1]
	if assistant.Role != string(RoleAssistant) {
		t.Fatalf("unexpected role at assistant index: %s", assistant.Role)
	}
	if len(assistant.ToolCalls) != 1 {
		t.Fatalf("expected 1 assistant tool call, got %d", len(assistant.ToolCalls))
	}
	if assistant.ToolCalls[0].ID != "call-1" {
		t.Fatalf("unexpected assistant tool call id: %+v", assistant.ToolCalls[0])
	}
	if assistant.ToolCalls[0].Function.Name != "bash" {
		t.Fatalf("unexpected assistant tool function: %+v", assistant.ToolCalls[0])
	}
}

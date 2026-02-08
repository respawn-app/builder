package llm

import (
	"context"
	"encoding/json"
	"net/http"
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
	}, openAIAuthMode{})
	if err != nil {
		t.Fatalf("build payload: %v", err)
	}

	if payload.Instructions != "sys" {
		t.Fatalf("expected instructions to carry system prompt, got %q", payload.Instructions)
	}
	if len(payload.Input) != 2 {
		t.Fatalf("expected 2 input items, got %d", len(payload.Input))
	}
	call := payload.Input[0]
	if call.Type != "function_call" {
		t.Fatalf("expected function_call input item, got %q", call.Type)
	}
	if call.CallID != "call-1" || call.Name != "bash" {
		t.Fatalf("unexpected function call item: %+v", call)
	}
	if call.Arguments != "{\"command\":\"pwd\"}" {
		t.Fatalf("unexpected function call arguments: %s", call.Arguments)
	}
	result := payload.Input[1]
	if result.Type != "function_call_output" || result.CallID != "call-1" {
		t.Fatalf("unexpected function call output item: %+v", result)
	}
	if result.Output != "{}" {
		t.Fatalf("unexpected function call output payload: %s", result.Output)
	}
}

func TestRequestURL_UsesCodexEndpointForOAuth(t *testing.T) {
	transport := NewHTTPTransport(staticAuth{})
	transport.BaseURL = "https://api.openai.com/v1"

	got := transport.requestURL(openAIAuthMode{IsOAuth: true})
	if got != codexResponsesEndpoint {
		t.Fatalf("expected oauth endpoint %q, got %q", codexResponsesEndpoint, got)
	}
	standard := transport.requestURL(openAIAuthMode{})
	if standard != "https://api.openai.com/v1/responses" {
		t.Fatalf("expected standard responses endpoint, got %q", standard)
	}
}

func TestApplyHeaders_OAuthAddsCodexHeaders(t *testing.T) {
	transport := NewHTTPTransport(staticAuth{})
	req, err := http.NewRequest(http.MethodPost, "https://example.com", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	transport.applyHeaders(req, "Bearer x", openAIAuthMode{
		IsOAuth:   true,
		AccountID: "acc-1",
	}, "session-1")

	if got := req.Header.Get("Authorization"); got != "Bearer x" {
		t.Fatalf("unexpected authorization header: %q", got)
	}
	if got := req.Header.Get("originator"); got != defaultOriginator {
		t.Fatalf("unexpected originator header: %q", got)
	}
	if got := req.Header.Get("User-Agent"); got == "" {
		t.Fatal("expected user agent header")
	}
	if got := req.Header.Get("session_id"); got != "session-1" {
		t.Fatalf("unexpected session_id header: %q", got)
	}
	if got := req.Header.Get("ChatGPT-Account-Id"); got != "acc-1" {
		t.Fatalf("unexpected account id header: %q", got)
	}
}

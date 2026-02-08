package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/openai/openai-go/v3/responses"
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
					{ID: "call-1", Name: "shell", Input: json.RawMessage(`{"command":"pwd"}`)},
				},
			},
			{Role: RoleTool, ToolCallID: "call-1", Name: "shell", Content: "{}"},
		},
	}, openAIAuthMode{})
	if err != nil {
		t.Fatalf("build payload: %v", err)
	}

	if !payload.Instructions.Valid() || payload.Instructions.Value != "sys" {
		t.Fatalf("expected instructions to carry system prompt, got %+v", payload.Instructions)
	}

	jsonPayload := mustMarshalObject(t, payload)
	inputRaw, ok := jsonPayload["input"].([]any)
	if !ok {
		t.Fatalf("expected input array, got %#v", jsonPayload["input"])
	}
	if len(inputRaw) != 2 {
		t.Fatalf("expected 2 input items, got %d", len(inputRaw))
	}

	call, ok := inputRaw[0].(map[string]any)
	if !ok {
		t.Fatalf("expected function_call object, got %#v", inputRaw[0])
	}
	if call["type"] != "function_call" {
		t.Fatalf("expected function_call input item, got %v", call["type"])
	}
	if call["call_id"] != "call-1" || call["name"] != "shell" {
		t.Fatalf("unexpected function call item: %+v", call)
	}
	if call["arguments"] != "{\"command\":\"pwd\"}" {
		t.Fatalf("unexpected function call arguments: %v", call["arguments"])
	}

	result, ok := inputRaw[1].(map[string]any)
	if !ok {
		t.Fatalf("expected function_call_output object, got %#v", inputRaw[1])
	}
	if result["type"] != "function_call_output" || result["call_id"] != "call-1" {
		t.Fatalf("unexpected function call output item: %+v", result)
	}
	if result["output"] != "{}" {
		t.Fatalf("unexpected function call output payload: %v", result["output"])
	}
}

func TestBuildResponsesInput_AssistantUsesOutputTextContent(t *testing.T) {
	items := buildResponsesInput([]Message{
		{Role: RoleUser, Content: "u1"},
		{Role: RoleAssistant, Content: "a1"},
	})
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}

	jsonItems := mustMarshalItems(t, items)
	if got := contentTypeAt(t, jsonItems[0]); got != "input_text" {
		t.Fatalf("user content type=%q", got)
	}
	if got := contentTypeAt(t, jsonItems[1]); got != "output_text" {
		t.Fatalf("assistant content type=%q", got)
	}
}

func TestBuildResponsesInput_NonAssistantRolesUseInputText(t *testing.T) {
	items := buildResponsesInput([]Message{
		{Role: RoleSystem, Content: "s1"},
		{Role: RoleDeveloper, Content: "d1"},
		{Role: RoleUser, Content: "u1"},
	})
	if len(items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(items))
	}

	jsonItems := mustMarshalItems(t, items)
	for i, item := range jsonItems {
		if got := contentTypeAt(t, item); got != "input_text" {
			t.Fatalf("item %d content type=%q", i, got)
		}
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

func TestBuildPayload_AppliesReasoningEffortForOpenAIModels(t *testing.T) {
	transport := NewHTTPTransport(staticAuth{})
	payload, err := transport.buildPayload(OpenAIRequest{
		Model:           "gpt-5",
		ReasoningEffort: "xhigh",
	}, openAIAuthMode{})
	if err != nil {
		t.Fatalf("build payload: %v", err)
	}
	if payload.Reasoning.Effort != "xhigh" {
		t.Fatalf("expected effort xhigh, got %q", payload.Reasoning.Effort)
	}
}

func TestBuildPayload_SkipsReasoningEffortForUnknownModelFamily(t *testing.T) {
	transport := NewHTTPTransport(staticAuth{})
	payload, err := transport.buildPayload(OpenAIRequest{
		Model:           "custom-model",
		ReasoningEffort: "high",
	}, openAIAuthMode{})
	if err != nil {
		t.Fatalf("build payload: %v", err)
	}
	if payload.Reasoning.Effort != "" {
		t.Fatalf("expected no reasoning payload for non-openai model, got %+v", payload.Reasoning)
	}

	jsonPayload := mustMarshalObject(t, payload)
	if _, ok := jsonPayload["reasoning"]; ok {
		t.Fatalf("expected reasoning to be omitted for non-openai model, got %+v", jsonPayload["reasoning"])
	}
}

func mustMarshalObject(t *testing.T, payload responses.ResponseNewParams) map[string]any {
	t.Helper()
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	return out
}

func mustMarshalItems(t *testing.T, items []responses.ResponseInputItemUnionParam) []map[string]any {
	t.Helper()
	b, err := json.Marshal(items)
	if err != nil {
		t.Fatalf("marshal input items: %v", err)
	}
	var out []map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal input items: %v", err)
	}
	return out
}

func contentTypeAt(t *testing.T, item map[string]any) string {
	t.Helper()
	parts, ok := item["content"].([]any)
	if !ok || len(parts) == 0 {
		t.Fatalf("expected content array, got %#v", item["content"])
	}
	part, ok := parts[0].(map[string]any)
	if !ok {
		t.Fatalf("expected first content object, got %#v", parts[0])
	}
	typ, ok := part["type"].(string)
	if !ok {
		t.Fatalf("expected content type string, got %#v", part["type"])
	}
	return typ
}

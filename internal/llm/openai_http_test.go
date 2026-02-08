package llm

import (
	"context"
	"encoding/json"
	"strings"
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

func TestServiceBaseURL_UsesCodexEndpointBaseForOAuth(t *testing.T) {
	transport := NewHTTPTransport(staticAuth{})
	transport.BaseURL = "https://api.openai.com/v1"

	got := transport.serviceBaseURL(openAIAuthMode{IsOAuth: true})
	if got != strings.TrimSuffix(codexResponsesEndpoint, "/responses") {
		t.Fatalf("expected oauth base endpoint %q, got %q", strings.TrimSuffix(codexResponsesEndpoint, "/responses"), got)
	}
	standard := transport.serviceBaseURL(openAIAuthMode{})
	if standard != "https://api.openai.com/v1" {
		t.Fatalf("expected standard base endpoint, got %q", standard)
	}
}

func TestBuildRequestOptions_OAuthAddsCodexHeaders(t *testing.T) {
	transport := NewHTTPTransport(staticAuth{})
	opts := transport.buildRequestOptions("Bearer x", openAIAuthMode{
		IsOAuth:   true,
		AccountID: "acc-1",
	}, "session-1")

	if len(opts) != 5 {
		t.Fatalf("expected 5 request options, got %d", len(opts))
	}
	if len(transport.buildRequestOptions("Bearer x", openAIAuthMode{}, "")) != 1 {
		t.Fatal("expected non-oauth options to include only authorization header")
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
	if payload.Reasoning.Summary != "concise" {
		t.Fatalf("expected concise reasoning summary, got %q", payload.Reasoning.Summary)
	}
	if len(payload.Include) != 1 || payload.Include[0] != responses.ResponseIncludableReasoningEncryptedContent {
		t.Fatalf("expected reasoning.encrypted_content include, got %+v", payload.Include)
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
	if len(payload.Include) != 0 {
		t.Fatalf("expected no include list for non-openai model, got %+v", payload.Include)
	}

	jsonPayload := mustMarshalObject(t, payload)
	if _, ok := jsonPayload["reasoning"]; ok {
		t.Fatalf("expected reasoning to be omitted for non-openai model, got %+v", jsonPayload["reasoning"])
	}
}

func TestBuildResponsesInput_AssistantReasoningItemsUseEncryptedContentOnly(t *testing.T) {
	items := buildResponsesInput([]Message{
		{
			Role:    RoleAssistant,
			Content: "a1",
			ReasoningItems: []ReasoningItem{
				{ID: "rs_1", EncryptedContent: "enc_1"},
			},
		},
	})
	if len(items) != 2 {
		t.Fatalf("expected assistant message + reasoning item, got %d", len(items))
	}

	jsonItems := mustMarshalItems(t, items)
	second := jsonItems[1]
	if second["type"] != "reasoning" {
		t.Fatalf("expected reasoning item type, got %#v", second["type"])
	}
	if second["id"] != "rs_1" {
		t.Fatalf("expected reasoning id rs_1, got %#v", second["id"])
	}
	if second["encrypted_content"] != "enc_1" {
		t.Fatalf("expected encrypted content enc_1, got %#v", second["encrypted_content"])
	}
	if text, ok := second["text"].(string); ok && strings.TrimSpace(text) != "" {
		t.Fatalf("expected no reasoning text to be serialized, got %q", text)
	}
}

func TestBuildPayload_AddsAdditionalPropertiesFalseToToolSchemas(t *testing.T) {
	transport := NewHTTPTransport(staticAuth{})
	payload, err := transport.buildPayload(OpenAIRequest{
		Model: "gpt-5",
		Tools: []Tool{
			{
				Name:   "ask_question",
				Schema: json.RawMessage(`{"type":"object","required":["question"],"properties":{"question":{"type":"string"},"meta":{"type":"object","properties":{"foo":{"type":"string"}}}}}`),
			},
		},
	}, openAIAuthMode{})
	if err != nil {
		t.Fatalf("build payload: %v", err)
	}

	jsonPayload := mustMarshalObject(t, payload)
	tools, ok := jsonPayload["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("expected one tool, got %#v", jsonPayload["tools"])
	}
	tool, ok := tools[0].(map[string]any)
	if !ok {
		t.Fatalf("unexpected tool value: %#v", tools[0])
	}
	if strict, ok := tool["strict"].(bool); !ok || strict {
		t.Fatalf("expected function tool strict=false, got %#v", tool["strict"])
	}
	params, ok := tool["parameters"].(map[string]any)
	if !ok {
		t.Fatalf("expected parameters object, got %#v", tool["parameters"])
	}
	if got, ok := params["additionalProperties"].(bool); !ok || got {
		t.Fatalf("expected root additionalProperties=false, got %#v", params["additionalProperties"])
	}

	props, ok := params["properties"].(map[string]any)
	if !ok {
		t.Fatalf("expected root properties object, got %#v", params["properties"])
	}
	meta, ok := props["meta"].(map[string]any)
	if !ok {
		t.Fatalf("expected nested meta object schema, got %#v", props["meta"])
	}
	if got, ok := meta["additionalProperties"].(bool); !ok || got {
		t.Fatalf("expected nested additionalProperties=false, got %#v", meta["additionalProperties"])
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

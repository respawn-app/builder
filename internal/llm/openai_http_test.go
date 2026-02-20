package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	}, nil)
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
	}, nil)
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
	if len(transport.buildRequestOptions("Bearer x", openAIAuthMode{}, "session-1")) != 4 {
		t.Fatal("expected non-oauth options to include auth/session/caching headers")
	}
	if len(transport.buildRequestOptions("Bearer x", openAIAuthMode{}, "")) != 3 {
		t.Fatal("expected non-oauth options to include auth/caching headers")
	}
}

func TestBuildPayload_UsesTransportStoreSetting(t *testing.T) {
	transport := NewHTTPTransport(staticAuth{})
	transport.Store = true
	payload, err := transport.buildPayload(OpenAIRequest{Model: "gpt-5"}, openAIAuthMode{})
	if err != nil {
		t.Fatalf("build payload: %v", err)
	}
	jsonPayload := mustMarshalObject(t, payload)
	if got, ok := jsonPayload["store"].(bool); !ok || !got {
		t.Fatalf("expected store=true in payload, got %#v", jsonPayload["store"])
	}
}

func TestBuildPayload_AddsNativeWebSearchToolWhenEnabled(t *testing.T) {
	transport := NewHTTPTransport(staticAuth{})
	payload, err := transport.buildPayload(OpenAIRequest{
		Model:                 "gpt-5",
		EnableNativeWebSearch: true,
	}, openAIAuthMode{})
	if err != nil {
		t.Fatalf("build payload: %v", err)
	}

	jsonPayload := mustMarshalObject(t, payload)
	tools, ok := jsonPayload["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("expected one native tool, got %#v", jsonPayload["tools"])
	}
	tool, ok := tools[0].(map[string]any)
	if !ok {
		t.Fatalf("expected web search tool object, got %#v", tools[0])
	}
	if got := tool["type"]; got != "web_search" {
		t.Fatalf("expected web_search tool, got %#v", got)
	}
}

func TestBuildPayload_DoesNotAddNativeWebSearchToolWhenDisabled(t *testing.T) {
	transport := NewHTTPTransport(staticAuth{})
	payload, err := transport.buildPayload(OpenAIRequest{
		Model:                 "gpt-5",
		EnableNativeWebSearch: false,
	}, openAIAuthMode{})
	if err != nil {
		t.Fatalf("build payload: %v", err)
	}

	jsonPayload := mustMarshalObject(t, payload)
	if _, ok := jsonPayload["tools"]; ok {
		t.Fatalf("expected no tools in payload, got %#v", jsonPayload["tools"])
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
	}, nil)
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

func TestBuildResponsesInput_CanonicalCompactionItemRoundTrip(t *testing.T) {
	items := buildResponsesInput(nil, []ResponseItem{
		{Type: ResponseItemTypeMessage, Role: RoleUser, Content: "u1"},
		{Type: ResponseItemTypeCompaction, ID: "cmp_1", EncryptedContent: "enc_1"},
	})
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	jsonItems := mustMarshalItems(t, items)
	if got := contentTypeAt(t, jsonItems[0]); got != "input_text" {
		t.Fatalf("expected user input text content, got %q", got)
	}
	if got := jsonItems[0]["role"]; got != "user" {
		t.Fatalf("expected user role, got %#v", got)
	}
	if got := jsonItems[1]["type"]; got != "compaction" {
		t.Fatalf("expected compaction item, got %#v", got)
	}
	if got := jsonItems[1]["encrypted_content"]; got != "enc_1" {
		t.Fatalf("unexpected compaction encrypted content: %#v", got)
	}
}

func TestParseOutputItems_PreservesCompactionItem(t *testing.T) {
	raw := []byte(`[
		{
			"type":"message",
			"role":"user",
			"id":"msg_1",
			"content":[{"type":"input_text","text":"hello"}]
		},
		{
			"type":"compaction",
			"id":"cmp_1",
			"encrypted_content":"enc_1"
		}
	]`)
	var output []responses.ResponseOutputItemUnion
	if err := json.Unmarshal(raw, &output); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	items, assistantText, assistantPhase, toolCalls, reasoning, reasoningItems := parseOutputItems(output)
	if assistantText != "" {
		t.Fatalf("expected no assistant text, got %q", assistantText)
	}
	if assistantPhase != "" {
		t.Fatalf("expected empty assistant phase, got %q", assistantPhase)
	}
	if len(toolCalls) != 0 || len(reasoning) != 0 || len(reasoningItems) != 0 {
		t.Fatalf("expected no tool/reasoning outputs, got calls=%d reasoning=%d encrypted=%d", len(toolCalls), len(reasoning), len(reasoningItems))
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 canonical items, got %d", len(items))
	}
	if items[1].Type != ResponseItemTypeCompaction || items[1].EncryptedContent != "enc_1" {
		t.Fatalf("unexpected compaction item: %+v", items[1])
	}
}

func TestCompactRequestTargetsResponsesCompactPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses/compact" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"resp_cmp_1",
			"object":"response.compaction",
			"created_at":1731459200,
			"output":[
				{"type":"message","role":"user","content":[{"type":"input_text","text":"u1"}]},
				{"type":"compaction","id":"cmp_1","encrypted_content":"enc_1"}
			],
			"usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15}
		}`))
	}))
	defer server.Close()

	transport := NewHTTPTransport(staticAuth{})
	transport.BaseURL = server.URL + "/v1"
	transport.Client = server.Client()

	resp, err := transport.Compact(context.Background(), OpenAICompactionRequest{
		Model: "gpt-5",
		InputItems: []ResponseItem{
			{Type: ResponseItemTypeMessage, Role: RoleUser, Content: "u1"},
		},
	})
	if err != nil {
		t.Fatalf("compact request failed: %v", err)
	}
	if len(resp.OutputItems) != 2 {
		t.Fatalf("expected compact output items, got %d", len(resp.OutputItems))
	}
	if resp.OutputItems[1].Type != ResponseItemTypeCompaction {
		t.Fatalf("expected compaction output item, got %+v", resp.OutputItems[1])
	}
}

func TestCompactRequestAcceptsJSONBodyWithNonJSONContentType(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses/compact" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(`{
			"id":"resp_cmp_1",
			"object":"response.compaction",
			"created_at":1731459200,
			"output":[
				{"type":"message","role":"user","content":[{"type":"input_text","text":"u1"}]},
				{"type":"compaction","id":"cmp_1","encrypted_content":"enc_1"}
			],
			"usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15}
		}`))
	}))
	defer server.Close()

	transport := NewHTTPTransport(staticAuth{})
	transport.BaseURL = server.URL + "/v1"
	transport.Client = server.Client()

	resp, err := transport.Compact(context.Background(), OpenAICompactionRequest{
		Model: "gpt-5",
		InputItems: []ResponseItem{
			{Type: ResponseItemTypeMessage, Role: RoleUser, Content: "u1"},
		},
	})
	if err != nil {
		t.Fatalf("compact request failed: %v", err)
	}
	if len(resp.OutputItems) != 2 {
		t.Fatalf("expected compact output items, got %d", len(resp.OutputItems))
	}
	if resp.OutputItems[1].Type != ResponseItemTypeCompaction {
		t.Fatalf("expected compaction output item, got %+v", resp.OutputItems[1])
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

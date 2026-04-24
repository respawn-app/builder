package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"builder/server/auth"
	"builder/shared/toolspec"

	openai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/responses"
)

type staticAuth struct{}

func (staticAuth) AuthorizationHeader(context.Context) (string, error) {
	return "Bearer token", nil
}

type oauthStaticAuth struct{}

func (oauthStaticAuth) AuthorizationHeader(context.Context) (string, error) {
	return "Bearer token", nil
}

func (oauthStaticAuth) OpenAIAuthMetadata(context.Context) (string, string, error) {
	return "oauth", "acc-1", nil
}

type missingAuth struct{}

func (missingAuth) AuthorizationHeader(context.Context) (string, error) {
	return "", auth.ErrAuthNotConfigured
}

func requireProviderCapabilities(t *testing.T, transport *HTTPTransport, mode openAIAuthMode) ProviderCapabilities {
	t.Helper()
	caps, err := transport.providerCapabilitiesForMode(mode)
	if err != nil {
		t.Fatalf("resolve provider capabilities: %v", err)
	}
	return caps
}

func TestBuildPayload_SerializesAssistantToolCalls(t *testing.T) {
	transport := NewHTTPTransport(staticAuth{})
	payload, err := transport.buildPayload(OpenAIRequest{
		Model:        "gpt-5",
		SystemPrompt: "sys",
		Items: ItemsFromMessages([]Message{
			{
				Role:    RoleAssistant,
				Content: "",
				ToolCalls: []ToolCall{
					{ID: "call-1", Name: "shell", Input: json.RawMessage(`{"command":"pwd"}`)},
				},
			},
			{Role: RoleTool, ToolCallID: "call-1", Name: "shell", Content: "{}"},
		}),
	}, openAIAuthMode{}, requireProviderCapabilities(t, transport, openAIAuthMode{}))
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

func TestBuildResponsesInput_AssistantUsesTypedMessageInput(t *testing.T) {
	items := buildResponsesInput(ItemsFromMessages([]Message{
		{Role: RoleUser, Content: "u1"},
		{Role: RoleAssistant, Content: "a1"},
	}))
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}

	jsonItems := mustMarshalItems(t, items)
	if got := contentTypeAt(t, jsonItems[0]); got != "input_text" {
		t.Fatalf("user content type=%q", got)
	}
	if got := jsonItems[1]["type"]; got != "message" {
		t.Fatalf("assistant item type=%#v", got)
	}
	if got := jsonItems[1]["role"]; got != string(RoleAssistant) {
		t.Fatalf("assistant role=%#v", got)
	}
	if got := jsonItems[1]["status"]; got != "completed" {
		t.Fatalf("assistant status=%#v", got)
	}
	if got := contentTypeAt(t, jsonItems[1]); got != "output_text" {
		t.Fatalf("assistant content type=%q", got)
	}
}

func TestBuildResponsesInput_AssistantPreservesPhase(t *testing.T) {
	items := buildResponsesInput(ItemsFromMessages([]Message{{Role: RoleAssistant, Content: "a1", Phase: MessagePhaseCommentary}}))
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}

	jsonItems := mustMarshalItems(t, items)
	if got := jsonItems[0]["type"]; got != "message" {
		t.Fatalf("assistant item type=%#v", got)
	}
	if got := jsonItems[0]["phase"]; got != string(MessagePhaseCommentary) {
		t.Fatalf("assistant phase=%#v", got)
	}
	if got := jsonItems[0]["status"]; got != "completed" {
		t.Fatalf("assistant status=%#v", got)
	}
	if got := contentTypeAt(t, jsonItems[0]); got != "output_text" {
		t.Fatalf("assistant content type=%q", got)
	}
}

func TestBuildResponsesInput_CanonicalAssistantPreservesPhase(t *testing.T) {
	items := buildResponsesInput([]ResponseItem{{
		Type:    ResponseItemTypeMessage,
		Role:    RoleAssistant,
		Content: "done",
		Phase:   MessagePhaseFinal,
	}})
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}

	jsonItems := mustMarshalItems(t, items)
	if got := jsonItems[0]["type"]; got != "message" {
		t.Fatalf("assistant item type=%#v", got)
	}
	if got := jsonItems[0]["phase"]; got != string(MessagePhaseFinal) {
		t.Fatalf("assistant phase=%#v", got)
	}
	if got := jsonItems[0]["status"]; got != "completed" {
		t.Fatalf("assistant status=%#v", got)
	}
	if got := contentTypeAt(t, jsonItems[0]); got != "output_text" {
		t.Fatalf("assistant content type=%q", got)
	}
}

func TestBuildResponsesInput_NonAssistantRolesUseInputText(t *testing.T) {
	items := buildResponsesInput(ItemsFromMessages([]Message{
		{Role: RoleSystem, Content: "s1"},
		{Role: RoleDeveloper, Content: "d1"},
		{Role: RoleUser, Content: "u1"},
	}))
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

func TestBuildResponsesInput_ToolOutputSupportsStructuredInputImageItems(t *testing.T) {
	items := buildResponsesInput(ItemsFromMessages([]Message{
		{
			Role:       RoleTool,
			ToolCallID: "call_1",
			Content:    `[{"type":"input_image","image_url":"data:image/png;base64,abc"}]`,
		},
	}))
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}

	jsonItems := mustMarshalItems(t, items)
	if got := jsonItems[0]["type"]; got != "function_call_output" {
		t.Fatalf("expected function_call_output item, got %#v", got)
	}
	output, ok := jsonItems[0]["output"].([]any)
	if !ok || len(output) != 1 {
		t.Fatalf("expected structured output array, got %#v", jsonItems[0]["output"])
	}
	part, ok := output[0].(map[string]any)
	if !ok {
		t.Fatalf("expected structured output object, got %#v", output[0])
	}
	if got := part["type"]; got != "input_image" {
		t.Fatalf("expected input_image output content, got %#v", got)
	}
	if got := part["image_url"]; got != "data:image/png;base64,abc" {
		t.Fatalf("unexpected image_url in structured output: %#v", got)
	}
}

func TestMapOpenAIRequestError_UsesOpenAISDKContractError(t *testing.T) {
	err := mapOpenAIRequestError(
		"openai",
		&openai.Error{StatusCode: 400, Code: "context_length_exceeded", Type: "invalid_request_error", Message: "prompt too long"},
		nil,
		"openai responses compact request failed",
	)
	if !IsContextLengthOverflowError(err) {
		t.Fatalf("expected overflow classification, got err=%v", err)
	}

	var providerErr *ProviderAPIError
	if !errors.As(err, &providerErr) {
		t.Fatalf("expected ProviderAPIError, got %T", err)
	}
	if providerErr.ProviderID != "openai" || providerErr.ProviderCode != "context_length_exceeded" {
		t.Fatalf("unexpected provider error mapping: %+v", providerErr)
	}
}

func TestMapOpenAIRequestError_UsesOpenAIErrorEnvelopeFromRawResponse(t *testing.T) {
	rawResp := &http.Response{
		StatusCode: 422,
		Body: io.NopCloser(strings.NewReader(
			`{"error":{"type":"invalid_request_error","code":"input_too_long","param":"input","message":"too many tokens"}}`,
		)),
	}
	err := mapOpenAIRequestError("openai", nil, rawResp, "openai responses compact request failed")
	if !IsContextLengthOverflowError(err) {
		t.Fatalf("expected overflow classification from raw response contract, got err=%v", err)
	}

	var providerErr *ProviderAPIError
	if !errors.As(err, &providerErr) {
		t.Fatalf("expected ProviderAPIError, got %T", err)
	}
	if providerErr.ProviderParam != "input" {
		t.Fatalf("expected param=input, got %+v", providerErr)
	}
}

func TestMapOpenAIRequestError_UnknownProviderIDFailsFast(t *testing.T) {
	rawResp := &http.Response{
		StatusCode: 400,
		Body: io.NopCloser(strings.NewReader(
			`{"error":{"type":"invalid_request_error","code":"context_length_exceeded","param":"input","message":"too many tokens"}}`,
		)),
	}
	err := mapOpenAIRequestError("ollama", nil, rawResp, "openai responses compact request failed")
	if err == nil {
		t.Fatal("expected missing provider reducer error")
	}
	var providerErr *ProviderAPIError
	if !errors.As(err, &providerErr) {
		t.Fatalf("expected ProviderAPIError, got %T err=%v", err, err)
	}
	if providerErr.Code != UnifiedErrorCodeProviderContract || providerErr.ProviderID != "ollama" {
		t.Fatalf("expected provider contract error for ollama, got %+v", providerErr)
	}
	if rawResp.Body != nil {
		t.Fatal("expected response body to be closed and cleared on reducer registration failure")
	}
	if !IsNonRetriableModelError(err) {
		t.Fatalf("expected provider contract error to be non-retriable, got err=%v", err)
	}
}

func TestMapOpenAIRequestError_HandlesNilResponseBody(t *testing.T) {
	rawResp := &http.Response{StatusCode: 500, Body: nil}
	err := mapOpenAIRequestError("openai", nil, rawResp, "openai responses compact request failed")

	var providerErr *ProviderAPIError
	if !errors.As(err, &providerErr) {
		t.Fatalf("expected ProviderAPIError, got %T", err)
	}
	if providerErr.Raw != "<empty error body>" {
		t.Fatalf("expected empty body sentinel, got %+v", providerErr)
	}
}

func TestMapOpenAIRequestError_RepopulatesRawResponseBody(t *testing.T) {
	body := `{"error":{"type":"invalid_request_error","code":"context_length_exceeded","param":"input","message":"too many tokens"}}`
	rawResp := &http.Response{StatusCode: 400, Body: io.NopCloser(strings.NewReader(body))}
	_ = mapOpenAIRequestError("openai", nil, rawResp, "openai responses compact request failed")
	if rawResp.Body == nil {
		t.Fatal("expected response body to be re-populated")
	}
	defer rawResp.Body.Close()
	buf, err := io.ReadAll(rawResp.Body)
	if err != nil {
		t.Fatalf("read re-populated body: %v", err)
	}
	if strings.TrimSpace(string(buf)) != body {
		t.Fatalf("expected original body to remain available, got %q", string(buf))
	}
}

func TestMapOpenAIRequestError_UnwrapStabilityAcrossWrappingLayers(t *testing.T) {
	err := mapOpenAIRequestError(
		"openai",
		&openai.Error{StatusCode: 400, Code: "context_length_exceeded", Type: "invalid_request_error", Message: "prompt too long"},
		nil,
		"openai responses compact request failed",
	)
	err = fmt.Errorf("openai compact: %w", err)

	var providerErr *ProviderAPIError
	if !errors.As(err, &providerErr) {
		t.Fatalf("expected ProviderAPIError in unwrap chain, got %T", err)
	}
	if providerErr.Code != UnifiedErrorCodeContextLengthOverflow {
		t.Fatalf("expected overflow code in unwrap chain, got %+v", providerErr)
	}
}

func TestCompactErrorPath_ReturnsProviderAPIErrorWithDetectedProviderID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"type":"invalid_request_error","code":"context_length_exceeded","param":"input","message":"too many tokens"}}`))
	}))
	defer server.Close()

	transport := NewHTTPTransport(staticAuth{})
	transport.BaseURL = server.URL + "/v1"

	_, err := transport.Compact(context.Background(), OpenAICompactionRequest{
		Model:      "gpt-5",
		SessionID:  "s1",
		InputItems: []ResponseItem{{Type: ResponseItemTypeMessage, Role: RoleUser, Content: "hello"}},
	})
	if err == nil {
		t.Fatal("expected compact error")
	}
	var providerErr *ProviderAPIError
	if !errors.As(err, &providerErr) {
		t.Fatalf("expected ProviderAPIError from transport path, got %T err=%v", err, err)
	}
	if providerErr.ProviderID != "openai" || providerErr.Code != UnifiedErrorCodeContextLengthOverflow {
		t.Fatalf("expected openai overflow classification on loopback transport, got %+v", providerErr)
	}
	if !IsNonRetriableModelError(err) {
		t.Fatalf("expected 400 overflow response to remain non-retriable, got %v", err)
	}
}

func TestBuildResponsesInput_CanonicalToolOutputPromotesStructuredInputFileItems(t *testing.T) {
	const pdfDataURL = "data:application/pdf;base64,Zm9v"
	items := buildResponsesInput([]ResponseItem{
		{
			Type:   ResponseItemTypeFunctionCallOutput,
			CallID: "call_1",
			Name:   string(toolspec.ToolViewImage),
			Output: json.RawMessage(`[{"type":"input_file","file_data":"data:application/pdf;base64,Zm9v","filename":"doc.pdf"}]`),
		},
	})
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}

	jsonItems := mustMarshalItems(t, items)
	if got := jsonItems[0]["type"]; got != "function_call_output" {
		t.Fatalf("expected function_call_output item, got %#v", got)
	}
	if output, ok := jsonItems[0]["output"].([]any); ok {
		for _, partRaw := range output {
			part, partOK := partRaw.(map[string]any)
			if !partOK {
				continue
			}
			if got := part["type"]; got == "input_file" {
				t.Fatalf("did not expect input_file inside function_call_output.output after promotion")
			}
		}
	}
	if output, ok := jsonItems[0]["output"].(string); !ok || strings.TrimSpace(output) == "" {
		t.Fatalf("expected non-empty string output for promoted file item, got %#v", jsonItems[0]["output"])
	}
	if got := jsonItems[1]["role"]; got != "user" {
		t.Fatalf("expected promoted user role, got %#v", got)
	}
	content, ok := jsonItems[1]["content"].([]any)
	if !ok || len(content) != 1 {
		t.Fatalf("expected one promoted content item, got %#v", jsonItems[1]["content"])
	}
	part, ok := content[0].(map[string]any)
	if !ok {
		t.Fatalf("expected promoted content object, got %#v", content[0])
	}
	if got := part["type"]; got != "input_file" {
		t.Fatalf("expected promoted input_file content, got %#v", got)
	}
	if got := part["file_data"]; got != pdfDataURL {
		t.Fatalf("unexpected file_data in promoted content: %#v", got)
	}
	if got := part["filename"]; got != "doc.pdf" {
		t.Fatalf("unexpected filename in promoted content: %#v", got)
	}
}

func TestBuildResponsesInput_MessageToolOutputPromotesPDFToInputMessage(t *testing.T) {
	const pdfDataURL = "data:application/pdf;base64,Zm9v"
	items := buildResponsesInput(ItemsFromMessages([]Message{
		{
			Role:       RoleTool,
			ToolCallID: "call_1",
			Name:       string(toolspec.ToolViewImage),
			Content:    `[{"type":"input_file","file_data":"data:application/pdf;base64,Zm9v","filename":"doc.pdf"}]`,
		},
	}))
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}

	jsonItems := mustMarshalItems(t, items)
	if got := jsonItems[0]["type"]; got != "function_call_output" {
		t.Fatalf("expected function_call_output item, got %#v", got)
	}
	if _, ok := jsonItems[0]["output"].([]any); ok {
		t.Fatalf("expected string output for promoted view_image PDF, got array")
	}
	if got := jsonItems[1]["role"]; got != "user" {
		t.Fatalf("expected promoted user role, got %#v", got)
	}
	content, ok := jsonItems[1]["content"].([]any)
	if !ok || len(content) != 1 {
		t.Fatalf("expected one promoted content item, got %#v", jsonItems[1]["content"])
	}
	part, ok := content[0].(map[string]any)
	if !ok {
		t.Fatalf("expected promoted content object, got %#v", content[0])
	}
	if got := part["type"]; got != "input_file" {
		t.Fatalf("expected promoted input_file content, got %#v", got)
	}
	if got := part["file_data"]; got != pdfDataURL {
		t.Fatalf("unexpected promoted file_data: %#v", got)
	}
	if got := part["filename"]; got != "doc.pdf" {
		t.Fatalf("unexpected promoted filename: %#v", got)
	}
}

func TestBuildResponsesInput_CanonicalNonViewImageToolOutputKeepsStructuredInputFileItems(t *testing.T) {
	items := buildResponsesInput([]ResponseItem{
		{
			Type:   ResponseItemTypeFunctionCallOutput,
			CallID: "call_1",
			Name:   string(toolspec.ToolExecCommand),
			Output: json.RawMessage(`[{"type":"input_file","file_data":"Zm9v","filename":"doc.pdf"}]`),
		},
	})
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}

	jsonItems := mustMarshalItems(t, items)
	if got := jsonItems[0]["type"]; got != "function_call_output" {
		t.Fatalf("expected function_call_output item, got %#v", got)
	}
	output, ok := jsonItems[0]["output"].([]any)
	if !ok || len(output) != 1 {
		t.Fatalf("expected structured output array, got %#v", jsonItems[0]["output"])
	}
	part, ok := output[0].(map[string]any)
	if !ok {
		t.Fatalf("expected structured output object, got %#v", output[0])
	}
	if got := part["type"]; got != "input_file" {
		t.Fatalf("expected input_file output content, got %#v", got)
	}
}

func TestServiceBaseURL_UsesCodexEndpointBaseForOAuth(t *testing.T) {
	transport := NewHTTPTransport(staticAuth{})
	transport.BaseURL = "https://attacker.example/v1"

	got := transport.serviceBaseURL(openAIAuthMode{IsOAuth: true})
	if got != strings.TrimSuffix(codexResponsesEndpoint, "/responses") {
		t.Fatalf("expected oauth base endpoint %q, got %q", strings.TrimSuffix(codexResponsesEndpoint, "/responses"), got)
	}
	standard := transport.serviceBaseURL(openAIAuthMode{})
	if standard != "https://attacker.example/v1" {
		t.Fatalf("expected standard base endpoint, got %q", standard)
	}
}

func TestNewOpenAIProviderClientCanonicalizesBareDefaultOpenAIBaseURL(t *testing.T) {
	client, err := newOpenAIProviderClient(ProviderClientOptions{Auth: staticAuth{}, OpenAIBaseURL: "https://api.openai.com"})
	if err != nil {
		t.Fatalf("new openai provider client: %v", err)
	}
	openAIClient, ok := client.(*OpenAIClient)
	if !ok {
		t.Fatalf("expected *OpenAIClient, got %T", client)
	}
	transport, ok := openAIClient.transport.(*HTTPTransport)
	if !ok {
		t.Fatalf("expected *HTTPTransport, got %T", openAIClient.transport)
	}
	if got := transport.serviceBaseURL(openAIAuthMode{}); got != defaultOpenAIBaseURL {
		t.Fatalf("service base url = %q, want %q", got, defaultOpenAIBaseURL)
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

func TestSupportsRequestInputTokenCount_DisablesCodexOAuth(t *testing.T) {
	transport := NewHTTPTransport(oauthStaticAuth{})

	supported, err := transport.SupportsRequestInputTokenCount(context.Background())
	if err != nil {
		t.Fatalf("SupportsRequestInputTokenCount: %v", err)
	}
	if supported {
		t.Fatal("expected chatgpt-codex oauth input token counting to be unsupported")
	}
}

func TestSupportsRequestInputTokenCount_AllowsStandardOpenAI(t *testing.T) {
	transport := NewHTTPTransport(staticAuth{})

	supported, err := transport.SupportsRequestInputTokenCount(context.Background())
	if err != nil {
		t.Fatalf("SupportsRequestInputTokenCount: %v", err)
	}
	if !supported {
		t.Fatal("expected standard openai input token counting to remain supported")
	}
}

func TestBuildRequestOptions_OmitsAuthorizationHeaderWhenAuthHeaderEmpty(t *testing.T) {
	transport := NewHTTPTransport(staticAuth{})
	if len(transport.buildRequestOptions("", openAIAuthMode{}, "")) != 2 {
		t.Fatal("expected empty auth header to omit Authorization request option")
	}
	if len(transport.buildRequestOptions("   ", openAIAuthMode{}, "")) != 2 {
		t.Fatal("expected whitespace auth header to omit Authorization request option")
	}
	if len(transport.buildRequestOptions("", openAIAuthMode{}, "session-1")) != 3 {
		t.Fatal("expected session header to remain when Authorization is omitted")
	}
}

func TestResolveAuth_AllowsAnonymousWhenBaseURLExplicitAndAuthNotConfigured(t *testing.T) {
	transport := NewHTTPTransport(missingAuth{})
	transport.BaseURL = "http://127.0.0.1:8080/v1"
	transport.BaseURLExplicit = true

	authHeader, mode, err := transport.resolveAuth(context.Background())
	if err != nil {
		t.Fatalf("resolveAuth: %v", err)
	}
	if authHeader != "" {
		t.Fatalf("expected empty auth header, got %q", authHeader)
	}
	if mode.IsOAuth || mode.AccountID != "" {
		t.Fatalf("expected anonymous non-oauth mode, got %+v", mode)
	}
}

func TestGenerate_ExplicitBaseURLAllowsAnonymousRequests(t *testing.T) {
	authHeaderErrs := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if got := strings.TrimSpace(r.Header.Get("Authorization")); got != "" {
			authHeaderErrs <- fmt.Errorf("expected anonymous request without Authorization header, got %q", got)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"resp_anon_1",
			"object":"response",
			"output":[
				{
					"type":"message",
					"id":"msg_anon_1",
					"role":"assistant",
					"status":"completed",
					"content":[{"type":"output_text","text":"hello from anonymous compatible server"}]
				}
			],
			"usage":{"input_tokens":11,"output_tokens":7,"total_tokens":18}
		}`))
	}))
	defer server.Close()
	targetURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse server url: %v", err)
	}

	transport := NewHTTPTransport(nil)
	transport.BaseURL = "https://example.openrouter.ai/v1"
	transport.BaseURLExplicit = true
	transport.Client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		cloned := req.Clone(req.Context())
		cloned.URL.Scheme = targetURL.Scheme
		cloned.URL.Host = targetURL.Host
		return server.Client().Transport.RoundTrip(cloned)
	})}

	providerCaps, err := transport.ProviderCapabilities(context.Background())
	if err != nil {
		t.Fatalf("provider capabilities: %v", err)
	}
	if providerCaps.ProviderID != "openai-compatible" {
		t.Fatalf("expected openai-compatible provider capabilities, got %+v", providerCaps)
	}

	resp, err := transport.Generate(context.Background(), OpenAIRequest{
		Model: "vendor-custom-model",
		Items: []ResponseItem{{Type: ResponseItemTypeMessage, Role: RoleUser, Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	select {
	case err := <-authHeaderErrs:
		t.Fatal(err)
	default:
	}
	if resp.AssistantText != "hello from anonymous compatible server" {
		t.Fatalf("assistant text = %q", resp.AssistantText)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestBuildPayload_UsesTransportStoreSetting(t *testing.T) {
	transport := NewHTTPTransport(staticAuth{})
	transport.Store = true
	payload, err := transport.buildPayload(OpenAIRequest{Model: "gpt-5"}, openAIAuthMode{}, requireProviderCapabilities(t, transport, openAIAuthMode{}))
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
	}, openAIAuthMode{}, requireProviderCapabilities(t, transport, openAIAuthMode{}))
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

func TestBuildPayload_SerializesPatchAsCustomGrammarTool(t *testing.T) {
	transport := NewHTTPTransport(staticAuth{})
	payload, err := transport.buildPayload(OpenAIRequest{
		Model: "gpt-5",
		Tools: []Tool{{
			Name:        string(toolspec.ToolPatch),
			Description: "Apply edits to files using freeform patch syntax.",
			Custom:      &CustomToolFormat{Type: "grammar", Syntax: "lark", Definition: "start: \"x\""},
		}},
	}, openAIAuthMode{}, requireProviderCapabilities(t, transport, openAIAuthMode{}))
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
		t.Fatalf("expected tool object, got %#v", tools[0])
	}
	if got := tool["type"]; got != "custom" {
		t.Fatalf("expected custom tool type, got %#v", got)
	}
	if got := tool["name"]; got != string(toolspec.ToolPatch) {
		t.Fatalf("expected patch tool name, got %#v", got)
	}
	if _, ok := tool["parameters"]; ok {
		t.Fatalf("custom patch tool must not include JSON parameters: %#v", tool)
	}
	format, ok := tool["format"].(map[string]any)
	if !ok {
		t.Fatalf("expected custom format object, got %#v", tool["format"])
	}
	if format["type"] != "grammar" || format["syntax"] != "lark" || format["definition"] != "start: \"x\"" {
		t.Fatalf("unexpected custom format: %#v", format)
	}
}

func TestBuildPayload_UsesExplicitPatchCustomGrammarTool(t *testing.T) {
	transport := NewHTTPTransport(oauthStaticAuth{})
	mode := openAIAuthMode{IsOAuth: true, AccountID: "acc-1"}
	payload, err := transport.buildPayload(OpenAIRequest{
		Model: "gpt-5.4",
		Tools: []Tool{
			{Name: string(toolspec.ToolExecCommand), Description: "shell", Schema: json.RawMessage(`{"type":"object","additionalProperties":false}`)},
			{Name: string(toolspec.ToolPatch), Description: "patch", Custom: &CustomToolFormat{Type: "grammar", Syntax: "lark", Definition: PatchToolLarkGrammar}},
		},
	}, mode, requireProviderCapabilities(t, transport, mode))
	if err != nil {
		t.Fatalf("build payload: %v", err)
	}

	jsonPayload := mustMarshalObject(t, payload)
	if got, ok := jsonPayload["parallel_tool_calls"].(bool); !ok || !got {
		t.Fatalf("expected parallel_tool_calls=true, got %#v", jsonPayload["parallel_tool_calls"])
	}
	tools, ok := jsonPayload["tools"].([]any)
	if !ok || len(tools) != 2 {
		t.Fatalf("expected two tools, got %#v", jsonPayload["tools"])
	}
	names := make([]string, 0, len(tools))
	for idx, raw := range tools {
		tool, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("expected tool object, got %#v", raw)
		}
		if idx == 0 {
			if got := tool["type"]; got != "function" {
				t.Fatalf("expected shell function tool, got %#v", got)
			}
		}
		name, ok := tool["name"].(string)
		if !ok {
			t.Fatalf("expected function name, got %#v", tool["name"])
		}
		names = append(names, name)
	}
	if !reflect.DeepEqual(names, []string{string(toolspec.ToolExecCommand), string(toolspec.ToolPatch)}) {
		t.Fatalf("tool names = %+v, want raw requested names only", names)
	}
	patchTool, ok := tools[1].(map[string]any)
	if !ok {
		t.Fatalf("expected patch tool object, got %#v", tools[1])
	}
	if got := patchTool["type"]; got != "custom" {
		t.Fatalf("expected patch custom tool, got %#v", got)
	}
	format, ok := patchTool["format"].(map[string]any)
	if !ok || format["type"] != "grammar" || format["syntax"] != "lark" {
		t.Fatalf("expected patch grammar format, got %#v", patchTool["format"])
	}
	if _, ok := patchTool["parameters"]; ok {
		t.Fatalf("custom patch tool must not include legacy JSON parameters: %#v", patchTool)
	}
}

func TestBuildFunctionToolParamRejectsBlankCustomToolName(t *testing.T) {
	_, err := buildFunctionToolParam(Tool{
		Name:   "   ",
		Custom: &CustomToolFormat{Type: "grammar", Syntax: "lark", Definition: "start: \"x\""},
	})
	if err == nil || !strings.Contains(err.Error(), "custom tool name is required") {
		t.Fatalf("error = %v, want blank custom tool name rejection", err)
	}
}

func TestBuildPayload_DoesNotAddNativeWebSearchToolWhenDisabled(t *testing.T) {
	transport := NewHTTPTransport(staticAuth{})
	payload, err := transport.buildPayload(OpenAIRequest{
		Model:                 "gpt-5",
		EnableNativeWebSearch: false,
	}, openAIAuthMode{}, requireProviderCapabilities(t, transport, openAIAuthMode{}))
	if err != nil {
		t.Fatalf("build payload: %v", err)
	}

	jsonPayload := mustMarshalObject(t, payload)
	if _, ok := jsonPayload["tools"]; ok {
		t.Fatalf("expected no tools in payload, got %#v", jsonPayload["tools"])
	}
}

func TestBuildPayload_SetsPromptCacheKey(t *testing.T) {
	transport := NewHTTPTransport(staticAuth{})
	payload, err := transport.buildPayload(OpenAIRequest{
		Model:          "gpt-5",
		PromptCacheKey: "cache-key-1",
	}, openAIAuthMode{}, requireProviderCapabilities(t, transport, openAIAuthMode{}))
	if err != nil {
		t.Fatalf("build payload: %v", err)
	}

	jsonPayload := mustMarshalObject(t, payload)
	if got := jsonPayload["prompt_cache_key"]; got != "cache-key-1" {
		t.Fatalf("expected prompt_cache_key=cache-key-1, got %#v", got)
	}
}

func TestBuildPayload_DoesNotSetPromptCacheKeyForOpenAICompatibleProvider(t *testing.T) {
	transport := NewHTTPTransport(staticAuth{})
	payload, err := transport.buildPayload(OpenAIRequest{
		Model:          "gpt-5",
		PromptCacheKey: "cache-key-1",
	}, openAIAuthMode{}, ProviderCapabilities{
		ProviderID:           "openai-compatible",
		SupportsResponsesAPI: true,
		IsOpenAIFirstParty:   false,
	})
	if err != nil {
		t.Fatalf("build payload: %v", err)
	}

	jsonPayload := mustMarshalObject(t, payload)
	if _, ok := jsonPayload["prompt_cache_key"]; ok {
		t.Fatalf("expected prompt_cache_key omitted for openai-compatible provider, got %#v", jsonPayload["prompt_cache_key"])
	}
}

func TestBuildPayload_SetsPromptCacheKeyWhenExplicitCapabilityIsEnabled(t *testing.T) {
	transport := NewHTTPTransport(staticAuth{})
	payload, err := transport.buildPayload(OpenAIRequest{
		Model:          "gpt-5",
		PromptCacheKey: "cache-key-1",
	}, openAIAuthMode{}, ProviderCapabilities{
		ProviderID:             "openai-compatible",
		SupportsResponsesAPI:   true,
		SupportsPromptCacheKey: true,
		IsOpenAIFirstParty:     false,
	})
	if err != nil {
		t.Fatalf("build payload: %v", err)
	}

	jsonPayload := mustMarshalObject(t, payload)
	if got := jsonPayload["prompt_cache_key"]; got != "cache-key-1" {
		t.Fatalf("expected prompt_cache_key=cache-key-1, got %#v", got)
	}
}

func TestBuildPayload_AppliesStructuredOutputJSONSchema(t *testing.T) {
	transport := NewHTTPTransport(staticAuth{})
	payload, err := transport.buildPayload(OpenAIRequest{
		Model: "gpt-5",
		StructuredOutput: &StructuredOutput{
			Name:   "reviewer_suggestions",
			Schema: json.RawMessage(`{"type":"object","properties":{"suggestions":{"type":"array","items":{"type":"string"}}},"required":["suggestions"],"additionalProperties":false}`),
			Strict: true,
		},
	}, openAIAuthMode{}, requireProviderCapabilities(t, transport, openAIAuthMode{}))
	if err != nil {
		t.Fatalf("build payload: %v", err)
	}

	jsonPayload := mustMarshalObject(t, payload)
	text, ok := jsonPayload["text"].(map[string]any)
	if !ok {
		t.Fatalf("expected text config in payload, got %#v", jsonPayload["text"])
	}
	format, ok := text["format"].(map[string]any)
	if !ok {
		t.Fatalf("expected text.format config in payload, got %#v", text["format"])
	}
	if format["type"] != "json_schema" {
		t.Fatalf("expected text.format.type=json_schema, got %#v", format["type"])
	}
	if format["name"] != "reviewer_suggestions" {
		t.Fatalf("expected text.format.name=reviewer_suggestions, got %#v", format["name"])
	}
	if strict, ok := format["strict"].(bool); !ok || !strict {
		t.Fatalf("expected text.format.strict=true, got %#v", format["strict"])
	}
}

func TestBuildPayload_AppliesConfiguredModelVerbosityForSupportedModels(t *testing.T) {
	transport := NewHTTPTransport(staticAuth{})
	transport.ModelVerbosity = "high"
	payload, err := transport.buildPayload(OpenAIRequest{Model: "gpt-5"}, openAIAuthMode{}, requireProviderCapabilities(t, transport, openAIAuthMode{}))
	if err != nil {
		t.Fatalf("build payload: %v", err)
	}

	jsonPayload := mustMarshalObject(t, payload)
	text, ok := jsonPayload["text"].(map[string]any)
	if !ok {
		t.Fatalf("expected text config in payload, got %#v", jsonPayload["text"])
	}
	if got := text["verbosity"]; got != "high" {
		t.Fatalf("expected text.verbosity=high, got %#v", got)
	}
}

func TestBuildPayload_IgnoresConfiguredModelVerbosityForUnsupportedModels(t *testing.T) {
	transport := NewHTTPTransport(staticAuth{})
	transport.ModelVerbosity = "high"
	payload, err := transport.buildPayload(OpenAIRequest{Model: "gpt-4.1"}, openAIAuthMode{}, requireProviderCapabilities(t, transport, openAIAuthMode{}))
	if err != nil {
		t.Fatalf("build payload: %v", err)
	}

	jsonPayload := mustMarshalObject(t, payload)
	if _, ok := jsonPayload["text"]; ok {
		t.Fatalf("expected text config to be omitted for unsupported model, got %#v", jsonPayload["text"])
	}
}

func TestBuildPayload_MergesConfiguredModelVerbosityWithStructuredOutput(t *testing.T) {
	transport := NewHTTPTransport(staticAuth{})
	transport.ModelVerbosity = "low"
	payload, err := transport.buildPayload(OpenAIRequest{
		Model: "gpt-5",
		StructuredOutput: &StructuredOutput{
			Name:   "reviewer_suggestions",
			Schema: json.RawMessage(`{"type":"object","properties":{"suggestions":{"type":"array","items":{"type":"string"}}},"required":["suggestions"],"additionalProperties":false}`),
			Strict: true,
		},
	}, openAIAuthMode{}, requireProviderCapabilities(t, transport, openAIAuthMode{}))
	if err != nil {
		t.Fatalf("build payload: %v", err)
	}

	jsonPayload := mustMarshalObject(t, payload)
	text, ok := jsonPayload["text"].(map[string]any)
	if !ok {
		t.Fatalf("expected text config in payload, got %#v", jsonPayload["text"])
	}
	if got := text["verbosity"]; got != "low" {
		t.Fatalf("expected text.verbosity=low, got %#v", got)
	}
	if _, ok := text["format"].(map[string]any); !ok {
		t.Fatalf("expected text.format to remain present, got %#v", text["format"])
	}
}

func TestBuildPayload_AppliesReasoningEffortForOpenAIModels(t *testing.T) {
	transport := NewHTTPTransport(staticAuth{})
	payload, err := transport.buildPayload(OpenAIRequest{
		Model:           "gpt-5",
		ReasoningEffort: "xhigh",
	}, openAIAuthMode{}, requireProviderCapabilities(t, transport, openAIAuthMode{}))
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

func TestBuildPayload_SkipsReasoningSummaryForUnknownModels(t *testing.T) {
	transport := NewHTTPTransport(staticAuth{})
	payload, err := transport.buildPayload(OpenAIRequest{
		Model:           "custom-model",
		ReasoningEffort: "high",
	}, openAIAuthMode{}, requireProviderCapabilities(t, transport, openAIAuthMode{}))
	if err != nil {
		t.Fatalf("build payload: %v", err)
	}
	if payload.Reasoning.Effort != "high" {
		t.Fatalf("expected reasoning payload for unknown model, got %+v", payload.Reasoning)
	}
	if payload.Reasoning.Summary != "" {
		t.Fatalf("expected reasoning.summary to be omitted for unknown model, got %q", payload.Reasoning.Summary)
	}
	if len(payload.Include) == 0 {
		t.Fatalf("expected encrypted reasoning include for unknown model, got %+v", payload.Include)
	}

	jsonPayload := mustMarshalObject(t, payload)
	reasoning, ok := jsonPayload["reasoning"].(map[string]any)
	if !ok {
		t.Fatalf("expected reasoning to be present for unknown model, got %+v", jsonPayload)
	}
	if _, ok := reasoning["summary"]; ok {
		t.Fatalf("expected reasoning.summary omitted for unknown model, got %+v", reasoning)
	}
}

func TestBuildPayload_SkipsReasoningSummaryForCodexSpark(t *testing.T) {
	transport := NewHTTPTransport(staticAuth{})
	transport.ModelVerbosity = "medium"
	payload, err := transport.buildPayload(OpenAIRequest{
		Model:           "gpt-5.3-codex-spark",
		ReasoningEffort: "high",
	}, openAIAuthMode{IsOAuth: true}, requireProviderCapabilities(t, transport, openAIAuthMode{IsOAuth: true}))
	if err != nil {
		t.Fatalf("build payload: %v", err)
	}
	if payload.Reasoning.Effort != "high" {
		t.Fatalf("expected reasoning payload for codex spark, got %+v", payload.Reasoning)
	}
	if payload.Reasoning.Summary != "" {
		t.Fatalf("expected reasoning.summary omitted for codex spark, got %+v", payload.Reasoning)
	}
	if len(payload.Include) != 1 || payload.Include[0] != responses.ResponseIncludableReasoningEncryptedContent {
		t.Fatalf("expected reasoning.encrypted_content include for codex spark, got %+v", payload.Include)
	}

	jsonPayload := mustMarshalObject(t, payload)
	reasoning, ok := jsonPayload["reasoning"].(map[string]any)
	if !ok {
		t.Fatalf("expected reasoning to be present for codex spark, got %+v", jsonPayload)
	}
	if _, ok := reasoning["summary"]; ok {
		t.Fatalf("expected reasoning.summary omitted for codex spark, got %+v", reasoning)
	}
	text, ok := jsonPayload["text"].(map[string]any)
	if !ok {
		t.Fatalf("expected text config in payload for codex spark, got %#v", jsonPayload["text"])
	}
	if got := text["verbosity"]; got != "medium" {
		t.Fatalf("expected text.verbosity=medium for codex spark, got %#v", got)
	}
}

func TestBuildPayload_AppliesFastModeForCodexProvider(t *testing.T) {
	transport := NewHTTPTransport(staticAuth{})
	payload, err := transport.buildPayload(OpenAIRequest{
		Model:    "gpt-5.3-codex",
		FastMode: true,
	}, openAIAuthMode{IsOAuth: true}, requireProviderCapabilities(t, transport, openAIAuthMode{IsOAuth: true}))
	if err != nil {
		t.Fatalf("build payload: %v", err)
	}
	if payload.ServiceTier != responses.ResponseNewParamsServiceTierPriority {
		t.Fatalf("expected priority service tier, got %q", payload.ServiceTier)
	}

	jsonPayload := mustMarshalObject(t, payload)
	if got := jsonPayload["service_tier"]; got != "priority" {
		t.Fatalf("expected service_tier=priority, got %#v", got)
	}
}

func TestBuildPayload_AppliesFastModeForOpenAIProvider(t *testing.T) {
	transport := NewHTTPTransport(staticAuth{})
	payload, err := transport.buildPayload(OpenAIRequest{
		Model:    "gpt-5.3-codex",
		FastMode: true,
	}, openAIAuthMode{}, requireProviderCapabilities(t, transport, openAIAuthMode{}))
	if err != nil {
		t.Fatalf("build payload: %v", err)
	}
	if payload.ServiceTier != responses.ResponseNewParamsServiceTierPriority {
		t.Fatalf("expected priority service tier for openai provider, got %q", payload.ServiceTier)
	}

	jsonPayload := mustMarshalObject(t, payload)
	if got := jsonPayload["service_tier"]; got != "priority" {
		t.Fatalf("expected service_tier=priority, got %#v", got)
	}
}

func TestBuildPayload_SkipsFastModeForRemoteOpenAICompatibleProvider(t *testing.T) {
	transport := NewHTTPTransport(staticAuth{})
	transport.BaseURL = "https://example.openai.azure.com/openai/v1"
	payload, err := transport.buildPayload(OpenAIRequest{
		Model:    "gpt-5.3-codex",
		FastMode: true,
	}, openAIAuthMode{}, requireProviderCapabilities(t, transport, openAIAuthMode{}))
	if err != nil {
		t.Fatalf("build payload: %v", err)
	}
	if payload.ServiceTier != "" {
		t.Fatalf("expected no service tier for remote openai-compatible provider, got %q", payload.ServiceTier)
	}

	jsonPayload := mustMarshalObject(t, payload)
	if _, ok := jsonPayload["service_tier"]; ok {
		t.Fatalf("expected service_tier omitted, got %+v", jsonPayload["service_tier"])
	}

	providerCaps, err := transport.providerCapabilitiesForMode(openAIAuthMode{})
	if err != nil {
		t.Fatalf("resolve provider capabilities: %v", err)
	}
	if providerCaps.ProviderID != "openai-compatible" || providerCaps.IsOpenAIFirstParty || providerCaps.SupportsResponsesCompact || providerCaps.SupportsNativeWebSearch || providerCaps.SupportsRequestInputTokenCount {
		t.Fatalf("expected conservative remote openai-compatible capabilities, got %+v", providerCaps)
	}
}

func TestBuildPayload_DefaultsReasoningEffortForUnknownModelFamily(t *testing.T) {
	transport := NewHTTPTransport(staticAuth{})
	payload, err := transport.buildPayload(OpenAIRequest{
		Model:           "custom-model",
		ReasoningEffort: "high",
	}, openAIAuthMode{}, requireProviderCapabilities(t, transport, openAIAuthMode{}))
	if err != nil {
		t.Fatalf("build payload: %v", err)
	}
	if payload.Reasoning.Effort != "high" {
		t.Fatalf("expected reasoning payload for unknown model, got %+v", payload.Reasoning)
	}
	if payload.Reasoning.Summary != "" {
		t.Fatalf("expected unknown model to omit reasoning summary, got %+v", payload.Reasoning)
	}
	if len(payload.Include) == 0 {
		t.Fatalf("expected encrypted reasoning include for unknown model, got %+v", payload.Include)
	}

	jsonPayload := mustMarshalObject(t, payload)
	if _, ok := jsonPayload["reasoning"]; !ok {
		t.Fatalf("expected reasoning to be present for unknown model, got %+v", jsonPayload)
	}
}

func TestBuildResponsesInput_AssistantReasoningItemsUseEncryptedContentOnly(t *testing.T) {
	items := buildResponsesInput(ItemsFromMessages([]Message{
		{
			Role:    RoleAssistant,
			Content: "a1",
			ReasoningItems: []ReasoningItem{
				{ID: "rs_1", EncryptedContent: "enc_1"},
			},
		},
	}))
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
	}, openAIAuthMode{}, requireProviderCapabilities(t, transport, openAIAuthMode{}))
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
	items := buildResponsesInput([]ResponseItem{
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

func TestParseOutputItems_UsesLastAssistantMessageWhenMultipleUnphased(t *testing.T) {
	raw := []byte(`[
		{
			"type":"message",
			"role":"assistant",
			"id":"msg_1",
			"content":[{"type":"output_text","text":"working..."}]
		},
		{
			"type":"message",
			"role":"assistant",
			"id":"msg_2",
			"content":[{"type":"output_text","text":"done"}]
		}
	]`)
	var output []responses.ResponseOutputItemUnion
	if err := json.Unmarshal(raw, &output); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	_, assistantText, assistantPhase, _, _, _ := parseOutputItems(output)
	if assistantText != "done" {
		t.Fatalf("assistantText = %q, want done", assistantText)
	}
	if assistantPhase != "" {
		t.Fatalf("assistantPhase = %q, want empty", assistantPhase)
	}
}

func TestParseOutputItems_UsesTrailingAssistantPhaseBlock(t *testing.T) {
	raw := []byte(`[
		{
			"type":"message",
			"role":"assistant",
			"id":"msg_1",
			"phase":"commentary",
			"content":[{"type":"output_text","text":"prep"}]
		},
		{
			"type":"message",
			"role":"assistant",
			"id":"msg_2",
			"phase":"final_answer",
			"content":[{"type":"output_text","text":"final-1"}]
		},
		{
			"type":"message",
			"role":"assistant",
			"id":"msg_3",
			"phase":"final_answer",
			"content":[{"type":"output_text","text":"final-2"}]
		}
	]`)
	var output []responses.ResponseOutputItemUnion
	if err := json.Unmarshal(raw, &output); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	_, assistantText, assistantPhase, _, _, _ := parseOutputItems(output)
	if assistantText != "final-1final-2" {
		t.Fatalf("assistantText = %q, want final-1final-2", assistantText)
	}
	if assistantPhase != MessagePhaseFinal {
		t.Fatalf("assistantPhase = %q, want %q", assistantPhase, MessagePhaseFinal)
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

func TestInputTokenCountPayloadMatchesCompactPayloadInputShape(t *testing.T) {
	transport := NewHTTPTransport(staticAuth{})
	canonicalItems := []ResponseItem{
		{Type: ResponseItemTypeMessage, Role: RoleUser, Content: "hello"},
		{Type: ResponseItemTypeFunctionCall, ID: "call_1", CallID: "call_1", Name: "shell", Arguments: json.RawMessage(`{"command":"pwd"}`)},
		{
			Type:   ResponseItemTypeFunctionCallOutput,
			CallID: "call_1",
			Name:   string(toolspec.ToolViewImage),
			Output: json.RawMessage(`[{"type":"input_file","file_data":"data:application/pdf;base64,Zm9v","filename":"doc.pdf"}]`),
		},
		{Type: ResponseItemTypeReasoning, ID: "rs_1", EncryptedContent: "enc_reasoning"},
		{Type: ResponseItemTypeCompaction, ID: "cmp_1", EncryptedContent: "enc_compaction"},
	}

	compactPayload, err := transport.buildCompactPayload(OpenAICompactionRequest{
		Model:        "gpt-5",
		Instructions: "compaction instructions",
		InputItems:   canonicalItems,
	})
	if err != nil {
		t.Fatalf("build compact payload: %v", err)
	}
	countPayload, err := transport.buildInputTokenCountParams(OpenAIRequest{
		Model:        "gpt-5",
		SystemPrompt: "compaction instructions",
		Items:        canonicalItems,
	}, requireProviderCapabilities(t, transport, openAIAuthMode{}))
	if err != nil {
		t.Fatalf("build input-token-count payload: %v", err)
	}

	compactJSON := mustMarshalJSONMap(t, compactPayload)
	countJSON := mustMarshalJSONMap(t, countPayload)
	if !reflect.DeepEqual(compactJSON["input"], countJSON["input"]) {
		t.Fatalf("expected input shape parity between compact and input-token-count payloads\ncompact=%#v\ncount=%#v", compactJSON["input"], countJSON["input"])
	}
	if compactJSON["instructions"] != countJSON["instructions"] {
		t.Fatalf("expected instructions parity between compact and input-token-count payloads, compact=%#v count=%#v", compactJSON["instructions"], countJSON["instructions"])
	}
}

func TestBuildInputTokenCountParams_AppliesConfiguredModelVerbosity(t *testing.T) {
	transport := NewHTTPTransport(staticAuth{})
	transport.ModelVerbosity = "medium"
	payload, err := transport.buildInputTokenCountParams(OpenAIRequest{Model: "gpt-5"}, requireProviderCapabilities(t, transport, openAIAuthMode{}))
	if err != nil {
		t.Fatalf("build input-token-count payload: %v", err)
	}

	jsonPayload := mustMarshalJSONMap(t, payload)
	text, ok := jsonPayload["text"].(map[string]any)
	if !ok {
		t.Fatalf("expected text config in payload, got %#v", jsonPayload["text"])
	}
	if got := text["verbosity"]; got != "medium" {
		t.Fatalf("expected text.verbosity=medium, got %#v", got)
	}
}

func TestCountRequestInputTokensTargetsResponsesInputTokensPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses/input_tokens" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"response.input_tokens","input_tokens":12345}`))
	}))
	defer server.Close()

	transport := NewHTTPTransport(staticAuth{})
	transport.BaseURL = server.URL + "/v1"
	transport.Client = server.Client()

	count, err := transport.CountRequestInputTokens(context.Background(), OpenAIRequest{
		Model:        "gpt-5",
		SystemPrompt: "sys",
		Items: []ResponseItem{
			{Type: ResponseItemTypeMessage, Role: RoleUser, Content: "hello"},
		},
	})
	if err != nil {
		t.Fatalf("count request input tokens failed: %v", err)
	}
	if count != 12345 {
		t.Fatalf("expected input token count 12345, got %d", count)
	}
}

func TestResolveModelContextWindowUsesModelMetadataFromAPI(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models/gpt-5" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"gpt-5",
			"object":"model",
			"created":1731459200,
			"owned_by":"openai",
			"context_window":272000
		}`))
	}))
	defer server.Close()

	transport := NewHTTPTransport(staticAuth{})
	transport.BaseURL = server.URL + "/v1"
	transport.Client = server.Client()
	transport.ContextWindowTokens = 0

	window, err := transport.ResolveModelContextWindow(context.Background(), "gpt-5")
	if err != nil {
		t.Fatalf("resolve model context window failed: %v", err)
	}
	if window != 272000 {
		t.Fatalf("expected context window 272000 from model metadata, got %d", window)
	}
}

func TestResolveModelContextWindowFallsBackToInputTokenLimitField(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models/gpt-5" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"gpt-5",
			"object":"model",
			"created":1731459200,
			"owned_by":"openai",
			"limits":{"input_token_limit":190000}
		}`))
	}))
	defer server.Close()

	transport := NewHTTPTransport(staticAuth{})
	transport.BaseURL = server.URL + "/v1"
	transport.Client = server.Client()
	transport.ContextWindowTokens = 0

	window, err := transport.ResolveModelContextWindow(context.Background(), "gpt-5")
	if err != nil {
		t.Fatalf("resolve model context window failed: %v", err)
	}
	if window != 190000 {
		t.Fatalf("expected context window 190000 from nested input_token_limit field, got %d", window)
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

func mustMarshalJSONMap(t *testing.T, payload any) map[string]any {
	t.Helper()
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
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

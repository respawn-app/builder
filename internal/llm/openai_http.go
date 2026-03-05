package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	openai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"
)

const (
	defaultOpenAIBaseURL   = "https://api.openai.com/v1"
	codexResponsesEndpoint = "https://chatgpt.com/backend-api/codex/responses"
	defaultOriginator      = "builder"
	defaultUserAgent       = "builder/dev"
	reasoningRoleSummary   = "reasoning"
)

type AuthHeaderProvider interface {
	AuthorizationHeader(ctx context.Context) (string, error)
}

type OpenAIAuthMetadataProvider interface {
	OpenAIAuthMetadata(ctx context.Context) (method string, accountID string, err error)
}

type openAIAuthMode struct {
	IsOAuth   bool
	AccountID string
}

type HTTPTransport struct {
	BaseURL             string
	Client              *http.Client
	Auth                AuthHeaderProvider
	Store               bool
	ContextWindowTokens int

	mu                  sync.RWMutex
	modelContextWindows map[string]int
}

func NewHTTPTransport(auth AuthHeaderProvider) *HTTPTransport {
	window := 200000
	if raw := strings.TrimSpace(os.Getenv("BUILDER_CONTEXT_WINDOW")); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 {
			window = v
		}
	}
	return &HTTPTransport{
		BaseURL:             defaultOpenAIBaseURL,
		Client:              &http.Client{Timeout: 120 * time.Second},
		Auth:                auth,
		ContextWindowTokens: window,
		modelContextWindows: make(map[string]int),
	}
}

func (t *HTTPTransport) Generate(ctx context.Context, request OpenAIRequest) (OpenAIResponse, error) {
	if t.Client == nil {
		t.Client = &http.Client{Timeout: 120 * time.Second}
	}
	windowTokens := t.resolveContextWindowFallback(ctx, request.Model)

	authHeader, mode, err := t.resolveAuth(ctx)
	if err != nil {
		return OpenAIResponse{}, err
	}

	payload, err := t.buildPayload(request, mode)
	if err != nil {
		return OpenAIResponse{}, err
	}

	service := t.newResponseService(mode)
	reqOpts := t.buildRequestOptions(authHeader, mode, request.SessionID)
	var rawResp *http.Response
	reqOpts = append(reqOpts, option.WithResponseInto(&rawResp))

	decoded, err := service.New(ctx, payload, reqOpts...)
	if err != nil {
		return OpenAIResponse{}, mapOpenAIRequestError(t.errorProviderID(mode), err, rawResp, "openai responses request failed")
	}
	if decoded == nil {
		return OpenAIResponse{}, fmt.Errorf("openai responses request failed: empty response")
	}

	outputItems, assistantText, assistantPhase, toolCalls, reasoning, reasoningItems := parseOutputItems(decoded.Output)
	return OpenAIResponse{
		AssistantText:  assistantText,
		AssistantPhase: assistantPhase,
		ToolCalls:      toolCalls,
		Reasoning:      reasoning,
		ReasoningItems: reasoningItems,
		OutputItems:    outputItems,
		Usage:          usageFromSDK(decoded.Usage, windowTokens),
	}, nil
}

func (t *HTTPTransport) GenerateStream(ctx context.Context, request OpenAIRequest, onDelta func(text string)) (OpenAIResponse, error) {
	if t.Client == nil {
		t.Client = &http.Client{Timeout: 120 * time.Second}
	}
	windowTokens := t.resolveContextWindowFallback(ctx, request.Model)

	authHeader, mode, err := t.resolveAuth(ctx)
	if err != nil {
		return OpenAIResponse{}, err
	}

	payload, err := t.buildPayload(request, mode)
	if err != nil {
		return OpenAIResponse{}, err
	}

	service := t.newResponseService(mode)
	reqOpts := t.buildRequestOptions(authHeader, mode, request.SessionID)
	var rawResp *http.Response
	reqOpts = append(reqOpts, option.WithResponseInto(&rawResp))

	stream := service.NewStreaming(ctx, payload, reqOpts...)
	defer stream.Close()

	var assistantText strings.Builder
	acc := newToolCallAccumulator()
	reasoningAcc := newReasoningAccumulator()
	usage := Usage{WindowTokens: windowTokens}
	var completed *responses.Response

	for stream.Next() {
		evt := stream.Current()
		switch evt.Type {
		case "response.output_text.delta":
			if evt.Delta != "" {
				assistantText.WriteString(evt.Delta)
				if onDelta != nil {
					onDelta(evt.Delta)
				}
			}
		case "response.output_item.added", "response.output_item.done":
			acc.UpsertFromOutput(evt.Item)
			reasoningAcc.UpsertReasoningItem(evt.Item)
		case "response.function_call_arguments.delta":
			acc.AppendArguments(evt.ItemID, evt.Delta)
		case "response.function_call_arguments.done":
			acc.SetArguments(evt.ItemID, evt.Arguments)
		case "response.reasoning_summary_text.delta":
			reasoningAcc.Append(reasoningRoleSummary, reasoningEventKey(evt.ItemID, evt.OutputIndex, evt.SummaryIndex), evt.Delta)
		case "response.reasoning_summary_text.done":
			reasoningAcc.Set(reasoningRoleSummary, reasoningEventKey(evt.ItemID, evt.OutputIndex, evt.SummaryIndex), evt.Text)
		case "response.reasoning_summary_part.added", "response.reasoning_summary_part.done":
			if evt.Part.Type == "summary_text" {
				reasoningAcc.Set(reasoningRoleSummary, reasoningEventKey(evt.ItemID, evt.OutputIndex, evt.SummaryIndex), evt.Part.Text)
			}
		case "response.completed":
			e := evt.AsResponseCompleted()
			completed = &e.Response
		}
	}
	if err := stream.Err(); err != nil {
		return OpenAIResponse{}, mapOpenAIRequestError(t.errorProviderID(mode), err, rawResp, "read responses stream events")
	}

	finalText := assistantText.String()
	finalCalls := acc.ToToolCalls()
	finalReasoning := reasoningAcc.Entries()
	finalReasoningItems := reasoningAcc.Items()
	finalOutputItems := buildOutputItemsFromStream(finalText, finalCalls, finalReasoning, finalReasoningItems)

	if completed != nil {
		if completed.Usage.InputTokens > 0 || completed.Usage.OutputTokens > 0 {
			usage = usageFromSDK(completed.Usage, windowTokens)
		}
		parsedItems, parsedText, parsedPhase, parsedCalls, parsedReasoning, parsedReasoningItems := parseOutputItems(completed.Output)
		// Treat response.completed as canonical output for assistant text.
		// Streaming deltas are provisional and can diverge from final structured items.
		finalText = parsedText
		finalPhase := MessagePhase("")
		if parsedPhase != "" {
			finalPhase = parsedPhase
		}
		acc.Merge(parsedCalls)
		finalCalls = acc.ToToolCalls()
		finalReasoning = mergeReasoningEntries(parsedReasoning, finalReasoning)
		finalReasoningItems = mergeReasoningItems(parsedReasoningItems, finalReasoningItems)
		if len(parsedItems) > 0 {
			finalOutputItems = parsedItems
		}

		return OpenAIResponse{
			AssistantText:  finalText,
			AssistantPhase: finalPhase,
			ToolCalls:      finalCalls,
			Reasoning:      finalReasoning,
			ReasoningItems: finalReasoningItems,
			OutputItems:    finalOutputItems,
			Usage:          usage,
		}, nil
	}

	return OpenAIResponse{
		AssistantText:  finalText,
		AssistantPhase: "",
		ToolCalls:      finalCalls,
		Reasoning:      finalReasoning,
		ReasoningItems: finalReasoningItems,
		OutputItems:    finalOutputItems,
		Usage:          usage,
	}, nil
}

func (t *HTTPTransport) Compact(ctx context.Context, request OpenAICompactionRequest) (OpenAICompactionResponse, error) {
	if t.Client == nil {
		t.Client = &http.Client{Timeout: 120 * time.Second}
	}
	windowTokens := t.resolveContextWindowFallback(ctx, request.Model)

	authHeader, mode, err := t.resolveAuth(ctx)
	if err != nil {
		return OpenAICompactionResponse{}, err
	}

	payload, err := t.buildCompactPayload(request)
	if err != nil {
		return OpenAICompactionResponse{}, err
	}

	service := t.newResponseService(mode)
	reqOpts := t.buildRequestOptions(authHeader, mode, request.SessionID)
	var rawResp *http.Response
	var rawBody []byte
	reqOpts = append(reqOpts,
		option.WithResponseInto(&rawResp),
		option.WithResponseBodyInto(&rawBody),
	)

	decoded, err := service.Compact(ctx, payload, reqOpts...)
	if err != nil {
		return OpenAICompactionResponse{}, mapOpenAIRequestError(t.errorProviderID(mode), err, rawResp, "openai responses compact request failed")
	}
	if len(bytes.TrimSpace(rawBody)) > 0 {
		var parsed responses.CompactedResponse
		if err := json.Unmarshal(rawBody, &parsed); err != nil {
			return OpenAICompactionResponse{}, fmt.Errorf("openai responses compact request failed: invalid compact response body: %w", err)
		}
		decoded = &parsed
	}
	if decoded == nil {
		return OpenAICompactionResponse{}, fmt.Errorf("openai responses compact request failed: empty response")
	}

	outputItems, _, _, _, _, _ := parseOutputItems(decoded.Output)
	return OpenAICompactionResponse{
		OutputItems:       outputItems,
		Usage:             usageFromSDK(decoded.Usage, windowTokens),
		TrimmedItemsCount: 0,
	}, nil
}

func (t *HTTPTransport) CountRequestInputTokens(ctx context.Context, request OpenAIRequest) (int, error) {
	if t.Client == nil {
		t.Client = &http.Client{Timeout: 120 * time.Second}
	}

	authHeader, mode, err := t.resolveAuth(ctx)
	if err != nil {
		return 0, err
	}

	payload, err := t.buildInputTokenCountParams(request)
	if err != nil {
		return 0, err
	}

	service := responses.NewInputTokenService(
		option.WithBaseURL(t.serviceBaseURL(mode)),
		option.WithHTTPClient(t.Client),
		option.WithMaxRetries(0),
	)
	reqOpts := t.buildRequestOptions(authHeader, mode, request.SessionID)
	var rawResp *http.Response
	reqOpts = append(reqOpts, option.WithResponseInto(&rawResp))

	decoded, err := service.Count(ctx, payload, reqOpts...)
	if err != nil {
		return 0, mapOpenAIRequestError(t.errorProviderID(mode), err, rawResp, "openai responses input_tokens request failed")
	}
	if decoded == nil {
		return 0, fmt.Errorf("openai responses input_tokens request failed: empty response")
	}
	resolvedWindow := parseContextWindowTokens(decoded.RawJSON())
	if resolvedWindow <= 0 {
		resolvedWindow = parseContextWindowTokensFromHeaders(rawResp)
	}
	t.cacheModelContextWindow(request.Model, resolvedWindow)
	if decoded.InputTokens < 0 {
		return 0, nil
	}
	return int(decoded.InputTokens), nil
}

func (t *HTTPTransport) ResolveModelContextWindow(ctx context.Context, model string) (int, error) {
	if t.Client == nil {
		t.Client = &http.Client{Timeout: 120 * time.Second}
	}
	if t.ContextWindowTokens > 0 {
		return t.ContextWindowTokens, nil
	}

	normalizedModel := strings.ToLower(strings.TrimSpace(model))
	if normalizedModel == "" {
		if t.ContextWindowTokens > 0 {
			return t.ContextWindowTokens, nil
		}
		return 0, nil
	}

	t.mu.RLock()
	if cached := t.modelContextWindows[normalizedModel]; cached > 0 {
		t.mu.RUnlock()
		return cached, nil
	}
	t.mu.RUnlock()

	resolved := 0
	authHeader, mode, err := t.resolveAuth(ctx)
	if err == nil {
		service := openai.NewModelService(
			option.WithBaseURL(t.serviceBaseURL(mode)),
			option.WithHTTPClient(t.Client),
			option.WithMaxRetries(0),
		)
		reqOpts := t.buildRequestOptions(authHeader, mode, "")
		var rawResp *http.Response
		reqOpts = append(reqOpts, option.WithResponseInto(&rawResp))
		modelResponse, modelErr := service.Get(ctx, strings.TrimSpace(model), reqOpts...)
		if modelErr == nil && modelResponse != nil {
			resolved = parseContextWindowTokens(modelResponse.RawJSON())
		}
		if resolved <= 0 {
			resolved = parseContextWindowTokensFromHeaders(rawResp)
		}
	}

	if resolved <= 0 {
		if fallbackMeta, ok := LookupModelMetadata(model); ok && fallbackMeta.ContextWindowTokens > 0 {
			resolved = fallbackMeta.ContextWindowTokens
		}
	}
	if resolved <= 0 {
		resolved = t.ContextWindowTokens
	}

	t.cacheModelContextWindow(model, resolved)
	return resolved, nil
}

func (t *HTTPTransport) ProviderCapabilities(ctx context.Context) (ProviderCapabilities, error) {
	_, mode, err := t.resolveAuth(ctx)
	if err != nil {
		return ProviderCapabilities{}, err
	}
	return InferProviderCapabilities(t.serviceBaseURL(mode), mode.IsOAuth), nil
}

func (t *HTTPTransport) resolveAuth(ctx context.Context) (string, openAIAuthMode, error) {
	authHeader, err := t.Auth.AuthorizationHeader(ctx)
	if err != nil {
		return "", openAIAuthMode{}, &AuthError{Err: err}
	}

	mode := openAIAuthMode{}
	if provider, ok := t.Auth.(OpenAIAuthMetadataProvider); ok {
		method, accountID, err := provider.OpenAIAuthMetadata(ctx)
		if err != nil {
			return "", openAIAuthMode{}, &AuthError{Err: err}
		}
		mode.IsOAuth = method == "oauth"
		mode.AccountID = strings.TrimSpace(accountID)
	}
	return authHeader, mode, nil
}

func (t *HTTPTransport) buildPayload(request OpenAIRequest, mode openAIAuthMode) (responses.ResponseNewParams, error) {
	input := buildResponsesInput(request.Messages, request.Items)

	tools := make([]responses.ToolUnionParam, 0, len(request.Tools))
	for _, tool := range request.Tools {
		if len(tool.Schema) > 0 && !json.Valid(tool.Schema) {
			return responses.ResponseNewParams{}, fmt.Errorf("invalid tool schema for %s", tool.Name)
		}
		params := map[string]any{"type": "object", "properties": map[string]any{}}
		if len(tool.Schema) > 0 {
			if err := json.Unmarshal(tool.Schema, &params); err != nil {
				return responses.ResponseNewParams{}, fmt.Errorf("invalid tool schema for %s", tool.Name)
			}
		}
		normalizeSchemaAdditionalProperties(params)
		toolParam := responses.ToolParamOfFunction(tool.Name, params, false)
		if desc := strings.TrimSpace(tool.Description); desc != "" && toolParam.OfFunction != nil {
			toolParam.OfFunction.Description = openai.String(desc)
		}
		tools = append(tools, toolParam)
	}
	if request.EnableNativeWebSearch {
		tools = append(tools, responses.ToolParamOfWebSearch(responses.WebSearchToolTypeWebSearch))
	}

	out := responses.ResponseNewParams{
		Model: request.Model,
		Store: openai.Bool(t.Store),
	}
	if len(input) > 0 {
		out.Input = responses.ResponseNewParamsInputUnion{OfInputItemList: input}
	}
	if instructions := strings.TrimSpace(request.SystemPrompt); instructions != "" {
		out.Instructions = openai.String(instructions)
	}
	if len(tools) > 0 {
		out.Tools = tools
		out.ParallelToolCalls = openai.Bool(true)
	}
	if shouldApplyReasoningEffort(request.Model, request.ReasoningEffort) {
		out.Reasoning = shared.ReasoningParam{
			Effort:  shared.ReasoningEffort(strings.TrimSpace(request.ReasoningEffort)),
			Summary: shared.ReasoningSummaryConcise,
		}
		out.Include = append(out.Include, responses.ResponseIncludableReasoningEncryptedContent)
	}
	if request.MaxTokens > 0 && !mode.IsOAuth {
		out.MaxOutputTokens = openai.Int(int64(request.MaxTokens))
	}
	if request.Temperature != 0 && !mode.IsOAuth {
		out.Temperature = openai.Float(request.Temperature)
	}
	if request.StructuredOutput != nil {
		var schema map[string]any
		if err := json.Unmarshal(request.StructuredOutput.Schema, &schema); err != nil {
			return responses.ResponseNewParams{}, fmt.Errorf("invalid structured output schema")
		}
		text := responses.ResponseTextConfigParam{
			Format: responses.ResponseFormatTextConfigParamOfJSONSchema(strings.TrimSpace(request.StructuredOutput.Name), schema),
		}
		if text.Format.OfJSONSchema != nil {
			if request.StructuredOutput.Strict {
				text.Format.OfJSONSchema.Strict = param.NewOpt(true)
			}
			if description := strings.TrimSpace(request.StructuredOutput.Description); description != "" {
				text.Format.OfJSONSchema.Description = param.NewOpt(description)
			}
		}
		out.Text = text
	}

	return out, nil
}

func (t *HTTPTransport) buildInputTokenCountParams(request OpenAIRequest) (responses.InputTokenCountParams, error) {
	input := buildResponsesInput(request.Messages, request.Items)

	tools := make([]responses.ToolUnionParam, 0, len(request.Tools))
	for _, tool := range request.Tools {
		if len(tool.Schema) > 0 && !json.Valid(tool.Schema) {
			return responses.InputTokenCountParams{}, fmt.Errorf("invalid tool schema for %s", tool.Name)
		}
		params := map[string]any{"type": "object", "properties": map[string]any{}}
		if len(tool.Schema) > 0 {
			if err := json.Unmarshal(tool.Schema, &params); err != nil {
				return responses.InputTokenCountParams{}, fmt.Errorf("invalid tool schema for %s", tool.Name)
			}
		}
		normalizeSchemaAdditionalProperties(params)
		toolParam := responses.ToolParamOfFunction(tool.Name, params, false)
		if description := strings.TrimSpace(tool.Description); description != "" && toolParam.OfFunction != nil {
			toolParam.OfFunction.Description = openai.String(description)
		}
		tools = append(tools, toolParam)
	}
	if request.EnableNativeWebSearch {
		tools = append(tools, responses.ToolParamOfWebSearch(responses.WebSearchToolTypeWebSearch))
	}

	out := responses.InputTokenCountParams{
		Model: param.NewOpt(strings.TrimSpace(request.Model)),
	}
	if len(input) > 0 {
		out.Input = responses.InputTokenCountParamsInputUnion{OfResponseInputItemArray: input}
	}
	if instructions := strings.TrimSpace(request.SystemPrompt); instructions != "" {
		out.Instructions = param.NewOpt(instructions)
	}
	if len(tools) > 0 {
		out.Tools = tools
		out.ParallelToolCalls = param.NewOpt(true)
	}
	if shouldApplyReasoningEffort(request.Model, request.ReasoningEffort) {
		out.Reasoning = shared.ReasoningParam{
			Effort:  shared.ReasoningEffort(strings.TrimSpace(request.ReasoningEffort)),
			Summary: shared.ReasoningSummaryConcise,
		}
	}
	if request.StructuredOutput != nil {
		var schema map[string]any
		if err := json.Unmarshal(request.StructuredOutput.Schema, &schema); err != nil {
			return responses.InputTokenCountParams{}, fmt.Errorf("invalid structured output schema")
		}
		text := responses.InputTokenCountParamsText{
			Format: responses.ResponseFormatTextConfigParamOfJSONSchema(strings.TrimSpace(request.StructuredOutput.Name), schema),
		}
		if text.Format.OfJSONSchema != nil {
			if request.StructuredOutput.Strict {
				text.Format.OfJSONSchema.Strict = param.NewOpt(true)
			}
			if description := strings.TrimSpace(request.StructuredOutput.Description); description != "" {
				text.Format.OfJSONSchema.Description = param.NewOpt(description)
			}
		}
		out.Text = text
	}

	return out, nil
}

func (t *HTTPTransport) buildCompactPayload(request OpenAICompactionRequest) (responses.ResponseCompactParams, error) {
	if strings.TrimSpace(request.Model) == "" {
		return responses.ResponseCompactParams{}, fmt.Errorf("compaction model is required")
	}
	input := buildResponsesInput(nil, request.InputItems)
	out := responses.ResponseCompactParams{
		Model: responses.ResponseCompactParamsModel(request.Model),
	}
	if len(input) > 0 {
		out.Input = responses.ResponseCompactParamsInputUnion{
			OfResponseInputItemArray: input,
		}
	}
	if instructions := strings.TrimSpace(request.Instructions); instructions != "" {
		out.Instructions = param.NewOpt(instructions)
	}
	return out, nil
}

package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"builder/internal/shared/textutil"
	"builder/internal/tools"

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
	}
}

func (t *HTTPTransport) Generate(ctx context.Context, request OpenAIRequest) (OpenAIResponse, error) {
	if t.Client == nil {
		t.Client = &http.Client{Timeout: 120 * time.Second}
	}

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
		return OpenAIResponse{}, mapOpenAIRequestError(err, rawResp, "openai responses request failed")
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
		Usage:          usageFromSDK(decoded.Usage, t.ContextWindowTokens),
	}, nil
}

func (t *HTTPTransport) GenerateStream(ctx context.Context, request OpenAIRequest, onDelta func(text string)) (OpenAIResponse, error) {
	if t.Client == nil {
		t.Client = &http.Client{Timeout: 120 * time.Second}
	}

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
	usage := Usage{WindowTokens: t.ContextWindowTokens}
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
		return OpenAIResponse{}, mapOpenAIRequestError(err, rawResp, "read responses stream events")
	}

	finalText := assistantText.String()
	finalCalls := acc.ToToolCalls()
	finalReasoning := reasoningAcc.Entries()
	finalReasoningItems := reasoningAcc.Items()
	finalOutputItems := buildOutputItemsFromStream(finalText, finalCalls, finalReasoning, finalReasoningItems)

	if completed != nil {
		if completed.Usage.InputTokens > 0 || completed.Usage.OutputTokens > 0 {
			usage = usageFromSDK(completed.Usage, t.ContextWindowTokens)
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
		return OpenAICompactionResponse{}, mapOpenAIRequestError(err, rawResp, "openai responses compact request failed")
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
		Usage:             usageFromSDK(decoded.Usage, t.ContextWindowTokens),
		TrimmedItemsCount: 0,
	}, nil
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

func buildResponsesInput(messages []Message, canonical []ResponseItem) []responses.ResponseInputItemUnionParam {
	if len(canonical) > 0 {
		return buildResponsesInputFromItems(canonical)
	}
	return buildResponsesInputFromMessages(messages)
}

func buildResponsesInputFromMessages(messages []Message) []responses.ResponseInputItemUnionParam {
	var items []responses.ResponseInputItemUnionParam
	for _, msg := range messages {
		switch msg.Role {
		case RoleTool:
			if strings.TrimSpace(msg.ToolCallID) == "" {
				continue
			}
			items = append(items, functionCallOutputInputItems(msg.ToolCallID, msg.Name, normalizeToolInput(msg.Content))...)
		case RoleAssistant:
			if strings.TrimSpace(msg.Content) != "" {
				items = append(items, messageInput(string(msg.Role), msg.Content))
			}
			for _, tc := range msg.ToolCalls {
				callID := strings.TrimSpace(tc.ID)
				if callID == "" {
					continue
				}
				items = append(items, responses.ResponseInputItemParamOfFunctionCall(normalizeToolArguments(string(tc.Input)), callID, tc.Name))
			}
			for _, ri := range msg.ReasoningItems {
				id := strings.TrimSpace(ri.ID)
				encrypted := strings.TrimSpace(ri.EncryptedContent)
				if id == "" || encrypted == "" {
					continue
				}
				items = append(items, responses.ResponseInputItemUnionParam{
					OfReasoning: &responses.ResponseReasoningItemParam{
						ID:               id,
						Summary:          []responses.ResponseReasoningItemSummaryParam{},
						EncryptedContent: param.NewOpt(encrypted),
					},
				})
			}
		default:
			if strings.TrimSpace(msg.Content) == "" {
				continue
			}
			items = append(items, messageInput(string(msg.Role), msg.Content))
		}
	}
	return items
}

func buildResponsesInputFromItems(canonical []ResponseItem) []responses.ResponseInputItemUnionParam {
	items := make([]responses.ResponseInputItemUnionParam, 0, len(canonical))
	for _, item := range canonical {
		switch item.Type {
		case ResponseItemTypeMessage:
			if strings.TrimSpace(item.Content) == "" {
				continue
			}
			items = append(items, messageInput(string(item.Role), item.Content))
		case ResponseItemTypeFunctionCall:
			callID := textutil.FirstNonEmpty(strings.TrimSpace(item.CallID), strings.TrimSpace(item.ID))
			if callID == "" {
				continue
			}
			items = append(items, responses.ResponseInputItemParamOfFunctionCall(normalizeToolArguments(string(item.Arguments)), callID, strings.TrimSpace(item.Name)))
		case ResponseItemTypeFunctionCallOutput:
			callID := strings.TrimSpace(item.CallID)
			if callID == "" {
				continue
			}
			items = append(items, functionCallOutputInputItems(callID, item.Name, item.Output)...)
		case ResponseItemTypeReasoning:
			id := strings.TrimSpace(item.ID)
			if id == "" {
				continue
			}
			reasoningParam := responses.ResponseReasoningItemParam{
				ID:      id,
				Summary: []responses.ResponseReasoningItemSummaryParam{},
			}
			for _, summary := range item.ReasoningSummary {
				text := strings.TrimSpace(summary.Text)
				if text == "" {
					continue
				}
				reasoningParam.Summary = append(reasoningParam.Summary, responses.ResponseReasoningItemSummaryParam{
					Text: text,
					Type: "summary_text",
				})
			}
			if encrypted := strings.TrimSpace(item.EncryptedContent); encrypted != "" {
				reasoningParam.EncryptedContent = param.NewOpt(encrypted)
			}
			items = append(items, responses.ResponseInputItemUnionParam{
				OfReasoning: &reasoningParam,
			})
		case ResponseItemTypeCompaction:
			encrypted := strings.TrimSpace(item.EncryptedContent)
			if encrypted == "" {
				continue
			}
			compactionParam := responses.ResponseCompactionItemParam{
				EncryptedContent: encrypted,
			}
			if id := strings.TrimSpace(item.ID); id != "" {
				compactionParam.ID = param.NewOpt(id)
			}
			items = append(items, responses.ResponseInputItemUnionParam{
				OfCompaction: &compactionParam,
			})
		default:
			if len(item.Raw) == 0 || !json.Valid(item.Raw) {
				continue
			}
			items = append(items, param.Override[responses.ResponseInputItemUnionParam](item.Raw))
		}
	}
	return items
}

func messageInput(role, text string) responses.ResponseInputItemUnionParam {
	role = strings.TrimSpace(role)
	if role == string(RoleAssistant) {
		content := []responses.ResponseOutputMessageContentUnionParam{
			{
				OfOutputText: &responses.ResponseOutputTextParam{
					Annotations: []responses.ResponseOutputTextAnnotationUnionParam{},
					Text:        text,
				},
			},
		}
		return responses.ResponseInputItemParamOfOutputMessage(content, "", responses.ResponseOutputMessageStatusCompleted)
	}

	inputRole := string(RoleUser)
	switch role {
	case string(RoleSystem), string(RoleDeveloper), string(RoleUser):
		inputRole = role
	}
	content := responses.ResponseInputMessageContentListParam{
		responses.ResponseInputContentParamOfInputText(text),
	}
	return responses.ResponseInputItemParamOfInputMessage(content, inputRole)
}

func parseOutputItems(items []responses.ResponseOutputItemUnion) ([]ResponseItem, string, MessagePhase, []ToolCall, []ReasoningEntry, []ReasoningItem) {
	canonical := make([]ResponseItem, 0, len(items))
	assistantSegments := make([]assistantOutputSegment, 0, len(items))
	toolCalls := make([]ToolCall, 0, len(items))
	reasoning := make([]ReasoningEntry, 0, len(items))
	reasoningItems := make([]ReasoningItem, 0, len(items))
	for _, item := range items {
		raw := json.RawMessage(item.RawJSON())
		switch item.Type {
		case "message":
			role := Role(strings.TrimSpace(string(item.Role)))
			if role == "" {
				role = RoleAssistant
			}
			textPartsForItem := make([]string, 0, len(item.Content))
			for _, part := range item.Content {
				if part.Type == "output_text" || part.Type == "text" || part.Type == "input_text" {
					textPartsForItem = append(textPartsForItem, part.Text)
				}
			}
			text := strings.Join(textPartsForItem, "")
			phase := parseMessagePhaseFromRaw(raw)
			canonical = append(canonical, ResponseItem{
				Type:    ResponseItemTypeMessage,
				Role:    role,
				Phase:   phase,
				ID:      item.ID,
				Content: text,
				Raw:     raw,
			})
			if role == RoleAssistant {
				assistantSegments = append(assistantSegments, assistantOutputSegment{
					Text:  text,
					Phase: phase,
				})
			}
		case "function_call":
			callID := textutil.FirstNonEmpty(strings.TrimSpace(item.CallID), strings.TrimSpace(item.ID))
			if callID == "" && strings.TrimSpace(item.Name) == "" {
				continue
			}
			args := normalizeToolInput(item.Arguments)
			canonical = append(canonical, ResponseItem{
				Type:      ResponseItemTypeFunctionCall,
				ID:        strings.TrimSpace(item.ID),
				CallID:    callID,
				Name:      item.Name,
				Arguments: args,
				Raw:       raw,
			})
			toolCalls = append(toolCalls, ToolCall{
				ID:    callID,
				Name:  item.Name,
				Input: args,
			})
		case "reasoning":
			reasoningItem := item.AsReasoning()
			reasoningSummary := make([]ReasoningEntry, 0, len(reasoningItem.Summary))
			for _, summary := range reasoningItem.Summary {
				if strings.TrimSpace(summary.Text) != "" {
					reasoningSummary = append(reasoningSummary, ReasoningEntry{
						Role: reasoningRoleSummary,
						Text: summary.Text,
					})
				}
				reasoning = appendReasoningEntry(reasoning, reasoningRoleSummary, summary.Text)
			}
			canonicalReasoning := ResponseItem{
				Type:             ResponseItemTypeReasoning,
				ID:               strings.TrimSpace(reasoningItem.ID),
				ReasoningSummary: reasoningSummary,
				EncryptedContent: strings.TrimSpace(reasoningItem.EncryptedContent),
				Raw:              raw,
			}
			canonical = append(canonical, canonicalReasoning)
			if id := strings.TrimSpace(reasoningItem.ID); id != "" {
				if encrypted := strings.TrimSpace(reasoningItem.EncryptedContent); encrypted != "" {
					reasoningItems = append(reasoningItems, ReasoningItem{
						ID:               id,
						EncryptedContent: encrypted,
					})
				}
			}
		case "compaction":
			compactionItem := item.AsCompaction()
			canonical = append(canonical, ResponseItem{
				Type:             ResponseItemTypeCompaction,
				ID:               strings.TrimSpace(compactionItem.ID),
				EncryptedContent: strings.TrimSpace(compactionItem.EncryptedContent),
				Raw:              raw,
			})
		default:
			if len(raw) > 0 && json.Valid(raw) {
				canonical = append(canonical, ResponseItem{
					Type: ResponseItemTypeOther,
					Raw:  raw,
				})
			}
		}
	}
	assistantText, assistantPhase := resolveAssistantOutput(assistantSegments)
	return canonical, assistantText, assistantPhase, toolCalls, reasoning, reasoningItems
}

type assistantOutputSegment struct {
	Text  string
	Phase MessagePhase
}

func resolveAssistantOutput(segments []assistantOutputSegment) (string, MessagePhase) {
	if len(segments) == 0 {
		return "", ""
	}
	last := len(segments) - 1
	if segments[last].Phase == "" {
		return segments[last].Text, ""
	}
	phase := segments[last].Phase
	start := last
	for start > 0 {
		if segments[start-1].Phase != phase {
			break
		}
		start--
	}
	textParts := make([]string, 0, last-start+1)
	for i := start; i <= last; i++ {
		textParts = append(textParts, segments[i].Text)
	}
	return strings.Join(textParts, ""), phase
}

func parseMessagePhaseFromRaw(raw json.RawMessage) MessagePhase {
	if len(raw) == 0 || !json.Valid(raw) {
		return ""
	}
	var payload struct {
		Phase string `json:"phase"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return ""
	}
	return normalizeMessagePhase(payload.Phase)
}

func appendReasoningEntry(entries []ReasoningEntry, role, text string) []ReasoningEntry {
	text = strings.TrimSpace(text)
	if text == "" {
		return entries
	}
	return append(entries, ReasoningEntry{
		Role: role,
		Text: text,
	})
}

func mergeReasoningEntries(primary, secondary []ReasoningEntry) []ReasoningEntry {
	out := make([]ReasoningEntry, 0, len(primary)+len(secondary))
	seen := make(map[string]struct{}, len(primary)+len(secondary))
	add := func(entries []ReasoningEntry) {
		for _, entry := range entries {
			role := strings.TrimSpace(entry.Role)
			text := strings.TrimSpace(entry.Text)
			if role == "" || text == "" {
				continue
			}
			key := role + "\x00" + text
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, ReasoningEntry{Role: role, Text: text})
		}
	}
	add(primary)
	add(secondary)
	return out
}

func mergeReasoningItems(primary, secondary []ReasoningItem) []ReasoningItem {
	out := make([]ReasoningItem, 0, len(primary)+len(secondary))
	seen := make(map[string]struct{}, len(primary)+len(secondary))
	add := func(items []ReasoningItem) {
		for _, item := range items {
			id := strings.TrimSpace(item.ID)
			encrypted := strings.TrimSpace(item.EncryptedContent)
			if id == "" || encrypted == "" {
				continue
			}
			if _, exists := seen[id]; exists {
				continue
			}
			seen[id] = struct{}{}
			out = append(out, ReasoningItem{
				ID:               id,
				EncryptedContent: encrypted,
			})
		}
	}
	add(primary)
	add(secondary)
	return out
}

func reasoningEventKey(itemID string, outputIndex, partIndex int64) string {
	itemID = strings.TrimSpace(itemID)
	if itemID == "" {
		return fmt.Sprintf("output:%d:part:%d", outputIndex, partIndex)
	}
	return fmt.Sprintf("%s:part:%d", itemID, partIndex)
}

type reasoningAccumulator struct {
	order         []string
	items         map[string]*ReasoningEntry
	reasoningIDs  []string
	reasoningByID map[string]ReasoningItem
}

func newReasoningAccumulator() *reasoningAccumulator {
	return &reasoningAccumulator{
		order:         make([]string, 0, 8),
		items:         make(map[string]*ReasoningEntry, 8),
		reasoningIDs:  make([]string, 0, 4),
		reasoningByID: make(map[string]ReasoningItem, 4),
	}
}

func (a *reasoningAccumulator) ensure(role, key string) *ReasoningEntry {
	role = strings.TrimSpace(role)
	key = strings.TrimSpace(key)
	if role == "" || key == "" {
		return nil
	}
	composite := role + "\x00" + key
	if item, ok := a.items[composite]; ok {
		return item
	}
	entry := &ReasoningEntry{Role: role}
	a.items[composite] = entry
	a.order = append(a.order, composite)
	return entry
}

func (a *reasoningAccumulator) Append(role, key, delta string) {
	delta = strings.TrimSpace(delta)
	if delta == "" {
		return
	}
	entry := a.ensure(role, key)
	if entry == nil {
		return
	}
	entry.Text += delta
}

func (a *reasoningAccumulator) Set(role, key, text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	entry := a.ensure(role, key)
	if entry == nil {
		return
	}
	entry.Text = text
}

func (a *reasoningAccumulator) Entries() []ReasoningEntry {
	if a == nil {
		return nil
	}
	out := make([]ReasoningEntry, 0, len(a.order))
	for _, key := range a.order {
		entry, ok := a.items[key]
		if !ok {
			continue
		}
		text := strings.TrimSpace(entry.Text)
		if text == "" {
			continue
		}
		out = append(out, ReasoningEntry{
			Role: entry.Role,
			Text: text,
		})
	}
	return out
}

func (a *reasoningAccumulator) UpsertReasoningItem(item responses.ResponseOutputItemUnion) {
	if item.Type != "reasoning" {
		return
	}
	reasoningItem := item.AsReasoning()
	id := strings.TrimSpace(reasoningItem.ID)
	if id == "" {
		return
	}
	for idx, summary := range reasoningItem.Summary {
		key := fmt.Sprintf("%s:summary:%d", id, idx)
		a.Set(reasoningRoleSummary, key, summary.Text)
	}
	encrypted := strings.TrimSpace(reasoningItem.EncryptedContent)
	if encrypted == "" {
		return
	}
	if _, exists := a.reasoningByID[id]; !exists {
		a.reasoningIDs = append(a.reasoningIDs, id)
	}
	a.reasoningByID[id] = ReasoningItem{
		ID:               id,
		EncryptedContent: encrypted,
	}
}

func (a *reasoningAccumulator) Items() []ReasoningItem {
	if a == nil {
		return nil
	}
	out := make([]ReasoningItem, 0, len(a.reasoningIDs))
	for _, id := range a.reasoningIDs {
		item, ok := a.reasoningByID[id]
		if !ok {
			continue
		}
		if strings.TrimSpace(item.ID) == "" || strings.TrimSpace(item.EncryptedContent) == "" {
			continue
		}
		out = append(out, item)
	}
	return out
}

type toolCallAccumulator struct {
	byKey     map[string]*toolCallState
	itemToKey map[string]string
	order     []string
}

type toolCallState struct {
	CallID string
	Name   string
	Args   strings.Builder
}

func newToolCallAccumulator() *toolCallAccumulator {
	return &toolCallAccumulator{
		byKey:     map[string]*toolCallState{},
		itemToKey: map[string]string{},
		order:     []string{},
	}
}

func (a *toolCallAccumulator) ensure(key string) *toolCallState {
	if key == "" {
		return nil
	}
	if s, ok := a.byKey[key]; ok {
		return s
	}
	s := &toolCallState{CallID: key}
	a.byKey[key] = s
	a.order = append(a.order, key)
	return s
}

func (a *toolCallAccumulator) UpsertFromOutput(item responses.ResponseOutputItemUnion) {
	if item.Type != "function_call" {
		return
	}
	key := textutil.FirstNonEmpty(strings.TrimSpace(item.CallID), strings.TrimSpace(item.ID))
	if key == "" {
		return
	}
	state := a.ensure(key)
	if state == nil {
		return
	}
	if v := strings.TrimSpace(item.CallID); v != "" {
		state.CallID = v
	}
	if v := strings.TrimSpace(item.Name); v != "" {
		state.Name = v
	}
	if item.ID != "" {
		a.itemToKey[item.ID] = key
	}
	if args := strings.TrimSpace(item.Arguments); args != "" {
		state.Args.Reset()
		state.Args.WriteString(args)
	}
}

func (a *toolCallAccumulator) AppendArguments(itemID, delta string) {
	key := textutil.FirstNonEmpty(strings.TrimSpace(a.itemToKey[itemID]), strings.TrimSpace(itemID))
	state := a.ensure(key)
	if state == nil || strings.TrimSpace(delta) == "" {
		return
	}
	state.Args.WriteString(delta)
}

func (a *toolCallAccumulator) SetArguments(itemID, arguments string) {
	key := textutil.FirstNonEmpty(strings.TrimSpace(a.itemToKey[itemID]), strings.TrimSpace(itemID))
	state := a.ensure(key)
	if state == nil {
		return
	}
	state.Args.Reset()
	state.Args.WriteString(arguments)
}

func (a *toolCallAccumulator) Merge(calls []ToolCall) {
	for _, call := range calls {
		key := textutil.FirstNonEmpty(strings.TrimSpace(call.ID), strings.TrimSpace(call.Name))
		state := a.ensure(key)
		if state == nil {
			continue
		}
		if v := strings.TrimSpace(call.ID); v != "" {
			state.CallID = v
		}
		if v := strings.TrimSpace(call.Name); v != "" {
			state.Name = v
		}
		if len(call.Input) > 0 {
			state.Args.Reset()
			state.Args.WriteString(normalizeToolArguments(string(call.Input)))
		}
	}
}

func (a *toolCallAccumulator) ToToolCalls() []ToolCall {
	out := make([]ToolCall, 0, len(a.order))
	for _, key := range a.order {
		state, ok := a.byKey[key]
		if !ok {
			continue
		}
		callID := textutil.FirstNonEmpty(strings.TrimSpace(state.CallID), key)
		if callID == "" && strings.TrimSpace(state.Name) == "" {
			continue
		}
		out = append(out, ToolCall{
			ID:    callID,
			Name:  state.Name,
			Input: normalizeToolInput(state.Args.String()),
		})
	}
	return out
}

func normalizeToolArguments(arguments string) string {
	arguments = strings.TrimSpace(arguments)
	if arguments == "" {
		return "{}"
	}
	if json.Valid([]byte(arguments)) {
		return arguments
	}
	quoted, _ := json.Marshal(arguments)
	return string(quoted)
}

func normalizeToolInput(arguments string) json.RawMessage {
	arguments = strings.TrimSpace(arguments)
	if arguments == "" {
		return json.RawMessage(`{}`)
	}
	if json.Valid([]byte(arguments)) {
		return json.RawMessage(arguments)
	}
	quoted, _ := json.Marshal(arguments)
	return quoted
}

func shouldApplyReasoningEffort(model, effort string) bool {
	effort = strings.TrimSpace(effort)
	if effort == "" {
		return false
	}
	return SupportsReasoningEffortModel(model)
}

func outputStringFromRaw(raw json.RawMessage) string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return ""
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	return trimmed
}

func functionCallOutputInputItems(callID string, toolName string, raw json.RawMessage) []responses.ResponseInputItemUnionParam {
	if contentItems, ok := functionCallOutputContentItemsFromRaw(raw); ok {
		if strings.TrimSpace(toolName) == string(tools.ToolViewImage) {
			if promotedInputMessage, promoted := promoteFunctionOutputFilesToInputMessage(contentItems); promoted {
				return []responses.ResponseInputItemUnionParam{
					responses.ResponseInputItemParamOfFunctionCallOutput(callID, "attached file content"),
					responses.ResponseInputItemParamOfInputMessage(promotedInputMessage, string(RoleUser)),
				}
			}
		}
		return []responses.ResponseInputItemUnionParam{responses.ResponseInputItemParamOfFunctionCallOutput(callID, contentItems)}
	}
	return []responses.ResponseInputItemUnionParam{responses.ResponseInputItemParamOfFunctionCallOutput(callID, outputStringFromRaw(raw))}
}

func promoteFunctionOutputFilesToInputMessage(contentItems responses.ResponseFunctionCallOutputItemListParam) (responses.ResponseInputMessageContentListParam, bool) {
	out := make(responses.ResponseInputMessageContentListParam, 0, len(contentItems))
	hasInputFile := false

	for _, item := range contentItems {
		switch {
		case item.OfInputText != nil:
			out = append(out, responses.ResponseInputContentParamOfInputText(item.OfInputText.Text))
		case item.OfInputImage != nil:
			image := responses.ResponseInputImageParam{}
			detail := responses.ResponseInputImageDetailAuto
			switch item.OfInputImage.Detail {
			case responses.ResponseInputImageContentDetailLow:
				detail = responses.ResponseInputImageDetailLow
			case responses.ResponseInputImageContentDetailHigh:
				detail = responses.ResponseInputImageDetailHigh
			case responses.ResponseInputImageContentDetailAuto:
				detail = responses.ResponseInputImageDetailAuto
			}
			image.Detail = detail
			if item.OfInputImage.ImageURL.Valid() {
				image.ImageURL = item.OfInputImage.ImageURL
			}
			if item.OfInputImage.FileID.Valid() {
				image.FileID = item.OfInputImage.FileID
			}
			out = append(out, responses.ResponseInputContentUnionParam{OfInputImage: &image})
		case item.OfInputFile != nil:
			hasInputFile = true
			file := responses.ResponseInputFileParam{}
			if item.OfInputFile.FileData.Valid() {
				file.FileData = item.OfInputFile.FileData
			}
			if item.OfInputFile.FileID.Valid() {
				file.FileID = item.OfInputFile.FileID
			}
			if item.OfInputFile.FileURL.Valid() {
				file.FileURL = item.OfInputFile.FileURL
			}
			if item.OfInputFile.Filename.Valid() {
				file.Filename = item.OfInputFile.Filename
			}
			out = append(out, responses.ResponseInputContentUnionParam{OfInputFile: &file})
		}
	}

	if !hasInputFile || len(out) == 0 {
		return nil, false
	}
	return out, true
}

func functionCallOutputContentItemsFromRaw(raw json.RawMessage) (responses.ResponseFunctionCallOutputItemListParam, bool) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || !strings.HasPrefix(trimmed, "[") {
		return nil, false
	}

	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err != nil {
		return nil, false
	}
	if len(arr) == 0 {
		return nil, false
	}

	out := make(responses.ResponseFunctionCallOutputItemListParam, 0, len(arr))
	for _, rawItem := range arr {
		item, ok := functionCallOutputContentItemFromRaw(rawItem)
		if !ok {
			return nil, false
		}
		out = append(out, item)
	}
	return out, true
}

func functionCallOutputContentItemFromRaw(raw json.RawMessage) (responses.ResponseFunctionCallOutputItemUnionParam, bool) {
	var item struct {
		Type     string `json:"type"`
		Text     string `json:"text"`
		ImageURL string `json:"image_url"`
		Detail   string `json:"detail"`
		FileID   string `json:"file_id"`
		FileData string `json:"file_data"`
		FileURL  string `json:"file_url"`
		Filename string `json:"filename"`
	}
	if err := json.Unmarshal(raw, &item); err != nil {
		return responses.ResponseFunctionCallOutputItemUnionParam{}, false
	}

	switch strings.ToLower(strings.TrimSpace(item.Type)) {
	case "input_text":
		return responses.ResponseFunctionCallOutputItemUnionParam{
			OfInputText: &responses.ResponseInputTextContentParam{Text: item.Text},
		}, true
	case "input_image":
		image := responses.ResponseInputImageContentParam{}
		if v := strings.TrimSpace(item.ImageURL); v != "" {
			image.ImageURL = param.NewOpt(v)
		}
		if v := strings.TrimSpace(item.FileID); v != "" {
			image.FileID = param.NewOpt(v)
		}
		switch strings.ToLower(strings.TrimSpace(item.Detail)) {
		case "low":
			image.Detail = responses.ResponseInputImageContentDetailLow
		case "high":
			image.Detail = responses.ResponseInputImageContentDetailHigh
		case "auto":
			image.Detail = responses.ResponseInputImageContentDetailAuto
		}
		if !image.ImageURL.Valid() && !image.FileID.Valid() {
			return responses.ResponseFunctionCallOutputItemUnionParam{}, false
		}
		return responses.ResponseFunctionCallOutputItemUnionParam{OfInputImage: &image}, true
	case "input_file":
		file := responses.ResponseInputFileContentParam{}
		if v := strings.TrimSpace(item.FileData); v != "" {
			file.FileData = param.NewOpt(v)
		}
		if v := strings.TrimSpace(item.FileURL); v != "" {
			file.FileURL = param.NewOpt(v)
		}
		if v := strings.TrimSpace(item.FileID); v != "" {
			file.FileID = param.NewOpt(v)
		}
		if v := strings.TrimSpace(item.Filename); v != "" {
			file.Filename = param.NewOpt(v)
		}
		if !file.FileData.Valid() && !file.FileURL.Valid() && !file.FileID.Valid() {
			return responses.ResponseFunctionCallOutputItemUnionParam{}, false
		}
		return responses.ResponseFunctionCallOutputItemUnionParam{OfInputFile: &file}, true
	default:
		return responses.ResponseFunctionCallOutputItemUnionParam{}, false
	}
}

func buildOutputItemsFromStream(text string, toolCalls []ToolCall, reasoning []ReasoningEntry, reasoningItems []ReasoningItem) []ResponseItem {
	items := make([]ResponseItem, 0, 1+len(toolCalls)+len(reasoningItems))
	if strings.TrimSpace(text) != "" {
		items = append(items, ResponseItem{
			Type:    ResponseItemTypeMessage,
			Role:    RoleAssistant,
			Content: text,
		})
	}
	for _, call := range toolCalls {
		callID := textutil.FirstNonEmpty(strings.TrimSpace(call.ID), strings.TrimSpace(call.Name))
		if callID == "" {
			continue
		}
		items = append(items, ResponseItem{
			Type:      ResponseItemTypeFunctionCall,
			ID:        callID,
			CallID:    callID,
			Name:      call.Name,
			Arguments: normalizeToolInput(string(call.Input)),
		})
	}
	summaryByID := map[string][]ReasoningEntry{}
	for _, entry := range reasoning {
		text := strings.TrimSpace(entry.Text)
		if text == "" {
			continue
		}
		summaryByID[""] = append(summaryByID[""], ReasoningEntry{
			Role: entry.Role,
			Text: text,
		})
	}
	for _, item := range reasoningItems {
		id := strings.TrimSpace(item.ID)
		encrypted := strings.TrimSpace(item.EncryptedContent)
		if id == "" || encrypted == "" {
			continue
		}
		items = append(items, ResponseItem{
			Type:             ResponseItemTypeReasoning,
			ID:               id,
			EncryptedContent: encrypted,
			ReasoningSummary: append([]ReasoningEntry(nil), summaryByID[""]...),
		})
	}
	return items
}

func truncateError(b []byte) string {
	s := strings.TrimSpace(string(b))
	if s == "" {
		return "<empty error body>"
	}
	return s
}

func (t *HTTPTransport) newResponseService(mode openAIAuthMode) responses.ResponseService {
	return responses.NewResponseService(
		option.WithBaseURL(t.serviceBaseURL(mode)),
		option.WithHTTPClient(t.Client),
		option.WithMaxRetries(0),
	)
}

func (t *HTTPTransport) serviceBaseURL(mode openAIAuthMode) string {
	if mode.IsOAuth {
		return strings.TrimSuffix(codexResponsesEndpoint, "/responses")
	}
	base := strings.TrimSuffix(t.BaseURL, "/")
	if base == "" {
		base = defaultOpenAIBaseURL
	}
	return base
}

func (t *HTTPTransport) buildRequestOptions(authHeader string, mode openAIAuthMode, sessionID string) []option.RequestOption {
	opts := []option.RequestOption{
		option.WithHeader("Authorization", authHeader),
		option.WithHeader("originator", defaultOriginator),
		option.WithHeader("User-Agent", defaultUserAgent),
	}
	if strings.TrimSpace(sessionID) != "" {
		opts = append(opts, option.WithHeader("session_id", sessionID))
	}
	if mode.IsOAuth {
		if mode.AccountID != "" {
			opts = append(opts, option.WithHeader("ChatGPT-Account-Id", mode.AccountID))
		}
	}
	return opts
}

func mapOpenAIRequestError(err error, rawResp *http.Response, prefix string) error {
	if rawResp != nil && rawResp.StatusCode >= 300 {
		return apiStatusErrorFromResponse(rawResp)
	}
	if err == nil {
		return fmt.Errorf("%s: unknown error", prefix)
	}
	return fmt.Errorf("%s: %w", prefix, err)
}

func apiStatusErrorFromResponse(rawResp *http.Response) error {
	if rawResp == nil {
		return &APIStatusError{StatusCode: 0, Body: "<empty error body>"}
	}
	defer rawResp.Body.Close()
	body, _ := io.ReadAll(rawResp.Body)
	return &APIStatusError{
		StatusCode: rawResp.StatusCode,
		Body:       truncateError(body),
	}
}

func usageFromSDK(usage responses.ResponseUsage, window int) Usage {
	out := Usage{
		InputTokens:  int(usage.InputTokens),
		OutputTokens: int(usage.OutputTokens),
		WindowTokens: window,
	}
	if usage.JSON.InputTokensDetails.Valid() && usage.InputTokensDetails.JSON.CachedTokens.Valid() {
		out.CachedInputTokens = int(usage.InputTokensDetails.CachedTokens)
		out.HasCachedInputTokens = true
	}
	return out
}

func normalizeSchemaAdditionalProperties(schema map[string]any) {
	normalizeSchemaNode(schema)
}

func normalizeSchemaNode(node any) {
	obj, ok := node.(map[string]any)
	if ok {
		if isJSONObjectSchema(obj) {
			if _, exists := obj["additionalProperties"]; !exists {
				obj["additionalProperties"] = false
			}
		}
		if props, ok := obj["properties"].(map[string]any); ok {
			for _, prop := range props {
				normalizeSchemaNode(prop)
			}
		}
		if defs, ok := obj["$defs"].(map[string]any); ok {
			for _, def := range defs {
				normalizeSchemaNode(def)
			}
		}
		if defs, ok := obj["definitions"].(map[string]any); ok {
			for _, def := range defs {
				normalizeSchemaNode(def)
			}
		}
		if items, exists := obj["items"]; exists {
			normalizeSchemaNode(items)
		}
		for _, key := range []string{"allOf", "anyOf", "oneOf"} {
			if list, ok := obj[key].([]any); ok {
				for _, item := range list {
					normalizeSchemaNode(item)
				}
			}
		}
		for _, key := range []string{"not", "if", "then", "else"} {
			if child, exists := obj[key]; exists {
				normalizeSchemaNode(child)
			}
		}
		return
	}

	if list, ok := node.([]any); ok {
		for _, item := range list {
			normalizeSchemaNode(item)
		}
	}
}

func isJSONObjectSchema(schema map[string]any) bool {
	if len(schema) == 0 {
		return false
	}
	if typeField, ok := schema["type"]; ok {
		switch v := typeField.(type) {
		case string:
			return strings.TrimSpace(v) == "object"
		case []any:
			for _, item := range v {
				if sv, ok := item.(string); ok && strings.TrimSpace(sv) == "object" {
					return true
				}
			}
		}
	}
	_, hasProps := schema["properties"]
	return hasProps
}

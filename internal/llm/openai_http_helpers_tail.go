package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"builder/internal/shared/textutil"
	"builder/internal/tools"

	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/responses"
)

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

func mapOpenAIRequestError(providerID string, err error, rawResp *http.Response, prefix string) error {
	reducer, reducerErr := providerErrorReducerForID(providerID)
	if reducerErr != nil {
		statusCode := 0
		if rawResp != nil {
			statusCode = rawResp.StatusCode
			if rawResp.Body != nil {
				rawResp.Body.Close()
				rawResp.Body = nil
			}
		}
		return fmt.Errorf("%s: %w", prefix, NewProviderContractError(providerID, statusCode, reducerErr))
	}
	reducedErr, ok := reducer.Reduce(err, rawResp)
	if ok && reducedErr != nil {
		return fmt.Errorf("%s: %w", prefix, reducedErr)
	}
	if err == nil {
		return fmt.Errorf("%s: unknown error", prefix)
	}
	return fmt.Errorf("%s: %w", prefix, err)
}

func (t *HTTPTransport) errorProviderID(mode openAIAuthMode) string {
	return InferProviderCapabilities(t.serviceBaseURL(mode), mode.IsOAuth).ProviderID
}

func (t *HTTPTransport) resolveContextWindowFallback(ctx context.Context, model string) int {
	if t.ContextWindowTokens > 0 {
		return t.ContextWindowTokens
	}
	resolved, err := t.ResolveModelContextWindow(ctx, model)
	if err == nil && resolved > 0 {
		return resolved
	}
	if fallbackMeta, ok := LookupModelMetadata(model); ok && fallbackMeta.ContextWindowTokens > 0 {
		return fallbackMeta.ContextWindowTokens
	}
	return 0
}

func (t *HTTPTransport) cacheModelContextWindow(model string, tokens int) {
	if tokens <= 0 {
		return
	}
	normalizedModel := strings.ToLower(strings.TrimSpace(model))
	if normalizedModel == "" {
		return
	}
	t.mu.Lock()
	t.modelContextWindows[normalizedModel] = tokens
	t.mu.Unlock()
}

func parseContextWindowTokens(rawJSON string) int {
	trimmed := strings.TrimSpace(rawJSON)
	if trimmed == "" {
		return 0
	}
	var decoded any
	if err := json.Unmarshal([]byte(trimmed), &decoded); err != nil {
		return 0
	}
	return findPositiveIntByPreferredKeys(
		decoded,
		[]string{"context_window", "model_context_window", "input_token_limit", "max_input_tokens", "context_length"},
	)
}

func parseContextWindowTokensFromHeaders(rawResp *http.Response) int {
	if rawResp == nil {
		return 0
	}
	for _, headerName := range []string{
		"x-openai-model-context-window",
		"openai-model-context-window",
		"x-model-context-window",
		"model-context-window",
		"x-context-window",
		"context-window",
	} {
		if parsed := parsePositiveInt(rawResp.Header.Get(headerName)); parsed > 0 {
			return parsed
		}
	}
	return 0
}

func findPositiveIntByPreferredKeys(node any, keys []string) int {
	switch typed := node.(type) {
	case map[string]any:
		for _, key := range keys {
			if value, ok := typed[key]; ok {
				if parsed := parsePositiveInt(value); parsed > 0 {
					return parsed
				}
			}
		}
		for _, value := range typed {
			if parsed := findPositiveIntByPreferredKeys(value, keys); parsed > 0 {
				return parsed
			}
		}
	case []any:
		for _, value := range typed {
			if parsed := findPositiveIntByPreferredKeys(value, keys); parsed > 0 {
				return parsed
			}
		}
	}
	return 0
}

func parsePositiveInt(value any) int {
	switch typed := value.(type) {
	case float64:
		parsed := int(typed)
		if parsed > 0 {
			return parsed
		}
	case json.Number:
		parsed, err := typed.Int64()
		if err == nil && parsed > 0 {
			return int(parsed)
		}
	case int:
		if typed > 0 {
			return typed
		}
	case int64:
		if typed > 0 {
			return int(typed)
		}
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(typed))
		if err == nil && parsed > 0 {
			return parsed
		}
	}
	return 0
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

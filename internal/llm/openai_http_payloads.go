package llm

import (
	"encoding/json"
	"fmt"
	"strings"

	openai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"
)

type openAIRequestPayloadBuilder struct {
	store        bool
	capabilities ProviderCapabilities
}

func newOpenAIRequestPayloadBuilder(store bool, capabilities ProviderCapabilities) openAIRequestPayloadBuilder {
	return openAIRequestPayloadBuilder{store: store, capabilities: capabilities}
}

func (t *HTTPTransport) buildPayload(request OpenAIRequest, mode openAIAuthMode) (responses.ResponseNewParams, error) {
	builder := newOpenAIRequestPayloadBuilder(t.Store, InferProviderCapabilities(t.serviceBaseURL(mode), mode.IsOAuth))
	return builder.BuildResponse(request, mode)
}

func (t *HTTPTransport) buildInputTokenCountParams(request OpenAIRequest) (responses.InputTokenCountParams, error) {
	builder := newOpenAIRequestPayloadBuilder(t.Store, InferProviderCapabilities(t.serviceBaseURL(openAIAuthMode{}), false))
	return builder.BuildInputTokenCount(request)
}

func (t *HTTPTransport) buildCompactPayload(request OpenAICompactionRequest) (responses.ResponseCompactParams, error) {
	return newOpenAIRequestPayloadBuilder(t.Store, ProviderCapabilities{}).BuildCompact(request)
}

func (b openAIRequestPayloadBuilder) BuildResponse(request OpenAIRequest, mode openAIAuthMode) (responses.ResponseNewParams, error) {
	input := buildResponsesInput(request.Messages, request.Items)
	tools, err := b.buildTools(request.Tools, request.EnableNativeWebSearch)
	if err != nil {
		return responses.ResponseNewParams{}, err
	}

	out := responses.ResponseNewParams{Model: request.Model, Store: openai.Bool(b.store)}
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
		out.Reasoning = shared.ReasoningParam{Effort: shared.ReasoningEffort(strings.TrimSpace(request.ReasoningEffort)), Summary: shared.ReasoningSummaryConcise}
		out.Include = append(out.Include, responses.ResponseIncludableReasoningEncryptedContent)
	}
	if request.FastMode && SupportsFastModeProvider(b.capabilities) {
		out.ServiceTier = responses.ResponseNewParamsServiceTierPriority
	}
	if request.MaxTokens > 0 && !mode.IsOAuth {
		out.MaxOutputTokens = openai.Int(int64(request.MaxTokens))
	}
	if request.Temperature != 0 && !mode.IsOAuth {
		out.Temperature = openai.Float(request.Temperature)
	}
	if request.StructuredOutput != nil {
		textConfig, err := buildResponseTextConfig(*request.StructuredOutput)
		if err != nil {
			return responses.ResponseNewParams{}, err
		}
		out.Text = textConfig
	}
	return out, nil
}

func (b openAIRequestPayloadBuilder) BuildInputTokenCount(request OpenAIRequest) (responses.InputTokenCountParams, error) {
	input := buildResponsesInput(request.Messages, request.Items)
	tools, err := b.buildTools(request.Tools, request.EnableNativeWebSearch)
	if err != nil {
		return responses.InputTokenCountParams{}, err
	}

	out := responses.InputTokenCountParams{Model: param.NewOpt(strings.TrimSpace(request.Model))}
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
		out.Reasoning = shared.ReasoningParam{Effort: shared.ReasoningEffort(strings.TrimSpace(request.ReasoningEffort)), Summary: shared.ReasoningSummaryConcise}
	}
	if request.StructuredOutput != nil {
		textConfig, err := buildInputTokenCountTextConfig(*request.StructuredOutput)
		if err != nil {
			return responses.InputTokenCountParams{}, err
		}
		out.Text = textConfig
	}
	return out, nil
}

func (openAIRequestPayloadBuilder) BuildCompact(request OpenAICompactionRequest) (responses.ResponseCompactParams, error) {
	if strings.TrimSpace(request.Model) == "" {
		return responses.ResponseCompactParams{}, fmt.Errorf("compaction model is required")
	}
	input := buildResponsesInput(nil, request.InputItems)
	out := responses.ResponseCompactParams{Model: responses.ResponseCompactParamsModel(request.Model)}
	if len(input) > 0 {
		out.Input = responses.ResponseCompactParamsInputUnion{OfResponseInputItemArray: input}
	}
	if instructions := strings.TrimSpace(request.Instructions); instructions != "" {
		out.Instructions = param.NewOpt(instructions)
	}
	return out, nil
}

func (b openAIRequestPayloadBuilder) buildTools(requestTools []Tool, enableNativeWebSearch bool) ([]responses.ToolUnionParam, error) {
	tools := make([]responses.ToolUnionParam, 0, len(requestTools)+1)
	for _, tool := range requestTools {
		toolParam, err := buildFunctionToolParam(tool)
		if err != nil {
			return nil, err
		}
		tools = append(tools, toolParam)
	}
	if enableNativeWebSearch {
		tools = append(tools, responses.ToolParamOfWebSearch(responses.WebSearchToolTypeWebSearch))
	}
	return tools, nil
}

func buildFunctionToolParam(tool Tool) (responses.ToolUnionParam, error) {
	if len(tool.Schema) > 0 && !json.Valid(tool.Schema) {
		return responses.ToolUnionParam{}, fmt.Errorf("invalid tool schema for %s", tool.Name)
	}
	params := map[string]any{"type": "object", "properties": map[string]any{}}
	if len(tool.Schema) > 0 {
		if err := json.Unmarshal(tool.Schema, &params); err != nil {
			return responses.ToolUnionParam{}, fmt.Errorf("invalid tool schema for %s", tool.Name)
		}
	}
	normalizeSchemaAdditionalProperties(params)
	toolParam := responses.ToolParamOfFunction(tool.Name, params, false)
	if description := strings.TrimSpace(tool.Description); description != "" && toolParam.OfFunction != nil {
		toolParam.OfFunction.Description = openai.String(description)
	}
	return toolParam, nil
}

func buildResponseTextConfig(output StructuredOutput) (responses.ResponseTextConfigParam, error) {
	schema, err := parseStructuredOutputSchema(output.Schema)
	if err != nil {
		return responses.ResponseTextConfigParam{}, err
	}
	text := responses.ResponseTextConfigParam{Format: responses.ResponseFormatTextConfigParamOfJSONSchema(strings.TrimSpace(output.Name), schema)}
	if text.Format.OfJSONSchema != nil {
		if output.Strict {
			text.Format.OfJSONSchema.Strict = param.NewOpt(true)
		}
		if description := strings.TrimSpace(output.Description); description != "" {
			text.Format.OfJSONSchema.Description = param.NewOpt(description)
		}
	}
	return text, nil
}

func buildInputTokenCountTextConfig(output StructuredOutput) (responses.InputTokenCountParamsText, error) {
	schema, err := parseStructuredOutputSchema(output.Schema)
	if err != nil {
		return responses.InputTokenCountParamsText{}, err
	}
	text := responses.InputTokenCountParamsText{Format: responses.ResponseFormatTextConfigParamOfJSONSchema(strings.TrimSpace(output.Name), schema)}
	if text.Format.OfJSONSchema != nil {
		if output.Strict {
			text.Format.OfJSONSchema.Strict = param.NewOpt(true)
		}
		if description := strings.TrimSpace(output.Description); description != "" {
			text.Format.OfJSONSchema.Description = param.NewOpt(description)
		}
	}
	return text, nil
}

func parseStructuredOutputSchema(raw json.RawMessage) (map[string]any, error) {
	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		return nil, fmt.Errorf("invalid structured output schema")
	}
	return schema, nil
}

func shouldApplyReasoningEffort(model, effort string) bool {
	effort = strings.TrimSpace(effort)
	if effort == "" {
		return false
	}
	return SupportsReasoningEffortModel(model)
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
		switch value := typeField.(type) {
		case string:
			return strings.TrimSpace(value) == "object"
		case []any:
			for _, item := range value {
				if stringValue, ok := item.(string); ok && strings.TrimSpace(stringValue) == "object" {
					return true
				}
			}
		}
	}
	_, hasProps := schema["properties"]
	return hasProps
}

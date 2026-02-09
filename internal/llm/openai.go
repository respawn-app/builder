package llm

import (
	"context"
	"fmt"
)

type OpenAIRequest struct {
	Model           string
	Temperature     float64
	MaxTokens       int
	ReasoningEffort string
	SystemPrompt    string
	SessionID       string
	Messages        []Message
	Items           []ResponseItem
	Tools           []Tool
}

type OpenAIResponse struct {
	AssistantText  string
	ToolCalls      []ToolCall
	Reasoning      []ReasoningEntry
	ReasoningItems []ReasoningItem
	OutputItems    []ResponseItem
	Usage          Usage
}

type OpenAICompactionRequest struct {
	Model        string
	Instructions string
	SessionID    string
	InputItems   []ResponseItem
}

type OpenAICompactionResponse struct {
	OutputItems       []ResponseItem
	Usage             Usage
	TrimmedItemsCount int
}

type OpenAITransport interface {
	Generate(ctx context.Context, request OpenAIRequest) (OpenAIResponse, error)
	Compact(ctx context.Context, request OpenAICompactionRequest) (OpenAICompactionResponse, error)
}

type OpenAIStreamingTransport interface {
	GenerateStream(ctx context.Context, request OpenAIRequest, onDelta func(text string)) (OpenAIResponse, error)
}

type OpenAIProviderCapabilitiesTransport interface {
	ProviderCapabilities(ctx context.Context) (ProviderCapabilities, error)
}

type OpenAIClient struct {
	transport OpenAITransport
}

func NewOpenAIClient(transport OpenAITransport) *OpenAIClient {
	return &OpenAIClient{transport: transport}
}

func (c *OpenAIClient) Generate(ctx context.Context, request Request) (Response, error) {
	if c == nil || c.transport == nil {
		return Response{}, ErrMissingTransport
	}
	if err := request.Validate(); err != nil {
		return Response{}, err
	}

	providerReq := OpenAIRequest{
		Model:           request.Model,
		Temperature:     request.Temperature,
		MaxTokens:       request.MaxTokens,
		ReasoningEffort: request.ReasoningEffort,
		SystemPrompt:    request.SystemPrompt,
		SessionID:       request.SessionID,
		Messages:        append([]Message(nil), request.Messages...),
		Items:           CloneResponseItems(request.Items),
		Tools:           append([]Tool(nil), request.Tools...),
	}

	providerResp, err := c.transport.Generate(ctx, providerReq)
	if err != nil {
		return Response{}, fmt.Errorf("openai generate: %w", err)
	}

	return Response{
		Assistant: Message{
			Role:           RoleAssistant,
			Content:        providerResp.AssistantText,
			ToolCalls:      append([]ToolCall(nil), providerResp.ToolCalls...),
			ReasoningItems: append([]ReasoningItem(nil), providerResp.ReasoningItems...),
		},
		ToolCalls:      providerResp.ToolCalls,
		Reasoning:      append([]ReasoningEntry(nil), providerResp.Reasoning...),
		ReasoningItems: append([]ReasoningItem(nil), providerResp.ReasoningItems...),
		OutputItems:    CloneResponseItems(providerResp.OutputItems),
		Usage:          providerResp.Usage,
	}, nil
}

func (c *OpenAIClient) GenerateStream(ctx context.Context, request Request, onDelta func(text string)) (Response, error) {
	if c == nil || c.transport == nil {
		return Response{}, ErrMissingTransport
	}
	if err := request.Validate(); err != nil {
		return Response{}, err
	}

	providerReq := OpenAIRequest{
		Model:           request.Model,
		Temperature:     request.Temperature,
		MaxTokens:       request.MaxTokens,
		ReasoningEffort: request.ReasoningEffort,
		SystemPrompt:    request.SystemPrompt,
		SessionID:       request.SessionID,
		Messages:        append([]Message(nil), request.Messages...),
		Items:           CloneResponseItems(request.Items),
		Tools:           append([]Tool(nil), request.Tools...),
	}

	if streamTransport, ok := c.transport.(OpenAIStreamingTransport); ok {
		providerResp, err := streamTransport.GenerateStream(ctx, providerReq, onDelta)
		if err != nil {
			return Response{}, fmt.Errorf("openai generate stream: %w", err)
		}
		return Response{
			Assistant: Message{
				Role:           RoleAssistant,
				Content:        providerResp.AssistantText,
				ToolCalls:      append([]ToolCall(nil), providerResp.ToolCalls...),
				ReasoningItems: append([]ReasoningItem(nil), providerResp.ReasoningItems...),
			},
			ToolCalls:      providerResp.ToolCalls,
			Reasoning:      append([]ReasoningEntry(nil), providerResp.Reasoning...),
			ReasoningItems: append([]ReasoningItem(nil), providerResp.ReasoningItems...),
			OutputItems:    CloneResponseItems(providerResp.OutputItems),
			Usage:          providerResp.Usage,
		}, nil
	}

	resp, err := c.Generate(ctx, request)
	if err != nil {
		return Response{}, err
	}
	if onDelta != nil && resp.Assistant.Content != "" {
		onDelta(resp.Assistant.Content)
	}
	return resp, nil
}

func (c *OpenAIClient) Compact(ctx context.Context, request CompactionRequest) (CompactionResponse, error) {
	if c == nil || c.transport == nil {
		return CompactionResponse{}, ErrMissingTransport
	}
	if request.Model == "" {
		return CompactionResponse{}, fmt.Errorf("%w: compaction model is required", ErrInvalidRequest)
	}

	providerReq := OpenAICompactionRequest{
		Model:        request.Model,
		Instructions: request.Instructions,
		SessionID:    request.SessionID,
		InputItems:   CloneResponseItems(request.InputItems),
	}
	providerResp, err := c.transport.Compact(ctx, providerReq)
	if err != nil {
		return CompactionResponse{}, fmt.Errorf("openai compact: %w", err)
	}
	return CompactionResponse{
		OutputItems:       CloneResponseItems(providerResp.OutputItems),
		Usage:             providerResp.Usage,
		TrimmedItemsCount: providerResp.TrimmedItemsCount,
	}, nil
}

func (c *OpenAIClient) ProviderCapabilities(ctx context.Context) (ProviderCapabilities, error) {
	if c == nil || c.transport == nil {
		return ProviderCapabilities{}, ErrMissingTransport
	}
	if transport, ok := c.transport.(OpenAIProviderCapabilitiesTransport); ok {
		return transport.ProviderCapabilities(ctx)
	}
	return ProviderCapabilities{
		ProviderID:                    "openai",
		SupportsResponsesAPI:          true,
		SupportsResponsesCompact:      true,
		SupportsReasoningEncrypted:    true,
		SupportsServerSideContextEdit: true,
		IsOpenAIFirstParty:            true,
	}, nil
}

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
	Tools           []Tool
}

type OpenAIResponse struct {
	AssistantText  string
	ToolCalls      []ToolCall
	Reasoning      []ReasoningEntry
	ReasoningItems []ReasoningItem
	Usage          Usage
}

type OpenAITransport interface {
	Generate(ctx context.Context, request OpenAIRequest) (OpenAIResponse, error)
}

type OpenAIStreamingTransport interface {
	GenerateStream(ctx context.Context, request OpenAIRequest, onDelta func(text string)) (OpenAIResponse, error)
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

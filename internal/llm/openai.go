package llm

import (
	"context"
	"fmt"
)

type OpenAIRequest struct {
	Model        string
	Temperature  float64
	MaxTokens    int
	SystemPrompt string
	Messages     []Message
	Tools        []Tool
}

type OpenAIResponse struct {
	AssistantText string
	ToolCalls     []ToolCall
	Usage         Usage
}

type OpenAITransport interface {
	Generate(ctx context.Context, request OpenAIRequest) (OpenAIResponse, error)
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
		Model:        request.Model,
		Temperature:  request.Temperature,
		MaxTokens:    request.MaxTokens,
		SystemPrompt: request.SystemPrompt,
		Messages:     append([]Message(nil), request.Messages...),
		Tools:        append([]Tool(nil), request.Tools...),
	}

	providerResp, err := c.transport.Generate(ctx, providerReq)
	if err != nil {
		return Response{}, fmt.Errorf("openai generate: %w", err)
	}

	return Response{
		Assistant: Message{
			Role:    RoleAssistant,
			Content: providerResp.AssistantText,
		},
		ToolCalls: providerResp.ToolCalls,
		Usage:     providerResp.Usage,
	}, nil
}

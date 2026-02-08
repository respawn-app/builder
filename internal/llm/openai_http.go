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
)

type AuthHeaderProvider interface {
	AuthorizationHeader(ctx context.Context) (string, error)
}

type HTTPTransport struct {
	BaseURL             string
	Client              *http.Client
	Auth                AuthHeaderProvider
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
		BaseURL:             "https://api.openai.com/v1",
		Client:              &http.Client{Timeout: 120 * time.Second},
		Auth:                auth,
		ContextWindowTokens: window,
	}
}

func (t *HTTPTransport) Generate(ctx context.Context, request OpenAIRequest) (OpenAIResponse, error) {
	if t.Client == nil {
		t.Client = &http.Client{Timeout: 120 * time.Second}
	}
	base := strings.TrimSuffix(t.BaseURL, "/")
	if base == "" {
		base = "https://api.openai.com/v1"
	}

	head, err := t.Auth.AuthorizationHeader(ctx)
	if err != nil {
		return OpenAIResponse{}, err
	}

	payload, err := t.buildPayload(request)
	if err != nil {
		return OpenAIResponse{}, err
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return OpenAIResponse{}, fmt.Errorf("marshal openai request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return OpenAIResponse{}, fmt.Errorf("create openai request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", head)

	resp, err := t.Client.Do(httpReq)
	if err != nil {
		return OpenAIResponse{}, fmt.Errorf("openai request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return OpenAIResponse{}, fmt.Errorf("read openai response: %w", err)
	}
	if resp.StatusCode >= 300 {
		return OpenAIResponse{}, fmt.Errorf("openai status %d: %s", resp.StatusCode, truncateError(respBody))
	}

	var decoded chatCompletionResponse
	if err := json.Unmarshal(respBody, &decoded); err != nil {
		return OpenAIResponse{}, fmt.Errorf("decode openai response: %w", err)
	}
	if len(decoded.Choices) == 0 {
		return OpenAIResponse{}, fmt.Errorf("openai response missing choices")
	}

	choice := decoded.Choices[0]
	toolCalls := make([]ToolCall, 0, len(choice.Message.ToolCalls))
	for _, c := range choice.Message.ToolCalls {
		toolCalls = append(toolCalls, ToolCall{
			ID:    c.ID,
			Name:  c.Function.Name,
			Input: json.RawMessage(c.Function.Arguments),
		})
	}

	usage := Usage{
		InputTokens:  decoded.Usage.PromptTokens,
		OutputTokens: decoded.Usage.CompletionTokens,
		WindowTokens: t.ContextWindowTokens,
	}

	return OpenAIResponse{
		AssistantText: choice.Message.Content,
		ToolCalls:     toolCalls,
		Usage:         usage,
	}, nil
}

func (t *HTTPTransport) buildPayload(request OpenAIRequest) (chatCompletionRequest, error) {
	msgs := make([]chatMessage, 0, len(request.Messages)+1)
	if strings.TrimSpace(request.SystemPrompt) != "" {
		msgs = append(msgs, chatMessage{Role: string(RoleSystem), Content: request.SystemPrompt})
	}
	for _, m := range request.Messages {
		item := chatMessage{Role: string(m.Role), Content: m.Content, Name: m.Name, ToolCallID: m.ToolCallID}
		msgs = append(msgs, item)
	}

	tools := make([]chatTool, 0, len(request.Tools))
	for _, tool := range request.Tools {
		if len(tool.Schema) > 0 && !json.Valid(tool.Schema) {
			return chatCompletionRequest{}, fmt.Errorf("invalid tool schema for %s", tool.Name)
		}
		params := json.RawMessage(`{"type":"object","properties":{}}`)
		if len(tool.Schema) > 0 {
			params = append(json.RawMessage(nil), tool.Schema...)
		}
		tools = append(tools, chatTool{
			Type: "function",
			Function: chatFunction{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  params,
			},
		})
	}

	out := chatCompletionRequest{
		Model:             request.Model,
		Messages:          msgs,
		Temperature:       &request.Temperature,
		ParallelToolCalls: true,
		Tools:             tools,
	}
	if request.MaxTokens > 0 {
		out.MaxTokens = &request.MaxTokens
	}
	return out, nil
}

type chatCompletionRequest struct {
	Model             string        `json:"model"`
	Messages          []chatMessage `json:"messages"`
	Tools             []chatTool    `json:"tools,omitempty"`
	Temperature       *float64      `json:"temperature,omitempty"`
	MaxTokens         *int          `json:"max_tokens,omitempty"`
	ParallelToolCalls bool          `json:"parallel_tool_calls"`
}

type chatMessage struct {
	Role       string `json:"role"`
	Content    string `json:"content,omitempty"`
	Name       string `json:"name,omitempty"`
	ToolCallID string `json:"tool_call_id,omitempty"`
}

type chatTool struct {
	Type     string       `json:"type"`
	Function chatFunction `json:"function"`
}

type chatFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
}

type chatCompletionResponse struct {
	Choices []struct {
		Message struct {
			Content   string `json:"content"`
			ToolCalls []struct {
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

func truncateError(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) <= 240 {
		return s
	}
	return s[:240] + "..."
}

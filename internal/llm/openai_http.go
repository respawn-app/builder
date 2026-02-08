package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
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
	toolCalls := convertToolCalls(choice.Message.ToolCalls)

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

func (t *HTTPTransport) GenerateStream(ctx context.Context, request OpenAIRequest, onDelta func(text string)) (OpenAIResponse, error) {
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
	payload.Stream = true
	payload.StreamOptions = &chatStreamOptions{IncludeUsage: true}

	body, err := json.Marshal(payload)
	if err != nil {
		return OpenAIResponse{}, fmt.Errorf("marshal openai stream request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return OpenAIResponse{}, fmt.Errorf("create openai stream request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", head)

	resp, err := t.Client.Do(httpReq)
	if err != nil {
		return OpenAIResponse{}, fmt.Errorf("openai stream request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return OpenAIResponse{}, fmt.Errorf("openai status %d: %s", resp.StatusCode, truncateError(respBody))
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 1024), 1024*1024)

	var fullText strings.Builder
	toolParts := map[int]*toolCallPart{}
	usage := Usage{WindowTokens: t.ContextWindowTokens}

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}

		var chunk chatCompletionStreamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return OpenAIResponse{}, fmt.Errorf("decode stream chunk: %w", err)
		}
		if chunk.Usage != nil {
			usage.InputTokens = chunk.Usage.PromptTokens
			usage.OutputTokens = chunk.Usage.CompletionTokens
			usage.WindowTokens = t.ContextWindowTokens
		}

		for _, choice := range chunk.Choices {
			if choice.Delta.Content != "" {
				fullText.WriteString(choice.Delta.Content)
				if onDelta != nil {
					onDelta(choice.Delta.Content)
				}
			}
			for _, tc := range choice.Delta.ToolCalls {
				part, ok := toolParts[tc.Index]
				if !ok {
					part = &toolCallPart{}
					toolParts[tc.Index] = part
				}
				if tc.ID != "" {
					part.ID = tc.ID
				}
				if tc.Function.Name != "" {
					part.Name = tc.Function.Name
				}
				if tc.Function.Arguments != "" {
					part.Arguments.WriteString(tc.Function.Arguments)
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return OpenAIResponse{}, fmt.Errorf("read stream chunks: %w", err)
	}

	toolCalls := make([]ToolCall, 0, len(toolParts))
	indexes := make([]int, 0, len(toolParts))
	for idx := range toolParts {
		indexes = append(indexes, idx)
	}
	sort.Ints(indexes)
	for _, idx := range indexes {
		part := toolParts[idx]
		toolCalls = append(toolCalls, ToolCall{
			ID:    part.ID,
			Name:  part.Name,
			Input: normalizeToolInput(part.Arguments.String()),
		})
	}

	return OpenAIResponse{
		AssistantText: fullText.String(),
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
		if len(m.ToolCalls) > 0 {
			item.ToolCalls = make([]chatToolCall, 0, len(m.ToolCalls))
			for _, tc := range m.ToolCalls {
				item.ToolCalls = append(item.ToolCalls, chatToolCall{
					ID:   tc.ID,
					Type: "function",
					Function: chatFunctionCall{
						Name:      tc.Name,
						Arguments: string(tc.Input),
					},
				})
			}
		}
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
	Model             string             `json:"model"`
	Messages          []chatMessage      `json:"messages"`
	Tools             []chatTool         `json:"tools,omitempty"`
	Temperature       *float64           `json:"temperature,omitempty"`
	MaxTokens         *int               `json:"max_tokens,omitempty"`
	ParallelToolCalls bool               `json:"parallel_tool_calls"`
	Stream            bool               `json:"stream,omitempty"`
	StreamOptions     *chatStreamOptions `json:"stream_options,omitempty"`
}

type chatStreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type chatMessage struct {
	Role       string         `json:"role"`
	Content    string         `json:"content,omitempty"`
	Name       string         `json:"name,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
	ToolCalls  []chatToolCall `json:"tool_calls,omitempty"`
}

type chatToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type,omitempty"`
	Function chatFunctionCall `json:"function"`
}

type chatFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
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

type chatCompletionStreamChunk struct {
	Choices []struct {
		Delta struct {
			Content   string `json:"content"`
			ToolCalls []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage,omitempty"`
}

type toolCallPart struct {
	ID        string
	Name      string
	Arguments strings.Builder
}

func convertToolCalls(in []struct {
	ID       string `json:"id"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}) []ToolCall {
	toolCalls := make([]ToolCall, 0, len(in))
	for _, c := range in {
		toolCalls = append(toolCalls, ToolCall{
			ID:    c.ID,
			Name:  c.Function.Name,
			Input: normalizeToolInput(c.Function.Arguments),
		})
	}
	return toolCalls
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

func truncateError(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) <= 240 {
		return s
	}
	return s[:240] + "..."
}

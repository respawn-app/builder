package llm

import (
	"builder/internal/session"
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

var (
	ErrInvalidRequest   = errors.New("invalid llm request")
	ErrMissingTransport = errors.New("openai transport is required")
)

type Role string

const (
	RoleSystem    Role = "system"
	RoleDeveloper Role = "developer"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

type Message struct {
	Role       Role       `json:"role"`
	Content    string     `json:"content,omitempty"`
	Name       string     `json:"name,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
}

type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Schema      json.RawMessage `json:"schema"`
}

type ToolCall struct {
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

type ToolResult struct {
	CallID  string          `json:"call_id"`
	Name    string          `json:"name"`
	Output  json.RawMessage `json:"output"`
	IsError bool            `json:"is_error"`
}

type Request struct {
	Model        string    `json:"model"`
	Temperature  float64   `json:"temperature"`
	MaxTokens    int       `json:"max_tokens"`
	SystemPrompt string    `json:"system_prompt"`
	SessionID    string    `json:"session_id,omitempty"`
	Messages     []Message `json:"messages"`
	Tools        []Tool    `json:"tools,omitempty"`
}

func (r Request) Validate() error {
	if r.Model == "" {
		return fmt.Errorf("%w: model is required", ErrInvalidRequest)
	}
	if r.MaxTokens < 0 {
		return fmt.Errorf("%w: max_tokens must be >= 0", ErrInvalidRequest)
	}
	for i := range r.Messages {
		if r.Messages[i].Role == "" {
			return fmt.Errorf("%w: message role is required at index %d", ErrInvalidRequest, i)
		}
	}
	for i := range r.Tools {
		if r.Tools[i].Name == "" {
			return fmt.Errorf("%w: tool name is required at index %d", ErrInvalidRequest, i)
		}
		if len(r.Tools[i].Schema) > 0 && !json.Valid(r.Tools[i].Schema) {
			return fmt.Errorf("%w: tool schema is invalid json at index %d", ErrInvalidRequest, i)
		}
	}
	return nil
}

func RequestFromLockedContract(locked session.LockedContract, systemPrompt string, messages []Message, tools []Tool) (Request, error) {
	if locked.Model == "" {
		return Request{}, fmt.Errorf("%w: locked model is required", ErrInvalidRequest)
	}
	if locked.MaxOutputToken < 0 {
		return Request{}, fmt.Errorf("%w: locked max output token must be >= 0", ErrInvalidRequest)
	}

	req := Request{
		Model:        locked.Model,
		Temperature:  locked.Temperature,
		MaxTokens:    locked.MaxOutputToken,
		SystemPrompt: systemPrompt,
		SessionID:    "",
		Messages:     append([]Message(nil), messages...),
		Tools:        append([]Tool(nil), tools...),
	}
	if err := req.Validate(); err != nil {
		return Request{}, err
	}
	return req, nil
}

type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	WindowTokens int `json:"window_tokens"`
}

func (u Usage) Percent() int {
	if u.WindowTokens <= 0 {
		return 0
	}
	total := u.InputTokens + u.OutputTokens
	if total <= 0 {
		return 0
	}
	pct := (total * 100) / u.WindowTokens
	if pct < 0 {
		return 0
	}
	if pct > 100 {
		return 100
	}
	return pct
}

type Response struct {
	Assistant Message    `json:"assistant"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
	Usage     Usage      `json:"usage"`
}

type Client interface {
	Generate(ctx context.Context, request Request) (Response, error)
}

type StreamClient interface {
	GenerateStream(ctx context.Context, request Request, onDelta func(text string)) (Response, error)
}

func AppendToolResultMessages(messages []Message, results []ToolResult) []Message {
	out := make([]Message, 0, len(messages)+len(results))
	out = append(out, messages...)
	for _, result := range results {
		out = append(out, Message{
			Role:       RoleTool,
			Name:       result.Name,
			ToolCallID: result.CallID,
			Content:    string(result.Output),
		})
	}
	return out
}

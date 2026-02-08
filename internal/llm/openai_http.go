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
	"strconv"
	"strings"
	"time"
)

const (
	defaultOpenAIBaseURL   = "https://api.openai.com/v1"
	codexResponsesEndpoint = "https://chatgpt.com/backend-api/codex/responses"
	defaultOriginator      = "builder"
	defaultUserAgent       = "builder/dev"
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
	body, err := json.Marshal(payload)
	if err != nil {
		return OpenAIResponse{}, fmt.Errorf("marshal openai responses request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, t.requestURL(mode), bytes.NewReader(body))
	if err != nil {
		return OpenAIResponse{}, fmt.Errorf("create openai responses request: %w", err)
	}
	t.applyHeaders(httpReq, authHeader, mode, request.SessionID)

	resp, err := t.Client.Do(httpReq)
	if err != nil {
		return OpenAIResponse{}, fmt.Errorf("openai responses request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return OpenAIResponse{}, fmt.Errorf("read openai responses response: %w", err)
	}
	if resp.StatusCode >= 300 {
		return OpenAIResponse{}, fmt.Errorf("openai status %d: %s", resp.StatusCode, truncateError(respBody))
	}

	var decoded responsesEnvelope
	if err := json.Unmarshal(respBody, &decoded); err != nil {
		return OpenAIResponse{}, fmt.Errorf("decode openai responses payload: %w", err)
	}

	assistantText, toolCalls := parseOutputItems(decoded.Output)
	return OpenAIResponse{
		AssistantText: assistantText,
		ToolCalls:     toolCalls,
		Usage: Usage{
			InputTokens:  decoded.Usage.InputTokens,
			OutputTokens: decoded.Usage.OutputTokens,
			WindowTokens: t.ContextWindowTokens,
		},
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
	payload.Stream = true

	body, err := json.Marshal(payload)
	if err != nil {
		return OpenAIResponse{}, fmt.Errorf("marshal openai stream request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, t.requestURL(mode), bytes.NewReader(body))
	if err != nil {
		return OpenAIResponse{}, fmt.Errorf("create openai stream request: %w", err)
	}
	t.applyHeaders(httpReq, authHeader, mode, request.SessionID)

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

	var assistantText strings.Builder
	acc := newToolCallAccumulator()
	usage := Usage{WindowTokens: t.ContextWindowTokens}
	var completed *responsesEnvelope

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

		var base responsesEventType
		if err := json.Unmarshal([]byte(data), &base); err != nil {
			return OpenAIResponse{}, fmt.Errorf("decode responses stream event: %w", err)
		}

		switch base.Type {
		case "response.output_text.delta":
			var evt responsesOutputTextDeltaEvent
			if err := json.Unmarshal([]byte(data), &evt); err != nil {
				return OpenAIResponse{}, fmt.Errorf("decode output text delta: %w", err)
			}
			if evt.Delta != "" {
				assistantText.WriteString(evt.Delta)
				if onDelta != nil {
					onDelta(evt.Delta)
				}
			}
		case "response.output_item.added", "response.output_item.done":
			var evt responsesOutputItemEvent
			if err := json.Unmarshal([]byte(data), &evt); err != nil {
				return OpenAIResponse{}, fmt.Errorf("decode output item event: %w", err)
			}
			acc.UpsertFromOutput(evt.Item)
		case "response.function_call_arguments.delta":
			var evt responsesFunctionCallArgsDeltaEvent
			if err := json.Unmarshal([]byte(data), &evt); err != nil {
				return OpenAIResponse{}, fmt.Errorf("decode function call args delta: %w", err)
			}
			acc.AppendArguments(evt.ItemID, evt.Delta)
		case "response.function_call_arguments.done":
			var evt responsesFunctionCallArgsDoneEvent
			if err := json.Unmarshal([]byte(data), &evt); err != nil {
				return OpenAIResponse{}, fmt.Errorf("decode function call args done: %w", err)
			}
			acc.SetArguments(evt.ItemID, evt.Arguments)
		case "response.completed":
			var evt responsesCompletedEvent
			if err := json.Unmarshal([]byte(data), &evt); err != nil {
				return OpenAIResponse{}, fmt.Errorf("decode completed event: %w", err)
			}
			completed = &evt.Response
		}
	}
	if err := scanner.Err(); err != nil {
		return OpenAIResponse{}, fmt.Errorf("read responses stream events: %w", err)
	}

	finalText := assistantText.String()
	finalCalls := acc.ToToolCalls()

	if completed != nil {
		if completed.Usage.InputTokens > 0 || completed.Usage.OutputTokens > 0 {
			usage.InputTokens = completed.Usage.InputTokens
			usage.OutputTokens = completed.Usage.OutputTokens
		}
		parsedText, parsedCalls := parseOutputItems(completed.Output)
		if finalText == "" {
			finalText = parsedText
		}
		acc.Merge(parsedCalls)
		finalCalls = acc.ToToolCalls()
	}

	return OpenAIResponse{
		AssistantText: finalText,
		ToolCalls:     finalCalls,
		Usage:         usage,
	}, nil
}

func (t *HTTPTransport) resolveAuth(ctx context.Context) (string, openAIAuthMode, error) {
	authHeader, err := t.Auth.AuthorizationHeader(ctx)
	if err != nil {
		return "", openAIAuthMode{}, err
	}

	mode := openAIAuthMode{}
	if provider, ok := t.Auth.(OpenAIAuthMetadataProvider); ok {
		method, accountID, err := provider.OpenAIAuthMetadata(ctx)
		if err != nil {
			return "", openAIAuthMode{}, err
		}
		mode.IsOAuth = method == "oauth"
		mode.AccountID = strings.TrimSpace(accountID)
	}
	return authHeader, mode, nil
}

func (t *HTTPTransport) requestURL(mode openAIAuthMode) string {
	if mode.IsOAuth {
		return codexResponsesEndpoint
	}
	base := strings.TrimSuffix(t.BaseURL, "/")
	if base == "" {
		base = defaultOpenAIBaseURL
	}
	return base + "/responses"
}

func (t *HTTPTransport) applyHeaders(req *http.Request, authHeader string, mode openAIAuthMode, sessionID string) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", authHeader)
	if mode.IsOAuth {
		req.Header.Set("originator", defaultOriginator)
		req.Header.Set("User-Agent", defaultUserAgent)
		if strings.TrimSpace(sessionID) != "" {
			req.Header.Set("session_id", sessionID)
		}
		if mode.AccountID != "" {
			req.Header.Set("ChatGPT-Account-Id", mode.AccountID)
		}
	}
}

func (t *HTTPTransport) buildPayload(request OpenAIRequest, mode openAIAuthMode) (responsesRequest, error) {
	input := buildResponsesInput(request.Messages)

	tools := make([]responsesFunctionTool, 0, len(request.Tools))
	for _, tool := range request.Tools {
		if len(tool.Schema) > 0 && !json.Valid(tool.Schema) {
			return responsesRequest{}, fmt.Errorf("invalid tool schema for %s", tool.Name)
		}
		params := json.RawMessage(`{"type":"object","properties":{}}`)
		if len(tool.Schema) > 0 {
			params = append(json.RawMessage(nil), tool.Schema...)
		}
		tools = append(tools, responsesFunctionTool{
			Type:        "function",
			Name:        tool.Name,
			Description: tool.Description,
			Parameters:  params,
		})
	}

	store := false
	out := responsesRequest{
		Model:        request.Model,
		Input:        input,
		Instructions: strings.TrimSpace(request.SystemPrompt),
		Tools:        tools,
		Store:        &store,
	}
	if request.MaxTokens > 0 && !mode.IsOAuth {
		out.MaxOutputTokens = &request.MaxTokens
	}
	if request.Temperature != 0 && !mode.IsOAuth {
		temp := request.Temperature
		out.Temperature = &temp
	}
	if len(tools) > 0 {
		parallel := true
		out.ParallelToolCalls = &parallel
	}
	return out, nil
}

func buildResponsesInput(messages []Message) []responsesInputItem {
	items := make([]responsesInputItem, 0, len(messages))
	for _, msg := range messages {
		switch msg.Role {
		case RoleTool:
			if strings.TrimSpace(msg.ToolCallID) == "" {
				if strings.TrimSpace(msg.Content) != "" {
					items = append(items, messageInput(string(msg.Role), msg.Content))
				}
				continue
			}
			items = append(items, responsesInputItem{
				Type:   "function_call_output",
				CallID: msg.ToolCallID,
				Output: msg.Content,
			})
		case RoleAssistant:
			if strings.TrimSpace(msg.Content) != "" {
				items = append(items, messageInput(string(msg.Role), msg.Content))
			}
			for _, tc := range msg.ToolCalls {
				callID := strings.TrimSpace(tc.ID)
				if callID == "" {
					continue
				}
				items = append(items, responsesInputItem{
					Type:      "function_call",
					CallID:    callID,
					Name:      tc.Name,
					Arguments: normalizeToolArguments(string(tc.Input)),
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

func messageInput(role, text string) responsesInputItem {
	return responsesInputItem{
		Type: "message",
		Role: role,
		Content: []responsesInputContent{
			{
				Type: "input_text",
				Text: text,
			},
		},
	}
}

func parseOutputItems(items []responsesOutputItem) (string, []ToolCall) {
	textParts := make([]string, 0, len(items))
	toolCalls := make([]ToolCall, 0, len(items))
	for _, item := range items {
		switch item.Type {
		case "message":
			if item.Role != "" && item.Role != string(RoleAssistant) {
				continue
			}
			for _, part := range item.Content {
				if part.Type == "output_text" || part.Type == "text" {
					textParts = append(textParts, part.Text)
				}
			}
			if item.Text != "" {
				textParts = append(textParts, item.Text)
			}
		case "output_text":
			if item.Text != "" {
				textParts = append(textParts, item.Text)
			}
		case "function_call":
			callID := firstNonEmpty(strings.TrimSpace(item.CallID), strings.TrimSpace(item.ID))
			if callID == "" && strings.TrimSpace(item.Name) == "" {
				continue
			}
			toolCalls = append(toolCalls, ToolCall{
				ID:    callID,
				Name:  item.Name,
				Input: normalizeToolInput(argumentsFromRaw(item.Arguments)),
			})
		}
	}
	return strings.Join(textParts, ""), toolCalls
}

type responsesRequest struct {
	Model             string                  `json:"model"`
	Input             []responsesInputItem    `json:"input,omitempty"`
	Instructions      string                  `json:"instructions,omitempty"`
	Tools             []responsesFunctionTool `json:"tools,omitempty"`
	Temperature       *float64                `json:"temperature,omitempty"`
	MaxOutputTokens   *int                    `json:"max_output_tokens,omitempty"`
	ParallelToolCalls *bool                   `json:"parallel_tool_calls,omitempty"`
	Store             *bool                   `json:"store,omitempty"`
	Stream            bool                    `json:"stream,omitempty"`
}

type responsesFunctionTool struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
}

type responsesInputItem struct {
	Type      string                  `json:"type,omitempty"`
	Role      string                  `json:"role,omitempty"`
	Content   []responsesInputContent `json:"content,omitempty"`
	CallID    string                  `json:"call_id,omitempty"`
	Name      string                  `json:"name,omitempty"`
	Arguments string                  `json:"arguments,omitempty"`
	Output    string                  `json:"output,omitempty"`
}

type responsesInputContent struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type responsesEnvelope struct {
	Output []responsesOutputItem `json:"output"`
	Usage  responsesUsage        `json:"usage"`
}

type responsesUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type responsesOutputItem struct {
	ID        string                   `json:"id,omitempty"`
	Type      string                   `json:"type"`
	Role      string                   `json:"role,omitempty"`
	Name      string                   `json:"name,omitempty"`
	CallID    string                   `json:"call_id,omitempty"`
	Arguments json.RawMessage          `json:"arguments,omitempty"`
	Content   []responsesOutputContent `json:"content,omitempty"`
	Text      string                   `json:"text,omitempty"`
}

type responsesOutputContent struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type responsesEventType struct {
	Type string `json:"type"`
}

type responsesOutputTextDeltaEvent struct {
	Delta string `json:"delta"`
}

type responsesOutputItemEvent struct {
	Item responsesOutputItem `json:"item"`
}

type responsesFunctionCallArgsDeltaEvent struct {
	ItemID string `json:"item_id"`
	Delta  string `json:"delta"`
}

type responsesFunctionCallArgsDoneEvent struct {
	ItemID    string `json:"item_id"`
	Arguments string `json:"arguments"`
}

type responsesCompletedEvent struct {
	Response responsesEnvelope `json:"response"`
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

func (a *toolCallAccumulator) UpsertFromOutput(item responsesOutputItem) {
	if item.Type != "function_call" {
		return
	}
	key := firstNonEmpty(strings.TrimSpace(item.CallID), strings.TrimSpace(item.ID))
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
	if args := strings.TrimSpace(argumentsFromRaw(item.Arguments)); args != "" {
		state.Args.Reset()
		state.Args.WriteString(args)
	}
}

func (a *toolCallAccumulator) AppendArguments(itemID, delta string) {
	key := firstNonEmpty(strings.TrimSpace(a.itemToKey[itemID]), strings.TrimSpace(itemID))
	state := a.ensure(key)
	if state == nil || strings.TrimSpace(delta) == "" {
		return
	}
	state.Args.WriteString(delta)
}

func (a *toolCallAccumulator) SetArguments(itemID, arguments string) {
	key := firstNonEmpty(strings.TrimSpace(a.itemToKey[itemID]), strings.TrimSpace(itemID))
	state := a.ensure(key)
	if state == nil {
		return
	}
	state.Args.Reset()
	state.Args.WriteString(arguments)
}

func (a *toolCallAccumulator) Merge(calls []ToolCall) {
	for _, call := range calls {
		key := firstNonEmpty(strings.TrimSpace(call.ID), strings.TrimSpace(call.Name))
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
		callID := firstNonEmpty(strings.TrimSpace(state.CallID), key)
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

func argumentsFromRaw(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	if len(raw) > 0 && raw[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err == nil {
			return s
		}
	}
	return string(raw)
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

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func truncateError(b []byte) string {
	s := strings.TrimSpace(string(b))
	if s == "" {
		return "<empty error body>"
	}
	return s
}

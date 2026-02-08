package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

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

	assistantText, toolCalls, reasoning, reasoningItems := parseOutputItems(decoded.Output)
	return OpenAIResponse{
		AssistantText:  assistantText,
		ToolCalls:      toolCalls,
		Reasoning:      reasoning,
		ReasoningItems: reasoningItems,
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

	if completed != nil {
		if completed.Usage.InputTokens > 0 || completed.Usage.OutputTokens > 0 {
			usage = usageFromSDK(completed.Usage, t.ContextWindowTokens)
		}
		parsedText, parsedCalls, parsedReasoning, parsedReasoningItems := parseOutputItems(completed.Output)
		if finalText == "" {
			finalText = parsedText
		}
		acc.Merge(parsedCalls)
		finalCalls = acc.ToToolCalls()
		finalReasoning = mergeReasoningEntries(parsedReasoning, finalReasoning)
		finalReasoningItems = mergeReasoningItems(parsedReasoningItems, finalReasoningItems)
	}

	return OpenAIResponse{
		AssistantText:  finalText,
		ToolCalls:      finalCalls,
		Reasoning:      finalReasoning,
		ReasoningItems: finalReasoningItems,
		Usage:          usage,
	}, nil
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

func (t *HTTPTransport) buildPayload(request OpenAIRequest, mode openAIAuthMode) (responses.ResponseNewParams, error) {
	input := buildResponsesInput(request.Messages)

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

	out := responses.ResponseNewParams{
		Model: request.Model,
		Store: openai.Bool(false),
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

	return out, nil
}

func buildResponsesInput(messages []Message) []responses.ResponseInputItemUnionParam {
	var items []responses.ResponseInputItemUnionParam
	for _, msg := range messages {
		switch msg.Role {
		case RoleTool:
			if strings.TrimSpace(msg.ToolCallID) == "" {
				continue
			}
			items = append(items, responses.ResponseInputItemParamOfFunctionCallOutput(msg.ToolCallID, msg.Content))
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

func parseOutputItems(items []responses.ResponseOutputItemUnion) (string, []ToolCall, []ReasoningEntry, []ReasoningItem) {
	textParts := make([]string, 0, len(items))
	toolCalls := make([]ToolCall, 0, len(items))
	reasoning := make([]ReasoningEntry, 0, len(items))
	reasoningItems := make([]ReasoningItem, 0, len(items))
	for _, item := range items {
		switch item.Type {
		case "message":
			if string(item.Role) != "" && string(item.Role) != string(RoleAssistant) {
				continue
			}
			for _, part := range item.Content {
				if part.Type == "output_text" || part.Type == "text" {
					textParts = append(textParts, part.Text)
				}
			}
		case "function_call":
			callID := firstNonEmpty(strings.TrimSpace(item.CallID), strings.TrimSpace(item.ID))
			if callID == "" && strings.TrimSpace(item.Name) == "" {
				continue
			}
			toolCalls = append(toolCalls, ToolCall{
				ID:    callID,
				Name:  item.Name,
				Input: normalizeToolInput(item.Arguments),
			})
		case "reasoning":
			reasoningItem := item.AsReasoning()
			for _, summary := range reasoningItem.Summary {
				reasoning = appendReasoningEntry(reasoning, reasoningRoleSummary, summary.Text)
			}
			if id := strings.TrimSpace(reasoningItem.ID); id != "" {
				if encrypted := strings.TrimSpace(reasoningItem.EncryptedContent); encrypted != "" {
					reasoningItems = append(reasoningItems, ReasoningItem{
						ID:               id,
						EncryptedContent: encrypted,
					})
				}
			}
		}
	}
	return strings.Join(textParts, ""), toolCalls, reasoning, reasoningItems
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
	if args := strings.TrimSpace(item.Arguments); args != "" {
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

func shouldApplyReasoningEffort(model, effort string) bool {
	effort = strings.TrimSpace(effort)
	if effort == "" {
		return false
	}
	model = strings.ToLower(strings.TrimSpace(model))
	if model == "" {
		return false
	}
	return strings.HasPrefix(model, "gpt-") || strings.HasPrefix(model, "o")
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
	}
	if mode.IsOAuth {
		opts = append(opts,
			option.WithHeader("originator", defaultOriginator),
			option.WithHeader("User-Agent", defaultUserAgent),
		)
		if strings.TrimSpace(sessionID) != "" {
			opts = append(opts, option.WithHeader("session_id", sessionID))
		}
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
	return Usage{
		InputTokens:  int(usage.InputTokens),
		OutputTokens: int(usage.OutputTokens),
		WindowTokens: window,
	}
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

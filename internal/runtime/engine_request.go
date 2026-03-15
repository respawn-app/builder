package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"builder/internal/llm"
	"builder/internal/session"
	"builder/internal/tools"
	"builder/prompts"
	xansi "github.com/charmbracelet/x/ansi"
)

func (e *Engine) buildRequest(ctx context.Context, _ string, allowTools bool) (llm.Request, error) {
	locked, err := e.ensureLocked()
	if err != nil {
		return llm.Request{}, err
	}

	var requestTools []llm.Tool
	if allowTools {
		requestTools = e.requestTools()
	} else {
		requestTools = []llm.Tool{}
	}

	msgs := e.snapshotMessages()
	msgs = sanitizeMessagesForLLM(msgs)
	items := e.snapshotItems()
	items = sanitizeItemsForLLM(items)

	req, err := llm.RequestFromLockedContractWithItems(locked, e.systemPrompt(locked), msgs, items, requestTools)
	if err != nil {
		return llm.Request{}, err
	}
	req.ReasoningEffort = e.ThinkingLevel()
	req.FastMode = e.FastModeEnabled()
	if allowTools {
		nativeWebSearch, nativeErr := e.enableNativeWebSearch(ctx)
		if nativeErr != nil {
			return llm.Request{}, nativeErr
		}
		req.EnableNativeWebSearch = nativeWebSearch
	}
	req.SessionID = e.store.Meta().SessionID
	return req, nil
}

func (e *Engine) enableNativeWebSearch(ctx context.Context) (bool, error) {
	if !hasEnabledTool(e.cfg.EnabledTools, tools.ToolWebSearch) {
		return false, nil
	}
	if !strings.EqualFold(strings.TrimSpace(e.cfg.WebSearchMode), "native") {
		return false, nil
	}
	caps, err := e.providerCapabilities(ctx)
	if err != nil {
		return false, fmt.Errorf("resolve provider capabilities for native web search: %w", err)
	}
	return caps.SupportsNativeWebSearch, nil
}

func hasEnabledTool(ids []tools.ID, toolID tools.ID) bool {
	for _, id := range ids {
		if id == toolID {
			return true
		}
	}
	return false
}

func (e *Engine) systemPrompt(locked session.LockedContract) string {
	includeToolPreambles := true
	if locked.ToolPreambles != nil {
		includeToolPreambles = *locked.ToolPreambles
	}
	return prompts.MainSystemPrompt(includeToolPreambles)
}

func summarizeOutputItemTypes(items []llm.ResponseItem) []string {
	if len(items) == 0 {
		return nil
	}
	counts := make(map[string]int, len(items))
	order := make([]string, 0, len(items))
	for _, item := range items {
		t := strings.TrimSpace(string(item.Type))
		if t == "" {
			t = "unknown"
		}
		if _, ok := counts[t]; !ok {
			order = append(order, t)
		}
		counts[t]++
	}
	out := make([]string, 0, len(order))
	for _, t := range order {
		out = append(out, fmt.Sprintf("%s:%d", t, counts[t]))
	}
	return out
}

type hostedToolExecution struct {
	Call   llm.ToolCall
	Result tools.Result
}

func hostedToolExecutionsFromOutputItems(items []llm.ResponseItem) []hostedToolExecution {
	out := make([]hostedToolExecution, 0, len(items))
	for _, item := range items {
		execution, ok := hostedWebSearchExecution(item)
		if !ok {
			continue
		}
		out = append(out, execution)
	}
	return out
}

func hostedWebSearchExecution(item llm.ResponseItem) (hostedToolExecution, bool) {
	raw := item.Raw
	if len(raw) == 0 || !json.Valid(raw) {
		return hostedToolExecution{}, false
	}
	var payload struct {
		Type   string `json:"type"`
		ID     string `json:"id"`
		Status string `json:"status"`
		Action struct {
			Type    string `json:"type"`
			Query   string `json:"query"`
			URL     string `json:"url"`
			Pattern string `json:"pattern"`
		} `json:"action"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return hostedToolExecution{}, false
	}
	if strings.TrimSpace(payload.Type) != "web_search_call" {
		return hostedToolExecution{}, false
	}
	callID := strings.TrimSpace(payload.ID)
	if callID == "" {
		callID = strings.TrimSpace(item.ID)
	}
	if callID == "" {
		callID = strings.TrimSpace(item.CallID)
	}
	if callID == "" {
		return hostedToolExecution{}, false
	}
	input := map[string]string{}
	actionType := strings.TrimSpace(payload.Action.Type)
	if actionType != "" {
		input["action"] = actionType
	}
	query := strings.TrimSpace(payload.Action.Query)
	if url := strings.TrimSpace(payload.Action.URL); url != "" {
		if query == "" {
			query = url
		}
		input["url"] = url
	}
	if pattern := strings.TrimSpace(payload.Action.Pattern); pattern != "" {
		if query == "" {
			query = pattern
		}
		input["pattern"] = pattern
	}
	if query == "" {
		if actionType != "" {
			query = actionType
		} else {
			query = "web search"
		}
	}
	input["query"] = query
	inputRaw, err := json.Marshal(input)
	if err != nil {
		return hostedToolExecution{}, false
	}
	output := append(json.RawMessage(nil), raw...)
	if !json.Valid(output) {
		output = mustJSON(map[string]any{"raw": string(raw)})
	}
	isError := strings.EqualFold(strings.TrimSpace(payload.Status), "failed")
	return hostedToolExecution{
		Call: llm.ToolCall{
			ID:    callID,
			Name:  string(tools.ToolWebSearch),
			Input: inputRaw,
		},
		Result: tools.Result{
			CallID:  callID,
			Name:    tools.ToolWebSearch,
			Output:  output,
			IsError: isError,
		},
	}, true
}

func (e *Engine) requestTools() []llm.Tool {
	defs := e.registry.Definitions()
	if len(defs) == 0 {
		return nil
	}
	out := make([]llm.Tool, 0, len(defs))
	locked := e.store.Meta().Locked
	for _, d := range defs {
		if d.ID == tools.ToolViewImage && !llm.LockedContractSupportsVisionInputs(locked, e.cfg.Model) {
			continue
		}
		out = append(out, llm.Tool{Name: string(d.ID), Description: d.Description, Schema: d.Schema})
	}
	return out
}

func sanitizeMessagesForLLM(messages []llm.Message) []llm.Message {
	if len(messages) == 0 {
		return messages
	}
	cleaned := make([]llm.Message, len(messages))
	for i, msg := range messages {
		cleaned[i] = msg
		content := xansi.Strip(msg.Content)
		if msg.Role == llm.RoleTool {
			content = normalizeToolMessageForLLM(content)
		}
		cleaned[i].Content = content
	}
	return cleaned
}

func sanitizeItemsForLLM(items []llm.ResponseItem) []llm.ResponseItem {
	if len(items) == 0 {
		return items
	}
	cleaned := llm.CloneResponseItems(items)
	for i := range cleaned {
		if cleaned[i].Type == llm.ResponseItemTypeMessage {
			cleaned[i].Content = xansi.Strip(cleaned[i].Content)
		}
		if cleaned[i].Type == llm.ResponseItemTypeFunctionCallOutput && len(cleaned[i].Output) > 0 {
			normalized := normalizeToolMessageForLLM(string(cleaned[i].Output))
			if json.Valid([]byte(normalized)) {
				cleaned[i].Output = json.RawMessage(normalized)
			} else {
				quoted, _ := json.Marshal(normalized)
				cleaned[i].Output = quoted
			}
		}
	}
	return cleaned
}

func normalizeToolMessageForLLM(content string) string {
	var payload any
	if err := json.Unmarshal([]byte(content), &payload); err != nil {
		return content
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(payload); err != nil {
		return content
	}
	return strings.TrimSuffix(buf.String(), "\n")
}

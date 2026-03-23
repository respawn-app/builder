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
	return e.buildRequestWithExtraMessages(ctx, nil, allowTools)
}

func (e *Engine) buildRequestWithExtraMessages(ctx context.Context, extra []llm.Message, allowTools bool) (llm.Request, error) {
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
	if len(extra) > 0 {
		msgs = append(msgs, extra...)
	}
	msgs = sanitizeMessagesForLLM(msgs)
	items := e.snapshotItems()
	if len(extra) > 0 {
		items = append(items, llm.ItemsFromMessages(extra)...)
	}
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
	if !tools.NeedsNativeWebSearch(e.cfg.EnabledTools, e.cfg.WebSearchMode) {
		return false, nil
	}
	caps, err := e.providerCapabilities(ctx)
	if err != nil {
		return false, fmt.Errorf("resolve provider capabilities for native web search: %w", err)
	}
	return caps.SupportsNativeWebSearch, nil
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

func hostedToolExecutionsFromOutputItems(items []llm.ResponseItem, defs []tools.Definition) []hostedToolExecution {
	hostedOutputs := make([]tools.HostedToolOutput, 0, len(items))
	for _, item := range items {
		hostedOutputs = append(hostedOutputs, tools.HostedToolOutput{
			ID:     strings.TrimSpace(item.ID),
			CallID: strings.TrimSpace(item.CallID),
			Raw:    append(json.RawMessage(nil), item.Raw...),
		})
	}
	decoded := tools.HostedExecutionsFromOutputs(hostedOutputs, defs)
	out := make([]hostedToolExecution, 0, len(decoded))
	for _, execution := range decoded {
		out = append(out, hostedToolExecution{
			Call: llm.ToolCall{
				ID:    execution.Call.ID,
				Name:  string(execution.Call.Name),
				Input: execution.Call.Input,
			},
			Result: execution.Result,
		})
	}
	return out
}

func (e *Engine) requestTools() []llm.Tool {
	supportsVision := llm.LockedContractSupportsVisionInputs(e.store.Meta().Locked, e.cfg.Model)
	defs := tools.RequestExposedDefinitionsForSession(e.cfg.EnabledTools, e.registry.Definitions(), supportsVision)
	if len(defs) == 0 {
		return nil
	}
	out := make([]llm.Tool, 0, len(defs))
	for _, d := range defs {
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

package multitooluseparallel

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"builder/internal/tools"
)

const recipientPrefix = "functions."

type Tool struct {
	getRegistry func() *tools.Registry
}

func New(getRegistry func() *tools.Registry) *Tool {
	return &Tool{getRegistry: getRegistry}
}

func (t *Tool) Name() tools.ID {
	return tools.ToolMultiToolUseParallel
}

type input struct {
	ToolUses []toolUse `json:"tool_uses"`
}

type toolUse struct {
	RecipientName string          `json:"recipient_name"`
	Parameters    json.RawMessage `json:"parameters"`
}

type subcallResult struct {
	RecipientName string `json:"recipient_name"`
	CallID        string `json:"call_id"`
	Name          string `json:"name"`
	IsError       bool   `json:"is_error"`
	Output        any    `json:"output,omitempty"`
}

type output struct {
	Results []subcallResult `json:"results"`
}

func (t *Tool) Call(ctx context.Context, c tools.Call) (tools.Result, error) {
	if t == nil || t.getRegistry == nil {
		return tools.ErrorResult(c, "tool registry is unavailable"), nil
	}
	registry := t.getRegistry()
	if registry == nil {
		return tools.ErrorResult(c, "tool registry is unavailable"), nil
	}

	var in input
	if err := json.Unmarshal(c.Input, &in); err != nil {
		return tools.ErrorResult(c, fmt.Sprintf("invalid input: %v", err)), nil
	}
	if len(in.ToolUses) == 0 {
		return tools.ErrorResult(c, "tool_uses must contain at least one entry"), nil
	}

	resolved := make([]struct {
		name   string
		toolID tools.ID
		input  json.RawMessage
	}, len(in.ToolUses))

	for i, use := range in.ToolUses {
		recipient := strings.TrimSpace(use.RecipientName)
		if !strings.HasPrefix(recipient, recipientPrefix) {
			return tools.ErrorResult(c, fmt.Sprintf("tool_uses[%d].recipient_name must use %q prefix", i, recipientPrefix)), nil
		}
		name := strings.TrimSpace(strings.TrimPrefix(recipient, recipientPrefix))
		if name == "" {
			return tools.ErrorResult(c, fmt.Sprintf("tool_uses[%d].recipient_name is required", i)), nil
		}
		toolID, ok := tools.ParseID(name)
		if !ok {
			return tools.ErrorResult(c, fmt.Sprintf("tool_uses[%d].recipient_name references unknown tool %q", i, recipient)), nil
		}
		if toolID == tools.ToolMultiToolUseParallel {
			return tools.ErrorResult(c, "recursive calls to multi_tool_use_parallel are not allowed"), nil
		}

		params := use.Parameters
		if len(params) == 0 {
			params = json.RawMessage(`{}`)
		}
		var obj map[string]any
		if err := json.Unmarshal(params, &obj); err != nil {
			return tools.ErrorResult(c, fmt.Sprintf("tool_uses[%d].parameters must be a JSON object: %v", i, err)), nil
		}
		resolved[i] = struct {
			name   string
			toolID tools.ID
			input  json.RawMessage
		}{
			name:   recipient,
			toolID: toolID,
			input:  params,
		}
	}

	results := make([]subcallResult, len(resolved))
	wg := sync.WaitGroup{}
	for i, call := range resolved {
		i := i
		call := call
		wg.Add(1)
		go func() {
			defer wg.Done()
			subCallID := fmt.Sprintf("%s.%d", c.ID, i+1)
			sub := subcallResult{
				RecipientName: call.name,
				CallID:        subCallID,
				Name:          string(call.toolID),
			}

			h, ok := registry.Get(call.toolID)
			if !ok {
				sub.IsError = true
				sub.Output = map[string]any{"error": "unknown tool"}
				results[i] = sub
				return
			}
			out, err := h.Call(ctx, tools.Call{
				ID:     subCallID,
				Name:   call.toolID,
				Input:  call.input,
				StepID: c.StepID,
			})
			if err != nil {
				sub.IsError = true
				sub.Output = map[string]any{"error": err.Error()}
				results[i] = sub
				return
			}
			sub.IsError = out.IsError
			if len(out.Output) > 0 {
				var parsed any
				if err := json.Unmarshal(out.Output, &parsed); err != nil {
					sub.Output = map[string]any{"raw_output": string(out.Output)}
				} else {
					sub.Output = parsed
				}
			}
			results[i] = sub
		}()
	}
	wg.Wait()

	body, err := json.Marshal(output{Results: results})
	if err != nil {
		return tools.Result{}, err
	}
	return tools.Result{
		CallID: c.ID,
		Name:   c.Name,
		Output: body,
	}, nil
}

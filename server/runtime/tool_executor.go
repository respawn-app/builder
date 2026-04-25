package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"builder/server/llm"
	"builder/server/tools"
	"builder/shared/toolspec"

	"github.com/google/uuid"
)

type defaultToolExecutor struct {
	engine *Engine
}

func (t *defaultToolExecutor) ExecuteToolCalls(ctx context.Context, stepID string, calls []llm.ToolCall) ([]tools.Result, error) {
	e := t.engine
	results := make([]tools.Result, len(calls))
	callErrs := make([]error, len(calls))
	wg := sync.WaitGroup{}
	runID := activeRunIDForStep(e, stepID)

	for i := range calls {
		call := calls[i]
		if call.ID == "" {
			call.ID = uuid.NewString()
		}
		toolID, knownTool := toolspec.ParseID(call.Name)
		executableCall := call
		if call.Custom && knownTool {
			executableCall.Input = executorInputForCustomTool(toolID, call.CustomInput)
		}
		started := Event{Kind: EventToolCallStarted, StepID: stepID, ToolCall: copiedToolCall(normalizeToolCallForTranscript(executableCall, e.transcriptWorkingDir())), CommittedTranscriptChanged: true}
		if start, ok := e.pendingToolCallStart(call.ID); ok {
			started.CommittedEntryStart = start
			started.CommittedEntryStartSet = true
		}
		e.emit(started)
		idx := i
		wg.Add(1)
		go func(tc llm.ToolCall, toolID toolspec.ID, knownTool bool) {
			defer wg.Done()
			defer e.forgetPendingToolCallStart(tc.ID)
			var callErr error

			if !knownTool {
				results[idx] = tools.Result{CallID: tc.ID, Name: toolspec.ID(tc.Name), IsError: true, Output: mustJSON(map[string]any{"error": "unknown tool"}), Summary: "unknown tool"}
				if err := e.persistToolCompletion(stepID, results[idx]); err != nil {
					callErrs[idx] = fmt.Errorf("persist tool completion (call_id=%s tool=%s): %w", tc.ID, results[idx].Name, err)
				} else {
					e.emit(Event{Kind: EventToolCallCompleted, StepID: stepID, ToolResult: copiedToolResult(results[idx]), CommittedTranscriptChanged: true})
				}
				return
			}
			h, ok := e.registry.Get(toolID)
			if toolID == toolspec.ToolWebSearch {
				if err := tools.ValidateWebSearchInput(tc.Input); err != nil {
					results[idx] = tools.ErrorResult(tools.Call{ID: tc.ID, Name: toolID, Input: tc.Input, RunID: runID, StepID: stepID}, tools.InvalidWebSearchQueryMessage)
					if err := e.persistToolCompletion(stepID, results[idx]); err != nil {
						callErrs[idx] = fmt.Errorf("persist tool completion (call_id=%s tool=%s): %w", tc.ID, results[idx].Name, err)
					} else {
						e.emit(Event{Kind: EventToolCallCompleted, StepID: stepID, ToolResult: copiedToolResult(results[idx]), CommittedTranscriptChanged: true})
					}
					return
				}
			}
			if !ok {
				results[idx] = tools.Result{CallID: tc.ID, Name: toolID, IsError: true, Output: mustJSON(map[string]any{"error": "unknown tool"}), Summary: "unknown tool"}
				if err := e.persistToolCompletion(stepID, results[idx]); err != nil {
					callErrs[idx] = fmt.Errorf("persist tool completion (call_id=%s tool=%s): %w", tc.ID, results[idx].Name, err)
				} else {
					e.emit(Event{Kind: EventToolCallCompleted, StepID: stepID, ToolResult: copiedToolResult(results[idx]), CommittedTranscriptChanged: true})
				}
				return
			}
			res, err := h.Call(ctx, tools.Call{ID: tc.ID, Name: toolID, Input: tc.Input, RunID: runID, StepID: stepID})
			if err != nil {
				callErr = err
				res = tools.Result{CallID: tc.ID, Name: toolID, IsError: true, Output: mustJSON(map[string]any{"error": err.Error()}), Summary: err.Error()}
			}
			if res.Name == "" {
				res.Name = toolID
			}
			results[idx] = res
			if err := e.persistToolCompletion(stepID, res); err != nil {
				persistErr := fmt.Errorf("persist tool completion (call_id=%s tool=%s): %w", tc.ID, res.Name, err)
				callErrs[idx] = errors.Join(callErr, persistErr)
				return
			}
			e.emit(Event{Kind: EventToolCallCompleted, StepID: stepID, ToolResult: copiedToolResult(res), CommittedTranscriptChanged: true})
			callErrs[idx] = callErr
		}(executableCall, toolID, knownTool)
	}

	wg.Wait()
	var joined error
	for _, err := range callErrs {
		joined = errors.Join(joined, err)
	}
	if joined != nil {
		return results, joined
	}
	return results, nil
}

func executorInputForCustomTool(toolID toolspec.ID, input string) json.RawMessage {
	switch toolID {
	case toolspec.ToolPatch:
		encoded, _ := json.Marshal(map[string]string{"patch": input})
		return encoded
	default:
		if json.Valid([]byte(input)) {
			return json.RawMessage(input)
		}
		encoded, _ := json.Marshal(input)
		return encoded
	}
}

func activeRunIDForStep(engine *Engine, stepID string) string {
	if engine == nil {
		return ""
	}
	snapshot := engine.ActiveRun()
	if snapshot == nil || snapshot.StepID != stepID {
		return ""
	}
	return snapshot.RunID
}

func copiedToolCall(call llm.ToolCall) *llm.ToolCall {
	copy := call
	return &copy
}

func copiedToolResult(result tools.Result) *tools.Result {
	copy := result
	return &copy
}

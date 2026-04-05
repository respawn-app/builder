package runtime

import (
	"builder/server/llm"
	"builder/server/tools"
	"builder/shared/transcript"
	"builder/shared/transcript/toolcodec"
)

func normalizeMessageForTranscript(msg llm.Message, workingDir string) llm.Message {
	if len(msg.ToolCalls) == 0 {
		return msg
	}
	normalized := msg
	normalized.ToolCalls = normalizeToolCallsForTranscript(msg.ToolCalls, workingDir)
	return normalized
}

func normalizeToolCallsForTranscript(calls []llm.ToolCall, workingDir string) []llm.ToolCall {
	if len(calls) == 0 {
		return nil
	}
	out := make([]llm.ToolCall, 0, len(calls))
	for _, call := range calls {
		out = append(out, normalizeToolCallForTranscript(call, workingDir))
	}
	return out
}

func normalizeToolCallForTranscript(call llm.ToolCall, workingDir string) llm.ToolCall {
	normalized := call
	meta := transcriptToolCallMeta(call, workingDir)
	if meta == nil {
		return normalized
	}
	normalized.Presentation = toolcodec.EncodeToolCallMeta(*meta)
	return normalized
}

func transcriptToolCallMeta(call llm.ToolCall, workingDir string) *transcript.ToolCallMeta {
	if meta := decodeToolCallMeta(call); meta != nil {
		return meta
	}
	built := tools.BuildCallTranscriptMeta(call.Name, tools.ToolCallContext{
		WorkingDir:                 workingDir,
		DefaultShellTimeoutSeconds: defaultShellTimeoutSecond,
	}, call.Input)
	return &built
}

func decodeToolCallMeta(call llm.ToolCall) *transcript.ToolCallMeta {
	meta, ok := toolcodec.DecodeToolCallMeta(call.Presentation)
	if !ok {
		return nil
	}
	return meta
}

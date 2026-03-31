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
	ctx := tools.ToolCallContext{
		WorkingDir:                 workingDir,
		DefaultShellTimeoutSeconds: defaultShellTimeoutSecond,
	}
	for _, call := range calls {
		normalized := call
		if len(normalized.Presentation) == 0 {
			meta := tools.BuildCallTranscriptMeta(normalized.Name, ctx, normalized.Input)
			normalized.Presentation = toolcodec.EncodeToolCallMeta(meta)
		}
		out = append(out, normalized)
	}
	return out
}

func decodeToolCallMeta(call llm.ToolCall) *transcript.ToolCallMeta {
	meta, ok := toolcodec.DecodeToolCallMeta(call.Presentation)
	if !ok {
		return nil
	}
	return meta
}

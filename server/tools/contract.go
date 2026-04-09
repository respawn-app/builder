package tools

import (
	"encoding/json"
	"strings"

	"builder/shared/transcript"
)

const (
	DefaultShellTimeoutSeconds = 300
	InlineMetaSeparator        = "\x1f"
	defaultToolCallFallback    = "tool call"
)

type RuntimeAvailability string

const (
	RuntimeAvailabilityLocal  RuntimeAvailability = "local"
	RuntimeAvailabilityHosted RuntimeAvailability = "hosted"
)

type RequestExposure struct {
	Enabled        bool
	RequiresVision bool
}

type RequestExposureContext struct {
	SupportsVision bool
}

func (r RequestExposure) Allowed(ctx RequestExposureContext) bool {
	if !r.Enabled {
		return false
	}
	if r.RequiresVision && !ctx.SupportsVision {
		return false
	}
	return true
}

type HostedToolOutput struct {
	ID     string
	CallID string
	Raw    json.RawMessage
}

type HostedCall struct {
	ID    string
	Name  ID
	Input json.RawMessage
}

type HostedExecution struct {
	Call   HostedCall
	Result Result
}

type ToolCallContext struct {
	WorkingDir                 string
	DefaultShellTimeoutSeconds int
}

type TranscriptContract struct {
	Presentation         transcript.ToolPresentationKind
	RenderBehavior       transcript.ToolCallRenderBehavior
	OmitSuccessfulResult bool
	BuildCallMeta        func(ctx ToolCallContext, raw json.RawMessage) transcript.ToolCallMeta
	FormatResult         func(Result) string
}

type LocalRuntimeBuilder string

const (
	LocalRuntimeBuilderShell                LocalRuntimeBuilder = "shell"
	LocalRuntimeBuilderExecCommand          LocalRuntimeBuilder = "exec_command"
	LocalRuntimeBuilderWriteStdin           LocalRuntimeBuilder = "write_stdin"
	LocalRuntimeBuilderViewImage            LocalRuntimeBuilder = "view_image"
	LocalRuntimeBuilderPatch                LocalRuntimeBuilder = "patch"
	LocalRuntimeBuilderAskQuestion          LocalRuntimeBuilder = "ask_question"
	LocalRuntimeBuilderTriggerHandoff       LocalRuntimeBuilder = "trigger_handoff"
	LocalRuntimeBuilderMultiToolUseParallel LocalRuntimeBuilder = "multi_tool_use_parallel"
)

type RuntimeContract struct {
	Availability       RuntimeAvailability
	NativeWebSearch    bool
	LocalBuilder       LocalRuntimeBuilder
	DecodeHostedOutput func(HostedToolOutput) (HostedExecution, bool)
}

type Contract struct {
	Runtime    RuntimeContract
	Request    RequestExposure
	Transcript TranscriptContract
}

func (d Definition) AvailableInLocalRuntime() bool {
	return d.contract.Runtime.Availability == RuntimeAvailabilityLocal
}

func (d Definition) LocalRuntimeBuilder() LocalRuntimeBuilder {
	return d.contract.Runtime.LocalBuilder
}

func (d Definition) ExposedToModelRequest(ctx RequestExposureContext) bool {
	return d.contract.Request.Allowed(ctx)
}

func (d Definition) BuildToolCallMeta(ctx ToolCallContext, raw json.RawMessage) transcript.ToolCallMeta {
	meta := transcript.ToolCallMeta{ToolName: string(d.ID)}
	if d.contract.Transcript.BuildCallMeta != nil {
		meta = d.contract.Transcript.BuildCallMeta(ctx, raw)
	}
	meta.ToolName = string(d.ID)
	if meta.Presentation == "" {
		meta.Presentation = d.contract.Transcript.Presentation
	}
	if meta.RenderBehavior == "" {
		meta.RenderBehavior = d.contract.Transcript.RenderBehavior
	}
	if strings.TrimSpace(meta.CompactText) == "" {
		meta.CompactText = strings.TrimSpace(meta.Command)
	}
	if strings.TrimSpace(meta.TimeoutLabel) == "" {
		meta.TimeoutLabel = strings.TrimSpace(meta.InlineMeta)
	}
	if d.contract.Transcript.OmitSuccessfulResult {
		meta.OmitSuccessfulResult = true
	}
	return transcript.NormalizeToolCallMeta(meta)
}

func (d Definition) FormatToolInput(ctx ToolCallContext, raw json.RawMessage) (string, string) {
	meta := d.BuildToolCallMeta(ctx, raw)
	return strings.TrimSpace(meta.Command), strings.TrimSpace(meta.InlineMeta)
}

func (d Definition) FormatToolResult(result Result) string {
	if d.contract.Transcript.FormatResult == nil {
		return strings.TrimSpace(string(result.Output))
	}
	return d.contract.Transcript.FormatResult(result)
}

func (d Definition) DecodeHostedOutput(item HostedToolOutput) (HostedExecution, bool) {
	if d.contract.Runtime.DecodeHostedOutput == nil {
		return HostedExecution{}, false
	}
	return d.contract.Runtime.DecodeHostedOutput(item)
}

func (d Definition) EnablesNativeWebSearch(mode string) bool {
	return d.contract.Runtime.NativeWebSearch && strings.EqualFold(strings.TrimSpace(mode), "native")
}

func DefinitionFor(id ID) (Definition, bool) {
	return definitionFor(id)
}

func definitionForToolName(toolName string) (Definition, bool) {
	id, ok := ParseID(strings.TrimSpace(toolName))
	if !ok {
		return Definition{}, false
	}
	return definitionFor(id)
}

func fallbackToolCallMeta(toolName string, raw json.RawMessage) transcript.ToolCallMeta {
	command := strings.TrimSpace(string(raw))
	if command == "" {
		command = defaultToolCallFallback
	}
	return transcript.NormalizeToolCallMeta(transcript.ToolCallMeta{
		ToolName:       strings.TrimSpace(toolName),
		Presentation:   transcript.ToolPresentationDefault,
		RenderBehavior: transcript.ToolCallRenderBehaviorDefault,
		Command:        command,
		CompactText:    command,
	})
}

func BuildCallTranscriptMeta(toolName string, ctx ToolCallContext, raw json.RawMessage) transcript.ToolCallMeta {
	def, ok := definitionForToolName(toolName)
	if !ok {
		return fallbackToolCallMeta(toolName, raw)
	}
	return def.BuildToolCallMeta(ctx, raw)
}

func FormatToolInputByName(toolName string, ctx ToolCallContext, raw json.RawMessage) (string, string) {
	def, ok := definitionForToolName(toolName)
	if !ok {
		meta := fallbackToolCallMeta(toolName, raw)
		return strings.TrimSpace(meta.Command), strings.TrimSpace(meta.InlineMeta)
	}
	return def.FormatToolInput(ctx, raw)
}

func FormatToolResultByName(toolName string, raw json.RawMessage, isError bool) string {
	def, ok := definitionForToolName(toolName)
	if ok {
		return def.FormatToolResult(Result{Name: def.ID, Output: raw, IsError: isError})
	}
	output := strings.TrimSpace(FormatGenericOutput(raw))
	if output == "" {
		if isError {
			return "tool failed"
		}
		return "done"
	}
	return output
}

func DefinitionsFor(ids []ID) []Definition {
	defs := make([]Definition, 0, len(ids))
	seen := make(map[ID]struct{}, len(ids))
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		def, ok := definitionFor(id)
		if !ok {
			continue
		}
		defs = append(defs, def)
	}
	return defs
}

func FilterRequestExposedDefinitions(defs []Definition, ctx RequestExposureContext) []Definition {
	out := make([]Definition, 0, len(defs))
	for _, def := range defs {
		if def.ExposedToModelRequest(ctx) {
			out = append(out, def)
		}
	}
	return out
}

func RequestExposedDefinitions(ids []ID, ctx RequestExposureContext) []Definition {
	return FilterRequestExposedDefinitions(DefinitionsFor(ids), ctx)
}

func RequestExposedDefinitionsForSession(enabled []ID, registered []Definition, ctx RequestExposureContext) []Definition {
	if len(enabled) > 0 {
		return RequestExposedDefinitions(enabled, ctx)
	}
	return FilterRequestExposedDefinitions(registered, ctx)
}

func NeedsNativeWebSearch(ids []ID, mode string) bool {
	for _, def := range DefinitionsFor(ids) {
		if def.EnablesNativeWebSearch(mode) {
			return true
		}
	}
	return false
}

func FormatToolResultForTranscript(result Result) string {
	return FormatToolResultByName(string(result.Name), result.Output, result.IsError)
}

func HostedExecutionsFromOutputs(items []HostedToolOutput, defs []Definition) []HostedExecution {
	if len(items) == 0 || len(defs) == 0 {
		return nil
	}
	out := make([]HostedExecution, 0, len(items))
	for _, item := range items {
		for _, def := range defs {
			execution, ok := def.DecodeHostedOutput(item)
			if !ok {
				continue
			}
			out = append(out, execution)
			break
		}
	}
	return out
}

func FormatGenericOutput(raw json.RawMessage) string {
	return formatOutputDefault(raw)
}

func FormatRawJSON(raw json.RawMessage) string {
	return formatRawJSON(raw)
}

func SplitInlineMeta(line string) (string, string) {
	parts := strings.SplitN(line, InlineMetaSeparator, 2)
	command := strings.TrimSpace(parts[0])
	if len(parts) == 1 {
		return command, ""
	}
	return command, strings.TrimSpace(parts[1])
}

func CompactToolCallText(meta *transcript.ToolCallMeta, text string) string {
	if meta != nil && meta.HasCompactText() {
		return strings.TrimSpace(meta.CompactText)
	}
	if meta != nil && meta.HasPatchSummary() {
		return strings.TrimSpace(meta.PatchSummary)
	}
	if meta != nil && strings.TrimSpace(meta.Command) != "" {
		return strings.TrimSpace(meta.Command)
	}
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return defaultToolCallFallback
	}
	parts := strings.SplitN(trimmed, "\n", 2)
	first := strings.TrimSpace(parts[0])
	if first == "" {
		return defaultToolCallFallback
	}
	command, _ := SplitInlineMeta(first)
	if command == "" {
		return defaultToolCallFallback
	}
	return command
}

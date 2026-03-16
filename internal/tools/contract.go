package tools

import (
	"encoding/json"
	"strings"

	"builder/internal/transcript"
)

const DefaultShellTimeoutSeconds = 300

type RuntimeAvailability string

const (
	RuntimeAvailabilityLocal  RuntimeAvailability = "local"
	RuntimeAvailabilityHosted RuntimeAvailability = "hosted"
)

type RequestExposure struct {
	Enabled        bool
	RequiresVision bool
}

func (r RequestExposure) Allowed(supportsVision bool) bool {
	if !r.Enabled {
		return false
	}
	if r.RequiresVision && !supportsVision {
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
	BuildCallMeta func(ctx ToolCallContext, raw json.RawMessage) transcript.ToolCallMeta
	FormatResult  func(Result) string
}

type RuntimeContract struct {
	Availability       RuntimeAvailability
	NativeWebSearch    bool
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

func (d Definition) ExposedToModelRequest(supportsVision bool) bool {
	return d.contract.Request.Allowed(supportsVision)
}

func (d Definition) BuildToolCallMeta(ctx ToolCallContext, raw json.RawMessage) transcript.ToolCallMeta {
	meta := transcript.ToolCallMeta{ToolName: string(d.ID)}
	if d.contract.Transcript.BuildCallMeta != nil {
		meta = d.contract.Transcript.BuildCallMeta(ctx, raw)
	}
	meta.ToolName = string(d.ID)
	if meta.Presentation == "" {
		meta.Presentation = transcript.ToolPresentationDefault
	}
	if meta.Presentation == transcript.ToolPresentationShell {
		meta.IsShell = true
	}
	if strings.TrimSpace(meta.CompactText) == "" {
		meta.CompactText = strings.TrimSpace(meta.Command)
	}
	if strings.TrimSpace(meta.TimeoutLabel) == "" {
		meta.TimeoutLabel = strings.TrimSpace(meta.InlineMeta)
	}
	return meta
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

func FormatGenericOutput(raw json.RawMessage) string {
	return formatOutputDefault(raw)
}

func FormatRawJSON(raw json.RawMessage) string {
	return formatRawJSON(raw)
}

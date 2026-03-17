package tools

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"builder/internal/transcript"
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
	Presentation         transcript.ToolPresentationKind
	OmitSuccessfulResult bool
	BuildCallMeta        func(ctx ToolCallContext, raw json.RawMessage) transcript.ToolCallMeta
	FormatResult         func(Result) string
}

type LocalRuntimeContext struct {
	WorkspaceRoot                   string
	OwnerSessionID                  string
	ShellDefaultTimeout             time.Duration
	ShellOutputMaxChars             int
	AllowNonCwdEdits                bool
	SupportsVision                  bool
	RegistryProvider                func() *Registry
	AskQuestionBroker               any
	BackgroundShellManager          any
	OutsideWorkspaceEditApprover    any
	OutsideWorkspaceReadApprover    any
	ViewImageOutsideWorkspaceLogger any
}

type LocalRuntimeFactory func(LocalRuntimeContext) (Handler, error)

type RuntimeContract struct {
	Availability       RuntimeAvailability
	NativeWebSearch    bool
	LocalFactory       LocalRuntimeFactory
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

func (d Definition) BuildLocalHandler(ctx LocalRuntimeContext) (Handler, error) {
	if !d.AvailableInLocalRuntime() {
		return nil, fmt.Errorf("tool %q is not available in local runtime", d.ID)
	}
	if d.contract.Runtime.LocalFactory == nil {
		return nil, fmt.Errorf("missing runtime tool factory for %q", d.ID)
	}
	return d.contract.Runtime.LocalFactory(ctx)
}

func (d Definition) HasLocalRuntimeFactory() bool {
	return d.contract.Runtime.LocalFactory != nil
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
		meta.Presentation = d.contract.Transcript.Presentation
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
	if d.contract.Transcript.OmitSuccessfulResult {
		meta.OmitSuccessfulResult = true
	}
	return meta
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

func ValidateLocalRuntimeFactoryCoverage() error {
	for _, id := range CatalogIDs() {
		def, ok := definitionFor(id)
		if !ok {
			return fmt.Errorf("missing tool definition for %q", id)
		}
		if !def.AvailableInLocalRuntime() {
			continue
		}
		if !def.HasLocalRuntimeFactory() {
			return fmt.Errorf("local runtime tool %q is missing a registered factory", id)
		}
	}
	return nil
}

func BuildCallTranscriptMeta(toolName string, ctx ToolCallContext, raw json.RawMessage) transcript.ToolCallMeta {
	id, ok := ParseID(strings.TrimSpace(toolName))
	if !ok {
		command := strings.TrimSpace(string(raw))
		if command == "" {
			command = defaultToolCallFallback
		}
		return transcript.ToolCallMeta{
			ToolName:     strings.TrimSpace(toolName),
			Presentation: transcript.ToolPresentationDefault,
			Command:      command,
			CompactText:  command,
		}
	}
	def, ok := definitionFor(id)
	if !ok {
		command := strings.TrimSpace(string(raw))
		if command == "" {
			command = defaultToolCallFallback
		}
		return transcript.ToolCallMeta{
			ToolName:     string(id),
			Presentation: transcript.ToolPresentationDefault,
			Command:      command,
			CompactText:  command,
		}
	}
	return def.BuildToolCallMeta(ctx, raw)
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

func FilterRequestExposedDefinitions(defs []Definition, supportsVision bool) []Definition {
	out := make([]Definition, 0, len(defs))
	for _, def := range defs {
		if def.ExposedToModelRequest(supportsVision) {
			out = append(out, def)
		}
	}
	return out
}

func RequestExposedDefinitions(ids []ID, supportsVision bool) []Definition {
	return FilterRequestExposedDefinitions(DefinitionsFor(ids), supportsVision)
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
	if def, ok := definitionFor(result.Name); ok {
		return def.FormatToolResult(result)
	}
	output := strings.TrimSpace(FormatGenericOutput(result.Output))
	if output == "" {
		if result.IsError {
			return "tool failed"
		}
		return "done"
	}
	return output
}

func BuildLocalRuntimeRegistry(enabled []ID, ctx LocalRuntimeContext) (*Registry, error) {
	if err := ValidateLocalRuntimeFactoryCoverage(); err != nil {
		return nil, err
	}
	enabledSet := make(map[ID]struct{}, len(enabled))
	for _, id := range enabled {
		enabledSet[id] = struct{}{}
	}
	handlers := make([]Handler, 0, len(enabledSet))
	var registry *Registry
	ctx.RegistryProvider = func() *Registry { return registry }
	for _, id := range CatalogIDs() {
		if _, ok := enabledSet[id]; !ok {
			continue
		}
		def, ok := definitionFor(id)
		if !ok {
			return nil, fmt.Errorf("missing tool definition for %q", id)
		}
		if !def.AvailableInLocalRuntime() {
			continue
		}
		handler, err := def.BuildLocalHandler(ctx)
		if err != nil {
			return nil, err
		}
		handlers = append(handlers, handler)
		registry = NewRegistry(handlers...)
	}
	if registry == nil {
		return NewRegistry(), nil
	}
	return registry, nil
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

func RegisterLocalRuntimeFactory(id ID, factory LocalRuntimeFactory) {
	if factory == nil {
		panic("runtime tool factory is nil for " + string(id))
	}
	def, ok := definitions[id]
	if !ok {
		panic("runtime tool factory references unknown tool " + string(id))
	}
	if def.contract.Runtime.Availability != RuntimeAvailabilityLocal {
		panic("runtime tool factory registered for non-local tool " + string(id))
	}
	if def.contract.Runtime.LocalFactory != nil {
		panic("runtime tool factory already registered for " + string(id))
	}
	def.contract.Runtime.LocalFactory = factory
	definitions[id] = def
}

func ResolveLocalRuntimeDependency[T any](value any, name string) (T, error) {
	resolved, ok := value.(T)
	if ok {
		return resolved, nil
	}
	var zero T
	return zero, fmt.Errorf("%s is unavailable", name)
}

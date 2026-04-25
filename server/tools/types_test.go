package tools

import (
	"builder/shared/toolspec"
	"builder/shared/transcript"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type stubHandler struct {
	id toolspec.ID
}

func (s stubHandler) Name() toolspec.ID { return s.id }

func (s stubHandler) Call(_ context.Context, c Call) (Result, error) {
	return Result{CallID: c.ID, Name: c.Name, Output: json.RawMessage(`{}`)}, nil
}

func TestRegistryDefinitionsFollowCentralCatalog(t *testing.T) {
	r := NewRegistry(
		stubHandler{id: toolspec.ToolPatch},
		stubHandler{id: toolspec.ToolExecCommand},
	)
	defs := r.Definitions()
	if len(defs) != 2 {
		t.Fatalf("definitions count=%d want 2", len(defs))
	}
	if defs[0].ID != toolspec.ToolPatch || defs[1].ID != toolspec.ToolExecCommand {
		t.Fatalf("definition order mismatch: %+v", defs)
	}
	if len(defs[0].Schema) == 0 || len(defs[1].Schema) == 0 {
		t.Fatalf("missing centralized schema: %+v", defs)
	}
}

func TestRegistryRejectsUnknownToolDefinition(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for unknown tool definition")
		}
	}()
	_ = NewRegistry(stubHandler{id: toolspec.ID("unknown_tool")})
}

func TestRegistryReplaceHandlersSwapsDefinitionsAtomically(t *testing.T) {
	r := NewRegistry(stubHandler{id: toolspec.ToolExecCommand})
	if defs := r.Definitions(); len(defs) != 1 || defs[0].ID != toolspec.ToolExecCommand {
		t.Fatalf("unexpected initial definitions: %+v", defs)
	}
	r.ReplaceHandlers(stubHandler{id: toolspec.ToolPatch}, stubHandler{id: toolspec.ToolWriteStdin})
	defs := r.Definitions()
	if len(defs) != 2 {
		t.Fatalf("definitions count=%d want 2", len(defs))
	}
	if defs[0].ID != toolspec.ToolPatch || defs[1].ID != toolspec.ToolWriteStdin {
		t.Fatalf("definition order mismatch after replace: %+v", defs)
	}
	if _, ok := r.Get(toolspec.ToolExecCommand); ok {
		t.Fatal("expected exec_command handler to be removed after replace")
	}
	if _, ok := r.Get(toolspec.ToolPatch); !ok {
		t.Fatal("expected patch handler after replace")
	}
}

func TestCentralDefinitionsRequireAdditionalPropertiesFalse(t *testing.T) {
	for id, def := range definitions {
		var schema map[string]any
		if err := json.Unmarshal(def.Schema, &schema); err != nil {
			t.Fatalf("tool %s has invalid schema json: %v", id, err)
		}
		got, ok := schema["additionalProperties"].(bool)
		if !ok || got {
			t.Fatalf("tool %s must define additionalProperties=false, got %#v", id, schema["additionalProperties"])
		}
	}
}

func TestDefaultEnabledToolIDsIncludesWebSearchAndViewImage(t *testing.T) {
	enabled := map[toolspec.ID]bool{}
	for _, id := range DefaultEnabledToolIDs() {
		enabled[id] = true
	}
	if !enabled[toolspec.ToolWebSearch] {
		t.Fatalf("expected %s to be default-enabled", toolspec.ToolWebSearch)
	}
	if !enabled[toolspec.ToolViewImage] {
		t.Fatalf("expected %s to be default-enabled", toolspec.ToolViewImage)
	}
	if enabled[toolspec.ToolTriggerHandoff] {
		t.Fatalf("expected %s to remain default-disabled", toolspec.ToolTriggerHandoff)
	}
}

func TestDefinitionContractsDriveRuntimeAndRequestExposure(t *testing.T) {
	execTool, ok := DefinitionFor(toolspec.ToolExecCommand)
	if !ok {
		t.Fatalf("expected %s definition", toolspec.ToolExecCommand)
	}
	if !execTool.AvailableInLocalRuntime() {
		t.Fatalf("expected %s to be available in local runtime", toolspec.ToolExecCommand)
	}
	if execTool.LocalRuntimeBuilder() != LocalRuntimeBuilderExecCommand {
		t.Fatalf("expected %s local runtime builder, got %q", toolspec.ToolExecCommand, execTool.LocalRuntimeBuilder())
	}
	if !execTool.ExposedToModelRequest(RequestExposureContext{}) {
		t.Fatalf("expected %s to be request-exposed without vision", toolspec.ToolExecCommand)
	}

	viewImage, ok := DefinitionFor(toolspec.ToolViewImage)
	if !ok {
		t.Fatalf("expected %s definition", toolspec.ToolViewImage)
	}
	if !viewImage.AvailableInLocalRuntime() {
		t.Fatalf("expected %s to be available in local runtime", toolspec.ToolViewImage)
	}
	if viewImage.LocalRuntimeBuilder() != LocalRuntimeBuilderViewImage {
		t.Fatalf("expected %s local runtime builder, got %q", toolspec.ToolViewImage, viewImage.LocalRuntimeBuilder())
	}
	if viewImage.ExposedToModelRequest(RequestExposureContext{}) {
		t.Fatalf("expected %s to remain hidden without vision support", toolspec.ToolViewImage)
	}
	if !viewImage.ExposedToModelRequest(RequestExposureContext{SupportsVision: true}) {
		t.Fatalf("expected %s to be request-exposed with vision support", toolspec.ToolViewImage)
	}

	triggerHandoff, ok := DefinitionFor(toolspec.ToolTriggerHandoff)
	if !ok {
		t.Fatalf("expected %s definition", toolspec.ToolTriggerHandoff)
	}
	if !triggerHandoff.AvailableInLocalRuntime() {
		t.Fatalf("expected %s to be available in local runtime", toolspec.ToolTriggerHandoff)
	}
	if triggerHandoff.LocalRuntimeBuilder() != LocalRuntimeBuilderTriggerHandoff {
		t.Fatalf("expected %s local runtime builder, got %q", toolspec.ToolTriggerHandoff, triggerHandoff.LocalRuntimeBuilder())
	}
	if !triggerHandoff.ExposedToModelRequest(RequestExposureContext{}) {
		t.Fatalf("expected %s to be request-exposed when enabled", toolspec.ToolTriggerHandoff)
	}

	webSearch, ok := DefinitionFor(toolspec.ToolWebSearch)
	if !ok {
		t.Fatalf("expected %s definition", toolspec.ToolWebSearch)
	}
	if webSearch.AvailableInLocalRuntime() {
		t.Fatalf("expected %s to remain hosted-only", toolspec.ToolWebSearch)
	}
	if webSearch.LocalRuntimeBuilder() != "" {
		t.Fatalf("expected %s to have no local runtime builder, got %q", toolspec.ToolWebSearch, webSearch.LocalRuntimeBuilder())
	}
	if webSearch.ExposedToModelRequest(RequestExposureContext{SupportsVision: true}) {
		t.Fatalf("expected %s to stay hidden from request tool declarations", toolspec.ToolWebSearch)
	}
	if !webSearch.EnablesNativeWebSearch("native") {
		t.Fatalf("expected %s to opt into native provider web search", toolspec.ToolWebSearch)
	}
	if webSearch.EnablesNativeWebSearch("off") {
		t.Fatalf("expected %s native web search to honor disabled mode", toolspec.ToolWebSearch)
	}
}

func TestDefinitionContractsBuildTranscriptMetadata(t *testing.T) {
	execTool, _ := DefinitionFor(toolspec.ToolExecCommand)
	shellMeta := execTool.BuildToolCallMeta(ToolCallContext{DefaultShellPath: "/bin/zsh", GOOS: "darwin"}, json.RawMessage(`{"command":"pwd"}`))
	if !shellMeta.IsShell || shellMeta.Presentation != "shell" {
		t.Fatalf("expected shell contract to mark shell presentation, got %+v", shellMeta)
	}
	if shellMeta.RenderBehavior != "shell" {
		t.Fatalf("expected shell render behavior, got %+v", shellMeta)
	}
	if shellMeta.Command != "pwd" || shellMeta.CompactText != "pwd" {
		t.Fatalf("unexpected shell transcript metadata: %+v", shellMeta)
	}
	if shellMeta.InlineMeta != "" || shellMeta.TimeoutLabel != "" {
		t.Fatalf("did not expect timeout metadata on exec_command, got %+v", shellMeta)
	}
	if shellMeta.RenderHint == nil || shellMeta.RenderHint.Kind != transcript.ToolRenderKindShell || shellMeta.RenderHint.ShellDialect != transcript.ToolShellDialectPosix {
		t.Fatalf("expected shell render hint with posix dialect, got %+v", shellMeta.RenderHint)
	}

	patch, _ := DefinitionFor(toolspec.ToolPatch)
	patchMeta := patch.BuildToolCallMeta(ToolCallContext{WorkingDir: "/workspace"}, json.RawMessage(`"*** Begin Patch\n*** Update File: a.go\n-old\n+new\n*** End Patch\n"`))
	if !patchMeta.OmitSuccessfulResult {
		t.Fatalf("expected patch transcript to suppress success result append, got %+v", patchMeta)
	}
	if patchMeta.PatchSummary == "" || patchMeta.PatchDetail == "" {
		t.Fatalf("expected patch transcript metadata, got %+v", patchMeta)
	}
	if patchMeta.PatchRender == nil {
		t.Fatalf("expected typed patch render metadata, got %+v", patchMeta)
	}
	if patchMeta.CompactText != patchMeta.PatchSummary || patchMeta.Command != patchMeta.PatchDetail {
		t.Fatalf("expected patch aliases normalized, got %+v", patchMeta)
	}
	freeformPatchMeta := patch.BuildToolCallMeta(ToolCallContext{WorkingDir: "/workspace"}, json.RawMessage(`"*** Begin Patch\n*** Update File: custom.go\n-old\n+new\n*** End Patch\n"`))
	if freeformPatchMeta.PatchSummary != "./custom.go +1 -1" {
		t.Fatalf("expected custom freeform patch input summary, got %+v", freeformPatchMeta)
	}

	askQuestion, _ := DefinitionFor(toolspec.ToolAskQuestion)
	askMeta := askQuestion.BuildToolCallMeta(ToolCallContext{}, json.RawMessage(`{"question":"Choose scope?","suggestions":["full"],"recommended_option_index":1}`))
	if askMeta.Presentation != "ask_question" {
		t.Fatalf("expected ask_question presentation, got %+v", askMeta)
	}
	if askMeta.RenderBehavior != "ask_question" {
		t.Fatalf("expected ask_question render behavior, got %+v", askMeta)
	}
	if askMeta.Question != "Choose scope?" || len(askMeta.Suggestions) != 1 {
		t.Fatalf("unexpected ask_question transcript metadata: %+v", askMeta)
	}
	if askMeta.RecommendedOptionIndex != 1 {
		t.Fatalf("unexpected ask_question recommended option index: %+v", askMeta)
	}

	triggerHandoff, _ := DefinitionFor(toolspec.ToolTriggerHandoff)
	handoffMeta := triggerHandoff.BuildToolCallMeta(ToolCallContext{}, json.RawMessage(`{"summarizer_prompt":"keep API details","future_agent_message":"resume with tests"}`))
	if handoffMeta.Command == "" || handoffMeta.CompactText == "" {
		t.Fatalf("expected trigger_handoff metadata to expose compact and detail text, got %+v", handoffMeta)
	}
}

func TestDefinitionContractsFormatLegacyAskQuestionFreeformOnSingleLine(t *testing.T) {
	askQuestion, _ := DefinitionFor(toolspec.ToolAskQuestion)
	got := askQuestion.FormatToolResult(Result{
		Name: toolspec.ToolAskQuestion,
		Output: json.RawMessage(`{
			"answer":"need extra context",
			"freeform_answer":"need extra context"
		}`),
	})

	if strings.TrimSpace(got) == "" {
		t.Fatal("expected non-empty ask freeform summary")
	}
}

func TestDefinitionContractsFormatLegacyAskQuestionApprovalCommentaryUsesDecisionOnly(t *testing.T) {
	askQuestion, _ := DefinitionFor(toolspec.ToolAskQuestion)
	got := askQuestion.FormatToolResult(Result{
		Name: toolspec.ToolAskQuestion,
		Output: json.RawMessage(`{
			"approval": {
				"decision": "deny",
				"commentary": "do not duplicate this"
			}
		}`),
	})

	if strings.TrimSpace(got) == "" {
		t.Fatal("expected non-empty approval compatibility summary")
	}
}

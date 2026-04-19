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
		stubHandler{id: toolspec.ToolShell},
	)
	defs := r.Definitions()
	if len(defs) != 2 {
		t.Fatalf("definitions count=%d want 2", len(defs))
	}
	if defs[0].ID != toolspec.ToolPatch || defs[1].ID != toolspec.ToolShell {
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
	shell, ok := DefinitionFor(toolspec.ToolShell)
	if !ok {
		t.Fatalf("expected %s definition", toolspec.ToolShell)
	}
	if !shell.AvailableInLocalRuntime() {
		t.Fatalf("expected %s to be available in local runtime", toolspec.ToolShell)
	}
	if shell.LocalRuntimeBuilder() != LocalRuntimeBuilderShell {
		t.Fatalf("expected %s local runtime builder, got %q", toolspec.ToolShell, shell.LocalRuntimeBuilder())
	}
	if !shell.ExposedToModelRequest(RequestExposureContext{}) {
		t.Fatalf("expected %s to be request-exposed without vision", toolspec.ToolShell)
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

	parallel, ok := DefinitionFor(toolspec.ToolMultiToolUseParallel)
	if !ok {
		t.Fatalf("expected %s definition", toolspec.ToolMultiToolUseParallel)
	}
	if !parallel.ExposedToModelRequest(RequestExposureContext{}) {
		t.Fatalf("expected %s to be request-exposed when enabled", toolspec.ToolMultiToolUseParallel)
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
	shell, _ := DefinitionFor(toolspec.ToolShell)
	shellMeta := shell.BuildToolCallMeta(ToolCallContext{DefaultShellTimeoutSeconds: DefaultShellTimeoutSeconds, DefaultShellPath: "/bin/zsh", GOOS: "darwin"}, json.RawMessage(`{"command":"pwd"}`))
	if !shellMeta.IsShell || shellMeta.Presentation != "shell" {
		t.Fatalf("expected shell contract to mark shell presentation, got %+v", shellMeta)
	}
	if shellMeta.RenderBehavior != "shell" {
		t.Fatalf("expected shell render behavior, got %+v", shellMeta)
	}
	if shellMeta.Command != "pwd" || shellMeta.CompactText != "pwd" {
		t.Fatalf("unexpected shell transcript metadata: %+v", shellMeta)
	}
	if shellMeta.InlineMeta != "timeout: 5m" || shellMeta.TimeoutLabel != "timeout: 5m" {
		t.Fatalf("expected shell timeout metadata, got %+v", shellMeta)
	}
	if shellMeta.RenderHint == nil || shellMeta.RenderHint.Kind != transcript.ToolRenderKindShell || shellMeta.RenderHint.ShellDialect != transcript.ToolShellDialectPosix {
		t.Fatalf("expected shell render hint with posix dialect, got %+v", shellMeta.RenderHint)
	}

	patch, _ := DefinitionFor(toolspec.ToolPatch)
	patchMeta := patch.BuildToolCallMeta(ToolCallContext{WorkingDir: "/workspace"}, json.RawMessage(`{"patch":"*** Begin Patch\n*** Update File: a.go\n-old\n+new\n*** End Patch\n"}`))
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
	if got, want := handoffMeta.CompactText, "Model requested compaction."; got != want {
		t.Fatalf("trigger_handoff compact text = %q, want %q", got, want)
	}
	if !strings.Contains(handoffMeta.Command, "Instructions:\nkeep API details") {
		t.Fatalf("expected trigger_handoff detail command to include instructions, got %+v", handoffMeta)
	}
	if !strings.Contains(handoffMeta.Command, "Future message:\nresume with tests") {
		t.Fatalf("expected trigger_handoff detail command to include future message, got %+v", handoffMeta)
	}
}

func TestDefinitionContractsFormatEmptyShellOutputAsNoOutput(t *testing.T) {
	shell, _ := DefinitionFor(toolspec.ToolShell)
	got := shell.FormatToolResult(Result{
		Name:   toolspec.ToolShell,
		Output: json.RawMessage(`{"output":" \n\t ","exit_code":0,"truncated":false}`),
	})

	if got != "No output" {
		t.Fatalf("expected No output, got %q", got)
	}
}

func TestDefinitionContractsFormatStructuredPatchFailure(t *testing.T) {
	patch, _ := DefinitionFor(toolspec.ToolPatch)
	got := patch.FormatToolResult(Result{
		Name: toolspec.ToolPatch,
		Output: json.RawMessage(`{
			"kind":"content_mismatch",
			"path":"main.go",
			"line":17,
			"error":"ignored"
		}`),
		IsError: true,
	})

	want := "Patch failed: mismatch between file content and model-provided patch in main.go at line 17."
	if got != want {
		t.Fatalf("patch failure summary = %q, want %q", got, want)
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

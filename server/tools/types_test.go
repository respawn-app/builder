package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type stubHandler struct {
	id ID
}

func (s stubHandler) Name() ID { return s.id }

func (s stubHandler) Call(_ context.Context, c Call) (Result, error) {
	return Result{CallID: c.ID, Name: c.Name, Output: json.RawMessage(`{}`)}, nil
}

func TestParseID(t *testing.T) {
	tests := []struct {
		in   string
		want ID
		ok   bool
	}{
		{in: "shell", want: ToolShell, ok: true},
		{in: "bash", want: ToolShell, ok: true},
		{in: "bash_command", want: ToolShell, ok: true},
		{in: "shell_command", want: ToolShell, ok: true},
		{in: "exec_command", want: ToolExecCommand, ok: true},
		{in: "write_stdin", want: ToolWriteStdin, ok: true},
		{in: "view_image", want: ToolViewImage, ok: true},
		{in: "read_image", want: ToolViewImage, ok: true},
		{in: "patch", want: ToolPatch, ok: true},
		{in: "ask_question", want: ToolAskQuestion, ok: true},
		{in: "trigger_handoff", want: ToolTriggerHandoff, ok: true},
		{in: "web_search", want: ToolWebSearch, ok: true},
		{in: "multi_tool_use_parallel", want: ToolMultiToolUseParallel, ok: true},
		{in: "parallel", want: ToolMultiToolUseParallel, ok: true},
		{in: "unknown", ok: false},
	}
	for _, tt := range tests {
		got, ok := ParseID(tt.in)
		if ok != tt.ok {
			t.Fatalf("ParseID(%q) ok=%t want %t", tt.in, ok, tt.ok)
		}
		if ok && got != tt.want {
			t.Fatalf("ParseID(%q)=%q want %q", tt.in, got, tt.want)
		}
	}
}

func TestRegistryDefinitionsFollowCentralCatalog(t *testing.T) {
	r := NewRegistry(
		stubHandler{id: ToolPatch},
		stubHandler{id: ToolShell},
	)
	defs := r.Definitions()
	if len(defs) != 2 {
		t.Fatalf("definitions count=%d want 2", len(defs))
	}
	if defs[0].ID != ToolPatch || defs[1].ID != ToolShell {
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
	_ = NewRegistry(stubHandler{id: ID("unknown_tool")})
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
	enabled := map[ID]bool{}
	for _, id := range DefaultEnabledToolIDs() {
		enabled[id] = true
	}
	if !enabled[ToolWebSearch] {
		t.Fatalf("expected %s to be default-enabled", ToolWebSearch)
	}
	if !enabled[ToolViewImage] {
		t.Fatalf("expected %s to be default-enabled", ToolViewImage)
	}
	if enabled[ToolTriggerHandoff] {
		t.Fatalf("expected %s to remain default-disabled", ToolTriggerHandoff)
	}
}

func TestDefinitionContractsDriveRuntimeAndRequestExposure(t *testing.T) {
	shell, ok := DefinitionFor(ToolShell)
	if !ok {
		t.Fatalf("expected %s definition", ToolShell)
	}
	if !shell.AvailableInLocalRuntime() {
		t.Fatalf("expected %s to be available in local runtime", ToolShell)
	}
	if shell.LocalRuntimeBuilder() != LocalRuntimeBuilderShell {
		t.Fatalf("expected %s local runtime builder, got %q", ToolShell, shell.LocalRuntimeBuilder())
	}
	if !shell.ExposedToModelRequest(RequestExposureContext{}) {
		t.Fatalf("expected %s to be request-exposed without vision", ToolShell)
	}

	viewImage, ok := DefinitionFor(ToolViewImage)
	if !ok {
		t.Fatalf("expected %s definition", ToolViewImage)
	}
	if !viewImage.AvailableInLocalRuntime() {
		t.Fatalf("expected %s to be available in local runtime", ToolViewImage)
	}
	if viewImage.LocalRuntimeBuilder() != LocalRuntimeBuilderViewImage {
		t.Fatalf("expected %s local runtime builder, got %q", ToolViewImage, viewImage.LocalRuntimeBuilder())
	}
	if viewImage.ExposedToModelRequest(RequestExposureContext{}) {
		t.Fatalf("expected %s to remain hidden without vision support", ToolViewImage)
	}
	if !viewImage.ExposedToModelRequest(RequestExposureContext{SupportsVision: true}) {
		t.Fatalf("expected %s to be request-exposed with vision support", ToolViewImage)
	}

	parallel, ok := DefinitionFor(ToolMultiToolUseParallel)
	if !ok {
		t.Fatalf("expected %s definition", ToolMultiToolUseParallel)
	}
	if !parallel.ExposedToModelRequest(RequestExposureContext{}) {
		t.Fatalf("expected %s to be request-exposed when enabled", ToolMultiToolUseParallel)
	}

	triggerHandoff, ok := DefinitionFor(ToolTriggerHandoff)
	if !ok {
		t.Fatalf("expected %s definition", ToolTriggerHandoff)
	}
	if !triggerHandoff.AvailableInLocalRuntime() {
		t.Fatalf("expected %s to be available in local runtime", ToolTriggerHandoff)
	}
	if triggerHandoff.LocalRuntimeBuilder() != LocalRuntimeBuilderTriggerHandoff {
		t.Fatalf("expected %s local runtime builder, got %q", ToolTriggerHandoff, triggerHandoff.LocalRuntimeBuilder())
	}
	if !triggerHandoff.ExposedToModelRequest(RequestExposureContext{}) {
		t.Fatalf("expected %s to be request-exposed when enabled", ToolTriggerHandoff)
	}

	webSearch, ok := DefinitionFor(ToolWebSearch)
	if !ok {
		t.Fatalf("expected %s definition", ToolWebSearch)
	}
	if webSearch.AvailableInLocalRuntime() {
		t.Fatalf("expected %s to remain hosted-only", ToolWebSearch)
	}
	if webSearch.LocalRuntimeBuilder() != "" {
		t.Fatalf("expected %s to have no local runtime builder, got %q", ToolWebSearch, webSearch.LocalRuntimeBuilder())
	}
	if webSearch.ExposedToModelRequest(RequestExposureContext{SupportsVision: true}) {
		t.Fatalf("expected %s to stay hidden from request tool declarations", ToolWebSearch)
	}
	if !webSearch.EnablesNativeWebSearch("native") {
		t.Fatalf("expected %s to opt into native provider web search", ToolWebSearch)
	}
	if webSearch.EnablesNativeWebSearch("off") {
		t.Fatalf("expected %s native web search to honor disabled mode", ToolWebSearch)
	}
}

func TestDefinitionContractsBuildTranscriptMetadata(t *testing.T) {
	shell, _ := DefinitionFor(ToolShell)
	shellMeta := shell.BuildToolCallMeta(ToolCallContext{DefaultShellTimeoutSeconds: DefaultShellTimeoutSeconds}, json.RawMessage(`{"command":"pwd"}`))
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

	patch, _ := DefinitionFor(ToolPatch)
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

	askQuestion, _ := DefinitionFor(ToolAskQuestion)
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

	triggerHandoff, _ := DefinitionFor(ToolTriggerHandoff)
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
	shell, _ := DefinitionFor(ToolShell)
	got := shell.FormatToolResult(Result{
		Name:   ToolShell,
		Output: json.RawMessage(`{"output":" \n\t ","exit_code":0,"truncated":false}`),
	})

	if got != "No output" {
		t.Fatalf("expected No output, got %q", got)
	}
}

func TestDefinitionContractsFormatLegacyAskQuestionFreeformOnSingleLine(t *testing.T) {
	askQuestion, _ := DefinitionFor(ToolAskQuestion)
	got := askQuestion.FormatToolResult(Result{
		Name: ToolAskQuestion,
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
	askQuestion, _ := DefinitionFor(ToolAskQuestion)
	got := askQuestion.FormatToolResult(Result{
		Name: ToolAskQuestion,
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

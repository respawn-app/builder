package prompts

import (
	"strings"
	"testing"

	"builder/cli/selfcmd"
)

func TestRenderSystemPromptTemplateUsesTypedFields(t *testing.T) {
	rendered := renderSystemPromptTemplate("calls={{.EstimatedToolCallsForContext}} cmd={{.BuilderRunCommand}} edit={{.ManualEditInstruction}}", SystemPromptTemplateArgs{
		EstimatedToolCallsForContext: 123,
		ManualEditInstruction:        "Use edit.",
	}, "")
	if !strings.Contains(rendered, "calls=123") {
		t.Fatalf("expected estimated tool calls rendered, got %q", rendered)
	}
	expectedCmd := "cmd=" + selfcmd.RunCommandPrefix()
	if !strings.Contains(rendered, expectedCmd) || strings.Contains(rendered, "{{") {
		t.Fatalf("expected %q in rendered output, got %q", expectedCmd, rendered)
	}
	if !strings.Contains(rendered, "edit=Use edit.") {
		t.Fatalf("expected manual edit instruction rendered, got %q", rendered)
	}
}

func TestCustomSystemPromptResolvesDefaultSystemPromptPlaceholder(t *testing.T) {
	defaultPrompt := BaseSystemPrompt(SystemPromptTemplateArgs{
		EstimatedToolCallsForContext: 123,
	})
	rendered, err := RenderCustomSystemPrompt("custom\n{{.DefaultSystemPrompt}}", false, SystemPromptTemplateArgs{
		EstimatedToolCallsForContext: 123,
	})
	if err != nil {
		t.Fatalf("RenderCustomSystemPrompt: %v", err)
	}
	if !strings.Contains(rendered, "custom\n") {
		t.Fatalf("expected custom prefix, got %q", rendered)
	}
	if !strings.Contains(rendered, defaultPrompt) || strings.Contains(rendered, "{{") {
		t.Fatalf("expected default prompt placeholder rendered, got %q", rendered)
	}
}

func TestRenderGoalNudgePrompt(t *testing.T) {
	rendered := RenderGoalNudgePrompt("ship /goal mode", "active")
	for _, want := range []string{
		"ship /goal mode",
		"Current goal status: active",
		"builder goal complete",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected goal nudge to contain %q, got %q", want, rendered)
		}
	}
	if strings.Contains(rendered, "{{") {
		t.Fatalf("expected goal nudge placeholders rendered, got %q", rendered)
	}
}

func TestRenderGoalSetPrompt(t *testing.T) {
	rendered := RenderGoalSetPrompt("ship /goal mode")
	if !strings.Contains(rendered, "ship /goal mode") {
		t.Fatalf("expected goal set prompt to contain objective, got %q", rendered)
	}
	if strings.Contains(rendered, "{{") {
		t.Fatalf("expected goal set placeholders rendered, got %q", rendered)
	}
}

func TestRenderGoalResumePrompt(t *testing.T) {
	rendered := RenderGoalResumePrompt("ship /goal mode")
	if !strings.Contains(rendered, "ship /goal mode") {
		t.Fatalf("expected goal resume prompt to contain objective, got %q", rendered)
	}
	if strings.Contains(rendered, "{{") {
		t.Fatalf("expected goal resume placeholders rendered, got %q", rendered)
	}
}

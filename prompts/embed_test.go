package prompts

import (
	"strings"
	"testing"
)

func TestRenderSystemPromptTemplateUsesTypedFields(t *testing.T) {
	rendered := renderSystemPromptTemplate("calls={{.EstimatedToolCallsForContext}} cmd={{.BuilderRunCommand}}", SystemPromptTemplateArgs{
		EstimatedToolCallsForContext: 123,
	})
	if !strings.Contains(rendered, "calls=123") {
		t.Fatalf("expected estimated tool calls rendered, got %q", rendered)
	}
	if !strings.Contains(rendered, "cmd=") || strings.Contains(rendered, "{{") {
		t.Fatalf("expected builder run command rendered, got %q", rendered)
	}
}

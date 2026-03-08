package prompts

import (
	"strings"
	"testing"
)

func TestMainSystemPromptIncludesToolPreamblesWhenEnabled(t *testing.T) {
	got := MainSystemPrompt(true)
	if !strings.Contains(got, "## Intermediary updates") {
		t.Fatalf("expected intermediary updates section in composed prompt")
	}
	if !strings.Contains(got, "## Final answer instructions") {
		t.Fatalf("expected base system prompt content in composed prompt")
	}
}

func TestMainSystemPromptOmitsToolPreamblesWhenDisabled(t *testing.T) {
	got := MainSystemPrompt(false)
	if strings.Contains(got, "## Intermediary updates") {
		t.Fatalf("did not expect intermediary updates section when disabled")
	}
	if !strings.Contains(got, "## Final answer instructions") {
		t.Fatalf("expected base system prompt content in composed prompt")
	}
}

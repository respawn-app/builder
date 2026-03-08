package prompts

import (
	"strings"
	"testing"
)

func TestMainSystemPromptIncludesToolPreamblesWhenEnabled(t *testing.T) {
	got := MainSystemPrompt(true)
	want := strings.TrimSpace(SystemPrompt) + "\n\n" + strings.TrimSpace(ToolPreamblesPrompt)
	if got != want {
		t.Fatalf("unexpected composed prompt when tool preambles enabled")
	}
}

func TestMainSystemPromptOmitsToolPreamblesWhenDisabled(t *testing.T) {
	got := MainSystemPrompt(false)
	want := strings.TrimSpace(SystemPrompt)
	if got != want {
		t.Fatalf("unexpected composed prompt when tool preambles disabled")
	}
}

package prompts

import (
	"strings"
	"testing"

	"builder/internal/selfcmd"
)

func TestMainSystemPromptIncludesToolPreamblesWhenEnabled(t *testing.T) {
	got := MainSystemPrompt(true)
	want := BaseSystemPrompt() + "\n\n" + strings.TrimSpace(ToolPreamblesPrompt)
	if got != want {
		t.Fatalf("unexpected composed prompt when tool preambles enabled")
	}
}

func TestMainSystemPromptOmitsToolPreamblesWhenDisabled(t *testing.T) {
	got := MainSystemPrompt(false)
	want := BaseSystemPrompt()
	if got != want {
		t.Fatalf("unexpected composed prompt when tool preambles disabled")
	}
}

func TestBaseSystemPromptRendersBuilderRunCommand(t *testing.T) {
	got := BaseSystemPrompt()
	if strings.Contains(got, runCommandPlaceholder) {
		t.Fatalf("expected run command placeholder to be rendered, got %q", got)
	}
	if !strings.Contains(got, selfcmd.RunCommandPrefix()) {
		t.Fatalf("expected prompt to include run command prefix %q", selfcmd.RunCommandPrefix())
	}
}

func TestRenderRunCommandPreservesQuotedExecutablePath(t *testing.T) {
	text := "run `{{builder_run_command}} \"work item\"` now"
	rendered := renderRunCommandWithPrefix(text, selfcmdTestRunCommandPrefix())
	want := "run `\"/tmp/path with space/builder\" run \"work item\"` now"
	if rendered != want {
		t.Fatalf("rendered prompt = %q, want %q", rendered, want)
	}
}

func TestCompactionSoonReminderPromptIsEmbedded(t *testing.T) {
	if strings.TrimSpace(CompactionSoonReminderPrompt) == "" {
		t.Fatal("expected compaction soon reminder prompt to be embedded")
	}
}

func selfcmdTestRunCommandPrefix() string {
	return "\"/tmp/path with space/builder\" run"
}

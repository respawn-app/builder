package prompts

import (
	"bytes"
	_ "embed"
	"fmt"
	"strings"
	"text/template"

	"builder/cli/selfcmd"
)

type SystemPromptTemplateArgs struct {
	EstimatedToolCallsForContext int
}

type systemPromptTemplateData struct {
	BuilderRunCommand            string
	EstimatedToolCallsForContext int
}

//go:embed system_prompt.md
var SystemPrompt string

//go:embed tool_preambles_prompt.md
var ToolPreamblesPrompt string

//go:embed compaction_prompt.md
var CompactionPrompt string

//go:embed compaction_summary_prefix.md
var CompactionSummaryPrefix string

//go:embed compaction_soon_reminder.md
var CompactionSoonReminderPrompt string

//go:embed compaction_soon_reminder_trigger_handoff.md
var CompactionSoonReminderTriggerHandoffPrompt string

//go:embed review_prompt.md
var ReviewPrompt string

//go:embed init_prompt.md
var InitPrompt string

//go:embed reviewer_system_prompt.md
var ReviewerSystemPrompt string

//go:embed skills_how_to_use_rules.md
var SkillsHowToUseRulesPrompt string

//go:embed headless_mode_prompt.md
var HeadlessModePrompt string

//go:embed headless_mode_exit_prompt.md
var HeadlessModeExitPrompt string

//go:embed worktree_mode_prompt.md
var WorktreeModePrompt string

//go:embed worktree_mode_exit_prompt.md
var WorktreeModeExitPrompt string

func MainSystemPrompt(includeToolPreambles bool, args SystemPromptTemplateArgs) string {
	base := renderSystemPromptTemplate(strings.TrimSpace(SystemPrompt), args)
	if !includeToolPreambles {
		return base
	}
	preambles := strings.TrimSpace(ToolPreamblesPrompt)
	if preambles == "" {
		return base
	}
	if base == "" {
		return preambles
	}
	return base + "\n\n" + preambles
}

func BaseSystemPrompt(args SystemPromptTemplateArgs) string {
	return renderSystemPromptTemplate(strings.TrimSpace(SystemPrompt), args)
}

func RenderCompactionSoonReminderPrompt(triggerHandoffEnabled bool) string {
	if triggerHandoffEnabled {
		return strings.TrimSpace(CompactionSoonReminderTriggerHandoffPrompt)
	}
	return strings.TrimSpace(CompactionSoonReminderPrompt)
}

func RenderWorktreeModePrompt(branch, cwd, worktreePath, workspaceRoot string) string {
	return renderWorktreePrompt(WorktreeModePrompt, map[string]string{
		"{{branch}}":         strings.TrimSpace(branch),
		"{{cwd}}":            strings.TrimSpace(cwd),
		"{{worktree_path}}":  strings.TrimSpace(worktreePath),
		"{{workspace_root}}": strings.TrimSpace(workspaceRoot),
	})
}

func RenderWorktreeModeExitPrompt(branch, cwd, worktreePath, workspaceRoot string) string {
	return renderWorktreePrompt(WorktreeModeExitPrompt, map[string]string{
		"{{branch}}":         strings.TrimSpace(branch),
		"{{cwd}}":            strings.TrimSpace(cwd),
		"{{worktree_path}}":  strings.TrimSpace(worktreePath),
		"{{workspace_root}}": strings.TrimSpace(workspaceRoot),
	})
}

func renderSystemPromptTemplate(text string, args SystemPromptTemplateArgs) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}
	tmpl, err := template.New("system_prompt").Option("missingkey=error").Parse(trimmed)
	if err != nil {
		panic(fmt.Errorf("parse system prompt template: %w", err))
	}
	var out bytes.Buffer
	if err := tmpl.Execute(&out, systemPromptTemplateData{
		BuilderRunCommand:            selfcmd.RunCommandPrefix(),
		EstimatedToolCallsForContext: args.EstimatedToolCallsForContext,
	}); err != nil {
		panic(fmt.Errorf("render system prompt template: %w", err))
	}
	return out.String()
}

func renderWorktreePrompt(template string, replacements map[string]string) string {
	text := strings.TrimSpace(template)
	if text == "" {
		return ""
	}
	for placeholder, value := range replacements {
		text = strings.ReplaceAll(text, placeholder, value)
	}
	return text
}

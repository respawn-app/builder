package prompts

import (
	_ "embed"
	"strconv"
	"strings"

	"builder/cli/selfcmd"
)

const (
	runCommandPlaceholder                = "{{builder_run_command}}"
	estimatedToolCallsContextPlaceholder = "{{estimated_tool_calls_for_context}}"
)

type SystemPromptTemplateArgs struct {
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

func renderRunCommand(text string) string {
	return renderRunCommandWithPrefix(text, selfcmd.RunCommandPrefix())
}

func renderSystemPromptTemplate(text string, args SystemPromptTemplateArgs) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}
	rendered := renderRunCommand(text)
	return strings.ReplaceAll(rendered, estimatedToolCallsContextPlaceholder, strconv.Itoa(args.EstimatedToolCallsForContext))
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

func renderRunCommandWithPrefix(text, prefix string) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}
	return strings.ReplaceAll(text, runCommandPlaceholder, prefix)
}

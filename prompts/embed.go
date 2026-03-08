package prompts

import (
	_ "embed"
	"strings"
)

//go:embed system_prompt.md
var SystemPrompt string

//go:embed tool_preambles_prompt.md
var ToolPreamblesPrompt string

//go:embed compaction_prompt.md
var CompactionPrompt string

//go:embed compaction_summary_prefix.md
var CompactionSummaryPrefix string

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

func MainSystemPrompt(includeToolPreambles bool) string {
	base := strings.TrimSpace(SystemPrompt)
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

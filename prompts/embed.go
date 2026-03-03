package prompts

import _ "embed"

//go:embed system_prompt.md
var SystemPrompt string

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

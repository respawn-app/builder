package prompts

import _ "embed"

//go:embed system_prompt.md
var SystemPrompt string

//go:embed compaction_prompt.md
var CompactionPrompt string

//go:embed compaction_summary_prefix.md
var CompactionSummaryPrefix string

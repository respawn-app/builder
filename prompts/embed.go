package prompts

import _ "embed"

//go:embed system_prompt.md
var SystemPrompt string

//go:embed tool_definitions.json
var ToolDefinitions []byte

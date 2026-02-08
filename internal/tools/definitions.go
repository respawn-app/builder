package tools

import "encoding/json"

var definitions = map[ID]Definition{
	ToolShell: {
		ID:          ToolShell,
		Description: "Execute a shell command in the user's environment and device.",
		Schema: json.RawMessage(`{
  "type": "object",
  "required": ["command"],
  "properties": {
    "command": {
      "type": "string",
      "description": "Command line to execute in login shell."
    },
    "timeout_seconds": {
      "type": "integer",
      "description": "Optional timeout in seconds (max 3600)."
    },
    "workdir": {
      "type": "string",
      "description": "Optional working directory, otherwise - cwd."
    }
  }
}`),
	},
	ToolPatch: {
		ID:          ToolPatch,
		Description: "Apply a freeform patch. This tool does not support deletion, for deletion, use a shell tool, like trash (preferred if available) or rm",
		Schema: json.RawMessage(`{
  "type": "object",
  "required": ["patch"],
  "properties": {
    "patch": {
      "type": "string",
      "description": "Patch text in freeform format."
    }
  }
}`),
	},
	ToolAskQuestion: {
		ID:          ToolAskQuestion,
		Description: "Ask the user a question and wait for answer. You should ask the user when planning your work or working to make product decisions, resolve ambiguities, define missing pieces that you cannot resolve by yourself. You should ask the user a lot of questions when planning to learn their desires, preferences, design, product vision, or implementation approach, and sometimes ask them questions when already working if you encounter a problem you can't resolve, a caveat, undefined area that materially affects the result or direction of your work. You should avoid asking the user obvious or harmless questions like 'should i run tests?' or 'do you want this done well?' which you can answer yourself. Each question interrupts the work and summons the user, treat it like pinging a coworker on Slack.",
		Schema: json.RawMessage(`{
  "type": "object",
  "required": ["question"],
  "properties": {
    "question": {
      "type": "string",
      "description": "Question text shown to the user."
    },
    "suggestions": {
      "type": "array",
      "description": "Optional predefined choice suggestions. The user can always answer their own way, but you should anticipate common answers, explain them in the option text briefly, pick exactly one recommended option and mark it as such. Strive to give users the best, sensible options possible, following best-practices, guidelines, or common sense.",
      "items": {"type": "string"}
    }
  }
}`),
	},
}

func definitionFor(id ID) (Definition, bool) {
	def, ok := definitions[id]
	return def, ok
}

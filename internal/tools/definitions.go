package tools

import "encoding/json"

var definitions = map[ID]Definition{
	ToolBash: {
		ID:          ToolBash,
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
		Description: "Ask the user a blocking question and wait for answer.",
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
      "description": "Optional predefined choices.",
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

package tools

import "encoding/json"

var definitions = map[ID]Definition{
	ToolBash: {
		ID:          ToolBash,
		Description: "Execute a shell command in non-TTY mode with merged stdout/stderr.",
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
      "description": "Optional working directory."
    }
  }
}`),
	},
	ToolPatch: {
		ID:          ToolPatch,
		Description: "Apply an atomic patch (add/update/move only). Delete blocks are forbidden.",
		Schema: json.RawMessage(`{
  "type": "object",
  "required": ["patch"],
  "properties": {
    "patch": {
      "type": "string",
      "description": "Patch text in apply_patch format."
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
    },
    "action": {
      "type": "object",
      "description": "Optional typed action to execute after answer.",
      "properties": {
        "id": {"type": "string"},
        "payload": {"type": "object"}
      }
    }
  }
}`),
	},
}

func definitionFor(id ID) (Definition, bool) {
	def, ok := definitions[id]
	return def, ok
}

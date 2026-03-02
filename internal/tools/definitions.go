package tools

import (
	"encoding/json"
	"sort"
)

type CatalogEntry struct {
	ID             ID
	Aliases        []string
	Description    string
	Schema         json.RawMessage
	DefaultEnabled bool
}

var catalogEntries = []CatalogEntry{
	{
		ID:             ToolShell,
		Aliases:        []string{"bash", "bash_command", "shell_command", "exec_command"},
		Description:    "Execute a shell command in the user's environment and device.",
		DefaultEnabled: true,
		Schema: json.RawMessage(`{
  "type": "object",
  "additionalProperties": false,
  "properties": {
    "command": {
      "type": "string",
      "description": "Command line to execute in login shell."
    },
    "cmd": {
      "type": "string",
      "description": "Alias for command."
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
	{
		ID:             ToolViewImage,
		Aliases:        []string{"read_image"},
		Description:    "Read a local image or PDF file by path and attach it to the model as native multimodal input content.",
		DefaultEnabled: true,
		Schema: json.RawMessage(`{
  "type": "object",
  "additionalProperties": false,
  "required": ["path"],
  "properties": {
    "path": {
      "type": "string",
      "description": "Local filesystem path to an image or PDF file. Relative paths resolve from the workspace root."
    }
  }
}`),
	},
	{
		ID:             ToolPatch,
		Aliases:        nil,
		Description:    "Apply a freeform patch. This tool does not support deletion, for deletion, use a shell tool, like trash (preferred if available) or rm",
		DefaultEnabled: true,
		Schema: json.RawMessage(`{
  "type": "object",
  "additionalProperties": false,
  "required": ["patch"],
  "properties": {
    "patch": {
      "type": "string",
      "description": "Patch text in freeform format."
    }
  }
}`),
	},
	{
		ID:             ToolAskQuestion,
		Aliases:        nil,
		Description:    "Ask the user a question. You should ask the user when planning your work or working to make product decisions, resolve ambiguities, define missing pieces that you cannot resolve by yourself, brainstorming with the user. You should ask the user a lot of questions when you're planning/brainstorming together to learn their desires, preferences, design, product vision, or implementation approach, and sometimes ask them questions when already working if you encounter a problem you can't resolve, a caveat, an undefined area that materially affects the result or direction of your work, etc. You should avoid asking the user obvious or harmless questions like 'Should I run tests?' or 'Where is file X?' which you can answer yourself. Each question pings the user, so treat it like pinging a coworker on Slack: unless they're actively chatting with you, pinging them could distract them. Stick to ONE question per this tool call, for multiple questions call this tool in parallel. Strive to provide multiple suggestions/options with every question if you can.",
		DefaultEnabled: true,
		Schema: json.RawMessage(`{
  "type": "object",
  "additionalProperties": false,
  "required": ["question"],
  "properties": {
    "question": {
      "type": "string",
      "description": "Question text shown to the user. You must only put exactly ONE question here."
    },
    "suggestions": {
      "type": "array",
      "description": "Optional choice suggestions. To fill this you should anticipate best practices or options, explain them in the item text briefly, pick exactly one recommended option and mark it as such. Strive to give users the best, sensible options possible, following best-practices, guidelines, and common sense.",
      "items": {"type": "string"}
    }
  }
}`),
	},
	{
		ID:             ToolWebSearch,
		Aliases:        nil,
		Description:    "Search the web for up-to-date external information using the provider-native web search capability when available. Use this when local workspace context is insufficient or the fact could be stale. Prefer primary and official sources, and prefer MCP resources/templates over web search when possible.",
		DefaultEnabled: true,
		Schema: json.RawMessage(`{
  "type": "object",
  "additionalProperties": false,
  "required": ["query"],
  "properties": {
    "query": {
      "type": "string",
      "description": "Required search query string. Keep it specific and concise; include concrete keywords (entity + property + timeframe) and optionally a site hint."
    },
    "allowed_domains": {
      "type": "array",
      "description": "Optional allowlist of domains to constrain sources to preferred/authoritative sites.",
      "items": {"type": "string"}
    },
    "blocked_domains": {
      "type": "array",
      "description": "Optional blocklist of domains to exclude low-quality or irrelevant sources.",
      "items": {"type": "string"}
    }
  }
}`),
	},
	{
		ID:             ToolMultiToolUseParallel,
		Aliases:        []string{"parallel"},
		Description:    "Use this function to run multiple tools simultaneously, but only if they can operate in parallel.",
		DefaultEnabled: true,
		Schema: json.RawMessage(`{
  "type": "object",
  "additionalProperties": false,
  "required": ["tool_uses"],
  "properties": {
    "tool_uses": {
      "type": "array",
      "description": "The tools to be executed in parallel. NOTE: only functions tools are permitted",
      "items": {
        "type": "object",
        "additionalProperties": false,
        "required": ["recipient_name", "parameters"],
        "properties": {
          "recipient_name": {
            "type": "string",
            "description": "The name of the tool to use. The format must be functions.<function_name>."
          },
          "parameters": {
            "type": "object",
            "description": "The parameters to pass to the tool. Ensure these are valid according to that tool's own specifications."
          }
        }
      }
    }
  }
}`),
	},
}

var (
	definitions       map[ID]Definition
	parseAliases      map[string]ID
	catalogIDs        []ID
	defaultEnabledIDs []ID
)

func init() {
	definitions = make(map[ID]Definition, len(catalogEntries))
	parseAliases = make(map[string]ID, len(catalogEntries)*2)
	catalogIDs = make([]ID, 0, len(catalogEntries))
	defaultEnabledIDs = make([]ID, 0, len(catalogEntries))

	for _, entry := range catalogEntries {
		definitions[entry.ID] = Definition{
			ID:          entry.ID,
			Description: entry.Description,
			Schema:      entry.Schema,
		}
		parseAliases[string(entry.ID)] = entry.ID
		for _, alias := range entry.Aliases {
			parseAliases[alias] = entry.ID
		}
		catalogIDs = append(catalogIDs, entry.ID)
		if entry.DefaultEnabled {
			defaultEnabledIDs = append(defaultEnabledIDs, entry.ID)
		}
	}

	sort.Slice(catalogIDs, func(i, j int) bool { return catalogIDs[i] < catalogIDs[j] })
	sort.Slice(defaultEnabledIDs, func(i, j int) bool { return defaultEnabledIDs[i] < defaultEnabledIDs[j] })
}

func Catalog() []CatalogEntry {
	out := make([]CatalogEntry, len(catalogEntries))
	copy(out, catalogEntries)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func CatalogIDs() []ID {
	out := make([]ID, len(catalogIDs))
	copy(out, catalogIDs)
	return out
}

func DefaultEnabledToolIDs() []ID {
	out := make([]ID, len(defaultEnabledIDs))
	copy(out, defaultEnabledIDs)
	return out
}

func parseCatalogID(v string) (ID, bool) {
	id, ok := parseAliases[v]
	return id, ok
}

func definitionFor(id ID) (Definition, bool) {
	def, ok := definitions[id]
	return def, ok
}

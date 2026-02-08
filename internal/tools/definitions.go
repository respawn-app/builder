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
		Aliases:        []string{"bash"},
		Description:    "Execute a shell command in the user's environment and device.",
		DefaultEnabled: true,
		Schema: json.RawMessage(`{
  "type": "object",
  "additionalProperties": false,
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
		Description:    "Ask the user a question and wait for answer. You should ask the user when planning your work or working to make product decisions, resolve ambiguities, define missing pieces that you cannot resolve by yourself. You should ask the user a lot of questions when planning to learn their desires, preferences, design, product vision, or implementation approach, and sometimes ask them questions when already working if you encounter a problem you can't resolve, a caveat, undefined area that materially affects the result or direction of your work. You should avoid asking the user obvious or harmless questions like 'should i run tests?' or 'do you want this done well?' which you can answer yourself. Each question interrupts the work and summons the user, treat it like pinging a coworker on Slack.",
		DefaultEnabled: true,
		Schema: json.RawMessage(`{
  "type": "object",
  "additionalProperties": false,
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

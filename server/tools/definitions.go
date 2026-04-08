package tools

import (
	"encoding/json"
	"sort"

	"builder/shared/transcript"
)

type CatalogEntry struct {
	ID             ID
	Aliases        []string
	Description    string
	Schema         json.RawMessage
	DefaultEnabled bool
	Contract       Contract
}

var catalogEntries = []CatalogEntry{
	{
		ID:             ToolShell,
		Aliases:        []string{"bash", "bash_command", "shell_command"},
		Description:    "Execute a shell command in the user's environment and device.",
		DefaultEnabled: true,
		Contract: localContract(
			LocalRuntimeBuilderShell,
			RequestExposure{Enabled: true},
			transcript.ToolPresentationShell,
			transcript.ToolCallRenderBehaviorShell,
			false,
			shellToolCallMeta(ToolShell),
			formatGenericToolResult,
		),
		Schema: json.RawMessage(`{
  "type": "object",
  "additionalProperties": false,
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
		ID:             ToolExecCommand,
		Aliases:        nil,
		Description:    "Runs a command in the user's default shell, returning output or a session ID for ongoing interaction.",
		DefaultEnabled: true,
		Contract: localContract(
			LocalRuntimeBuilderExecCommand,
			RequestExposure{Enabled: true},
			transcript.ToolPresentationShell,
			transcript.ToolCallRenderBehaviorShell,
			false,
			shellToolCallMeta(ToolExecCommand),
			formatGenericToolResult,
		),
		Schema: json.RawMessage(`{
  "type": "object",
  "additionalProperties": false,
  "required": ["cmd"],
  "properties": {
    "cmd": {
      "type": "string",
      "description": "Shell command to execute."
    },
    "workdir": {
      "type": "string",
      "description": "Optional working directory to run the command in; defaults to the workspace root."
    },
    "shell": {
      "type": "string",
      "description": "Shell binary to launch. Defaults to the user's default shell."
    },
    "login": {
      "type": "boolean",
      "description": "Whether to run the shell with login semantics. Defaults to true."
    },
    "tty": {
      "type": "boolean",
      "description": "Whether to keep stdin open for follow-up write_stdin calls. Defaults to false."
    },
    "yield_time_ms": {
      "type": "integer",
      "description": "How long to wait in milliseconds for output before yielding control and backgrounding the process. Omit this for most commands."
    },
    "max_output_tokens": {
      "type": "integer",
      "description": "Maximum amount of output to return. Excess output will be truncated, and the full clean log remains available on disk. Omit this unless you want an override."
    }
  }
}`),
	},
	{
		ID:             ToolWriteStdin,
		Aliases:        nil,
		Description:    "Writes characters to an existing exec_command session and returns recent output. Use empty chars to poll.",
		DefaultEnabled: true,
		Contract: localContract(
			LocalRuntimeBuilderWriteStdin,
			RequestExposure{Enabled: true},
			transcript.ToolPresentationShell,
			transcript.ToolCallRenderBehaviorShell,
			false,
			shellToolCallMeta(ToolWriteStdin),
			formatGenericToolResult,
		),
		Schema: json.RawMessage(`{
  "type": "object",
  "additionalProperties": false,
  "required": ["session_id"],
  "properties": {
    "session_id": {
      "type": "integer",
      "description": "Identifier of the running exec_command session."
    },
    "chars": {
      "type": "string",
      "description": "Bytes to write to stdin. May be empty to poll for output."
    },
    "yield_time_ms": {
      "type": "integer",
      "description": "How long to wait in milliseconds for output before yielding."
    },
    "max_output_tokens": {
      "type": "integer",
      "description": "Optional maximum amount of output to return back. Excess output will be truncated."
    }
  }
}`),
	},
	{
		ID:             ToolViewImage,
		Aliases:        []string{"read_image"},
		Description:    "View a local image or PDF file by path. You will see PDFs as images (not OCR/text).",
		DefaultEnabled: true,
		Contract: localContract(
			LocalRuntimeBuilderViewImage,
			RequestExposure{Enabled: true, RequiresVision: true},
			transcript.ToolPresentationDefault,
			transcript.ToolCallRenderBehaviorDefault,
			false,
			defaultToolCallMeta(ToolViewImage),
			formatViewImageToolResult,
		),
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
		Description:    "Apply a freeform patch.",
		DefaultEnabled: true,
		Contract: localContract(
			LocalRuntimeBuilderPatch,
			RequestExposure{Enabled: true},
			transcript.ToolPresentationDefault,
			transcript.ToolCallRenderBehaviorDefault,
			true,
			patchToolCallMeta(ToolPatch),
			formatPatchToolResult,
		),
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
		Description:    "Ask the user a question. You should ask the user when planning your work or working to make product decisions, resolve ambiguities, define missing pieces that you cannot resolve by yourself, brainstorming with the user. You should ask the user a lot of questions when you're planning/brainstorming together to learn their desires, preferences, design, product vision, or implementation approach, and sometimes ask them questions when already working if you encounter a problem you can't resolve, a caveat, an undefined area that materially affects the result or direction of your work, etc. You should avoid asking the user obvious or harmless questions like 'Should I run tests?' or 'Where is file X?' which you can answer yourself. Each question pings the user, so treat it like messaging a coworker on Slack: unless they're actively chatting with you, pinging them could distract them. Stick to ONE question per this tool call, for multiple questions call this tool in parallel. Strive to provide multiple suggestions/options with every question if applicable, and choosing one recommended option you deem best for user goals.",
		DefaultEnabled: true,
		Contract: localContract(
			LocalRuntimeBuilderAskQuestion,
			RequestExposure{Enabled: true},
			transcript.ToolPresentationAskQuestion,
			transcript.ToolCallRenderBehaviorAskQuestion,
			false,
			askQuestionToolCallMeta(ToolAskQuestion),
			formatAskQuestionToolResult,
		),
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
      "description": "Optional choice suggestions. Omit this field when you want a freeform-only answer. If you provide >1 suggestions, provide recommended_option_index. Strive to give users the best, sensible options possible, following best-practices, guidelines, and common sense.",
      "items": {"type": "string"}
    },
    "recommended_option_index": {
      "type": "integer",
      "description": "Optional 1-based index of the recommended suggestion."
    }
  }
}`),
	},
	{
		ID:             ToolTriggerHandoff,
		Aliases:        nil,
		Description:    "Trigger a proactive handoff to another agent. Using this tool is allowed only after a developer message appears in transcript that enables this tool. Do not use this tool before that reminder. Its arguments are persisted and shown to the user in detail mode, so only include concise user-safe instructions or notes, never private reasoning or chain-of-thought.",
		DefaultEnabled: false,
		Contract: localContract(
			LocalRuntimeBuilderTriggerHandoff,
			RequestExposure{Enabled: true},
			transcript.ToolPresentationDefault,
			transcript.ToolCallRenderBehaviorDefault,
			false,
			triggerHandoffToolCallMeta(ToolTriggerHandoff),
			formatTriggerHandoffToolResult,
		),
		Schema: json.RawMessage(`{
  "type": "object",
  "additionalProperties": false,
  "properties": {
    "summarizer_prompt": {
      "type": "string",
      "description": "Optional extra instructions for the handoff summarizer. The summarizer already receives generic guidance on preserving workspace state and transcript context. Only include concise, user-safe task guidance here; never include private reasoning or chain-of-thought."
    },
    "future_agent_message": {
      "type": "string",
      "description": "Optional user-safe message to forward verbatim to the next agent in addition to the detailed summary of current work. Only include specific concise facts or the next immediate step, not generic guidance, conversation summary, or private reasoning."
    }
  }
}`),
	},
	{
		ID:             ToolWebSearch,
		Aliases:        nil,
		Description:    "Search the web for up-to-date external information. Use this when local workspace context is insufficient or the fact could be stale, or for information beyond your model knowledge cutoff. Prefer primary and official sources.",
		DefaultEnabled: true,
		Contract: hostedContract(
			RequestExposure{Enabled: false},
			transcript.ToolPresentationDefault,
			transcript.ToolCallRenderBehaviorDefault,
			false,
			true,
			webSearchToolCallMeta(ToolWebSearch),
			formatWebSearchToolResult,
			decodeHostedWebSearchOutput,
		),
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
		DefaultEnabled: false,
		Contract: localContract(
			LocalRuntimeBuilderMultiToolUseParallel,
			RequestExposure{Enabled: true},
			transcript.ToolPresentationDefault,
			transcript.ToolCallRenderBehaviorDefault,
			false,
			defaultToolCallMeta(ToolMultiToolUseParallel),
			formatGenericToolResult,
		),
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
		validateCatalogEntry(entry)
		definitions[entry.ID] = Definition{
			ID:          entry.ID,
			Description: entry.Description,
			Schema:      entry.Schema,
			contract:    entry.Contract,
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

func validateCatalogEntry(entry CatalogEntry) {
	if entry.Contract.Runtime.Availability == "" {
		panic("tool contract is missing runtime availability for " + string(entry.ID))
	}
	if entry.Contract.Runtime.Availability == RuntimeAvailabilityHosted && entry.Contract.Runtime.DecodeHostedOutput == nil {
		panic("hosted tool contract is missing hosted output decoder for " + string(entry.ID))
	}
	if entry.Contract.Runtime.Availability == RuntimeAvailabilityLocal && entry.Contract.Runtime.LocalBuilder == "" {
		panic("local tool contract is missing local runtime builder for " + string(entry.ID))
	}
	if entry.Contract.Runtime.Availability == RuntimeAvailabilityHosted && entry.Contract.Runtime.LocalBuilder != "" {
		panic("hosted tool contract must not declare a local runtime builder for " + string(entry.ID))
	}
	if entry.Contract.Transcript.BuildCallMeta == nil {
		panic("tool contract is missing transcript call metadata builder for " + string(entry.ID))
	}
	if entry.Contract.Transcript.FormatResult == nil {
		panic("tool contract is missing transcript result formatter for " + string(entry.ID))
	}
	if entry.Contract.Transcript.Presentation == "" {
		panic("tool contract is missing transcript presentation for " + string(entry.ID))
	}
	if entry.Contract.Transcript.RenderBehavior == "" {
		panic("tool contract is missing transcript render behavior for " + string(entry.ID))
	}
}

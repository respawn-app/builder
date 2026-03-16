package config

import (
	"encoding/json"
	"strconv"
	"strings"

	"builder/internal/tools"
)

const (
	defaultModel               = "gpt-5.3-codex"
	defaultThinkingLevel       = "high"
	defaultTheme               = "dark"
	defaultModelContextWindow  = 400_000
	defaultModelTimeoutSeconds = 400
	defaultShellTimeoutSeconds = 300
	defaultMinimumExecToBgSec  = 15
	defaultShellOutputMaxChars = 16_000
	defaultBGShellsOutput      = "default"
	defaultCompactionThreshold = 360_000
	defaultReviewerFrequency   = "off"
	defaultReviewerThinking    = "low"
	defaultReviewerTimeoutSec  = 60
	defaultReviewerSuggestions = 5
	defaultTUIAlternateScreen  = "auto"
	defaultCompactionMode      = "native"
)

func defaultSettings() Settings {
	enabled := map[tools.ID]bool{}
	for _, id := range tools.CatalogIDs() {
		enabled[id] = false
	}
	for _, id := range tools.DefaultEnabledToolIDs() {
		enabled[id] = true
	}
	return Settings{
		Model:                            defaultModel,
		ThinkingLevel:                    defaultThinkingLevel,
		ModelVerbosity:                   "",
		ModelCapabilities:                ModelCapabilitiesOverride{},
		Theme:                            defaultTheme,
		TUIAlternateScreen:               TUIAlternateScreenPolicy(defaultTUIAlternateScreen),
		NotificationMethod:               "auto",
		ToolPreambles:                    true,
		PriorityRequestMode:              false,
		WebSearch:                        "off",
		ProviderCapabilities:             ProviderCapabilitiesOverride{},
		Store:                            false,
		AllowNonCwdEdits:                 false,
		ModelContextWindow:               defaultModelContextWindow,
		ContextCompactionThresholdTokens: defaultCompactionThreshold,
		MinimumExecToBgSeconds:           defaultMinimumExecToBgSec,
		CompactionMode:                   CompactionMode(defaultCompactionMode),
		EnabledTools:                     enabled,
		ShellOutputMaxChars:              defaultShellOutputMaxChars,
		BGShellsOutput:                   BGShellsOutputMode(defaultBGShellsOutput),
		Timeouts: Timeouts{
			ModelRequestSeconds: defaultModelTimeoutSeconds,
			ShellDefaultSeconds: defaultShellTimeoutSeconds,
		},
		Reviewer: ReviewerSettings{
			Frequency:      defaultReviewerFrequency,
			Model:          "",
			ThinkingLevel:  defaultReviewerThinking,
			TimeoutSeconds: defaultReviewerTimeoutSec,
			MaxSuggestions: defaultReviewerSuggestions,
		},
	}
}

func defaultSettingsTOML() string {
	defaults := defaultSettings()
	toolDefaults := map[string]bool{}
	for _, id := range tools.CatalogIDs() {
		toolDefaults[string(id)] = defaults.EnabledTools[id]
	}
	payload := map[string]any{
		"model":           defaults.Model,
		"thinking_level":  defaults.ThinkingLevel,
		"model_verbosity": defaults.ModelVerbosity,
		"model_capabilities": map[string]bool{
			"supports_reasoning_effort": defaults.ModelCapabilities.SupportsReasoningEffort,
			"supports_vision_inputs":    defaults.ModelCapabilities.SupportsVisionInputs,
		},
		"theme":                 defaults.Theme,
		"tui_alternate_screen":  defaults.TUIAlternateScreen,
		"notification_method":   defaults.NotificationMethod,
		"tool_preambles":        defaults.ToolPreambles,
		"priority_request_mode": defaults.PriorityRequestMode,
		"web_search":            defaults.WebSearch,
		"openai_base_url":       defaults.OpenAIBaseURL,
		"provider_capabilities": map[string]any{
			"provider_id":                       defaults.ProviderCapabilities.ProviderID,
			"supports_responses_api":            defaults.ProviderCapabilities.SupportsResponsesAPI,
			"supports_responses_compact":        defaults.ProviderCapabilities.SupportsResponsesCompact,
			"supports_native_web_search":        defaults.ProviderCapabilities.SupportsNativeWebSearch,
			"supports_reasoning_encrypted":      defaults.ProviderCapabilities.SupportsReasoningEncrypted,
			"supports_server_side_context_edit": defaults.ProviderCapabilities.SupportsServerSideContextEdit,
			"is_openai_first_party":             defaults.ProviderCapabilities.IsOpenAIFirstParty,
		},
		"store":                               defaults.Store,
		"allow_non_cwd_edits":                 defaults.AllowNonCwdEdits,
		"model_context_window":                defaults.ModelContextWindow,
		"context_compaction_threshold_tokens": defaults.ContextCompactionThresholdTokens,
		"minimum_exec_to_bg_seconds":          defaults.MinimumExecToBgSeconds,
		"shell_output_max_chars":              defaults.ShellOutputMaxChars,
		"bg_shells_output":                    defaults.BGShellsOutput,
		"compaction_mode":                     defaults.CompactionMode,
		"tools":                               toolDefaults,
		"timeouts": map[string]int{
			"model_request_seconds": defaults.Timeouts.ModelRequestSeconds,
			"shell_default_seconds": defaults.Timeouts.ShellDefaultSeconds,
		},
		"reviewer": map[string]any{
			"frequency":       defaults.Reviewer.Frequency,
			"model":           "<inherits model when unset>",
			"thinking_level":  defaults.Reviewer.ThinkingLevel,
			"timeout_seconds": defaults.Reviewer.TimeoutSeconds,
			"max_suggestions": defaults.Reviewer.MaxSuggestions,
		},
		"persistence_root": DefaultPersistence,
	}
	encoded, _ := json.MarshalIndent(payload, "", "  ")
	out := "# builder settings\n" +
		"# edit and restart builder to apply changes\n\n" +
		"# Unknown keys are rejected to keep config changes explicit and safe.\n\n" +
		"# compaction_mode options:\n" +
		"# - native: provider-native compaction when available, fallback to local\n" +
		"# - local: force local summary compaction\n" +
		"# - none: disable both automatic and manual compaction\n\n" +
		"# bg_shells_output applies directly to exit code 0 background shells.\n" +
		"# Non-zero exits use verbose only when bg_shells_output=verbose; otherwise\n" +
		"# they fall back to default truncation.\n\n" +
		"# exec_command yield_time_ms values below minimum_exec_to_bg_seconds are\n" +
		"# clamped up and surfaced to the model as a warning before command output.\n\n" +
		"# This JSON block mirrors current defaults for readability:\n" +
		"# " + strings.ReplaceAll(string(encoded), "\n", "\n# ") + "\n\n" +
		"model = \"" + defaults.Model + "\"\n" +
		"thinking_level = \"" + defaults.ThinkingLevel + "\"\n" +
		"# Optional Responses API text verbosity for GPT-5-family OpenAI providers.\n" +
		"# Valid values: low, medium, high. Leave empty to let the provider default.\n" +
		"model_verbosity = \"" + string(defaults.ModelVerbosity) + "\"\n" +
		"theme = \"" + defaults.Theme + "\"\n" +
		"tui_alternate_screen = \"" + string(defaults.TUIAlternateScreen) + "\"\n" +
		"notification_method = \"" + defaults.NotificationMethod + "\"\n" +
		"# Known tradeoff: sessions started in headless mode never include intermediary-update\n" +
		"# instructions for their lifetime because the dispatch contract is locked on first use.\n" +
		"tool_preambles = " + strconv.FormatBool(defaults.ToolPreambles) + "\n" +
		"priority_request_mode = " + strconv.FormatBool(defaults.PriorityRequestMode) + "\n" +
		"web_search = \"" + defaults.WebSearch + "\"\n" +
		"openai_base_url = \"" + defaults.OpenAIBaseURL + "\"\n" +
		"store = " + strconv.FormatBool(defaults.Store) + "\n" +
		"allow_non_cwd_edits = " + strconv.FormatBool(defaults.AllowNonCwdEdits) + "\n" +
		"model_context_window = " + strconv.Itoa(defaults.ModelContextWindow) + "\n" +
		"context_compaction_threshold_tokens = " + strconv.Itoa(defaults.ContextCompactionThresholdTokens) + "\n" +
		"minimum_exec_to_bg_seconds = " + strconv.Itoa(defaults.MinimumExecToBgSeconds) + "\n" +
		"shell_output_max_chars = " + strconv.Itoa(defaults.ShellOutputMaxChars) + "\n" +
		"bg_shells_output = \"" + string(defaults.BGShellsOutput) + "\"\n" +
		"compaction_mode = \"" + string(defaults.CompactionMode) + "\"\n" +
		"persistence_root = \"" + DefaultPersistence + "\"\n\n" +
		"# Optional explicit capability overrides for custom/alias models. Uncomment only\n" +
		"# when the reviewed registry does not cover your configured model.\n" +
		"# [model_capabilities]\n" +
		"# supports_reasoning_effort = true\n" +
		"# supports_vision_inputs = true\n\n" +
		"# Optional explicit provider capability overrides. These are only needed for\n" +
		"# custom providers or stale built-in contracts. Keep them conservative to\n" +
		"# avoid unsupported provider-native features.\n" +
		"# [provider_capabilities]\n" +
		"# provider_id = \"custom-provider\"\n" +
		"# supports_responses_api = true\n" +
		"# supports_responses_compact = false\n" +
		"# supports_native_web_search = false\n" +
		"# supports_reasoning_encrypted = false\n" +
		"# supports_server_side_context_edit = false\n" +
		"# is_openai_first_party = false\n"
	out += "\n[tools]\n"
	for _, id := range tools.CatalogIDs() {
		out += strconv.Quote(string(id)) + " = " + strconv.FormatBool(defaults.EnabledTools[id]) + "\n"
	}
	out += "\n" +
		"[timeouts]\n" +
		"model_request_seconds = " + strconv.Itoa(defaults.Timeouts.ModelRequestSeconds) + "\n" +
		"shell_default_seconds = " + strconv.Itoa(defaults.Timeouts.ShellDefaultSeconds) + "\n\n" +
		"[reviewer]\n" +
		"frequency = \"" + defaults.Reviewer.Frequency + "\"\n" +
		"# model defaults to `model` when unset\n" +
		"thinking_level = \"" + defaults.Reviewer.ThinkingLevel + "\"\n" +
		"timeout_seconds = " + strconv.Itoa(defaults.Reviewer.TimeoutSeconds) + "\n" +
		"max_suggestions = " + strconv.Itoa(defaults.Reviewer.MaxSuggestions) + "\n"
	return out
}

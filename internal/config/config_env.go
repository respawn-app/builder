package config

import (
	"errors"
	"fmt"
	"strings"
)

type envLookup func(string) (string, bool)

func settingsOverlayFromEnv(lookup envLookup) (settingsOverlay, error) {
	overlay := settingsOverlay{}
	if v, ok := lookupTrimmedEnv(lookup, "BUILDER_MODEL"); ok {
		overlay.Model = stringPtr(v)
	}
	if v, ok := lookupTrimmedEnv(lookup, "BUILDER_THINKING_LEVEL"); ok {
		overlay.ThinkingLevel = stringPtr(v)
	}
	if reasoning, ok := lookupTrimmedEnv(lookup, "BUILDER_MODEL_SUPPORTS_REASONING_EFFORT"); ok {
		parsed, err := parseBoolString(reasoning, "BUILDER_MODEL_SUPPORTS_REASONING_EFFORT")
		if err != nil {
			return settingsOverlay{}, err
		}
		if overlay.ModelCapabilities == nil {
			overlay.ModelCapabilities = &ModelCapabilitiesOverride{}
		}
		overlay.ModelCapabilities.SupportsReasoningEffort = *parsed
	}
	if vision, ok := lookupTrimmedEnv(lookup, "BUILDER_MODEL_SUPPORTS_VISION_INPUTS"); ok {
		parsed, err := parseBoolString(vision, "BUILDER_MODEL_SUPPORTS_VISION_INPUTS")
		if err != nil {
			return settingsOverlay{}, err
		}
		if overlay.ModelCapabilities == nil {
			overlay.ModelCapabilities = &ModelCapabilitiesOverride{}
		}
		overlay.ModelCapabilities.SupportsVisionInputs = *parsed
	}
	if v, ok := lookupTrimmedEnv(lookup, "BUILDER_THEME"); ok {
		overlay.Theme = stringPtr(v)
	}
	if v, ok := lookupTrimmedEnv(lookup, "BUILDER_TUI_ALTERNATE_SCREEN"); ok {
		normalized := normalizeTUIAlternateScreenPolicy(v)
		overlay.TUIAlternateScreen = &normalized
	}
	if v, ok := lookupTrimmedEnv(lookup, "BUILDER_NOTIFICATION_METHOD"); ok {
		overlay.NotificationMethod = stringPtr(v)
	}
	if v, ok := lookupTrimmedEnv(lookup, "BUILDER_TOOL_PREAMBLES"); ok {
		parsed, err := parseBoolString(v, "BUILDER_TOOL_PREAMBLES")
		if err != nil {
			return settingsOverlay{}, err
		}
		overlay.ToolPreambles = parsed
	}
	if v, ok := lookupTrimmedEnv(lookup, "BUILDER_WEB_SEARCH"); ok {
		overlay.WebSearch = stringPtr(v)
	}
	if v, ok := lookupTrimmedEnv(lookup, "BUILDER_OPENAI_BASE_URL"); ok {
		overlay.OpenAIBaseURL = stringPtr(v)
	}
	if providerID, ok := lookupTrimmedEnv(lookup, "BUILDER_PROVIDER_CAPABILITY_ID"); ok {
		if overlay.ProviderCapabilities == nil {
			overlay.ProviderCapabilities = &ProviderCapabilitiesOverride{}
		}
		overlay.ProviderCapabilities.ProviderID = providerID
	}
	if v, ok := lookupTrimmedEnv(lookup, "BUILDER_PROVIDER_SUPPORTS_RESPONSES_API"); ok {
		parsed, err := parseBoolString(v, "BUILDER_PROVIDER_SUPPORTS_RESPONSES_API")
		if err != nil {
			return settingsOverlay{}, err
		}
		if overlay.ProviderCapabilities == nil {
			overlay.ProviderCapabilities = &ProviderCapabilitiesOverride{}
		}
		overlay.ProviderCapabilities.SupportsResponsesAPI = *parsed
	}
	if v, ok := lookupTrimmedEnv(lookup, "BUILDER_PROVIDER_SUPPORTS_RESPONSES_COMPACT"); ok {
		parsed, err := parseBoolString(v, "BUILDER_PROVIDER_SUPPORTS_RESPONSES_COMPACT")
		if err != nil {
			return settingsOverlay{}, err
		}
		if overlay.ProviderCapabilities == nil {
			overlay.ProviderCapabilities = &ProviderCapabilitiesOverride{}
		}
		overlay.ProviderCapabilities.SupportsResponsesCompact = *parsed
	}
	if v, ok := lookupTrimmedEnv(lookup, "BUILDER_PROVIDER_SUPPORTS_NATIVE_WEB_SEARCH"); ok {
		parsed, err := parseBoolString(v, "BUILDER_PROVIDER_SUPPORTS_NATIVE_WEB_SEARCH")
		if err != nil {
			return settingsOverlay{}, err
		}
		if overlay.ProviderCapabilities == nil {
			overlay.ProviderCapabilities = &ProviderCapabilitiesOverride{}
		}
		overlay.ProviderCapabilities.SupportsNativeWebSearch = *parsed
	}
	if v, ok := lookupTrimmedEnv(lookup, "BUILDER_PROVIDER_SUPPORTS_REASONING_ENCRYPTED"); ok {
		parsed, err := parseBoolString(v, "BUILDER_PROVIDER_SUPPORTS_REASONING_ENCRYPTED")
		if err != nil {
			return settingsOverlay{}, err
		}
		if overlay.ProviderCapabilities == nil {
			overlay.ProviderCapabilities = &ProviderCapabilitiesOverride{}
		}
		overlay.ProviderCapabilities.SupportsReasoningEncrypted = *parsed
	}
	if v, ok := lookupTrimmedEnv(lookup, "BUILDER_PROVIDER_SUPPORTS_SERVER_SIDE_CONTEXT_EDIT"); ok {
		parsed, err := parseBoolString(v, "BUILDER_PROVIDER_SUPPORTS_SERVER_SIDE_CONTEXT_EDIT")
		if err != nil {
			return settingsOverlay{}, err
		}
		if overlay.ProviderCapabilities == nil {
			overlay.ProviderCapabilities = &ProviderCapabilitiesOverride{}
		}
		overlay.ProviderCapabilities.SupportsServerSideContextEdit = *parsed
	}
	if v, ok := lookupTrimmedEnv(lookup, "BUILDER_PROVIDER_IS_OPENAI_FIRST_PARTY"); ok {
		parsed, err := parseBoolString(v, "BUILDER_PROVIDER_IS_OPENAI_FIRST_PARTY")
		if err != nil {
			return settingsOverlay{}, err
		}
		if overlay.ProviderCapabilities == nil {
			overlay.ProviderCapabilities = &ProviderCapabilitiesOverride{}
		}
		overlay.ProviderCapabilities.IsOpenAIFirstParty = *parsed
	}
	if v, ok := lookupTrimmedEnv(lookup, "BUILDER_STORE"); ok {
		parsed, err := parseBoolString(v, "BUILDER_STORE")
		if err != nil {
			return settingsOverlay{}, err
		}
		overlay.Store = parsed
	}
	if v, ok := lookupTrimmedEnv(lookup, "BUILDER_ALLOW_NON_CWD_EDITS"); ok {
		parsed, err := parseBoolString(v, "BUILDER_ALLOW_NON_CWD_EDITS")
		if err != nil {
			return settingsOverlay{}, err
		}
		overlay.AllowNonCwdEdits = parsed
	}
	if v, ok := lookupTrimmedEnv(lookup, "BUILDER_MODEL_CONTEXT_WINDOW"); ok {
		parsed, err := parsePositiveIntString(v, "BUILDER_MODEL_CONTEXT_WINDOW")
		if err != nil {
			return settingsOverlay{}, err
		}
		overlay.ModelContextWindow = parsed
	}
	if v, ok := lookupTrimmedEnv(lookup, "BUILDER_CONTEXT_COMPACTION_THRESHOLD_TOKENS"); ok {
		parsed, err := parsePositiveIntString(v, "BUILDER_CONTEXT_COMPACTION_THRESHOLD_TOKENS")
		if err != nil {
			return settingsOverlay{}, err
		}
		overlay.ContextCompactionThresholdTokens = parsed
	}
	if v, ok := lookupTrimmedEnv(lookup, "BUILDER_MINIMUM_EXEC_TO_BG_SECONDS"); ok {
		parsed, err := parsePositiveIntString(v, "BUILDER_MINIMUM_EXEC_TO_BG_SECONDS")
		if err != nil {
			return settingsOverlay{}, err
		}
		overlay.MinimumExecToBgSeconds = parsed
	}
	if raw, exists := lookup("BUILDER_USE_NATIVE_COMPACTION"); exists && strings.TrimSpace(raw) != "" {
		return settingsOverlay{}, errors.New("unsupported env var: BUILDER_USE_NATIVE_COMPACTION")
	}
	if v, ok := lookupTrimmedEnv(lookup, "BUILDER_COMPACTION_MODE"); ok {
		normalized := normalizeCompactionMode(v)
		overlay.CompactionMode = &normalized
	}
	if v, ok := lookupTrimmedEnv(lookup, "BUILDER_SHELL_OUTPUT_MAX_CHARS"); ok {
		parsed, err := parsePositiveIntString(v, "BUILDER_SHELL_OUTPUT_MAX_CHARS")
		if err != nil {
			return settingsOverlay{}, err
		}
		overlay.ShellOutputMaxChars = parsed
	}
	if v, ok := lookupTrimmedEnv(lookup, "BUILDER_BG_SHELLS_OUTPUT"); ok {
		normalized := BGShellsOutputMode(v)
		overlay.BGShellsOutput = &normalized
	}
	if v, ok := lookupTrimmedEnv(lookup, "BUILDER_REVIEWER_FREQUENCY"); ok {
		overlay.Reviewer.Frequency = stringPtr(v)
	}
	if v, ok := lookupTrimmedEnv(lookup, "BUILDER_REVIEWER_MODEL"); ok {
		overlay.Reviewer.Model = stringPtr(v)
	}
	if v, ok := lookupTrimmedEnv(lookup, "BUILDER_REVIEWER_THINKING_LEVEL"); ok {
		overlay.Reviewer.ThinkingLevel = stringPtr(v)
	}
	if v, ok := lookupTrimmedEnv(lookup, "BUILDER_REVIEWER_TIMEOUT_SECONDS"); ok {
		parsed, err := parsePositiveIntString(v, "BUILDER_REVIEWER_TIMEOUT_SECONDS")
		if err != nil {
			return settingsOverlay{}, err
		}
		overlay.Reviewer.TimeoutSeconds = parsed
	}
	if v, ok := lookupTrimmedEnv(lookup, "BUILDER_REVIEWER_MAX_SUGGESTIONS"); ok {
		parsed, err := parsePositiveIntString(v, "BUILDER_REVIEWER_MAX_SUGGESTIONS")
		if err != nil {
			return settingsOverlay{}, err
		}
		overlay.Reviewer.MaxSuggestions = parsed
	}
	if v, ok := lookupTrimmedEnv(lookup, "BUILDER_MODEL_TIMEOUT_SECONDS"); ok {
		parsed, err := parsePositiveIntString(v, "BUILDER_MODEL_TIMEOUT_SECONDS")
		if err != nil {
			return settingsOverlay{}, err
		}
		overlay.Timeouts.ModelRequestSeconds = parsed
	}
	if v, ok := lookupTrimmedEnv(lookup, "BUILDER_SHELL_TIMEOUT_SECONDS"); ok {
		parsed, err := parsePositiveIntString(v, "BUILDER_SHELL_TIMEOUT_SECONDS")
		if err != nil {
			return settingsOverlay{}, err
		}
		overlay.Timeouts.ShellDefaultSeconds = parsed
	} else if v, ok := lookupTrimmedEnv(lookup, "BUILDER_BASH_TIMEOUT_SECONDS"); ok {
		parsed, err := parsePositiveIntString(v, "BUILDER_BASH_TIMEOUT_SECONDS")
		if err != nil {
			return settingsOverlay{}, err
		}
		overlay.Timeouts.ShellDefaultSeconds = parsed
	}
	if v, ok := lookupTrimmedEnv(lookup, "BUILDER_TOOLS"); ok {
		enabled, err := parseEnabledToolsCSV(v)
		if err != nil {
			return settingsOverlay{}, fmt.Errorf("invalid BUILDER_TOOLS: %w", err)
		}
		overlay.EnabledTools = resetEnabledToolMap(enabled)
	}
	if v, ok := lookupTrimmedEnv(lookup, "BUILDER_PERSISTENCE_ROOT"); ok {
		overlay.PersistenceRoot = stringPtr(v)
	}
	return overlay, nil
}

func lookupTrimmedEnv(lookup envLookup, key string) (string, bool) {
	raw, ok := lookup(key)
	if !ok {
		return "", false
	}
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", false
	}
	return trimmed, true
}

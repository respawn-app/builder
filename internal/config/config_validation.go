package config

import (
	"errors"
	"fmt"
	"strings"

	"builder/internal/theme"
)

func validateSettings(v Settings, sources map[string]string) error {
	return configRegistry.validate(settingsState{Settings: v}, sources)
}

func validateModelNotEmpty(state settingsState, _ map[string]string) error {
	if strings.TrimSpace(state.Settings.Model) == "" {
		return errors.New("settings model must not be empty")
	}
	return nil
}

func validateProviderOverrideRequiresModel(state settingsState, sources map[string]string) error {
	if strings.TrimSpace(state.Settings.ProviderOverride) != "" && strings.TrimSpace(sources["model"]) == "default" {
		return fmt.Errorf("provider_override requires an explicit model override; set model alongside provider_override")
	}
	return nil
}

func validateProviderOverrideValue(state settingsState, _ map[string]string) error {
	switch normalizeProviderOverride(state.Settings.ProviderOverride) {
	case "", "openai", "anthropic":
		return nil
	default:
		return fmt.Errorf("invalid provider_override %q (expected openai|anthropic)", state.Settings.ProviderOverride)
	}
}

func validateOpenAIBaseURL(state settingsState, _ map[string]string) error {
	provider := normalizeProviderOverride(state.Settings.ProviderOverride)
	if strings.TrimSpace(state.Settings.OpenAIBaseURL) != "" && provider != "" && provider != "openai" {
		return fmt.Errorf("provider_override %q conflicts with openai_base_url; openai_base_url requires provider_override=openai or unset", state.Settings.ProviderOverride)
	}
	return nil
}

func validateProviderCapabilitiesProviderID(state settingsState, _ map[string]string) error {
	capabilities := state.Settings.ProviderCapabilities
	if strings.TrimSpace(capabilities.ProviderID) != "" {
		return nil
	}
	if capabilities.SupportsResponsesAPI || capabilities.SupportsResponsesCompact || capabilities.SupportsNativeWebSearch || capabilities.SupportsReasoningEncrypted || capabilities.SupportsServerSideContextEdit || capabilities.IsOpenAIFirstParty {
		return fmt.Errorf("provider_capabilities.provider_id must not be empty when provider capability overrides are set")
	}
	return nil
}

func validateThinkingLevel(state settingsState, _ map[string]string) error {
	// Custom/provider-specific thinking levels are intentionally allowed.
	return nil
}

func validateModelVerbosity(state settingsState, _ map[string]string) error {
	switch strings.ToLower(strings.TrimSpace(string(state.Settings.ModelVerbosity))) {
	case "", "low", "medium", "high":
		return nil
	default:
		return fmt.Errorf("invalid model_verbosity %q (expected low|medium|high)", state.Settings.ModelVerbosity)
	}
}

func validateTheme(state settingsState, _ map[string]string) error {
	switch theme.Normalize(state.Settings.Theme) {
	case theme.Auto, theme.Light, theme.Dark:
		return nil
	default:
		return fmt.Errorf("invalid theme %q (expected auto|light|dark)", state.Settings.Theme)
	}
}

func validateTUIAlternateScreen(state settingsState, _ map[string]string) error {
	switch strings.ToLower(strings.TrimSpace(string(state.Settings.TUIAlternateScreen))) {
	case "auto", "always", "never":
		return nil
	default:
		return fmt.Errorf("invalid tui_alternate_screen %q (expected auto|always|never)", state.Settings.TUIAlternateScreen)
	}
}

func validateNotificationMethod(state settingsState, _ map[string]string) error {
	switch strings.ToLower(strings.TrimSpace(state.Settings.NotificationMethod)) {
	case "auto", "osc9", "bel":
		return nil
	default:
		return fmt.Errorf("invalid notification_method %q (expected auto|osc9|bel)", state.Settings.NotificationMethod)
	}
}

func validateWebSearch(state settingsState, _ map[string]string) error {
	switch strings.ToLower(strings.TrimSpace(state.Settings.WebSearch)) {
	case "off", "native":
		return nil
	case "custom":
		return fmt.Errorf("web_search=custom is not implemented yet")
	default:
		return fmt.Errorf("invalid web_search %q (expected off|native|custom)", state.Settings.WebSearch)
	}
}

func validateTimeouts(state settingsState, _ map[string]string) error {
	if state.Settings.Timeouts.ModelRequestSeconds <= 0 {
		return fmt.Errorf("timeouts.model_request_seconds must be > 0")
	}
	if state.Settings.Timeouts.ShellDefaultSeconds <= 0 {
		return fmt.Errorf("timeouts.shell_default_seconds must be > 0")
	}
	return nil
}

func validateShellOutputMaxChars(state settingsState, _ map[string]string) error {
	if state.Settings.ShellOutputMaxChars <= 0 {
		return fmt.Errorf("shell_output_max_chars must be > 0")
	}
	return nil
}

func validateMinimumExecToBgSeconds(state settingsState, _ map[string]string) error {
	if state.Settings.MinimumExecToBgSeconds <= 0 {
		return fmt.Errorf("minimum_exec_to_bg_seconds must be > 0")
	}
	return nil
}

func validateBGShellsOutput(state settingsState, _ map[string]string) error {
	switch strings.ToLower(strings.TrimSpace(string(state.Settings.BGShellsOutput))) {
	case "default", "verbose", "concise":
		return nil
	default:
		return fmt.Errorf("invalid bg_shells_output %q (expected default|verbose|concise)", state.Settings.BGShellsOutput)
	}
}

func validateContextWindow(state settingsState, _ map[string]string) error {
	if state.Settings.ContextCompactionThresholdTokens <= 0 {
		return fmt.Errorf("context_compaction_threshold_tokens must be > 0")
	}
	if state.Settings.ModelContextWindow <= 0 {
		return fmt.Errorf("model_context_window must be > 0")
	}
	if state.Settings.ContextCompactionThresholdTokens >= state.Settings.ModelContextWindow {
		return fmt.Errorf("context_compaction_threshold_tokens must be < model_context_window")
	}
	if state.Settings.PreSubmitCompactionLeadTokens <= 0 {
		return fmt.Errorf("pre_submit_compaction_lead_tokens must be > 0")
	}
	return nil
}

func validateCompactionMode(state settingsState, _ map[string]string) error {
	switch strings.ToLower(strings.TrimSpace(string(state.Settings.CompactionMode))) {
	case "native", "local", "none":
		return nil
	default:
		return fmt.Errorf("invalid compaction_mode %q (expected native|local|none)", state.Settings.CompactionMode)
	}
}

func validateReviewer(state settingsState, _ map[string]string) error {
	reviewer := state.Settings.Reviewer
	switch strings.ToLower(strings.TrimSpace(reviewer.Frequency)) {
	case "off", "all", "edits":
	default:
		return fmt.Errorf("invalid reviewer.frequency %q (expected off|all|edits)", reviewer.Frequency)
	}
	if strings.TrimSpace(reviewer.Model) == "" {
		return fmt.Errorf("reviewer.model must not be empty")
	}
	if reviewer.TimeoutSeconds <= 0 {
		return fmt.Errorf("reviewer.timeout_seconds must be > 0")
	}
	return nil
}

func normalizeTUIAlternateScreenPolicy(raw string) TUIAlternateScreenPolicy {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "auto":
		return TUIAlternateScreenAuto
	case "always":
		return TUIAlternateScreenAlways
	case "never":
		return TUIAlternateScreenNever
	default:
		return TUIAlternateScreenPolicy(strings.TrimSpace(raw))
	}
}

func normalizeCompactionMode(raw string) CompactionMode {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "native":
		return CompactionModeNative
	case "local":
		return CompactionModeLocal
	case "none":
		return CompactionModeNone
	default:
		return CompactionMode(strings.TrimSpace(raw))
	}
}

func normalizeModelVerbosity(raw string) ModelVerbosity {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "low":
		return ModelVerbosityLow
	case "medium":
		return ModelVerbosityMedium
	case "high":
		return ModelVerbosityHigh
	default:
		return ModelVerbosity(strings.TrimSpace(raw))
	}
}

func normalizeProviderOverride(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

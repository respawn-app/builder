package config

import (
	"errors"
	"fmt"
	"strings"

	"builder/internal/tools"
)

func validateSettings(v Settings) error {
	if strings.TrimSpace(v.Model) == "" {
		return errors.New("settings model must not be empty")
	}
	if strings.TrimSpace(v.ProviderCapabilities.ProviderID) == "" {
		if v.ProviderCapabilities.SupportsResponsesAPI || v.ProviderCapabilities.SupportsResponsesCompact || v.ProviderCapabilities.SupportsNativeWebSearch || v.ProviderCapabilities.SupportsReasoningEncrypted || v.ProviderCapabilities.SupportsServerSideContextEdit || v.ProviderCapabilities.IsOpenAIFirstParty {
			return fmt.Errorf("provider_capabilities.provider_id must not be empty when provider capability overrides are set")
		}
	}
	switch strings.ToLower(strings.TrimSpace(v.ThinkingLevel)) {
	case "low", "medium", "high", "xhigh":
	default:
		return fmt.Errorf("invalid thinking_level %q (expected low|medium|high|xhigh)", v.ThinkingLevel)
	}
	if strings.EqualFold(strings.TrimSpace(v.Theme), "light") || strings.EqualFold(strings.TrimSpace(v.Theme), "dark") {
		// ok
	} else {
		return fmt.Errorf("invalid theme %q (expected light|dark)", v.Theme)
	}
	switch strings.ToLower(strings.TrimSpace(string(v.TUIAlternateScreen))) {
	case "auto", "always", "never":
	default:
		return fmt.Errorf("invalid tui_alternate_screen %q (expected auto|always|never)", v.TUIAlternateScreen)
	}
	switch strings.ToLower(strings.TrimSpace(v.NotificationMethod)) {
	case "auto", "osc9", "bel":
	default:
		return fmt.Errorf("invalid notification_method %q (expected auto|osc9|bel)", v.NotificationMethod)
	}
	switch strings.ToLower(strings.TrimSpace(v.WebSearch)) {
	case "off", "native":
	case "custom":
		return fmt.Errorf("web_search=custom is not implemented yet")
	default:
		return fmt.Errorf("invalid web_search %q (expected off|native|custom)", v.WebSearch)
	}
	if v.Timeouts.ModelRequestSeconds <= 0 {
		return fmt.Errorf("timeouts.model_request_seconds must be > 0")
	}
	if v.Timeouts.ShellDefaultSeconds <= 0 {
		return fmt.Errorf("timeouts.shell_default_seconds must be > 0")
	}
	if v.ShellOutputMaxChars <= 0 {
		return fmt.Errorf("shell_output_max_chars must be > 0")
	}
	if v.MinimumExecToBgSeconds <= 0 {
		return fmt.Errorf("minimum_exec_to_bg_seconds must be > 0")
	}
	switch strings.ToLower(strings.TrimSpace(string(v.BGShellsOutput))) {
	case "default", "verbose", "concise":
	default:
		return fmt.Errorf("invalid bg_shells_output %q (expected default|verbose|concise)", v.BGShellsOutput)
	}
	if v.ContextCompactionThresholdTokens <= 0 {
		return fmt.Errorf("context_compaction_threshold_tokens must be > 0")
	}
	if v.ModelContextWindow <= 0 {
		return fmt.Errorf("model_context_window must be > 0")
	}
	if v.ContextCompactionThresholdTokens >= v.ModelContextWindow {
		return fmt.Errorf("context_compaction_threshold_tokens must be < model_context_window")
	}
	switch strings.ToLower(strings.TrimSpace(string(v.CompactionMode))) {
	case "native", "local", "none":
	default:
		return fmt.Errorf("invalid compaction_mode %q (expected native|local|none)", v.CompactionMode)
	}
	for _, id := range tools.CatalogIDs() {
		if _, ok := v.EnabledTools[id]; !ok {
			v.EnabledTools[id] = false
		}
	}
	switch strings.ToLower(strings.TrimSpace(v.Reviewer.Frequency)) {
	case "off", "all", "edits":
	default:
		return fmt.Errorf("invalid reviewer.frequency %q (expected off|all|edits)", v.Reviewer.Frequency)
	}
	switch strings.ToLower(strings.TrimSpace(v.Reviewer.ThinkingLevel)) {
	case "low", "medium", "high", "xhigh":
	default:
		return fmt.Errorf("invalid reviewer.thinking_level %q (expected low|medium|high|xhigh)", v.Reviewer.ThinkingLevel)
	}
	if strings.TrimSpace(v.Reviewer.Model) == "" {
		return fmt.Errorf("reviewer.model must not be empty")
	}
	if v.Reviewer.TimeoutSeconds <= 0 {
		return fmt.Errorf("reviewer.timeout_seconds must be > 0")
	}
	if v.Reviewer.MaxSuggestions <= 0 {
		return fmt.Errorf("reviewer.max_suggestions must be > 0")
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

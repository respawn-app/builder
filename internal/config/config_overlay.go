package config

import (
	"fmt"
	"strconv"
	"strings"

	"builder/internal/tools"
)

type settingsOverlay struct {
	Model                            *string
	ThinkingLevel                    *string
	ModelVerbosity                   *ModelVerbosity
	ModelCapabilities                *ModelCapabilitiesOverride
	Theme                            *string
	TUIAlternateScreen               *TUIAlternateScreenPolicy
	NotificationMethod               *string
	ToolPreambles                    *bool
	PriorityRequestMode              *bool
	WebSearch                        *string
	OpenAIBaseURL                    *string
	ProviderCapabilities             *ProviderCapabilitiesOverride
	Store                            *bool
	AllowNonCwdEdits                 *bool
	ModelContextWindow               *int
	ContextCompactionThresholdTokens *int
	MinimumExecToBgSeconds           *int
	CompactionMode                   *CompactionMode
	EnabledTools                     map[tools.ID]bool
	Timeouts                         timeoutsOverlay
	ShellOutputMaxChars              *int
	BGShellsOutput                   *BGShellsOutputMode
	Reviewer                         reviewerOverlay
	PersistenceRoot                  *string
}

type timeoutsOverlay struct {
	ModelRequestSeconds *int
	ShellDefaultSeconds *int
}

type reviewerOverlay struct {
	Frequency      *string
	Model          *string
	ThinkingLevel  *string
	TimeoutSeconds *int
	MaxSuggestions *int
}

func defaultSourceMap() map[string]string {
	sources := map[string]string{
		"model":                               "default",
		"thinking_level":                      "default",
		"model_verbosity":                     "default",
		"model_capabilities":                  "default",
		"theme":                               "default",
		"tui_alternate_screen":                "default",
		"notification_method":                 "default",
		"tool_preambles":                      "default",
		"priority_request_mode":               "default",
		"web_search":                          "default",
		"openai_base_url":                     "default",
		"provider_capabilities":               "default",
		"store":                               "default",
		"allow_non_cwd_edits":                 "default",
		"model_context_window":                "default",
		"context_compaction_threshold_tokens": "default",
		"minimum_exec_to_bg_seconds":          "default",
		"compaction_mode":                     "default",
		"shell_output_max_chars":              "default",
		"bg_shells_output":                    "default",
		"timeouts.model_request":              "default",
		"timeouts.shell_default":              "default",
		"reviewer.frequency":                  "default",
		"reviewer.model":                      "default",
		"reviewer.thinking_level":             "default",
		"reviewer.timeout_seconds":            "default",
		"reviewer.max_suggestions":            "default",
	}
	for _, id := range tools.CatalogIDs() {
		sources["tools."+string(id)] = "default"
	}
	return sources
}

func settingsOverlayFromFile(cfg fileSettings, settingsPath string) (settingsOverlay, error) {
	overlay := settingsOverlay{}
	if v := strings.TrimSpace(cfg.Model); v != "" {
		overlay.Model = &v
	}
	if v := strings.TrimSpace(cfg.ThinkingLevel); v != "" {
		overlay.ThinkingLevel = &v
	}
	if v := strings.TrimSpace(cfg.ModelVerbosity); v != "" {
		normalized := normalizeModelVerbosity(v)
		overlay.ModelVerbosity = &normalized
	}
	if cfg.ModelCapabilities.SupportsReasoningEffort != nil || cfg.ModelCapabilities.SupportsVisionInputs != nil {
		overlay.ModelCapabilities = &ModelCapabilitiesOverride{
			SupportsReasoningEffort: cfg.ModelCapabilities.SupportsReasoningEffort != nil && *cfg.ModelCapabilities.SupportsReasoningEffort,
			SupportsVisionInputs:    cfg.ModelCapabilities.SupportsVisionInputs != nil && *cfg.ModelCapabilities.SupportsVisionInputs,
		}
	}
	if v := strings.TrimSpace(cfg.Theme); v != "" {
		overlay.Theme = &v
	}
	if v := strings.TrimSpace(cfg.TUIAlternateScreen); v != "" {
		normalized := normalizeTUIAlternateScreenPolicy(v)
		overlay.TUIAlternateScreen = &normalized
	}
	if v := strings.TrimSpace(cfg.NotificationMethod); v != "" {
		overlay.NotificationMethod = &v
	}
	if cfg.ToolPreambles != nil {
		overlay.ToolPreambles = cfg.ToolPreambles
	}
	if cfg.PriorityRequestMode != nil {
		overlay.PriorityRequestMode = cfg.PriorityRequestMode
	}
	if v := strings.TrimSpace(cfg.WebSearch); v != "" {
		overlay.WebSearch = &v
	}
	if v := strings.TrimSpace(cfg.OpenAIBaseURL); v != "" {
		overlay.OpenAIBaseURL = &v
	}
	if strings.TrimSpace(cfg.ProviderCapabilities.ProviderID) != "" ||
		cfg.ProviderCapabilities.SupportsResponsesAPI != nil ||
		cfg.ProviderCapabilities.SupportsResponsesCompact != nil ||
		cfg.ProviderCapabilities.SupportsNativeWebSearch != nil ||
		cfg.ProviderCapabilities.SupportsReasoningEncrypted != nil ||
		cfg.ProviderCapabilities.SupportsServerSideContextEdit != nil ||
		cfg.ProviderCapabilities.IsOpenAIFirstParty != nil {
		overlay.ProviderCapabilities = &ProviderCapabilitiesOverride{
			ProviderID:                    strings.TrimSpace(cfg.ProviderCapabilities.ProviderID),
			SupportsResponsesAPI:          cfg.ProviderCapabilities.SupportsResponsesAPI != nil && *cfg.ProviderCapabilities.SupportsResponsesAPI,
			SupportsResponsesCompact:      cfg.ProviderCapabilities.SupportsResponsesCompact != nil && *cfg.ProviderCapabilities.SupportsResponsesCompact,
			SupportsNativeWebSearch:       cfg.ProviderCapabilities.SupportsNativeWebSearch != nil && *cfg.ProviderCapabilities.SupportsNativeWebSearch,
			SupportsReasoningEncrypted:    cfg.ProviderCapabilities.SupportsReasoningEncrypted != nil && *cfg.ProviderCapabilities.SupportsReasoningEncrypted,
			SupportsServerSideContextEdit: cfg.ProviderCapabilities.SupportsServerSideContextEdit != nil && *cfg.ProviderCapabilities.SupportsServerSideContextEdit,
			IsOpenAIFirstParty:            cfg.ProviderCapabilities.IsOpenAIFirstParty != nil && *cfg.ProviderCapabilities.IsOpenAIFirstParty,
		}
	}
	if cfg.Store != nil {
		overlay.Store = cfg.Store
	}
	if cfg.AllowNonCwdEdits != nil {
		overlay.AllowNonCwdEdits = cfg.AllowNonCwdEdits
	}
	if cfg.ModelContextWindow > 0 {
		overlay.ModelContextWindow = intPtr(cfg.ModelContextWindow)
	}
	if cfg.ContextCompactionThresholdTokens > 0 {
		overlay.ContextCompactionThresholdTokens = intPtr(cfg.ContextCompactionThresholdTokens)
	}
	if cfg.MinimumExecToBgSeconds > 0 {
		overlay.MinimumExecToBgSeconds = intPtr(cfg.MinimumExecToBgSeconds)
	}
	if v := strings.TrimSpace(cfg.CompactionMode); v != "" {
		normalized := normalizeCompactionMode(v)
		overlay.CompactionMode = &normalized
	}
	if cfg.ShellOutputMaxChars > 0 {
		overlay.ShellOutputMaxChars = intPtr(cfg.ShellOutputMaxChars)
	}
	if v := strings.TrimSpace(cfg.BGShellsOutput); v != "" {
		normalized := BGShellsOutputMode(v)
		overlay.BGShellsOutput = &normalized
	}
	if v := strings.TrimSpace(cfg.Reviewer.Frequency); v != "" {
		overlay.Reviewer.Frequency = &v
	}
	if v := strings.TrimSpace(cfg.Reviewer.Model); v != "" {
		overlay.Reviewer.Model = &v
	}
	if v := strings.TrimSpace(cfg.Reviewer.ThinkingLevel); v != "" {
		overlay.Reviewer.ThinkingLevel = &v
	}
	if cfg.Reviewer.TimeoutSeconds > 0 {
		overlay.Reviewer.TimeoutSeconds = intPtr(cfg.Reviewer.TimeoutSeconds)
	}
	if cfg.Reviewer.MaxSuggestions > 0 {
		overlay.Reviewer.MaxSuggestions = intPtr(cfg.Reviewer.MaxSuggestions)
	}
	if cfg.Timeouts.ModelRequestSeconds > 0 {
		overlay.Timeouts.ModelRequestSeconds = intPtr(cfg.Timeouts.ModelRequestSeconds)
	}
	if cfg.Timeouts.ShellDefaultSeconds > 0 {
		overlay.Timeouts.ShellDefaultSeconds = intPtr(cfg.Timeouts.ShellDefaultSeconds)
	} else if cfg.Timeouts.BashDefaultSeconds > 0 {
		overlay.Timeouts.ShellDefaultSeconds = intPtr(cfg.Timeouts.BashDefaultSeconds)
	}
	if len(cfg.Tools) > 0 {
		overlay.EnabledTools = make(map[tools.ID]bool, len(cfg.Tools))
		for k, v := range cfg.Tools {
			id, ok := tools.ParseID(strings.TrimSpace(k))
			if !ok {
				return settingsOverlay{}, fmt.Errorf("invalid tools key in %s: %q", settingsPath, k)
			}
			overlay.EnabledTools[id] = v
		}
	}
	if v := strings.TrimSpace(cfg.PersistenceRoot); v != "" {
		overlay.PersistenceRoot = &v
	}
	return overlay, nil
}

func settingsOverlayFromCLI(opts LoadOptions) (settingsOverlay, error) {
	overlay := settingsOverlay{}
	if v := strings.TrimSpace(opts.Model); v != "" {
		overlay.Model = &v
	}
	if v := strings.TrimSpace(opts.ThinkingLevel); v != "" {
		overlay.ThinkingLevel = &v
	}
	if v := strings.TrimSpace(opts.Theme); v != "" {
		overlay.Theme = &v
	}
	if v := strings.TrimSpace(opts.OpenAIBaseURL); v != "" {
		overlay.OpenAIBaseURL = &v
	}
	if opts.ModelTimeoutSeconds > 0 {
		overlay.Timeouts.ModelRequestSeconds = intPtr(opts.ModelTimeoutSeconds)
	}
	if opts.ShellTimeoutSeconds > 0 {
		overlay.Timeouts.ShellDefaultSeconds = intPtr(opts.ShellTimeoutSeconds)
	}
	if v := strings.TrimSpace(opts.Tools); v != "" {
		enabled, err := parseEnabledToolsCSV(v)
		if err != nil {
			return settingsOverlay{}, fmt.Errorf("invalid tools flag: %w", err)
		}
		overlay.EnabledTools = resetEnabledToolMap(enabled)
	}
	return overlay, nil
}

func applySettingsOverlay(settings *Settings, persistenceRoot *string, persistenceSource *string, sources map[string]string, overlay settingsOverlay, source string) {
	if overlay.Model != nil {
		settings.Model = *overlay.Model
		sources["model"] = source
	}
	if overlay.ThinkingLevel != nil {
		settings.ThinkingLevel = *overlay.ThinkingLevel
		sources["thinking_level"] = source
	}
	if overlay.ModelVerbosity != nil {
		settings.ModelVerbosity = *overlay.ModelVerbosity
		sources["model_verbosity"] = source
	}
	if overlay.ModelCapabilities != nil {
		settings.ModelCapabilities = *overlay.ModelCapabilities
		sources["model_capabilities"] = source
	}
	if overlay.Theme != nil {
		settings.Theme = *overlay.Theme
		sources["theme"] = source
	}
	if overlay.TUIAlternateScreen != nil {
		settings.TUIAlternateScreen = *overlay.TUIAlternateScreen
		sources["tui_alternate_screen"] = source
	}
	if overlay.NotificationMethod != nil {
		settings.NotificationMethod = *overlay.NotificationMethod
		sources["notification_method"] = source
	}
	if overlay.ToolPreambles != nil {
		settings.ToolPreambles = *overlay.ToolPreambles
		sources["tool_preambles"] = source
	}
	if overlay.PriorityRequestMode != nil {
		settings.PriorityRequestMode = *overlay.PriorityRequestMode
		sources["priority_request_mode"] = source
	}
	if overlay.WebSearch != nil {
		settings.WebSearch = *overlay.WebSearch
		sources["web_search"] = source
	}
	if overlay.OpenAIBaseURL != nil {
		settings.OpenAIBaseURL = *overlay.OpenAIBaseURL
		sources["openai_base_url"] = source
	}
	if overlay.ProviderCapabilities != nil {
		settings.ProviderCapabilities = *overlay.ProviderCapabilities
		sources["provider_capabilities"] = source
	}
	if overlay.Store != nil {
		settings.Store = *overlay.Store
		sources["store"] = source
	}
	if overlay.AllowNonCwdEdits != nil {
		settings.AllowNonCwdEdits = *overlay.AllowNonCwdEdits
		sources["allow_non_cwd_edits"] = source
	}
	if overlay.ModelContextWindow != nil {
		settings.ModelContextWindow = *overlay.ModelContextWindow
		sources["model_context_window"] = source
	}
	if overlay.ContextCompactionThresholdTokens != nil {
		settings.ContextCompactionThresholdTokens = *overlay.ContextCompactionThresholdTokens
		sources["context_compaction_threshold_tokens"] = source
	}
	if overlay.MinimumExecToBgSeconds != nil {
		settings.MinimumExecToBgSeconds = *overlay.MinimumExecToBgSeconds
		sources["minimum_exec_to_bg_seconds"] = source
	}
	if overlay.CompactionMode != nil {
		settings.CompactionMode = *overlay.CompactionMode
		sources["compaction_mode"] = source
	}
	if overlay.ShellOutputMaxChars != nil {
		settings.ShellOutputMaxChars = *overlay.ShellOutputMaxChars
		sources["shell_output_max_chars"] = source
	}
	if overlay.BGShellsOutput != nil {
		settings.BGShellsOutput = *overlay.BGShellsOutput
		sources["bg_shells_output"] = source
	}
	if overlay.Reviewer.Frequency != nil {
		settings.Reviewer.Frequency = *overlay.Reviewer.Frequency
		sources["reviewer.frequency"] = source
	}
	if overlay.Reviewer.Model != nil {
		settings.Reviewer.Model = *overlay.Reviewer.Model
		sources["reviewer.model"] = source
	}
	if overlay.Reviewer.ThinkingLevel != nil {
		settings.Reviewer.ThinkingLevel = *overlay.Reviewer.ThinkingLevel
		sources["reviewer.thinking_level"] = source
	}
	if overlay.Reviewer.TimeoutSeconds != nil {
		settings.Reviewer.TimeoutSeconds = *overlay.Reviewer.TimeoutSeconds
		sources["reviewer.timeout_seconds"] = source
	}
	if overlay.Reviewer.MaxSuggestions != nil {
		settings.Reviewer.MaxSuggestions = *overlay.Reviewer.MaxSuggestions
		sources["reviewer.max_suggestions"] = source
	}
	if overlay.Timeouts.ModelRequestSeconds != nil {
		settings.Timeouts.ModelRequestSeconds = *overlay.Timeouts.ModelRequestSeconds
		sources["timeouts.model_request"] = source
	}
	if overlay.Timeouts.ShellDefaultSeconds != nil {
		settings.Timeouts.ShellDefaultSeconds = *overlay.Timeouts.ShellDefaultSeconds
		sources["timeouts.shell_default"] = source
	}
	for id, enabled := range overlay.EnabledTools {
		settings.EnabledTools[id] = enabled
		sources["tools."+string(id)] = source
	}
	if overlay.PersistenceRoot != nil {
		*persistenceRoot = *overlay.PersistenceRoot
		*persistenceSource = source
	}
}

func inheritReviewerModel(settings *Settings) {
	if strings.TrimSpace(settings.Reviewer.Model) == "" {
		settings.Reviewer.Model = settings.Model
	}
}

func parseEnabledToolsCSV(raw string) ([]tools.ID, error) {
	parts := strings.Split(raw, ",")
	seen := map[tools.ID]bool{}
	out := make([]tools.ID, 0, len(parts))
	for _, p := range parts {
		name := strings.TrimSpace(p)
		if name == "" {
			continue
		}
		id, ok := tools.ParseID(name)
		if !ok {
			return nil, fmt.Errorf("unknown tool %q", name)
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out, nil
}

func resetEnabledToolMap(enabled []tools.ID) map[tools.ID]bool {
	out := make(map[tools.ID]bool, len(tools.CatalogIDs()))
	for _, id := range tools.CatalogIDs() {
		out[id] = false
	}
	for _, id := range enabled {
		out[id] = true
	}
	return out
}

func withPersistenceSource(s map[string]string, persistence string) map[string]string {
	out := map[string]string{}
	for k, v := range s {
		out[k] = v
	}
	out["persistence_root"] = persistence
	return out
}

func stringPtr(value string) *string {
	return &value
}

func intPtr(value int) *int {
	return &value
}

func boolPtr(value bool) *bool {
	return &value
}

func parseBoolString(raw string, envName string) (*bool, error) {
	parsed, err := strconv.ParseBool(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid %s: %q", envName, raw)
	}
	return &parsed, nil
}

func parsePositiveIntString(raw string, envName string) (*int, error) {
	parsed, err := strconv.Atoi(raw)
	if err != nil || parsed <= 0 {
		return nil, fmt.Errorf("invalid %s: %q", envName, raw)
	}
	return &parsed, nil
}

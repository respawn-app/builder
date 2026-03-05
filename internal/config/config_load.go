package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"builder/internal/tools"
)

func Load(workspaceRoot string, opts LoadOptions) (App, error) {
	if workspaceRoot == "" {
		return App{}, errors.New("workspace root is required")
	}

	absWorkspace, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return App{}, fmt.Errorf("resolve workspace root: %w", err)
	}

	settingsPath, created, err := ensureDefaultSettingsFile()
	if err != nil {
		return App{}, err
	}

	cfg, err := readSettingsFile(settingsPath)
	if err != nil {
		return App{}, err
	}

	merged := defaultSettings()
	sources := map[string]string{
		"model":                               "default",
		"thinking_level":                      "default",
		"theme":                               "default",
		"tui_alternate_screen":                "default",
		"tui_scroll_mode":                     "default",
		"notification_method":                 "default",
		"web_search":                          "default",
		"openai_base_url":                     "default",
		"store":                               "default",
		"allow_non_cwd_edits":                 "default",
		"model_context_window":                "default",
		"context_compaction_threshold_tokens": "default",
		"compaction_mode":                     "default",
		"shell_output_max_chars":              "default",
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
	persistenceRoot := DefaultPersistence
	persistenceRootSource := "default"

	if strings.TrimSpace(cfg.Model) != "" {
		merged.Model = strings.TrimSpace(cfg.Model)
		sources["model"] = "file"
	}
	if strings.TrimSpace(cfg.ThinkingLevel) != "" {
		merged.ThinkingLevel = strings.TrimSpace(cfg.ThinkingLevel)
		sources["thinking_level"] = "file"
	}
	if strings.TrimSpace(cfg.Theme) != "" {
		merged.Theme = strings.TrimSpace(cfg.Theme)
		sources["theme"] = "file"
	}
	if strings.TrimSpace(cfg.TUIAlternateScreen) != "" {
		merged.TUIAlternateScreen = normalizeTUIAlternateScreenPolicy(cfg.TUIAlternateScreen)
		sources["tui_alternate_screen"] = "file"
	}
	if strings.TrimSpace(cfg.TUIScrollMode) != "" {
		merged.TUIScrollMode = normalizeTUIScrollMode(cfg.TUIScrollMode)
		sources["tui_scroll_mode"] = "file"
	}
	if strings.TrimSpace(cfg.NotificationMethod) != "" {
		merged.NotificationMethod = strings.TrimSpace(cfg.NotificationMethod)
		sources["notification_method"] = "file"
	}
	if strings.TrimSpace(cfg.WebSearch) != "" {
		merged.WebSearch = strings.TrimSpace(cfg.WebSearch)
		sources["web_search"] = "file"
	}
	if strings.TrimSpace(cfg.OpenAIBaseURL) != "" {
		merged.OpenAIBaseURL = strings.TrimSpace(cfg.OpenAIBaseURL)
		sources["openai_base_url"] = "file"
	}
	if cfg.Store != nil {
		merged.Store = *cfg.Store
		sources["store"] = "file"
	}
	if cfg.AllowNonCwdEdits != nil {
		merged.AllowNonCwdEdits = *cfg.AllowNonCwdEdits
		sources["allow_non_cwd_edits"] = "file"
	}
	if cfg.ModelContextWindow > 0 {
		merged.ModelContextWindow = cfg.ModelContextWindow
		sources["model_context_window"] = "file"
	}
	if cfg.ContextCompactionThresholdTokens > 0 {
		merged.ContextCompactionThresholdTokens = cfg.ContextCompactionThresholdTokens
		sources["context_compaction_threshold_tokens"] = "file"
	}
	if strings.TrimSpace(cfg.CompactionMode) != "" {
		merged.CompactionMode = normalizeCompactionMode(cfg.CompactionMode)
		sources["compaction_mode"] = "file"
	}
	if cfg.ShellOutputMaxChars > 0 {
		merged.ShellOutputMaxChars = cfg.ShellOutputMaxChars
		sources["shell_output_max_chars"] = "file"
	}
	if strings.TrimSpace(cfg.Reviewer.Frequency) != "" {
		merged.Reviewer.Frequency = strings.TrimSpace(cfg.Reviewer.Frequency)
		sources["reviewer.frequency"] = "file"
	}
	if strings.TrimSpace(cfg.Reviewer.Model) != "" {
		merged.Reviewer.Model = strings.TrimSpace(cfg.Reviewer.Model)
		sources["reviewer.model"] = "file"
	}
	if strings.TrimSpace(cfg.Reviewer.ThinkingLevel) != "" {
		merged.Reviewer.ThinkingLevel = strings.TrimSpace(cfg.Reviewer.ThinkingLevel)
		sources["reviewer.thinking_level"] = "file"
	}
	if cfg.Reviewer.TimeoutSeconds > 0 {
		merged.Reviewer.TimeoutSeconds = cfg.Reviewer.TimeoutSeconds
		sources["reviewer.timeout_seconds"] = "file"
	}
	if cfg.Reviewer.MaxSuggestions > 0 {
		merged.Reviewer.MaxSuggestions = cfg.Reviewer.MaxSuggestions
		sources["reviewer.max_suggestions"] = "file"
	}
	if cfg.Timeouts.ModelRequestSeconds > 0 {
		merged.Timeouts.ModelRequestSeconds = cfg.Timeouts.ModelRequestSeconds
		sources["timeouts.model_request"] = "file"
	}
	if cfg.Timeouts.ShellDefaultSeconds > 0 {
		merged.Timeouts.ShellDefaultSeconds = cfg.Timeouts.ShellDefaultSeconds
		sources["timeouts.shell_default"] = "file"
	} else if cfg.Timeouts.BashDefaultSeconds > 0 {
		merged.Timeouts.ShellDefaultSeconds = cfg.Timeouts.BashDefaultSeconds
		sources["timeouts.shell_default"] = "file"
	}
	for k, v := range cfg.Tools {
		id, ok := tools.ParseID(strings.TrimSpace(k))
		if !ok {
			return App{}, fmt.Errorf("invalid tools key in %s: %q", settingsPath, k)
		}
		merged.EnabledTools[id] = v
		sources["tools."+string(id)] = "file"
	}
	if strings.TrimSpace(cfg.PersistenceRoot) != "" {
		persistenceRoot = strings.TrimSpace(cfg.PersistenceRoot)
		persistenceRootSource = "file"
	}

	if v := strings.TrimSpace(os.Getenv("BUILDER_MODEL")); v != "" {
		merged.Model = v
		sources["model"] = "env"
	}
	if v := strings.TrimSpace(os.Getenv("BUILDER_THINKING_LEVEL")); v != "" {
		merged.ThinkingLevel = v
		sources["thinking_level"] = "env"
	}
	if v := strings.TrimSpace(os.Getenv("BUILDER_THEME")); v != "" {
		merged.Theme = v
		sources["theme"] = "env"
	}
	if v := strings.TrimSpace(os.Getenv("BUILDER_TUI_ALTERNATE_SCREEN")); v != "" {
		merged.TUIAlternateScreen = normalizeTUIAlternateScreenPolicy(v)
		sources["tui_alternate_screen"] = "env"
	}
	if v := strings.TrimSpace(os.Getenv("BUILDER_TUI_SCROLL_MODE")); v != "" {
		merged.TUIScrollMode = normalizeTUIScrollMode(v)
		sources["tui_scroll_mode"] = "env"
	}
	if v := strings.TrimSpace(os.Getenv("BUILDER_NOTIFICATION_METHOD")); v != "" {
		merged.NotificationMethod = v
		sources["notification_method"] = "env"
	}
	if v := strings.TrimSpace(os.Getenv("BUILDER_WEB_SEARCH")); v != "" {
		merged.WebSearch = v
		sources["web_search"] = "env"
	}
	if v := strings.TrimSpace(os.Getenv("BUILDER_OPENAI_BASE_URL")); v != "" {
		merged.OpenAIBaseURL = v
		sources["openai_base_url"] = "env"
	}
	if v := strings.TrimSpace(os.Getenv("BUILDER_STORE")); v != "" {
		enabled, err := strconv.ParseBool(v)
		if err != nil {
			return App{}, fmt.Errorf("invalid BUILDER_STORE: %q", v)
		}
		merged.Store = enabled
		sources["store"] = "env"
	}
	if v := strings.TrimSpace(os.Getenv("BUILDER_ALLOW_NON_CWD_EDITS")); v != "" {
		enabled, err := strconv.ParseBool(v)
		if err != nil {
			return App{}, fmt.Errorf("invalid BUILDER_ALLOW_NON_CWD_EDITS: %q", v)
		}
		merged.AllowNonCwdEdits = enabled
		sources["allow_non_cwd_edits"] = "env"
	}
	if v := strings.TrimSpace(os.Getenv("BUILDER_MODEL_CONTEXT_WINDOW")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return App{}, fmt.Errorf("invalid BUILDER_MODEL_CONTEXT_WINDOW: %q", v)
		}
		merged.ModelContextWindow = n
		sources["model_context_window"] = "env"
	}
	if v := strings.TrimSpace(os.Getenv("BUILDER_CONTEXT_COMPACTION_THRESHOLD_TOKENS")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return App{}, fmt.Errorf("invalid BUILDER_CONTEXT_COMPACTION_THRESHOLD_TOKENS: %q", v)
		}
		merged.ContextCompactionThresholdTokens = n
		sources["context_compaction_threshold_tokens"] = "env"
	}
	if raw, exists := os.LookupEnv("BUILDER_USE_NATIVE_COMPACTION"); exists && strings.TrimSpace(raw) != "" {
		return App{}, errors.New("unsupported env var: BUILDER_USE_NATIVE_COMPACTION")
	}
	if v := strings.TrimSpace(os.Getenv("BUILDER_COMPACTION_MODE")); v != "" {
		merged.CompactionMode = normalizeCompactionMode(v)
		sources["compaction_mode"] = "env"
	}
	if v := strings.TrimSpace(os.Getenv("BUILDER_SHELL_OUTPUT_MAX_CHARS")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return App{}, fmt.Errorf("invalid BUILDER_SHELL_OUTPUT_MAX_CHARS: %q", v)
		}
		merged.ShellOutputMaxChars = n
		sources["shell_output_max_chars"] = "env"
	}
	if v := strings.TrimSpace(os.Getenv("BUILDER_REVIEWER_FREQUENCY")); v != "" {
		merged.Reviewer.Frequency = v
		sources["reviewer.frequency"] = "env"
	}
	if v := strings.TrimSpace(os.Getenv("BUILDER_REVIEWER_MODEL")); v != "" {
		merged.Reviewer.Model = v
		sources["reviewer.model"] = "env"
	}
	if v := strings.TrimSpace(os.Getenv("BUILDER_REVIEWER_THINKING_LEVEL")); v != "" {
		merged.Reviewer.ThinkingLevel = v
		sources["reviewer.thinking_level"] = "env"
	}
	if v := strings.TrimSpace(os.Getenv("BUILDER_REVIEWER_TIMEOUT_SECONDS")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return App{}, fmt.Errorf("invalid BUILDER_REVIEWER_TIMEOUT_SECONDS: %q", v)
		}
		merged.Reviewer.TimeoutSeconds = n
		sources["reviewer.timeout_seconds"] = "env"
	}
	if v := strings.TrimSpace(os.Getenv("BUILDER_REVIEWER_MAX_SUGGESTIONS")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return App{}, fmt.Errorf("invalid BUILDER_REVIEWER_MAX_SUGGESTIONS: %q", v)
		}
		merged.Reviewer.MaxSuggestions = n
		sources["reviewer.max_suggestions"] = "env"
	}
	if v := strings.TrimSpace(os.Getenv("BUILDER_MODEL_TIMEOUT_SECONDS")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return App{}, fmt.Errorf("invalid BUILDER_MODEL_TIMEOUT_SECONDS: %q", v)
		}
		merged.Timeouts.ModelRequestSeconds = n
		sources["timeouts.model_request"] = "env"
	}
	if v := strings.TrimSpace(os.Getenv("BUILDER_SHELL_TIMEOUT_SECONDS")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return App{}, fmt.Errorf("invalid BUILDER_SHELL_TIMEOUT_SECONDS: %q", v)
		}
		merged.Timeouts.ShellDefaultSeconds = n
		sources["timeouts.shell_default"] = "env"
	} else if v := strings.TrimSpace(os.Getenv("BUILDER_BASH_TIMEOUT_SECONDS")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return App{}, fmt.Errorf("invalid BUILDER_BASH_TIMEOUT_SECONDS: %q", v)
		}
		merged.Timeouts.ShellDefaultSeconds = n
		sources["timeouts.shell_default"] = "env"
	}
	if v := strings.TrimSpace(os.Getenv("BUILDER_TOOLS")); v != "" {
		enabled, err := parseEnabledToolsCSV(v)
		if err != nil {
			return App{}, fmt.Errorf("invalid BUILDER_TOOLS: %w", err)
		}
		for _, id := range tools.CatalogIDs() {
			merged.EnabledTools[id] = false
			sources["tools."+string(id)] = "env"
		}
		for _, id := range enabled {
			merged.EnabledTools[id] = true
			sources["tools."+string(id)] = "env"
		}
	}
	if v := strings.TrimSpace(os.Getenv("BUILDER_PERSISTENCE_ROOT")); v != "" {
		persistenceRoot = v
		persistenceRootSource = "env"
	}

	if strings.TrimSpace(opts.Model) != "" {
		merged.Model = strings.TrimSpace(opts.Model)
		sources["model"] = "cli"
	}
	if strings.TrimSpace(opts.ThinkingLevel) != "" {
		merged.ThinkingLevel = strings.TrimSpace(opts.ThinkingLevel)
		sources["thinking_level"] = "cli"
	}
	if strings.TrimSpace(opts.Theme) != "" {
		merged.Theme = strings.TrimSpace(opts.Theme)
		sources["theme"] = "cli"
	}
	if strings.TrimSpace(opts.OpenAIBaseURL) != "" {
		merged.OpenAIBaseURL = strings.TrimSpace(opts.OpenAIBaseURL)
		sources["openai_base_url"] = "cli"
	}
	if opts.ModelTimeoutSeconds > 0 {
		merged.Timeouts.ModelRequestSeconds = opts.ModelTimeoutSeconds
		sources["timeouts.model_request"] = "cli"
	}
	if opts.ShellTimeoutSeconds > 0 {
		merged.Timeouts.ShellDefaultSeconds = opts.ShellTimeoutSeconds
		sources["timeouts.shell_default"] = "cli"
	}
	if strings.TrimSpace(opts.Tools) != "" {
		enabled, err := parseEnabledToolsCSV(opts.Tools)
		if err != nil {
			return App{}, fmt.Errorf("invalid tools flag: %w", err)
		}
		for _, id := range tools.CatalogIDs() {
			merged.EnabledTools[id] = false
			sources["tools."+string(id)] = "cli"
		}
		for _, id := range enabled {
			merged.EnabledTools[id] = true
			sources["tools."+string(id)] = "cli"
		}
	}

	if sources["reviewer.model"] == "default" {
		merged.Reviewer.Model = merged.Model
	}

	if err := validateSettings(merged); err != nil {
		return App{}, err
	}

	expandedPersistenceRoot, err := expandTildePath(persistenceRoot)
	if err != nil {
		return App{}, fmt.Errorf("expand persistence root: %w", err)
	}

	absRoot, err := filepath.Abs(expandedPersistenceRoot)
	if err != nil {
		return App{}, fmt.Errorf("resolve persistence root: %w", err)
	}
	if err := os.MkdirAll(absRoot, 0o755); err != nil {
		return App{}, fmt.Errorf("create persistence root: %w", err)
	}

	return App{
		AppName:         DefaultAppName,
		WorkspaceRoot:   absWorkspace,
		PersistenceRoot: absRoot,
		Settings:        merged,
		Source: SourceReport{
			SettingsPath:         settingsPath,
			CreatedDefaultConfig: created,
			Sources:              withPersistenceSource(sources, persistenceRootSource),
		},
	}, nil
}

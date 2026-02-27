package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"builder/internal/tools"

	"github.com/BurntSushi/toml"
	"github.com/google/uuid"
)

const (
	DefaultAppName       = "builder"
	DefaultPersistence   = "~/.builder"
	workspaceIndexName   = "workspaces.json"
	globalAuthConfigName = "auth.json"

	defaultModel               = "gpt-5.3-codex"
	defaultThinkingLevel       = "high"
	defaultTheme               = "dark"
	defaultModelContextWindow  = 400_000
	defaultModelTimeoutSeconds = 400
	defaultShellTimeoutSeconds = 300
	defaultShellOutputMaxChars = 16_000
	defaultCompactionThreshold = 360_000
	defaultReviewerFrequency   = "off"
	defaultReviewerThinking    = "low"
	defaultReviewerTimeoutSec  = 60
	defaultReviewerSuggestions = 5
	defaultTUIAlternateScreen  = "auto"
)

type TUIAlternateScreenPolicy string

const (
	TUIAlternateScreenAuto   TUIAlternateScreenPolicy = "auto"
	TUIAlternateScreenAlways TUIAlternateScreenPolicy = "always"
	TUIAlternateScreenNever  TUIAlternateScreenPolicy = "never"
)

type LoadOptions struct {
	Model               string
	ThinkingLevel       string
	Theme               string
	ModelTimeoutSeconds int
	ShellTimeoutSeconds int
	Tools               string
	OpenAIBaseURL       string
}

type Timeouts struct {
	ModelRequestSeconds int
	ShellDefaultSeconds int
}

type Settings struct {
	Model                            string
	ThinkingLevel                    string
	Theme                            string
	TUIAlternateScreen               TUIAlternateScreenPolicy
	NotificationMethod               string
	WebSearch                        string
	OpenAIBaseURL                    string
	Store                            bool
	AllowNonCwdEdits                 bool
	ModelContextWindow               int
	ContextCompactionThresholdTokens int
	UseNativeCompaction              bool
	EnabledTools                     map[tools.ID]bool
	Timeouts                         Timeouts
	ShellOutputMaxChars              int
	Reviewer                         ReviewerSettings
}

type ReviewerSettings struct {
	Frequency      string
	Model          string
	ThinkingLevel  string
	TimeoutSeconds int
	MaxSuggestions int
}

type SourceReport struct {
	SettingsPath         string
	CreatedDefaultConfig bool
	Sources              map[string]string
}

type App struct {
	AppName         string
	WorkspaceRoot   string
	PersistenceRoot string
	Settings        Settings
	Source          SourceReport
}

type fileSettings struct {
	Model              string          `toml:"model"`
	ThinkingLevel      string          `toml:"thinking_level"`
	Theme              string          `toml:"theme"`
	TUIAlternateScreen string          `toml:"tui_alternate_screen"`
	NotificationMethod string          `toml:"notification_method"`
	WebSearch          string          `toml:"web_search"`
	Tools              map[string]bool `toml:"tools"`
	Timeouts           struct {
		ModelRequestSeconds int `toml:"model_request_seconds"`
		ShellDefaultSeconds int `toml:"shell_default_seconds"`
		BashDefaultSeconds  int `toml:"bash_default_seconds"`
	} `toml:"timeouts"`
	PersistenceRoot                  string `toml:"persistence_root"`
	OpenAIBaseURL                    string `toml:"openai_base_url"`
	Store                            *bool  `toml:"store"`
	AllowNonCwdEdits                 *bool  `toml:"allow_non_cwd_edits"`
	ModelContextWindow               int    `toml:"model_context_window"`
	ContextCompactionThresholdTokens int    `toml:"context_compaction_threshold_tokens"`
	ShellOutputMaxChars              int    `toml:"shell_output_max_chars"`
	UseNativeCompaction              *bool  `toml:"use_native_compaction"`
	Reviewer                         struct {
		Frequency      string `toml:"frequency"`
		Model          string `toml:"model"`
		ThinkingLevel  string `toml:"thinking_level"`
		TimeoutSeconds int    `toml:"timeout_seconds"`
		MaxSuggestions int    `toml:"max_suggestions"`
	} `toml:"reviewer"`
}

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
		"notification_method":                 "default",
		"web_search":                          "default",
		"openai_base_url":                     "default",
		"store":                               "default",
		"allow_non_cwd_edits":                 "default",
		"model_context_window":                "default",
		"context_compaction_threshold_tokens": "default",
		"use_native_compaction":               "default",
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
	if cfg.UseNativeCompaction != nil {
		merged.UseNativeCompaction = *cfg.UseNativeCompaction
		sources["use_native_compaction"] = "file"
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
	if v := strings.TrimSpace(os.Getenv("BUILDER_USE_NATIVE_COMPACTION")); v != "" {
		enabled, err := strconv.ParseBool(v)
		if err != nil {
			return App{}, fmt.Errorf("invalid BUILDER_USE_NATIVE_COMPACTION: %q", v)
		}
		merged.UseNativeCompaction = enabled
		sources["use_native_compaction"] = "env"
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
		Theme:                            defaultTheme,
		TUIAlternateScreen:               TUIAlternateScreenPolicy(defaultTUIAlternateScreen),
		NotificationMethod:               "auto",
		WebSearch:                        "off",
		Store:                            false,
		AllowNonCwdEdits:                 false,
		ModelContextWindow:               defaultModelContextWindow,
		ContextCompactionThresholdTokens: defaultCompactionThreshold,
		UseNativeCompaction:              true,
		EnabledTools:                     enabled,
		ShellOutputMaxChars:              defaultShellOutputMaxChars,
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

func validateSettings(v Settings) error {
	if strings.TrimSpace(v.Model) == "" {
		return errors.New("settings model must not be empty")
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
		// ok
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
	if v.ContextCompactionThresholdTokens <= 0 {
		return fmt.Errorf("context_compaction_threshold_tokens must be > 0")
	}
	if v.ModelContextWindow <= 0 {
		return fmt.Errorf("model_context_window must be > 0")
	}
	if v.ContextCompactionThresholdTokens >= v.ModelContextWindow {
		return fmt.Errorf("context_compaction_threshold_tokens must be < model_context_window")
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

func EnabledToolIDs(v Settings) []tools.ID {
	ids := make([]tools.ID, 0, len(v.EnabledTools))
	for _, id := range tools.CatalogIDs() {
		if v.EnabledTools[id] {
			ids = append(ids, id)
		}
	}
	return ids
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

func expandTildePath(path string) (string, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" || !strings.HasPrefix(trimmed, "~") {
		return trimmed, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	if trimmed == "~" {
		return home, nil
	}
	if strings.HasPrefix(trimmed, "~/") {
		return filepath.Join(home, strings.TrimPrefix(trimmed, "~/")), nil
	}
	if strings.HasPrefix(trimmed, "~\\") {
		return filepath.Join(home, strings.TrimPrefix(trimmed, "~\\")), nil
	}
	return trimmed, nil
}

func ensureDefaultSettingsFile() (path string, created bool, err error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false, fmt.Errorf("resolve home dir: %w", err)
	}
	dir := filepath.Join(home, ".builder")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", false, fmt.Errorf("create settings dir: %w", err)
	}
	path = filepath.Join(dir, "config.toml")
	if _, err := os.Stat(path); err == nil {
		return path, false, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", false, fmt.Errorf("stat settings file: %w", err)
	}
	content := defaultSettingsTOML()
	if writeErr := os.WriteFile(path, []byte(content), 0o644); writeErr != nil {
		return "", false, fmt.Errorf("write default settings file: %w", writeErr)
	}
	return path, true, nil
}

func readSettingsFile(path string) (fileSettings, error) {
	var cfg fileSettings
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, fmt.Errorf("read settings file %s: %w", path, err)
	}
	if strings.TrimSpace(string(data)) == "" {
		return cfg, nil
	}
	if _, err := toml.NewDecoder(bytes.NewReader(data)).Decode(&cfg); err != nil {
		return cfg, fmt.Errorf("parse settings file %s: %w", path, err)
	}
	return cfg, nil
}

func defaultSettingsTOML() string {
	defaults := defaultSettings()
	toolDefaults := map[string]bool{}
	for _, id := range tools.CatalogIDs() {
		toolDefaults[string(id)] = defaults.EnabledTools[id]
	}
	payload := map[string]any{
		"model":                               defaults.Model,
		"thinking_level":                      defaults.ThinkingLevel,
		"theme":                               defaults.Theme,
		"tui_alternate_screen":                defaults.TUIAlternateScreen,
		"notification_method":                 defaults.NotificationMethod,
		"web_search":                          defaults.WebSearch,
		"openai_base_url":                     defaults.OpenAIBaseURL,
		"store":                               defaults.Store,
		"allow_non_cwd_edits":                 defaults.AllowNonCwdEdits,
		"model_context_window":                defaults.ModelContextWindow,
		"context_compaction_threshold_tokens": defaults.ContextCompactionThresholdTokens,
		"shell_output_max_chars":              defaults.ShellOutputMaxChars,
		"use_native_compaction":               defaults.UseNativeCompaction,
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
		"# This JSON block mirrors current defaults for readability:\n" +
		"# " + strings.ReplaceAll(string(encoded), "\n", "\n# ") + "\n\n" +
		"model = \"" + defaults.Model + "\"\n" +
		"thinking_level = \"" + defaults.ThinkingLevel + "\"\n" +
		"theme = \"" + defaults.Theme + "\"\n" +
		"tui_alternate_screen = \"" + string(defaults.TUIAlternateScreen) + "\"\n" +
		"notification_method = \"" + defaults.NotificationMethod + "\"\n" +
		"web_search = \"" + defaults.WebSearch + "\"\n" +
		"openai_base_url = \"" + defaults.OpenAIBaseURL + "\"\n" +
		"store = " + strconv.FormatBool(defaults.Store) + "\n" +
		"allow_non_cwd_edits = " + strconv.FormatBool(defaults.AllowNonCwdEdits) + "\n" +
		"model_context_window = " + strconv.Itoa(defaults.ModelContextWindow) + "\n" +
		"context_compaction_threshold_tokens = " + strconv.Itoa(defaults.ContextCompactionThresholdTokens) + "\n" +
		"shell_output_max_chars = " + strconv.Itoa(defaults.ShellOutputMaxChars) + "\n" +
		"use_native_compaction = " + strconv.FormatBool(defaults.UseNativeCompaction) + "\n" +
		"persistence_root = \"" + DefaultPersistence + "\"\n\n" +
		"[tools]\n"
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

func withPersistenceSource(s map[string]string, persistence string) map[string]string {
	out := map[string]string{}
	for k, v := range s {
		out[k] = v
	}
	out["persistence_root"] = persistence
	return out
}

type workspaceIndex struct {
	Entries map[string]string `json:"entries"`
}

func ResolveWorkspaceContainer(cfg App) (string, string, error) {
	idxPath := filepath.Join(cfg.PersistenceRoot, workspaceIndexName)
	idx, err := loadWorkspaceIndex(idxPath)
	if err != nil {
		return "", "", err
	}

	if name, ok := idx.Entries[cfg.WorkspaceRoot]; ok {
		return name, filepath.Join(cfg.PersistenceRoot, name), nil
	}

	base := filepath.Base(cfg.WorkspaceRoot)
	container := fmt.Sprintf("%s-%s", base, uuid.NewString())
	idx.Entries[cfg.WorkspaceRoot] = container
	if err := saveWorkspaceIndexAtomic(idxPath, idx); err != nil {
		return "", "", err
	}

	containerDir := filepath.Join(cfg.PersistenceRoot, container)
	if err := os.MkdirAll(containerDir, 0o755); err != nil {
		return "", "", fmt.Errorf("create workspace container: %w", err)
	}

	return container, containerDir, nil
}

func GlobalAuthConfigPath(cfg App) string {
	return filepath.Join(cfg.PersistenceRoot, globalAuthConfigName)
}

func loadWorkspaceIndex(path string) (workspaceIndex, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return workspaceIndex{Entries: map[string]string{}}, nil
		}
		return workspaceIndex{}, fmt.Errorf("read workspace index: %w", err)
	}

	var idx workspaceIndex
	if err := json.Unmarshal(data, &idx); err != nil {
		return workspaceIndex{}, fmt.Errorf("parse workspace index: %w", err)
	}
	if idx.Entries == nil {
		idx.Entries = map[string]string{}
	}
	return idx, nil
}

func saveWorkspaceIndexAtomic(path string, idx workspaceIndex) error {
	data, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal workspace index: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write workspace index tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("replace workspace index: %w", err)
	}
	return nil
}

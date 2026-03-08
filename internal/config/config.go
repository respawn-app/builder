package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"builder/internal/tools"

	"github.com/BurntSushi/toml"
	"github.com/google/uuid"
)

const (
	DefaultAppName       = "builder"
	DefaultPersistence   = "~/.builder"
	sessionsDirName      = "sessions"
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
	defaultTUIScrollMode       = "alt"
	defaultCompactionMode      = "native"
)

type TUIAlternateScreenPolicy string

type TUIScrollMode string

type CompactionMode string

const (
	TUIAlternateScreenAuto   TUIAlternateScreenPolicy = "auto"
	TUIAlternateScreenAlways TUIAlternateScreenPolicy = "always"
	TUIAlternateScreenNever  TUIAlternateScreenPolicy = "never"

	TUIScrollModeAlt    TUIScrollMode = "alt"
	TUIScrollModeNative TUIScrollMode = "native"

	CompactionModeNative CompactionMode = "native"
	CompactionModeLocal  CompactionMode = "local"
	CompactionModeNone   CompactionMode = "none"
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
	TUIScrollMode                    TUIScrollMode
	NotificationMethod               string
	ToolPreambles                    bool
	WebSearch                        string
	OpenAIBaseURL                    string
	Store                            bool
	AllowNonCwdEdits                 bool
	ModelContextWindow               int
	ContextCompactionThresholdTokens int
	CompactionMode                   CompactionMode
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
	TUIScrollMode      string          `toml:"tui_scroll_mode"`
	NotificationMethod string          `toml:"notification_method"`
	ToolPreambles      *bool           `toml:"tool_preambles"`
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
	CompactionMode                   string `toml:"compaction_mode"`
	Reviewer                         struct {
		Frequency      string `toml:"frequency"`
		Model          string `toml:"model"`
		ThinkingLevel  string `toml:"thinking_level"`
		TimeoutSeconds int    `toml:"timeout_seconds"`
		MaxSuggestions int    `toml:"max_suggestions"`
	} `toml:"reviewer"`
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
		TUIScrollMode:                    TUIScrollMode(defaultTUIScrollMode),
		NotificationMethod:               "auto",
		ToolPreambles:                    true,
		WebSearch:                        "off",
		Store:                            false,
		AllowNonCwdEdits:                 false,
		ModelContextWindow:               defaultModelContextWindow,
		ContextCompactionThresholdTokens: defaultCompactionThreshold,
		CompactionMode:                   CompactionMode(defaultCompactionMode),
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
	switch strings.ToLower(strings.TrimSpace(string(v.TUIScrollMode))) {
	case "alt", "native":
	default:
		return fmt.Errorf("invalid tui_scroll_mode %q (expected alt|native)", v.TUIScrollMode)
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

func normalizeTUIScrollMode(raw string) TUIScrollMode {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "alt":
		return TUIScrollModeAlt
	case "native":
		return TUIScrollModeNative
	default:
		return TUIScrollMode(strings.TrimSpace(raw))
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
	metadata, err := toml.NewDecoder(bytes.NewReader(data)).Decode(&cfg)
	if err != nil {
		return cfg, fmt.Errorf("parse settings file %s: %w", path, err)
	}
	if err := validateNoUnknownSettingsKeys(metadata.Undecoded()); err != nil {
		return cfg, fmt.Errorf("parse settings file %s: %w", path, err)
	}
	return cfg, nil
}

func validateNoUnknownSettingsKeys(keys []toml.Key) error {
	if len(keys) == 0 {
		return nil
	}
	names := make([]string, 0, len(keys))
	for _, key := range keys {
		keyName := strings.TrimSpace(key.String())
		names = append(names, keyName)
	}
	sort.Strings(names)
	return fmt.Errorf("unknown settings key(s): %s", strings.Join(names, ", "))
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
		"tui_scroll_mode":                     defaults.TUIScrollMode,
		"notification_method":                 defaults.NotificationMethod,
		"tool_preambles":                      defaults.ToolPreambles,
		"web_search":                          defaults.WebSearch,
		"openai_base_url":                     defaults.OpenAIBaseURL,
		"store":                               defaults.Store,
		"allow_non_cwd_edits":                 defaults.AllowNonCwdEdits,
		"model_context_window":                defaults.ModelContextWindow,
		"context_compaction_threshold_tokens": defaults.ContextCompactionThresholdTokens,
		"shell_output_max_chars":              defaults.ShellOutputMaxChars,
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
		"# Note: tui_scroll_mode=native forces main UI to normal buffer even if\n" +
		"# tui_alternate_screen=always, so transcript replay stays visible in scrollback.\n\n" +
		"# This JSON block mirrors current defaults for readability:\n" +
		"# " + strings.ReplaceAll(string(encoded), "\n", "\n# ") + "\n\n" +
		"model = \"" + defaults.Model + "\"\n" +
		"thinking_level = \"" + defaults.ThinkingLevel + "\"\n" +
		"theme = \"" + defaults.Theme + "\"\n" +
		"tui_alternate_screen = \"" + string(defaults.TUIAlternateScreen) + "\"\n" +
		"tui_scroll_mode = \"" + string(defaults.TUIScrollMode) + "\"\n" +
		"notification_method = \"" + defaults.NotificationMethod + "\"\n" +
		"# Known tradeoff: sessions started in headless mode never include intermediary-update\n" +
		"# instructions for their lifetime because the dispatch contract is locked on first use.\n" +
		"tool_preambles = " + strconv.FormatBool(defaults.ToolPreambles) + "\n" +
		"web_search = \"" + defaults.WebSearch + "\"\n" +
		"openai_base_url = \"" + defaults.OpenAIBaseURL + "\"\n" +
		"store = " + strconv.FormatBool(defaults.Store) + "\n" +
		"allow_non_cwd_edits = " + strconv.FormatBool(defaults.AllowNonCwdEdits) + "\n" +
		"model_context_window = " + strconv.Itoa(defaults.ModelContextWindow) + "\n" +
		"context_compaction_threshold_tokens = " + strconv.Itoa(defaults.ContextCompactionThresholdTokens) + "\n" +
		"shell_output_max_chars = " + strconv.Itoa(defaults.ShellOutputMaxChars) + "\n" +
		"compaction_mode = \"" + string(defaults.CompactionMode) + "\"\n" +
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
		return name, filepath.Join(SessionsRoot(cfg), name), nil
	}

	base := filepath.Base(cfg.WorkspaceRoot)
	container := fmt.Sprintf("%s-%s", base, uuid.NewString())
	idx.Entries[cfg.WorkspaceRoot] = container
	if err := saveWorkspaceIndexAtomic(idxPath, idx); err != nil {
		return "", "", err
	}

	containerDir := filepath.Join(SessionsRoot(cfg), container)
	if err := os.MkdirAll(containerDir, 0o755); err != nil {
		return "", "", fmt.Errorf("create workspace container: %w", err)
	}

	return container, containerDir, nil
}

func SessionsRoot(cfg App) string {
	return filepath.Join(cfg.PersistenceRoot, sessionsDirName)
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

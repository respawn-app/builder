package config

import (
	"path/filepath"

	"builder/internal/tools"
)

const (
	DefaultAppName       = "builder"
	DefaultPersistence   = "~/.builder"
	sessionsDirName      = "sessions"
	workspaceIndexName   = "workspaces.json"
	globalAuthConfigName = "auth.json"
)

type TUIAlternateScreenPolicy string

type TUIScrollMode string

type CompactionMode string
type BGShellsOutputMode string

const (
	TUIAlternateScreenAuto   TUIAlternateScreenPolicy = "auto"
	TUIAlternateScreenAlways TUIAlternateScreenPolicy = "always"
	TUIAlternateScreenNever  TUIAlternateScreenPolicy = "never"

	TUIScrollModeAlt    TUIScrollMode = "alt"
	TUIScrollModeNative TUIScrollMode = "native"

	CompactionModeNative CompactionMode = "native"
	CompactionModeLocal  CompactionMode = "local"
	CompactionModeNone   CompactionMode = "none"

	BGShellsOutputDefault BGShellsOutputMode = "default"
	BGShellsOutputVerbose BGShellsOutputMode = "verbose"
	BGShellsOutputConcise BGShellsOutputMode = "concise"
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
	PriorityRequestMode              bool
	WebSearch                        string
	OpenAIBaseURL                    string
	Store                            bool
	AllowNonCwdEdits                 bool
	ModelContextWindow               int
	ContextCompactionThresholdTokens int
	MinimumExecToBgSeconds           int
	CompactionMode                   CompactionMode
	EnabledTools                     map[tools.ID]bool
	Timeouts                         Timeouts
	ShellOutputMaxChars              int
	BGShellsOutput                   BGShellsOutputMode
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
	Model               string          `toml:"model"`
	ThinkingLevel       string          `toml:"thinking_level"`
	Theme               string          `toml:"theme"`
	TUIAlternateScreen  string          `toml:"tui_alternate_screen"`
	TUIScrollMode       string          `toml:"tui_scroll_mode"`
	NotificationMethod  string          `toml:"notification_method"`
	ToolPreambles       *bool           `toml:"tool_preambles"`
	PriorityRequestMode *bool           `toml:"priority_request_mode"`
	WebSearch           string          `toml:"web_search"`
	Tools               map[string]bool `toml:"tools"`
	Timeouts            struct {
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
	MinimumExecToBgSeconds           int    `toml:"minimum_exec_to_bg_seconds"`
	ShellOutputMaxChars              int    `toml:"shell_output_max_chars"`
	BGShellsOutput                   string `toml:"bg_shells_output"`
	CompactionMode                   string `toml:"compaction_mode"`
	Reviewer                         struct {
		Frequency      string `toml:"frequency"`
		Model          string `toml:"model"`
		ThinkingLevel  string `toml:"thinking_level"`
		TimeoutSeconds int    `toml:"timeout_seconds"`
		MaxSuggestions int    `toml:"max_suggestions"`
	} `toml:"reviewer"`
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

func SessionsRoot(cfg App) string {
	return filepath.Join(cfg.PersistenceRoot, sessionsDirName)
}

func GlobalAuthConfigPath(cfg App) string {
	return filepath.Join(cfg.PersistenceRoot, globalAuthConfigName)
}

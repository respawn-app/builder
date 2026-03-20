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

type CompactionMode string
type BGShellsOutputMode string
type ModelVerbosity string

const (
	TUIAlternateScreenAuto   TUIAlternateScreenPolicy = "auto"
	TUIAlternateScreenAlways TUIAlternateScreenPolicy = "always"
	TUIAlternateScreenNever  TUIAlternateScreenPolicy = "never"

	CompactionModeNative CompactionMode = "native"
	CompactionModeLocal  CompactionMode = "local"
	CompactionModeNone   CompactionMode = "none"

	BGShellsOutputDefault BGShellsOutputMode = "default"
	BGShellsOutputVerbose BGShellsOutputMode = "verbose"
	BGShellsOutputConcise BGShellsOutputMode = "concise"

	ModelVerbosityLow    ModelVerbosity = "low"
	ModelVerbosityMedium ModelVerbosity = "medium"
	ModelVerbosityHigh   ModelVerbosity = "high"
)

type LoadOptions struct {
	Model               string
	ProviderOverride    string
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
	ModelVerbosity                   ModelVerbosity
	ModelCapabilities                ModelCapabilitiesOverride
	Theme                            string
	TUIAlternateScreen               TUIAlternateScreenPolicy
	NotificationMethod               string
	ToolPreambles                    bool
	PriorityRequestMode              bool
	WebSearch                        string
	ProviderOverride                 string
	OpenAIBaseURL                    string
	ProviderCapabilities             ProviderCapabilitiesOverride
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

type ModelCapabilitiesOverride struct {
	SupportsReasoningEffort bool
	SupportsVisionInputs    bool
}

type ProviderCapabilitiesOverride struct {
	ProviderID                    string
	SupportsResponsesAPI          bool
	SupportsResponsesCompact      bool
	SupportsNativeWebSearch       bool
	SupportsReasoningEncrypted    bool
	SupportsServerSideContextEdit bool
	IsOpenAIFirstParty            bool
}

type ReviewerSettings struct {
	Frequency      string
	Model          string
	ThinkingLevel  string
	TimeoutSeconds int
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

type settingsFile map[string]any

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

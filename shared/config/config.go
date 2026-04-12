package config

import (
	"fmt"
	"path/filepath"

	"builder/server/tools"
	"builder/shared/protocol"
)

const (
	DefaultAppName       = "builder"
	DefaultPersistence   = "~/.builder"
	sessionsDirName      = "sessions"
	databaseDirName      = "db"
	workspaceIndexName   = "workspaces.json"
	globalAuthConfigName = "auth.json"
)

type TUIAlternateScreenPolicy string

type CompactionMode string
type BGShellsOutputMode string
type CacheWarningMode string
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

	CacheWarningModeOff     CacheWarningMode = "off"
	CacheWarningModeDefault CacheWarningMode = "default"
	CacheWarningModeVerbose CacheWarningMode = "verbose"

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
	Debug                            bool
	ServerHost                       string
	ServerPort                       int
	WebSearch                        string
	ProviderOverride                 string
	OpenAIBaseURL                    string
	ProviderCapabilities             ProviderCapabilitiesOverride
	Store                            bool
	AllowNonCwdEdits                 bool
	ModelContextWindow               int
	ContextCompactionThresholdTokens int
	PreSubmitCompactionLeadTokens    int
	MinimumExecToBgSeconds           int
	CompactionMode                   CompactionMode
	EnabledTools                     map[tools.ID]bool
	SkillToggles                     map[string]bool
	Timeouts                         Timeouts
	ShellOutputMaxChars              int
	BGShellsOutput                   BGShellsOutputMode
	CacheWarningMode                 CacheWarningMode
	Reviewer                         ReviewerSettings
}

type ModelCapabilitiesOverride struct {
	SupportsReasoningEffort bool
	SupportsVisionInputs    bool
}

type ProviderCapabilitiesOverride struct {
	ProviderID                     string
	SupportsResponsesAPI           bool
	SupportsResponsesCompact       bool
	SupportsRequestInputTokenCount bool
	SupportsPromptCacheKey         bool
	SupportsNativeWebSearch        bool
	SupportsReasoningEncrypted     bool
	SupportsServerSideContextEdit  bool
	IsOpenAIFirstParty             bool
}

type ReviewerSettings struct {
	Frequency      string
	Model          string
	ThinkingLevel  string
	TimeoutSeconds int
	VerboseOutput  bool
}

type SourceReport struct {
	SettingsPath         string
	SettingsFileExists   bool
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

func ProjectsRoot(cfg App) string {
	return filepath.Join(cfg.PersistenceRoot, "projects")
}

func ProjectRoot(cfg App, projectID string) string {
	return filepath.Join(ProjectsRoot(cfg), projectID)
}

func ProjectSessionsRoot(cfg App, projectID string) string {
	return filepath.Join(ProjectRoot(cfg, projectID), sessionsDirName)
}

func ProjectSessionDir(cfg App, projectID string, sessionID string) string {
	return filepath.Join(ProjectSessionsRoot(cfg, projectID), sessionID)
}

func DatabaseRoot(cfg App) string {
	return filepath.Join(cfg.PersistenceRoot, databaseDirName)
}

func MainDatabasePath(cfg App) string {
	return filepath.Join(DatabaseRoot(cfg), "main.sqlite3")
}

func GlobalAuthConfigPath(cfg App) string {
	return filepath.Join(cfg.PersistenceRoot, globalAuthConfigName)
}

func MigrationBackupsRoot(cfg App) string {
	return filepath.Join(cfg.PersistenceRoot, "migration-backups")
}

func MigrationsRoot(cfg App) string {
	return filepath.Join(cfg.PersistenceRoot, "migrations")
}

func ServerListenAddress(cfg App) string {
	return fmt.Sprintf("%s:%d", cfg.Settings.ServerHost, cfg.Settings.ServerPort)
}

func ServerRPCURL(cfg App) string {
	return "ws://" + ServerListenAddress(cfg) + protocol.RPCPath
}

func ServerHTTPBaseURL(cfg App) string {
	return "http://" + ServerListenAddress(cfg)
}

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
	DefaultPersistence   = "./agents/builder"
	workspaceIndexName   = "workspaces.json"
	globalAuthConfigName = "auth.json"

	defaultModel               = "gpt-5"
	defaultThinkingLevel       = "medium"
	defaultTheme               = "dark"
	defaultModelTimeoutSeconds = 120
	defaultBashTimeoutSeconds  = 300
)

type LoadOptions struct {
	Model               string
	ThinkingLevel       string
	Theme               string
	ModelTimeoutSeconds int
	BashTimeoutSeconds  int
	Tools               string
}

type Timeouts struct {
	ModelRequestSeconds int
	BashDefaultSeconds  int
}

type Settings struct {
	Model         string
	ThinkingLevel string
	Theme         string
	EnabledTools  map[tools.ID]bool
	Timeouts      Timeouts
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
	Model         string          `toml:"model"`
	ThinkingLevel string          `toml:"thinking_level"`
	Theme         string          `toml:"theme"`
	Tools         map[string]bool `toml:"tools"`
	Timeouts      struct {
		ModelRequestSeconds int `toml:"model_request_seconds"`
		BashDefaultSeconds  int `toml:"bash_default_seconds"`
	} `toml:"timeouts"`
	PersistenceRoot string `toml:"persistence_root"`
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
		"model":                  "default",
		"thinking_level":         "default",
		"theme":                  "default",
		"timeouts.model_request": "default",
		"timeouts.bash_default":  "default",
	}
	for _, id := range sortedToolIDs() {
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
	if cfg.Timeouts.ModelRequestSeconds > 0 {
		merged.Timeouts.ModelRequestSeconds = cfg.Timeouts.ModelRequestSeconds
		sources["timeouts.model_request"] = "file"
	}
	if cfg.Timeouts.BashDefaultSeconds > 0 {
		merged.Timeouts.BashDefaultSeconds = cfg.Timeouts.BashDefaultSeconds
		sources["timeouts.bash_default"] = "file"
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
	if v := strings.TrimSpace(os.Getenv("BUILDER_MODEL_TIMEOUT_SECONDS")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return App{}, fmt.Errorf("invalid BUILDER_MODEL_TIMEOUT_SECONDS: %q", v)
		}
		merged.Timeouts.ModelRequestSeconds = n
		sources["timeouts.model_request"] = "env"
	}
	if v := strings.TrimSpace(os.Getenv("BUILDER_BASH_TIMEOUT_SECONDS")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return App{}, fmt.Errorf("invalid BUILDER_BASH_TIMEOUT_SECONDS: %q", v)
		}
		merged.Timeouts.BashDefaultSeconds = n
		sources["timeouts.bash_default"] = "env"
	}
	if v := strings.TrimSpace(os.Getenv("BUILDER_TOOLS")); v != "" {
		enabled, err := parseEnabledToolsCSV(v)
		if err != nil {
			return App{}, fmt.Errorf("invalid BUILDER_TOOLS: %w", err)
		}
		for _, id := range sortedToolIDs() {
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
	if opts.ModelTimeoutSeconds > 0 {
		merged.Timeouts.ModelRequestSeconds = opts.ModelTimeoutSeconds
		sources["timeouts.model_request"] = "cli"
	}
	if opts.BashTimeoutSeconds > 0 {
		merged.Timeouts.BashDefaultSeconds = opts.BashTimeoutSeconds
		sources["timeouts.bash_default"] = "cli"
	}
	if strings.TrimSpace(opts.Tools) != "" {
		enabled, err := parseEnabledToolsCSV(opts.Tools)
		if err != nil {
			return App{}, fmt.Errorf("invalid tools flag: %w", err)
		}
		for _, id := range sortedToolIDs() {
			merged.EnabledTools[id] = false
			sources["tools."+string(id)] = "cli"
		}
		for _, id := range enabled {
			merged.EnabledTools[id] = true
			sources["tools."+string(id)] = "cli"
		}
	}

	if err := validateSettings(merged); err != nil {
		return App{}, err
	}

	absRoot, err := filepath.Abs(persistenceRoot)
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
	for _, id := range sortedToolIDs() {
		enabled[id] = true
	}
	return Settings{
		Model:         defaultModel,
		ThinkingLevel: defaultThinkingLevel,
		Theme:         defaultTheme,
		EnabledTools:  enabled,
		Timeouts: Timeouts{
			ModelRequestSeconds: defaultModelTimeoutSeconds,
			BashDefaultSeconds:  defaultBashTimeoutSeconds,
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
	if v.Timeouts.ModelRequestSeconds <= 0 {
		return fmt.Errorf("timeouts.model_request_seconds must be > 0")
	}
	if v.Timeouts.BashDefaultSeconds <= 0 {
		return fmt.Errorf("timeouts.bash_default_seconds must be > 0")
	}
	for _, id := range sortedToolIDs() {
		if _, ok := v.EnabledTools[id]; !ok {
			v.EnabledTools[id] = false
		}
	}
	return nil
}

func EnabledToolIDs(v Settings) []tools.ID {
	ids := make([]tools.ID, 0, len(v.EnabledTools))
	for _, id := range sortedToolIDs() {
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

func sortedToolIDs() []tools.ID {
	ids := []tools.ID{tools.ToolAskQuestion, tools.ToolBash, tools.ToolPatch}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
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
	payload := map[string]any{
		"model":          defaults.Model,
		"thinking_level": defaults.ThinkingLevel,
		"theme":          defaults.Theme,
		"tools": map[string]bool{
			string(tools.ToolAskQuestion): defaults.EnabledTools[tools.ToolAskQuestion],
			string(tools.ToolBash):        defaults.EnabledTools[tools.ToolBash],
			string(tools.ToolPatch):       defaults.EnabledTools[tools.ToolPatch],
		},
		"timeouts": map[string]int{
			"model_request_seconds": defaults.Timeouts.ModelRequestSeconds,
			"bash_default_seconds":  defaults.Timeouts.BashDefaultSeconds,
		},
		"persistence_root": DefaultPersistence,
	}
	encoded, _ := json.MarshalIndent(payload, "", "  ")
	return "# builder settings\n" +
		"# edit and restart builder to apply changes\n\n" +
		"# This JSON block mirrors current defaults for readability:\n" +
		"# " + strings.ReplaceAll(string(encoded), "\n", "\n# ") + "\n\n" +
		"model = \"" + defaults.Model + "\"\n" +
		"thinking_level = \"" + defaults.ThinkingLevel + "\"\n" +
		"theme = \"" + defaults.Theme + "\"\n" +
		"persistence_root = \"" + DefaultPersistence + "\"\n\n" +
		"[tools]\n" +
		string(tools.ToolAskQuestion) + " = true\n" +
		string(tools.ToolBash) + " = true\n" +
		string(tools.ToolPatch) + " = true\n\n" +
		"[timeouts]\n" +
		"model_request_seconds = " + strconv.Itoa(defaults.Timeouts.ModelRequestSeconds) + "\n" +
		"bash_default_seconds = " + strconv.Itoa(defaults.Timeouts.BashDefaultSeconds) + "\n"
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

package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

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
	if err := os.WriteFile(path, []byte(defaultSettingsTOML()), 0o644); err != nil {
		return "", false, fmt.Errorf("write default settings file: %w", err)
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
		names = append(names, strings.TrimSpace(key.String()))
	}
	sort.Strings(names)
	return fmt.Errorf("unknown settings key(s): %s", strings.Join(names, ", "))
}

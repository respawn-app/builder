package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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

func readSettingsFile(path string) (settingsFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read settings file %s: %w", path, err)
	}
	if strings.TrimSpace(string(data)) == "" {
		return settingsFile{}, nil
	}
	var raw settingsFile
	if _, err := toml.NewDecoder(bytes.NewReader(data)).Decode(&raw); err != nil {
		return nil, fmt.Errorf("parse settings file %s: %w", path, err)
	}
	return raw, nil
}

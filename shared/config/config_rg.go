package config

import (
	"fmt"
	"path/filepath"
)

const managedRGConfigName = "rg.conf"

const managedRGConfigContents = `# Builder-managed ripgrep defaults.
# User-editable. Builder only creates this file when missing.
--heading
--line-number
--max-columns=200
--max-columns-preview
`

func ResolveManagedRGConfigPath() (string, error) {
	settingsPath, err := resolveSettingsFilePath()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(settingsPath), managedRGConfigName), nil
}

func EnsureManagedRGConfigFile() (path string, created bool, err error) {
	path, err = ResolveManagedRGConfigPath()
	if err != nil {
		return "", false, err
	}
	created, err = writeSettingsFileIfMissing(path, managedRGConfigContents)
	if err != nil {
		return "", false, fmt.Errorf("write managed rg config: %w", err)
	}
	return path, created, nil
}

func writeManagedRGConfigFileForSettingsPath(settingsPath string) (string, error) {
	path := filepath.Join(filepath.Dir(settingsPath), managedRGConfigName)
	_, err := writeSettingsFileIfMissing(path, managedRGConfigContents)
	return path, err
}

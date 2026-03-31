package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

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

func preparePersistenceRoot(path string) (string, error) {
	expanded, err := expandTildePath(path)
	if err != nil {
		return "", fmt.Errorf("expand persistence root: %w", err)
	}
	absRoot, err := filepath.Abs(expanded)
	if err != nil {
		return "", fmt.Errorf("resolve persistence root: %w", err)
	}
	if err := os.MkdirAll(absRoot, 0o755); err != nil {
		return "", fmt.Errorf("create persistence root: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(absRoot, sessionsDirName), 0o755); err != nil {
		return "", fmt.Errorf("create sessions root: %w", err)
	}
	return absRoot, nil
}

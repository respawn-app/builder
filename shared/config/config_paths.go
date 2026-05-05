package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var processStartHome = os.Getenv("HOME")

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
	if err := refuseRealPersistenceRootUnderGoTest(absRoot); err != nil {
		return "", err
	}
	if err := os.MkdirAll(absRoot, 0o755); err != nil {
		return "", fmt.Errorf("create persistence root: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(absRoot, sessionsDirName), 0o755); err != nil {
		return "", fmt.Errorf("create sessions root: %w", err)
	}
	return absRoot, nil
}

func refuseRealPersistenceRootUnderGoTest(absRoot string) error {
	if os.Getenv("BUILDER_ALLOW_REAL_PERSISTENCE_ROOT_IN_TESTS") == "1" {
		return nil
	}
	if !strings.HasSuffix(filepath.Base(os.Args[0]), ".test") {
		return nil
	}
	home := strings.TrimSpace(processStartHome)
	if home == "" {
		return nil
	}
	realRoot, err := filepath.Abs(filepath.Join(home, ".builder"))
	if err != nil {
		return fmt.Errorf("resolve process-start persistence root: %w", err)
	}
	if filepath.Clean(absRoot) != filepath.Clean(realRoot) {
		return nil
	}
	return fmt.Errorf("refusing to use process-start persistence root %s from a Go test binary; tests must provide an isolated config root before calling Load", absRoot)
}

func prepareWorktreeBaseDir(persistenceRoot string, path string) (string, error) {
	raw := strings.TrimSpace(path)
	if raw == "" {
		raw = filepath.Join(persistenceRoot, "worktrees")
	}
	expanded, err := expandTildePath(raw)
	if err != nil {
		return "", fmt.Errorf("expand worktree base dir: %w", err)
	}
	resolved := expanded
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(persistenceRoot, resolved)
	}
	absRoot, err := filepath.Abs(resolved)
	if err != nil {
		return "", fmt.Errorf("resolve worktree base dir: %w", err)
	}
	if err := os.MkdirAll(absRoot, 0o755); err != nil {
		return "", fmt.Errorf("create worktree base dir: %w", err)
	}
	return absRoot, nil
}

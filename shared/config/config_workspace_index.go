package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type workspaceIndex struct {
	Entries map[string]string `json:"entries"`
}

func ResolveWorkspaceContainer(cfg App) (string, string, error) {
	sessionsRoot := SessionsRoot(cfg)
	if err := os.MkdirAll(sessionsRoot, 0o755); err != nil {
		return "", "", fmt.Errorf("create sessions root: %w", err)
	}

	canonicalRoot, err := canonicalWorkspaceRoot(cfg.WorkspaceRoot)
	if err != nil {
		return "", "", err
	}
	if legacyContainer, ok, err := legacyWorkspaceContainer(cfg, canonicalRoot); err != nil {
		return "", "", err
	} else if ok {
		if !isValidWorkspaceContainerName(legacyContainer) {
			return "", "", fmt.Errorf("invalid legacy workspace container %q", legacyContainer)
		}
		containerDir := filepath.Join(sessionsRoot, legacyContainer)
		if err := os.MkdirAll(containerDir, 0o755); err != nil {
			return "", "", fmt.Errorf("create workspace container: %w", err)
		}
		return legacyContainer, containerDir, nil
	}

	container := deterministicWorkspaceContainerName(canonicalRoot)
	containerDir := filepath.Join(sessionsRoot, container)
	if err := os.MkdirAll(containerDir, 0o755); err != nil {
		return "", "", fmt.Errorf("create workspace container: %w", err)
	}
	return container, containerDir, nil
}

func ProjectIDForWorkspaceRoot(workspaceRoot string) (string, error) {
	canonicalRoot, err := canonicalWorkspaceRoot(workspaceRoot)
	if err != nil {
		return "", err
	}
	return deterministicProjectID(canonicalRoot), nil
}

func canonicalWorkspaceRoot(workspaceRoot string) (string, error) {
	absRoot, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return "", fmt.Errorf("resolve workspace root: %w", err)
	}
	canonicalRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("canonicalize workspace root: %w", err)
		}
		canonicalRoot = absRoot
	}
	return filepath.Clean(canonicalRoot), nil
}

func deterministicWorkspaceContainerName(canonicalRoot string) string {
	base := sanitizedWorkspaceContainerPrefix(filepath.Base(canonicalRoot))
	sum := sha256.Sum256([]byte(canonicalRoot))
	return fmt.Sprintf("%s-%s", base, hex.EncodeToString(sum[:]))
}

func deterministicProjectID(canonicalRoot string) string {
	sum := sha256.Sum256([]byte(canonicalRoot))
	return fmt.Sprintf("project-%s", hex.EncodeToString(sum[:]))
}

func legacyWorkspaceContainer(cfg App, canonicalRoot string) (string, bool, error) {
	idxPath := filepath.Join(cfg.PersistenceRoot, workspaceIndexName)
	idx, err := loadWorkspaceIndex(idxPath)
	if err != nil {
		return "", false, err
	}
	for _, key := range legacyWorkspaceLookupKeys(cfg.WorkspaceRoot, canonicalRoot) {
		if container, ok := idx.Entries[key]; ok {
			return container, true, nil
		}
	}
	return "", false, nil
}

func legacyWorkspaceLookupKeys(workspaceRoot, canonicalRoot string) []string {
	keys := make([]string, 0, 2)
	appendKey := func(value string) {
		trimmed := filepath.Clean(strings.TrimSpace(value))
		if trimmed == "" {
			return
		}
		for _, existing := range keys {
			if existing == trimmed {
				return
			}
		}
		keys = append(keys, trimmed)
	}
	appendKey(workspaceRoot)
	appendKey(canonicalRoot)
	return keys
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

func sanitizedWorkspaceContainerPrefix(base string) string {
	trimmed := strings.TrimSpace(base)
	if trimmed == "" || trimmed == "." || trimmed == string(filepath.Separator) {
		return "workspace"
	}

	var b strings.Builder
	b.Grow(len(trimmed))
	lastDash := false
	for i := 0; i < len(trimmed); i++ {
		c := trimmed[i]
		if isASCIILetter(c) || isASCIIDigit(c) {
			b.WriteByte(c)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}

	prefix := strings.Trim(b.String(), "-")
	if prefix == "" {
		return "workspace"
	}
	const maxPrefixLength = 32
	if len(prefix) > maxPrefixLength {
		prefix = strings.Trim(prefix[:maxPrefixLength], "-")
		if prefix == "" {
			return "workspace"
		}
	}
	return prefix
}

func isValidWorkspaceContainerName(name string) bool {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" || trimmed == "." || trimmed == ".." || filepath.IsAbs(trimmed) {
		return false
	}
	if strings.ContainsAny(trimmed, `/\\`) {
		return false
	}
	return filepath.Clean(trimmed) == trimmed && filepath.Base(trimmed) == trimmed
}

func isASCIILetter(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isASCIIDigit(c byte) bool {
	return c >= '0' && c <= '9'
}

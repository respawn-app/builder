package shell

import (
	"path/filepath"
	"strings"
)

func ResolveWorkdir(workspaceRoot string, requested string) string {
	base := strings.TrimSpace(workspaceRoot)
	if base != "" {
		base = filepath.Clean(base)
	}
	trimmed := strings.TrimSpace(requested)
	if trimmed == "" {
		return base
	}
	if filepath.IsAbs(trimmed) {
		return filepath.Clean(trimmed)
	}
	if base == "" {
		return filepath.Clean(trimmed)
	}
	return filepath.Clean(filepath.Join(base, trimmed))
}

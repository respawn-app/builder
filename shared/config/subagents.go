package config

import "strings"

const BuiltInSubagentRoleFast = "fast"

func NormalizeSubagentRole(raw string) string {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	if normalized == "" {
		return ""
	}
	for _, r := range normalized {
		if r >= 'a' && r <= 'z' {
			continue
		}
		if r >= '0' && r <= '9' {
			continue
		}
		switch r {
		case '-', '_':
			continue
		default:
			return ""
		}
	}
	return normalized
}

func normalizeSubagentRoleKey(raw string) string {
	return NormalizeSubagentRole(raw)
}

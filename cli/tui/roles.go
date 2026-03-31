package tui

import (
	"builder/shared/transcript"
	"strings"
)

const roleManualCompactionCarryover = string(transcript.EntryRoleManualCompactionCarryover)

func isCompactionRole(role string) bool {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "compaction_notice", "compaction_summary", roleManualCompactionCarryover:
		return true
	default:
		return false
	}
}

func isDetailOnlyRole(role string) bool {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "thinking", "thinking_trace", "reasoning", "compaction_summary", roleManualCompactionCarryover, "error", "warning":
		return true
	default:
		return false
	}
}

func isStyledMetaRole(role string) bool {
	trimmed := strings.ToLower(strings.TrimSpace(role))
	return isCompactionRole(trimmed) || trimmed == "reviewer_status" || trimmed == "reviewer_suggestions" || trimmed == "error" || trimmed == "warning"
}

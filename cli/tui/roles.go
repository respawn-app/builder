package tui

import "builder/shared/transcript"

const roleManualCompactionCarryover = string(transcript.EntryRoleManualCompactionCarryover)
const roleCompactionSummary = string(transcript.EntryRoleCompactionSummary)
const roleDeveloperContext = string(transcript.EntryRoleDeveloperContext)
const roleDeveloperFeedback = string(transcript.EntryRoleDeveloperFeedback)
const roleInterruption = string(transcript.EntryRoleInterruption)

func isCompactionRole(role string) bool {
	switch transcript.NormalizeEntryRole(role) {
	case "compaction_notice", roleCompactionSummary, roleManualCompactionCarryover:
		return true
	default:
		return false
	}
}

func isDetailOnlyRole(role string) bool {
	switch transcript.NormalizeEntryRole(role) {
	case "thinking", "thinking_trace", "reasoning", roleCompactionSummary, roleDeveloperContext, roleManualCompactionCarryover, "error", "warning":
		return true
	default:
		return false
	}
}

func isStyledMetaRole(role string) bool {
	trimmed := transcript.NormalizeEntryRole(role)
	return isCompactionRole(trimmed) || trimmed == "reviewer_status" || trimmed == "reviewer_suggestions" || trimmed == "error" || trimmed == "warning" || trimmed == "cache_warning" || trimmed == roleDeveloperContext || trimmed == roleDeveloperFeedback || trimmed == roleInterruption
}

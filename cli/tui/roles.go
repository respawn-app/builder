package tui

import "builder/shared/transcript"

const roleManualCompactionCarryover = string(transcript.EntryRoleManualCompactionCarryover)
const roleCompactionSummary = string(transcript.EntryRoleCompactionSummary)
const roleDeveloperContext = string(transcript.EntryRoleDeveloperContext)
const roleDeveloperFeedback = string(transcript.EntryRoleDeveloperFeedback)
const roleInterruption = string(transcript.EntryRoleInterruption)

const interruptionUserVisibleText = "You interrupted"

func isCompactionRole(role string) bool {
	switch transcript.NormalizeEntryRole(role) {
	case "compaction_notice", roleCompactionSummary, roleManualCompactionCarryover:
		return true
	default:
		return false
	}
}

func isDetailOnlyRole(role string) bool {
	normalized := transcript.NormalizeEntryRole(role)
	switch normalized {
	case "thinking", "thinking_trace", "reasoning", roleCompactionSummary, roleDeveloperContext, roleManualCompactionCarryover, "error", "warning":
		return true
	default:
		return transcriptMessageStyleForRole(normalized) == transcriptMessageStyleWarning
	}
}

func isStyledMetaRole(role string) bool {
	trimmed := transcript.NormalizeEntryRole(role)
	return isCompactionRole(trimmed) || transcriptMessageStyleForRole(trimmed) != transcriptMessageStyleNone || trimmed == roleDeveloperContext || trimmed == roleDeveloperFeedback || trimmed == roleInterruption
}

func transcriptDisplayText(role, text string) string {
	switch transcript.NormalizeEntryRole(role) {
	case roleInterruption:
		return interruptionUserVisibleText
	default:
		return text
	}
}

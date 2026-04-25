package tui

import "builder/shared/transcript"

const roleManualCompactionCarryover = string(transcript.EntryRoleManualCompactionCarryover)
const roleCompactionSummary = string(transcript.EntryRoleCompactionSummary)
const roleDeveloperContext = string(transcript.EntryRoleDeveloperContext)
const roleDeveloperFeedback = string(transcript.EntryRoleDeveloperFeedback)
const roleDeveloperErrorFeedback = string(transcript.EntryRoleDeveloperErrorFeedback)
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
	return defaultEntryVisibilityForRole(role) == transcript.EntryVisibilityDetailOnly
}

func isVisibleInOngoing(entry TranscriptEntry) bool {
	switch entryVisibility(entry) {
	case transcript.EntryVisibilityDetailOnly:
		return false
	default:
		return true
	}
}

func entryVisibility(entry TranscriptEntry) transcript.EntryVisibility {
	if explicit := transcript.NormalizeEntryVisibility(entry.Visibility); explicit != transcript.EntryVisibilityAuto {
		return explicit
	}
	return defaultEntryVisibilityForRole(entry.Role)
}

func defaultEntryVisibilityForRole(role string) transcript.EntryVisibility {
	normalized := transcript.NormalizeEntryRole(role)
	switch normalized {
	case "thinking", "thinking_trace", "reasoning", roleCompactionSummary, roleDeveloperContext, roleManualCompactionCarryover, roleInterruption, "error", "warning", "cache_warning":
		return transcript.EntryVisibilityDetailOnly
	default:
		if transcriptMessageStyleForRole(normalized) == transcriptMessageStyleWarning {
			return transcript.EntryVisibilityDetailOnly
		}
		return transcript.EntryVisibilityAll
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

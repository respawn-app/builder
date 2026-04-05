package transcript

import "strings"

type EntryRole string

// EntryRoleCompactionSummary marks a persisted compaction or handoff summary.
const EntryRoleCompactionSummary EntryRole = "compaction_summary"

// EntryRoleManualCompactionCarryover marks the synthetic message that preserves
// the last user prompt across a manual compaction boundary.
const EntryRoleManualCompactionCarryover EntryRole = "manual_compaction_carryover"

// EntryRoleDeveloperContext marks developer/meta context that should only
// appear in detail mode (AGENTS, skills, environment, headless prompts).
const EntryRoleDeveloperContext EntryRole = "developer_context"

// EntryRoleDeveloperFeedback marks developer feedback that should remain
// visible in ongoing mode.
const EntryRoleDeveloperFeedback EntryRole = "developer_feedback"

// EntryRoleInterruption marks persisted interruption notices.
const EntryRoleInterruption EntryRole = "interruption"

func NormalizeEntryRole(role string) string {
	return strings.ToLower(strings.TrimSpace(role))
}

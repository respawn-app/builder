package transcript

type EntryRole string

// EntryRoleManualCompactionCarryover marks the synthetic message that preserves
// the last user prompt across a manual compaction boundary.
const EntryRoleManualCompactionCarryover EntryRole = "manual_compaction_carryover"

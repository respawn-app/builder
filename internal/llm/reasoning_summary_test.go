package llm

import "testing"

func TestSplitReasoningSummarySeparatesStatusFromSummary(t *testing.T) {
	parts := splitReasoningSummary("**Preparing patch**\n\nI am exploring options.\n**Running checks**")
	if parts.Status != "Running checks" {
		t.Fatalf("status = %q, want %q", parts.Status, "Running checks")
	}
	if parts.Summary != "I am exploring options." {
		t.Fatalf("summary = %q, want %q", parts.Summary, "I am exploring options.")
	}
}

func TestSplitReasoningSummaryPreservesPlainFormattingMarkers(t *testing.T) {
	parts := splitReasoningSummary("**First status**\n\n`literal` details\n\n**Second status**\nMore details")
	if parts.Status != "Second status" {
		t.Fatalf("status = %q, want %q", parts.Status, "Second status")
	}
	if parts.Summary != "`literal` details\n\nMore details" {
		t.Fatalf("summary = %q", parts.Summary)
	}
}

func TestNormalizeReasoningEntriesDropsStatusOnlyEntries(t *testing.T) {
	got := normalizeReasoningEntries([]ReasoningEntry{{Role: "reasoning", Text: "**Preparing patch**"}})
	if len(got) != 0 {
		t.Fatalf("expected no persisted reasoning entries, got %+v", got)
	}
}

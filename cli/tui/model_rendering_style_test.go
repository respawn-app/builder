package tui

import "testing"

func TestToolResultIndexFindMatchingToolResultIndexSkipsConsumedDuplicateResults(t *testing.T) {
	entries := []TranscriptEntry{
		{Role: "tool_call", ToolCallID: "call_a"},
		{Role: "tool_result_ok", ToolCallID: "call_a", Text: "first"},
		{Role: "assistant", Text: "gap"},
		{Role: "tool_result_ok", ToolCallID: "call_a", Text: "second"},
	}
	index := buildToolResultIndex(entries)
	consumed := map[int]struct{}{1: {}}

	got := index.findMatchingToolResultIndex(entries, 0, consumed)
	if got != 3 {
		t.Fatalf("expected later unmatched duplicate result, got %d", got)
	}

	got = index.findMatchingToolResultIndex(entries, 0, consumed)
	if got != 3 {
		t.Fatalf("expected repeated lookup to stay on same unmatched result, got %d", got)
	}

	consumed[3] = struct{}{}
	got = index.findMatchingToolResultIndex(entries, 0, consumed)
	if got != -1 {
		t.Fatalf("expected no remaining matches after consuming duplicates, got %d", got)
	}
}

func TestToolResultIndexFindMatchingToolResultIndexPrefersAdjacentMatch(t *testing.T) {
	entries := []TranscriptEntry{
		{Role: "tool_call", ToolCallID: "call_a"},
		{Role: "tool_result_ok", ToolCallID: "call_a", Text: "adjacent"},
		{Role: "tool_result_ok", ToolCallID: "call_a", Text: "later"},
	}
	index := buildToolResultIndex(entries)

	got := index.findMatchingToolResultIndex(entries, 0, map[int]struct{}{})
	if got != 1 {
		t.Fatalf("expected adjacent result to win, got %d", got)
	}
}

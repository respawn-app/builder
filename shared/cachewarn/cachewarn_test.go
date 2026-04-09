package cachewarn

import "testing"

func TestTextFormatsConversationCacheMiss(t *testing.T) {
	warning := Warning{Scope: ScopeConversation, Reason: ReasonNonPostfix, LostInputTokens: 12_300}
	if got := Text(warning); got != "Cache miss: request was not a postfix of the previous request for the same cache key, -12k tokens" {
		t.Fatalf("Text() = %q", got)
	}
}

func TestTextFormatsSupervisorCacheMiss(t *testing.T) {
	warning := Warning{Scope: ScopeReviewer, Reason: ReasonNonPostfix, LostInputTokens: 1_200}
	if got := Text(warning); got != "Cache miss: supervisor request was not a postfix of the previous request for the same cache key, -1.2k tokens" {
		t.Fatalf("Text() = %q", got)
	}
}

func TestTextFormatsLegacyCompactionCacheMiss(t *testing.T) {
	warning := Warning{Scope: ScopeConversation, Reason: ReasonCompaction, LostInputTokens: 0}
	if got := Text(warning); got != "Cache miss: compaction, -0k tokens" {
		t.Fatalf("Text() = %q", got)
	}
}

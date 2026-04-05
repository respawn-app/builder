package cachewarn

import (
	"fmt"
	"strings"
)

type Scope string

const (
	ScopePrimary  Scope = "primary"
	ScopeReviewer Scope = "reviewer"
)

type Reason string

const (
	ReasonContextCompaction Reason = "context_compaction"
	ReasonHistoryRewrite    Reason = "history_rewrite"
	ReasonReviewerRollback  Reason = "reviewer_rollback"
	ReasonFork              Reason = "fork"
)

type StateEvent struct {
	Scope             Scope  `json:"scope"`
	RequestDigest     string `json:"request_digest,omitempty"`
	InputTokens       int    `json:"input_tokens,omitempty"`
	CachedInputTokens int    `json:"cached_input_tokens,omitempty"`
	HasCachedInput    bool   `json:"has_cached_input,omitempty"`
}

type InvalidationEvent struct {
	Scope  Scope  `json:"scope"`
	Reason Reason `json:"reason"`
}

func NormalizeScope(raw string) (Scope, bool) {
	switch Scope(strings.ToLower(strings.TrimSpace(raw))) {
	case ScopePrimary:
		return ScopePrimary, true
	case ScopeReviewer:
		return ScopeReviewer, true
	default:
		return "", false
	}
}

func WarningText(scope Scope, reason Reason, cachedTokens int, hasCachedTokens bool) string {
	prefix := "Cache invalidated"
	if scope == ScopeReviewer {
		prefix = "Supervisor cache invalidated"
	}

	message := prefix + reasonSuffix(reason) + "."
	if hasCachedTokens && cachedTokens > 0 {
		message += " Previous cached tokens: " + compactTokenCount(cachedTokens) + "."
	}
	return message
}

func reasonSuffix(reason Reason) string {
	switch reason {
	case ReasonContextCompaction:
		return " by context compaction"
	case ReasonReviewerRollback:
		return " by supervisor rollback"
	case ReasonFork:
		return " by fork"
	case ReasonHistoryRewrite:
		return " by history rewrite"
	default:
		return ""
	}
}

func compactTokenCount(tokens int) string {
	if tokens < 1_000 {
		return fmt.Sprintf("%d", tokens)
	}
	if tokens%1_000 == 0 {
		return fmt.Sprintf("%dk", tokens/1_000)
	}
	if tokens >= 100_000 {
		return fmt.Sprintf("%dk", tokens/1_000)
	}
	return fmt.Sprintf("%.1fk", float64(tokens)/1_000)
}

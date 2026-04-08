package cachewarn

import (
	"fmt"
	"strings"
)

type Scope string
type Reason string

const (
	ScopeConversation Scope = "conversation"
	ScopeReviewer     Scope = "reviewer"

	// ReasonCompaction is retained for persisted legacy warnings only.
	// Active runtime cache-lineage logic should rotate cache keys instead of
	// emitting a synthetic compaction invalidation warning.
	ReasonCompaction   Reason = "compaction"
	ReasonNonPostfix   Reason = "non_postfix"
	ReasonReuseDropped Reason = "reuse_dropped"
)

type Warning struct {
	Scope           Scope  `json:"scope,omitempty"`
	Reason          Reason `json:"reason"`
	CacheKey        string `json:"cache_key,omitempty"`
	LostInputTokens int    `json:"lost_input_tokens,omitempty"`
}

func Text(w Warning) string {
	return fmt.Sprintf("Cache miss: %s, -%s tokens", reasonText(w), formatTokenDeltaThousands(w.LostInputTokens))
}

func reasonText(w Warning) string {
	switch w.Reason {
	case ReasonCompaction:
		return "compaction"
	case ReasonNonPostfix:
		if w.Scope == ScopeReviewer {
			return "supervisor request was not a postfix of the previous request for the same cache key"
		}
		return "request was not a postfix of the previous request for the same cache key"
	case ReasonReuseDropped:
		if w.Scope == ScopeReviewer {
			return "postfix-compatible supervisor cache reuse disappeared"
		}
		return "postfix-compatible cache reuse disappeared"
	default:
		trimmed := strings.TrimSpace(string(w.Reason))
		if trimmed == "" {
			return "unknown reason"
		}
		return trimmed
	}
}

func formatTokenDeltaThousands(tokens int) string {
	if tokens < 0 {
		tokens = 0
	}
	if tokens < 10_000 {
		thousands := float64(tokens) / 1000.0
		formatted := fmt.Sprintf("%.1f", thousands)
		formatted = strings.TrimSuffix(formatted, ".0")
		return formatted + "k"
	}
	rounded := (tokens + 500) / 1000
	return fmt.Sprintf("%dk", rounded)
}

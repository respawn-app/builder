package cachewarn

import "strings"

type Scope string
type Reason string

const (
	ScopeConversation Scope = "conversation"
	ScopeReviewer     Scope = "reviewer"

	ReasonNonPostfix   Reason = "non_postfix"
	ReasonReuseDropped Reason = "reuse_dropped"
)

type Warning struct {
	Scope    Scope  `json:"scope,omitempty"`
	Reason   Reason `json:"reason"`
	CacheKey string `json:"cache_key,omitempty"`
}

func Text(w Warning) string {
	switch w.Reason {
	case ReasonNonPostfix:
		if w.Scope == ScopeReviewer {
			return "Prompt cache continuity broke for reviewer requests: this request was not a postfix of the previous request for the same cache key."
		}
		return "Prompt cache continuity broke: this request was not a postfix of the previous request for the same cache key."
	case ReasonReuseDropped:
		if w.Scope == ScopeReviewer {
			return "Prompt cache reuse disappeared for a postfix-compatible reviewer request. The provider did not expose the cause."
		}
		return "Prompt cache reuse disappeared for a postfix-compatible request. The provider did not expose the cause."
	default:
		if strings.TrimSpace(string(w.Reason)) == "" {
			return "Prompt cache warning."
		}
		return "Prompt cache warning: " + strings.TrimSpace(string(w.Reason))
	}
}

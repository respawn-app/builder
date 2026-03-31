package tools

import (
	"encoding/json"
	"testing"
)

func TestValidateWebSearchInputRejectsWhitespaceQuery(t *testing.T) {
	err := ValidateWebSearchInput(json.RawMessage(`{"query":"   "}`))
	if err == nil {
		t.Fatal("expected whitespace-only query to be rejected")
	}
	if err.Error() != InvalidWebSearchQueryMessage {
		t.Fatalf("unexpected validation error: %v", err)
	}
}

func TestFormatWebSearchDisplayTextQuotesQuery(t *testing.T) {
	if got := FormatWebSearchDisplayText(" latest golang release "); got != `web search: "latest golang release"` {
		t.Fatalf("unexpected display text: %q", got)
	}
}

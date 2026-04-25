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

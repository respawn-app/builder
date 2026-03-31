package actions

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestUnknownActionIsFatal(t *testing.T) {
	r := NewRegistry()
	err := r.Execute(context.Background(), "missing", json.RawMessage(`{}`))
	if err == nil {
		t.Fatalf("expected error for unknown action")
	}
	if !strings.Contains(err.Error(), "fatal") {
		t.Fatalf("expected fatal marker, got %v", err)
	}
}
